package servers

import (
	"testing"

	"github.com/ryc2077/m365plus/pkg/client"
)

func goalUserItem(text string) map[string]any {
	return map[string]any{
		"role":    "user",
		"content": []any{map[string]any{"type": "input_text", "text": text}},
	}
}

func updateGoalCall(callID string) map[string]any {
	return map[string]any{
		"type": "function_call", "call_id": callID,
		"name": updateGoalToolName, "arguments": `{"status":"in_progress"}`,
	}
}

func updateGoalOutput(callID, status string) map[string]any {
	return map[string]any{
		"type": "function_call_output", "call_id": callID,
		"output": `{"goal":{"status":"` + status + `"}}`,
	}
}

func TestGoalContinuationState(t *testing.T) {
	if responsesGoalContinuationOpen([]any{goalUserItem("plain question")}) {
		t.Fatal("plain request was read as an open goal")
	}
	open := []any{
		goalUserItem(goalContextMarker + "ship the parser</codex_internal_context>"),
		updateGoalCall("call_1"), updateGoalOutput("call_1", "in_progress"),
	}
	if !responsesGoalContinuationOpen(open) {
		t.Fatal("in-progress goal was read as closed")
	}
	for _, status := range []string{"complete", "blocked"} {
		closed := []any{
			goalUserItem(goalContextMarker + "ship the parser</codex_internal_context>"),
			updateGoalCall("call_1"), updateGoalOutput("call_1", status),
		}
		if responsesGoalContinuationOpen(closed) {
			t.Errorf("status %q left the goal open", status)
		}
	}
}

func TestGoalContinuationReadsCustomToolReport(t *testing.T) {
	input := []any{
		goalUserItem(goalContextMarker + "ship the parser</codex_internal_context>"),
		map[string]any{"type": "custom_tool_call", "call_id": "ctc_1", "name": updateGoalToolName},
		map[string]any{"type": "custom_tool_call_output", "call_id": "ctc_1", "output": `{"goal":{"status":"complete"}}`},
	}
	if responsesGoalContinuationOpen(input) {
		t.Fatal("custom update_goal report did not close the goal")
	}
}

func TestMessagePhaseFollowsToolCallsAndGoal(t *testing.T) {
	calls := []client.ToolCall{{ID: "call_1"}}
	if got := responsesMessagePhase(false, nil); got != "final_answer" {
		t.Errorf("plain answer phase = %q", got)
	}
	if got := responsesMessagePhase(false, calls); got != "commentary" {
		t.Errorf("tool-calling phase = %q", got)
	}
	if got := responsesMessagePhase(true, nil); got != "commentary" {
		t.Errorf("open-goal phase = %q", got)
	}
}
