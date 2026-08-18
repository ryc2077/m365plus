package webadmin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ryc2077/m365plus/pkg/webadmin/outbound"
)

type singBoxImportRequest struct {
	Links string `json:"links"`
}

func decodeBase64Text(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	encodings := []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding}
	for _, encoding := range encodings {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("invalid base64 content")
}

func nodeName(parsed *url.URL) string {
	if name, err := url.QueryUnescape(parsed.Fragment); err == nil && strings.TrimSpace(name) != "" {
		return strings.TrimSpace(name)
	}
	if parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return strings.ToUpper(parsed.Scheme) + " node"
}

func parseServerPort(parsed *url.URL) (string, int, error) {
	server := parsed.Hostname()
	if server == "" {
		return "", 0, fmt.Errorf("server is required")
	}
	portText := parsed.Port()
	if portText == "" {
		return "", 0, fmt.Errorf("server port is required")
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil || port < 1 || port > 65535 {
		return "", 0, fmt.Errorf("invalid server port")
	}
	return server, port, nil
}

func applyTransportAndTLS(outboundConfig map[string]any, query url.Values) {
	transportType := strings.ToLower(query.Get("type"))
	if transportType == "ws" {
		transport := map[string]any{"type": "ws"}
		if path := query.Get("path"); path != "" {
			transport["path"] = path
		}
		if host := query.Get("host"); host != "" {
			transport["headers"] = map[string]any{"Host": host}
		}
		outboundConfig["transport"] = transport
	} else if transportType == "grpc" {
		transport := map[string]any{"type": "grpc"}
		if serviceName := query.Get("serviceName"); serviceName != "" {
			transport["service_name"] = serviceName
		}
		outboundConfig["transport"] = transport
	}
	security := strings.ToLower(query.Get("security"))
	if security == "tls" || security == "reality" {
		tlsConfig := map[string]any{"enabled": true}
		if serverName := query.Get("sni"); serverName != "" {
			tlsConfig["server_name"] = serverName
		} else if host := query.Get("host"); host != "" {
			tlsConfig["server_name"] = host
		}
		if query.Get("allowInsecure") == "1" {
			tlsConfig["insecure"] = true
		}
		if fingerprint := query.Get("fp"); fingerprint != "" {
			tlsConfig["utls"] = map[string]any{"enabled": true, "fingerprint": fingerprint}
		}
		if security == "reality" {
			reality := map[string]any{"enabled": true}
			if publicKey := query.Get("pbk"); publicKey != "" {
				reality["public_key"] = publicKey
			}
			if shortID := query.Get("sid"); shortID != "" {
				reality["short_id"] = shortID
			}
			tlsConfig["reality"] = reality
		}
		outboundConfig["tls"] = tlsConfig
	}
}

func parseStandardShareLink(raw string) (singBoxNode, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return singBoxNode{}, err
	}
	protocol := strings.ToLower(parsed.Scheme)
	if protocol == "hy2" {
		protocol = "hysteria2"
	}
	server, port, err := parseServerPort(parsed)
	if err != nil {
		return singBoxNode{}, err
	}
	config := map[string]any{"server": server, "server_port": port}
	username := ""
	password := ""
	if parsed.User != nil {
		username = parsed.User.Username()
		password, _ = parsed.User.Password()
	}
	switch protocol {
	case "vless":
		config["uuid"] = username
		if flow := parsed.Query().Get("flow"); flow != "" {
			config["flow"] = flow
		}
	case "trojan", "hysteria2":
		if password == "" {
			password = username
		}
		config["password"] = password
	case "tuic":
		config["uuid"] = username
		config["password"] = password
	default:
		return singBoxNode{}, fmt.Errorf("unsupported share link protocol %q", protocol)
	}
	applyTransportAndTLS(config, parsed.Query())
	return singBoxNode{Name: nodeName(parsed), Protocol: protocol, Outbound: config, Enabled: true}, nil
}

func parseVMessShareLink(raw string) (singBoxNode, error) {
	payload := strings.TrimPrefix(strings.TrimSpace(raw), "vmess://")
	decoded, err := decodeBase64Text(payload)
	if err != nil {
		return singBoxNode{}, err
	}
	var source map[string]any
	if err := json.Unmarshal(decoded, &source); err != nil {
		return singBoxNode{}, fmt.Errorf("invalid VMess JSON: %w", err)
	}
	server, _ := source["add"].(string)
	uuid, _ := source["id"].(string)
	portText := fmt.Sprint(source["port"])
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil || server == "" || uuid == "" {
		return singBoxNode{}, fmt.Errorf("invalid VMess server, port, or UUID")
	}
	config := map[string]any{"server": server, "server_port": port, "uuid": uuid, "security": "auto"}
	query := url.Values{}
	for sourceKey, queryKey := range map[string]string{"net": "type", "path": "path", "host": "host", "tls": "security", "sni": "sni", "fp": "fp"} {
		if value, ok := source[sourceKey].(string); ok && value != "" {
			query.Set(queryKey, value)
		}
	}
	applyTransportAndTLS(config, query)
	name, _ := source["ps"].(string)
	if strings.TrimSpace(name) == "" {
		name = server
	}
	return singBoxNode{Name: name, Protocol: "vmess", Outbound: config, Enabled: true}, nil
}

func parseShadowsocksShareLink(raw string) (singBoxNode, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return singBoxNode{}, err
	}
	server, port, parseErr := parseServerPort(parsed)
	if parseErr != nil {
		return singBoxNode{}, parseErr
	}
	credentials := ""
	if parsed.User != nil {
		credentials = parsed.User.String()
	}
	if !strings.Contains(credentials, ":") {
		decoded, decodeErr := decodeBase64Text(credentials)
		if decodeErr != nil {
			return singBoxNode{}, fmt.Errorf("invalid Shadowsocks credentials")
		}
		credentials = string(decoded)
	}
	parts := strings.SplitN(credentials, ":", 2)
	if len(parts) != 2 {
		return singBoxNode{}, fmt.Errorf("invalid Shadowsocks credentials")
	}
	return singBoxNode{Name: nodeName(parsed), Protocol: "shadowsocks", Outbound: map[string]any{"server": server, "server_port": port, "method": parts[0], "password": parts[1]}, Enabled: true}, nil
}

func parseSingBoxShareLink(raw string) (singBoxNode, error) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(lower, "vmess://"):
		return parseVMessShareLink(raw)
	case strings.HasPrefix(lower, "ss://"):
		return parseShadowsocksShareLink(raw)
	default:
		return parseStandardShareLink(raw)
	}
}

const singBoxProxyURL = "socks5://sing-box:1080"

type singBoxNode struct {
	ID       string         `json:"id"`
	Name     string         `json:"name"`
	Protocol string         `json:"protocol"`
	Outbound map[string]any `json:"outbound"`
	Enabled  bool           `json:"enabled"`
}

type singBoxStore struct {
	mu       sync.Mutex
	path     string
	Nodes    []singBoxNode `json:"nodes"`
	Selected string        `json:"selected,omitempty"`
}

func openSingBoxStore() *singBoxStore {
	dir := strings.TrimSpace(os.Getenv("M365_DATA_DIR"))
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config", "m365-copilot2api")
	}
	store := &singBoxStore{path: filepath.Join(dir, "sing-box-nodes.json")}
	if data, err := os.ReadFile(store.path); err == nil {
		_ = json.Unmarshal(data, store)
		migrated := false
		for index := range store.Nodes {
			transport, ok := store.Nodes[index].Outbound["transport"].(map[string]any)
			if !ok || transport["type"] != "ws" {
				continue
			}
			maxEarlyData, hasEarlyData := transport["max_early_data"]
			if !hasEarlyData {
				continue
			}
			value := fmt.Sprint(maxEarlyData)
			if value == "" || value == "0" {
				continue
			}
			path, _ := transport["path"].(string)
			if path == "" {
				path = "/"
			}
			separator := "?"
			if strings.Contains(path, "?") {
				separator = "&"
			}
			transport["path"] = path + separator + "ed=" + value
			delete(transport, "max_early_data")
			delete(transport, "early_data_header_name")
			migrated = true
		}
		if migrated {
			_ = store.save()
		}
	}
	return store
}

func (s *singBoxStore) save() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	if err := s.writeConfigLocked(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, 0600)
}

func (s *singBoxStore) writeConfigLocked() error {
	outbounds := []map[string]any{}
	selected := s.Selected
	nodeIDs := make([]string, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		if !node.Enabled {
			continue
		}
		outboundConfig := make(map[string]any, len(node.Outbound)+2)
		maps.Copy(outboundConfig, node.Outbound)
		outboundConfig["type"] = node.Protocol
		outboundConfig["tag"] = node.ID
		outbounds = append(outbounds, outboundConfig)
		nodeIDs = append(nodeIDs, node.ID)
		if selected == "" {
			selected = node.ID
		}
	}
	outbounds = append(outbounds, map[string]any{"type": "direct", "tag": "direct"})
	final := "direct"
	if len(nodeIDs) > 0 {
		validSelected := slices.Contains(nodeIDs, selected)
		if !validSelected {
			selected = nodeIDs[0]
		}
		s.Selected = selected
		outbounds = append(outbounds, map[string]any{
			"type": "selector", "tag": "proxy", "outbounds": nodeIDs,
			"default": selected, "interrupt_exist_connections": true,
		})
		final = "proxy"
	}
	config := map[string]any{
		"log":          map[string]any{"level": "info", "timestamp": true},
		"inbounds":     []map[string]any{{"type": "socks", "tag": "socks-in", "listen": "0.0.0.0", "listen_port": 1080}},
		"outbounds":    outbounds,
		"route":        map[string]any{"final": final},
		"experimental": map[string]any{"clash_api": map[string]any{"external_controller": "0.0.0.0:9090"}},
	}
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	configPath := filepath.Join(filepath.Dir(s.path), "sing-box", "config.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
		return err
	}
	return writeFileAtomic(configPath, data, 0600)
}

func newSingBoxNodeID() string {
	raw := make([]byte, 8)
	_, _ = rand.Read(raw)
	return "node-" + hex.EncodeToString(raw)
}

func validateSingBoxNode(node singBoxNode) error {
	supported := map[string]bool{
		"shadowsocks": true, "vmess": true, "vless": true, "trojan": true,
		"hysteria2": true, "tuic": true, "wireguard": true, "socks": true, "http": true,
	}
	node.Protocol = strings.ToLower(strings.TrimSpace(node.Protocol))
	if !supported[node.Protocol] {
		return fmt.Errorf("unsupported sing-box protocol %q", node.Protocol)
	}
	if strings.TrimSpace(node.Name) == "" {
		return fmt.Errorf("node name is required")
	}
	if len(node.Outbound) == 0 {
		return fmt.Errorf("outbound configuration is required")
	}
	return nil
}

func publicSingBoxNodes(nodes []singBoxNode, selected ...string) []map[string]any {
	active := ""
	if len(selected) > 0 {
		active = selected[0]
	}
	result := make([]map[string]any, 0, len(nodes))
	for _, node := range nodes {
		result = append(result, map[string]any{
			"id":       node.ID,
			"name":     node.Name,
			"protocol": node.Protocol,
			"enabled":  node.Enabled,
			"active":   node.ID == active,
		})
	}
	return result
}

func singBoxControllerRequest(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://sing-box:9090"+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	return (&http.Client{Timeout: 12 * time.Second}).Do(req)
}

func (s *Server) switchSingBoxNode(ctx context.Context, id string) error {
	s.singBox.mu.Lock()
	found := false
	previous := s.singBox.Selected
	for _, node := range s.singBox.Nodes {
		if node.ID == id && node.Enabled {
			found = true
			break
		}
	}
	if found {
		s.singBox.Selected = id
	}
	s.singBox.mu.Unlock()
	if !found {
		return fmt.Errorf("node not found")
	}
	if err := s.singBox.save(); err != nil {
		s.singBox.mu.Lock()
		s.singBox.Selected = previous
		s.singBox.mu.Unlock()
		return err
	}
	body, _ := json.Marshal(map[string]string{"name": id})
	resp, err := singBoxControllerRequest(ctx, http.MethodPut, "/proxies/proxy", body)
	if err != nil {
		s.singBox.mu.Lock()
		s.singBox.Selected = previous
		s.singBox.mu.Unlock()
		_ = s.singBox.save()
		return fmt.Errorf("sing-box controller unavailable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		s.singBox.mu.Lock()
		s.singBox.Selected = previous
		s.singBox.mu.Unlock()
		_ = s.singBox.save()
		return fmt.Errorf("sing-box switch returned %s", resp.Status)
	}
	return nil
}

func checkSingBoxNode(ctx context.Context, id string) (int, error) {
	path := "/proxies/" + url.PathEscape(id) + "/delay?timeout=10000&url=" + url.QueryEscape("http://www.msftconnecttest.com/connecttest.txt")
	resp, err := singBoxControllerRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	var result struct {
		Delay int `json:"delay"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return 0, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || result.Delay <= 0 {
		return 0, fmt.Errorf("node delay test failed")
	}
	return result.Delay, nil
}

func (s *Server) singBoxNodes(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.singBox.mu.Lock()
		nodes := append([]singBoxNode(nil), s.singBox.Nodes...)
		selected := s.singBox.Selected
		s.singBox.mu.Unlock()
		jsonOut(w, map[string]any{"nodes": publicSingBoxNodes(nodes, selected), "proxyUrl": singBoxProxyURL})
	case http.MethodPut:
		action := r.URL.Query().Get("action")
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeOpenAIError(w, 400, "invalid_request_error", "node id is required")
			return
		}
		switch action {
		case "switch":
			if err := s.switchSingBoxNode(r.Context(), id); err != nil {
				writeOpenAIError(w, 400, "switch_error", err.Error())
				return
			}
			jsonOut(w, map[string]any{"ok": true, "active": id})
		case "check":
			delay, err := checkSingBoxNode(r.Context(), id)
			if err != nil {
				jsonOut(w, map[string]any{"ok": false, "reachable": false, "delayMs": 0, "error": err.Error()})
				return
			}
			jsonOut(w, map[string]any{"ok": true, "reachable": true, "delayMs": delay})
		default:
			writeOpenAIError(w, 400, "invalid_request_error", "unsupported action")
		}
	case http.MethodPost:
		var request singBoxImportRequest
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 256*1024)).Decode(&request) != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "bad json")
			return
		}
		links := strings.Fields(request.Links)
		if len(links) == 0 {
			writeOpenAIError(w, 400, "invalid_request_error", "paste at least one node link")
			return
		}
		nodes := make([]singBoxNode, 0, len(links))
		for _, link := range links {
			node, err := parseSingBoxShareLink(link)
			if err != nil {
				writeOpenAIError(w, 400, "invalid_request_error", err.Error())
				return
			}
			node.ID = newSingBoxNodeID()
			if err := validateSingBoxNode(node); err != nil {
				writeOpenAIError(w, 400, "invalid_request_error", err.Error())
				return
			}
			nodes = append(nodes, node)
		}
		s.singBox.mu.Lock()
		s.singBox.Nodes = append(s.singBox.Nodes, nodes...)
		s.singBox.mu.Unlock()
		if err := s.singBox.save(); err != nil {
			writeOpenAIError(w, 500, "storage_error", err.Error())
			return
		}
		if err := outbound.AddProxy(singBoxProxyURL); err != nil {
			writeOpenAIError(w, 500, "proxy_error", err.Error())
			return
		}
		if err := s.persistProxyPool(); err != nil {
			writeOpenAIError(w, 500, "storage_error", err.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "nodes": publicSingBoxNodes(nodes), "added": len(nodes), "restartRequired": true})
	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeOpenAIError(w, 400, "invalid_request_error", "node id is required")
			return
		}
		s.singBox.mu.Lock()
		found := false
		kept := s.singBox.Nodes[:0]
		for _, node := range s.singBox.Nodes {
			if node.ID == id {
				found = true
				continue
			}
			kept = append(kept, node)
		}
		s.singBox.Nodes = kept
		remaining := len(kept)
		s.singBox.mu.Unlock()
		if !found {
			writeOpenAIError(w, 404, "not_found", "node not found")
			return
		}
		if err := s.singBox.save(); err != nil {
			writeOpenAIError(w, 500, "storage_error", err.Error())
			return
		}
		if remaining == 0 {
			_ = outbound.RemoveProxy(singBoxProxyURL)
			_ = s.persistProxyPool()
		}
		jsonOut(w, map[string]any{"ok": true, "restartRequired": true})
	default:
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}
