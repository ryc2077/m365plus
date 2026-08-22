package servers

import (
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/models"
)

func TestReasoningEffortValidationAndThreshold(t *testing.T) {
	cases := map[string]bool{
		"none": false, "minimal": false, "low": false,
		"MEDIUM": true, " high ": true, "xhigh": true, "max": true,
	}
	for effort, want := range cases {
		got, err := reasoningEffortRequestsDeliberation(&responsesReasoning{Effort: effort})
		if err != nil || got != want {
			t.Fatalf("effort %q: deliberate=%v err=%v, want %v", effort, got, err, want)
		}
	}
	_, err := reasoningEffortRequestsDeliberation(&responsesReasoning{Effort: "turbo"})
	if err == nil || !strings.Contains(err.Error(), "turbo") {
		t.Fatalf("invalid effort error = %v", err)
	}
}

func TestApplyReasoningEffortRoutesOnlyToExistingVariant(t *testing.T) {
	base := models.ModelRegistry["gpt5.5"]
	if got := applyReasoningEffort("gpt-5.5", base, true); got.Tone != models.ModelRegistry["gpt5.5-reasoning"].Tone {
		t.Fatalf("advertised model ID was not routed to reasoning variant: %q", got.Tone)
	}
	variant := models.ModelRegistry["gpt5.5-reasoning"]
	if got := applyReasoningEffort("gpt5.5-reasoning", variant, true); got.Tone != variant.Tone {
		t.Fatalf("reasoning variant was rerouted to %q", got.Tone)
	}
	if got := applyReasoningEffort("gpt5.5", base, false); got.Tone != base.Tone {
		t.Fatalf("low effort rerouted model to %q", got.Tone)
	}
}
