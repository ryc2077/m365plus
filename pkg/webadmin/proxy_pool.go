package webadmin

import (
	"encoding/json"
	"github.com/ryc2077/m365plus/pkg/webadmin/outbound"
	"net/http"
	"net/url"
	"strings"
)

func isSingBoxShareLink(raw string) bool {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	switch strings.ToLower(parsed.Scheme) {
	case "vless", "vmess", "trojan", "ss", "hysteria2", "hy2", "tuic":
		return true
	default:
		return false
	}
}

func (s *Server) importSingBoxLinks(links []string) ([]singBoxNode, error) {
	nodes := make([]singBoxNode, 0, len(links))
	for _, link := range links {
		node, err := parseSingBoxShareLink(link)
		if err != nil {
			return nil, err
		}
		node.ID = newSingBoxNodeID()
		if err := validateSingBoxNode(node); err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	if len(nodes) == 0 {
		return nodes, nil
	}
	s.singBox.mu.Lock()
	s.singBox.Nodes = append(s.singBox.Nodes, nodes...)
	s.singBox.mu.Unlock()
	if err := s.singBox.save(); err != nil {
		return nil, err
	}
	return nodes, nil
}

func (s *Server) persistProxyPool() error {
	v := s.settings.get()
	items := outbound.ProxyPoolStatus()
	v.ProxyPool = make([]string, 0, len(items))
	for _, item := range items {
		if raw, ok := item["url"].(string); ok {
			v.ProxyPool = append(v.ProxyPool, raw)
		}
	}
	return s.settings.save(v)
}

func proxyEnabled(settings runtimeSettings) bool {
	return settings.ProxyEnabled == nil || *settings.ProxyEnabled
}

func (s *Server) setProxyEnabled(enabled bool) error {
	settings := s.settings.get()
	if enabled {
		if err := outbound.ConfigurePool(settings.ProxyPool); err != nil {
			return err
		}
	} else if err := outbound.Configure(""); err != nil {
		return err
	}
	settings.ProxyEnabled = &enabled
	return s.settings.save(settings)
}

func (s *Server) proxyPool(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPut && r.URL.Query().Get("action") == "toggle" {
		var body struct {
			Enabled *bool `json:"enabled"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body) != nil || body.Enabled == nil {
			writeOpenAIError(w, 400, "invalid_request_error", "enabled is required")
			return
		}
		if err := s.setProxyEnabled(*body.Enabled); err != nil {
			writeOpenAIError(w, 500, "proxy_error", err.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "enabled": *body.Enabled})
		return
	}
	if r.Method == http.MethodPut && r.URL.Query().Get("action") == "check" {
		p := outbound.CurrentPool()
		if p == nil {
			jsonOut(w, map[string]any{"ok": true, "proxies": []map[string]any{}})
			return
		}
		jsonOut(w, map[string]any{"ok": true, "proxies": p.CheckAll(r.Context())})
		return
	}
	switch r.Method {
	case http.MethodGet:
		proxies := outbound.ProxyPoolStatus()
		s.singBox.mu.Lock()
		nodes := append([]singBoxNode(nil), s.singBox.Nodes...)
		selected := s.singBox.Selected
		s.singBox.mu.Unlock()
		jsonOut(w, map[string]any{"enabled": proxyEnabled(s.settings.get()), "proxies": proxies, "singBoxNodes": publicSingBoxNodes(nodes, selected)})
	case http.MethodPost:
		var body struct {
			URL  string   `json:"url"`
			URLs []string `json:"urls"`
		}
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 64*1024)).Decode(&body) != nil {
			writeOpenAIError(w, 400, "invalid_request_error", "bad json")
			return
		}
		urls := append(body.URLs, body.URL)
		directLinks := make([]string, 0)
		singBoxLinks := make([]string, 0)
		for _, raw := range urls {
			for _, v := range strings.FieldsFunc(raw, func(r rune) bool { return r == '\n' || r == '\r' || r == ',' }) {
				link := strings.TrimSpace(v)
				if link == "" {
					continue
				}
				if isSingBoxShareLink(link) {
					singBoxLinks = append(singBoxLinks, link)
				} else {
					directLinks = append(directLinks, link)
				}
			}
		}
		for _, link := range directLinks {
			if err := outbound.AddProxy(link); err != nil {
				writeOpenAIError(w, 400, "invalid_request_error", err.Error())
				return
			}
		}
		nodes, err := s.importSingBoxLinks(singBoxLinks)
		if err != nil {
			writeOpenAIError(w, 400, "invalid_request_error", err.Error())
			return
		}
		if len(nodes) > 0 {
			if err := outbound.AddProxy(singBoxProxyURL); err != nil {
				writeOpenAIError(w, 500, "proxy_error", err.Error())
				return
			}
		}
		if err := s.persistProxyPool(); err != nil {
			writeOpenAIError(w, 500, "storage_error", err.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "added": len(directLinks) + len(nodes), "directAdded": len(directLinks), "singBoxAdded": len(nodes), "proxies": outbound.ProxyPoolStatus(), "singBoxNodes": publicSingBoxNodes(nodes)})
	case http.MethodDelete:
		raw := strings.TrimRight(strings.TrimSpace(r.URL.Query().Get("url")), "/")
		if raw == "" {
			if err := outbound.ConfigurePool(nil); err != nil {
				writeOpenAIError(w, 400, "invalid_request_error", err.Error())
				return
			}
		} else if err := outbound.RemoveProxy(raw); err != nil {
			writeOpenAIError(w, 400, "invalid_request_error", err.Error())
			return
		}
		if err := s.persistProxyPool(); err != nil {
			writeOpenAIError(w, 500, "storage_error", err.Error())
			return
		}
		jsonOut(w, map[string]any{"ok": true, "proxies": outbound.ProxyPoolStatus()})
	default:
		writeOpenAIError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed")
	}
}
