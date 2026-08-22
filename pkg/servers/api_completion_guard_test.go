package servers

import (
	"testing"

	"github.com/ryc2077/m365plus/pkg/client"
	"github.com/ryc2077/m365plus/pkg/toolcalling"
)

func TestWithoutUnverifiedCompletionClaimReplacesUnsupportedReport(t *testing.T) {
	got := withoutUnverifiedCompletionClaim(
		"I successfully deployed the service.",
		true,
		toolcalling.Ledger{},
		nil,
	)
	if got != unverifiedCompletionNotice {
		t.Fatalf("got %q, want guard notice", got)
	}
}

func TestWithoutUnverifiedCompletionClaimKeepsEvidenceBackedReport(t *testing.T) {
	ledger := toolcalling.BuildLedger(
		[]toolcalling.LedgerCall{{ID: "call_1", Name: "deploy", Arguments: "{}"}},
		[]toolcalling.LedgerResult{{ID: "call_1", Content: "ok"}},
		1,
	)
	want := "I successfully deployed the service."
	if got := withoutUnverifiedCompletionClaim(want, true, ledger, nil); got != want {
		t.Fatalf("evidence-backed report changed to %q", got)
	}
}

func TestWithoutUnverifiedCompletionClaimKeepsActiveToolCall(t *testing.T) {
	want := "I successfully deployed the service."
	calls := []client.ToolCall{{ID: "call_1", Type: "function"}}
	if got := withoutUnverifiedCompletionClaim(want, true, toolcalling.Ledger{}, calls); got != want {
		t.Fatalf("report with active tool call changed to %q", got)
	}
}
