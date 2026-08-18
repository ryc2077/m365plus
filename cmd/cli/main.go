// Command line interface for M365 Copilot.
// Single binary with subcommands: serve (API server), setup-wizard (browser-based setup).
// Default mode (no subcommand) runs CLI query or interactive mode.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ryc2077/m365plus/pkg/accounts"
	"github.com/ryc2077/m365plus/pkg/auth"
	"github.com/ryc2077/m365plus/pkg/logging"
	"github.com/ryc2077/m365plus/pkg/models"
	"github.com/ryc2077/m365plus/pkg/payload"
	"github.com/ryc2077/m365plus/pkg/servers"
	"github.com/ryc2077/m365plus/pkg/setup"
	"github.com/ryc2077/m365plus/pkg/webadmin"
)

const (
	// defaultRefreshTokenFile is the default path for the refresh token.
	defaultRefreshTokenFile = "data/tokens/rt_90day.txt"
	// defaultCacheFile is the default path for the token cache.
	defaultCacheFile = "data/tokens/token_cache.json"
	// defaultPort is the default port for the API server.
	defaultPort = 8000
)

func main() {
	// Initialize dual-writer logger (stdout + data/proxy.log)
	if err := logging.Init(logging.LevelDebug); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize logger: %v\n", err)
		os.Exit(1)
	}
	defer logging.Close()
	logging.Infof("M365Bridge v%s starting", models.Version)

	// Check for subcommand
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "serve":
			runServer(os.Args[2:])
			return
		case "setup-wizard":
			runSetupWizard(os.Args[2:])
			return
		}
	}

	// Default: CLI mode
	runCLI()
}

// runServer starts the HTTP API server with the management plane mounted.
func runServer(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", defaultPort, "Port to listen on")
	showVersion := fs.Bool("version", false, "Show version")
	fs.Parse(args)

	if *showVersion {
		fmt.Printf("M365 Copilot API Server v%s\n", models.Version)
		os.Exit(0)
	}

	config := models.LoadConfig()

	// Multi-account management plane is always mounted. The single-account
	// token manager remains the fallback for legacy API-key-free setups and
	// for the setup wizard, so the strict M365_TENANT_ID/M365_USER_OID check
	// is relaxed when account store already holds accounts.
	if config.TenantID == "" || config.UserOID == "" {
		if os.Getenv("M365_TENANT_ID") == "" || os.Getenv("M365_USER_OID") == "" {
			logging.Warnf("M365_TENANT_ID/M365_USER_OID not set; management plane will require account login")
		}
	}

	tokenManager := auth.NewTokenManager(
		config.TenantID,
		config.ClientID,
		config.Scope,
		defaultRefreshTokenFile,
		defaultCacheFile,
	)
	if config.UserOID != "" {
		tokenManager.SetUserOID(config.UserOID)
	}

	admin, err := webadmin.New()
	if err != nil {
		logging.Fatalf("Error initializing management plane: %v", err)
	}

	apiServer := servers.NewAPIServer(config, tokenManager)
	apiServer.SetAccountResolver(admin)
	apiServer.SetUsageRecorder(admin)
	admin.SetDataPlane(apiServer.Handler())
	admin.SetModelTester(func(ctx context.Context, acc accounts.AccountToken, model string) (string, error) {
		return probeModel(ctx, apiServer, acc, model)
	})
	admin.SetChatFunc(func(ctx context.Context, acc accounts.AccountToken, model string, messages []webadmin.ChatMessage) (<-chan webadmin.ChatChunk, error) {
		return adminChatStream(ctx, apiServer, acc, model, messages)
	})

	handler := admin.Routes()
	apiServer.StartBackgroundTasks()

	srv := &http.Server{
		Addr:    fmt.Sprintf(":%d", *port),
		Handler: handler,
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	errChan := make(chan error, 1)
	go func() {
		logging.Infof("M365Bridge Plus listening on :%d", *port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errChan <- err
		}
	}()

	select {
	case <-sigChan:
		logging.Info("Shutting down server...")
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		_ = apiServer.Stop()
		logging.Info("Server stopped")
	case err := <-errChan:
		logging.Fatalf("Server error: %v", err)
	}
}

// probeModel sends a minimal probe through the data plane for the model test.
func probeModel(ctx context.Context, apiServer *servers.APIServer, acc accounts.AccountToken, model string) (string, error) {
	// Route through the account-bound client for a faithful round-trip.
	return apiServer.ProbeModel(ctx, acc, model)
}

// adminChatStream adapts the admin console chat hook to the data plane's
// streaming conversation method.
func adminChatStream(ctx context.Context, apiServer *servers.APIServer, acc accounts.AccountToken, model string, messages []webadmin.ChatMessage) (<-chan webadmin.ChatChunk, error) {
	msgs := make([]payload.Message, 0, len(messages))
	for _, m := range messages {
		msgs = append(msgs, payload.Message{Role: m.Role, Content: m.Content})
	}
	upstream, err := apiServer.ChatStream(ctx, acc, model, msgs)
	if err != nil {
		return nil, err
	}
	out := make(chan webadmin.ChatChunk)
	go func() {
		defer close(out)
		for c := range upstream {
			if c.Error != nil {
				out <- webadmin.ChatChunk{Error: c.Error.Error()}
				return
			}
			if c.Thinking != "" {
				out <- webadmin.ChatChunk{Thinking: c.Thinking}
			}
			if c.Text != "" {
				out <- webadmin.ChatChunk{Text: c.Text}
			}
		}
	}()
	return out, nil
}

// runSetupWizard runs the browser-based setup wizard.
func runSetupWizard(args []string) {
	fs := flag.NewFlagSet("setup-wizard", flag.ExitOnError)
	file := fs.String("file", "data/setup.json", "Path to setup JSON file containing oid, tenant, and refresh_token")
	fs.Parse(args)

	if err := setup.Run(*file); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

// runCLI runs the default CLI mode (single query or interactive).
func runCLI() {
	// Parse command-line flags
	model := flag.String("model", "auto", "Model to use (auto, quick, reasoning, gpt5.5, gpt5.5-reasoning, gpt5.6-reasoning, claude, claude-sonnet, claude-opus, claude-fable, claude-sonnet-4-20250514)")
	reasoning := flag.Bool("reasoning", false, "Use reasoning mode")
	interactive := flag.Bool("i", false, "Interactive mode")
	noStream := flag.Bool("no-stream", false, "Disable streaming")
	listModels := flag.Bool("list-models", false, "List available models")
	showVersion := flag.Bool("version", false, "Show version")

	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("M365Bridge v%s\n", models.Version)
		os.Exit(0)
	}

	// Load configuration
	config := models.LoadConfig()

	// Validate required configuration
	if config.TenantID == "" || config.UserOID == "" {
		fmt.Fprintf(os.Stderr, "Error: M365_TENANT_ID and M365_USER_OID environment variables are required\n")
		fmt.Fprintf(os.Stderr, "\nGet them from: https://graph.microsoft.com/v1.0/me (id and tenantId)\n")
		fmt.Fprintf(os.Stderr, "\nOr run the setup wizard to configure automatically\n")
		os.Exit(1)
	}

	// Initialize token manager
	tokenManager := auth.NewTokenManager(
		config.TenantID,
		config.ClientID,
		config.Scope,
		defaultRefreshTokenFile,
		defaultCacheFile,
	)

	// Create CLI server
	cliServer := servers.NewCLIServer(config, tokenManager)
	defer cliServer.Close()

	// Prepare options
	options := &servers.CLIOptions{
		Model:       *model,
		Reasoning:   *reasoning,
		Interactive: *interactive,
		NoStream:    *noStream,
		Prompt:      flag.Arg(0),
		ListModels:  *listModels,
	}

	// Run CLI
	if err := cliServer.Run(options); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
