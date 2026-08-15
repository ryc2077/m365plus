package models

import "testing"

func TestGPT56NamedVariants(t *testing.T) {
	tests := []struct {
		model string
		tone  string
	}{
		{model: "gpt-5.6-sol", tone: "Gpt_5_6_Reasoning"},
		{model: "gpt-5.6-terra", tone: "Gpt_5_6_Reasoning"},
		{model: "gpt-5.6-luna", tone: "Gpt_5_6_Reasoning"},
	}
	for _, test := range tests {
		t.Run(test.model, func(t *testing.T) {
			config := LookupModel(test.model)
			if config.OpenAIID != test.model {
				t.Fatalf("OpenAIID = %q, want %q", config.OpenAIID, test.model)
			}
			if config.Tone != test.tone {
				t.Fatalf("Tone = %q, want %q", config.Tone, test.tone)
			}
		})
	}
}
