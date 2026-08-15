package webadmin

import (
	"encoding/base64"
	"testing"
)

func TestValidateSingBoxNode(t *testing.T) {
	node := singBoxNode{Name: "test", Protocol: "vless", Outbound: map[string]any{"server": "example.com", "server_port": 443}}
	if err := validateSingBoxNode(node); err != nil {
		t.Fatalf("valid node rejected: %v", err)
	}
	node.Protocol = "unsupported"
	if err := validateSingBoxNode(node); err == nil {
		t.Fatal("unsupported protocol accepted")
	}
}

func TestParseVLESSShareLink(t *testing.T) {
	node, err := parseSingBoxShareLink("vless://11111111-1111-1111-1111-111111111111@104.18.80.2:80/?type=ws&encryption=none&host=edge.example.com&path=%2F%3Fed%3D2048#Mobile")
	if err != nil {
		t.Fatal(err)
	}
	if node.Protocol != "vless" || node.Name != "Mobile" {
		t.Fatalf("unexpected node: %#v", node)
	}
	if node.Outbound["uuid"] != "11111111-1111-1111-1111-111111111111" {
		t.Fatal("UUID not parsed")
	}
	transport, ok := node.Outbound["transport"].(map[string]any)
	if !ok || transport["type"] != "ws" || transport["path"] != "/" {
		t.Fatalf("unexpected transport: %#v", transport)
	}
	if transport["max_early_data"] != 2048 || transport["early_data_header_name"] != "Sec-WebSocket-Protocol" {
		t.Fatalf("WebSocket early data was not converted: %#v", transport)
	}
}

func TestParseVMessShareLink(t *testing.T) {
	payload := base64.StdEncoding.EncodeToString([]byte(`{"v":"2","ps":"VMess node","add":"example.com","port":"443","id":"11111111-1111-1111-1111-111111111111","net":"ws","path":"/ws","host":"example.com","tls":"tls"}`))
	node, err := parseSingBoxShareLink("vmess://" + payload)
	if err != nil {
		t.Fatal(err)
	}
	if node.Protocol != "vmess" || node.Name != "VMess node" {
		t.Fatalf("unexpected node: %#v", node)
	}
}

func TestPublicSingBoxNodesRedactsOutbound(t *testing.T) {
	nodes := []singBoxNode{{ID: "node-1", Name: "secret", Protocol: "trojan", Enabled: true, Outbound: map[string]any{"password": "sensitive"}}}
	public := publicSingBoxNodes(nodes)
	if len(public) != 1 {
		t.Fatalf("unexpected node count: %d", len(public))
	}
	if _, exposed := public[0]["outbound"]; exposed {
		t.Fatal("outbound secrets exposed")
	}
}
