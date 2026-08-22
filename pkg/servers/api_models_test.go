package servers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ryc2077/m365plus/pkg/models"
)

func fetchModelCatalog(t *testing.T, cfg *models.Config) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	api := &APIServer{config: cfg}
	api.handleModels(recorder, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d", recorder.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode catalog: %v", err)
	}
	return body
}

func TestModelsCatalogIsStableDeduplicatedAndCompatible(t *testing.T) {
	cfg := &models.Config{ContextWindowTokens: 200000, MaxOutputTokens: 64000}
	first := fetchModelCatalog(t, cfg)
	second := fetchModelCatalog(t, cfg)
	firstData := first["data"].([]any)
	secondData := second["data"].([]any)
	if len(firstData) != len(secondData) || len(firstData) == 0 {
		t.Fatalf("unstable or empty catalog")
	}
	seen := map[string]bool{}
	for i, raw := range firstData {
		entry := raw.(map[string]any)
		id := entry["id"].(string)
		if seen[id] {
			t.Fatalf("duplicate model %q", id)
		}
		seen[id] = true
		if id != secondData[i].(map[string]any)["id"] {
			t.Fatalf("catalog order changed")
		}
		if entry["max_input_tokens"].(float64) != 136000 || entry["supports_tools"] != true {
			t.Fatalf("incompatible model entry: %#v", entry)
		}
		if entry["base_instructions"] == "" || entry["apply_patch_tool_type"] != "freeform" {
			t.Fatalf("missing Codex fields for %q", id)
		}
	}
	if first["has_more"] != false || first["first_id"] == nil || first["last_id"] == nil {
		t.Fatalf("missing Anthropic pagination fields: %#v", first)
	}
}

func TestModelsInputBudgetNeverBecomesZero(t *testing.T) {
	body := fetchModelCatalog(t, &models.Config{ContextWindowTokens: 1000000, MaxOutputTokens: 1000000})
	entry := body["data"].([]any)[0].(map[string]any)
	if entry["max_input_tokens"].(float64) != 1000000 {
		t.Fatalf("max_input_tokens = %v", entry["max_input_tokens"])
	}
}
