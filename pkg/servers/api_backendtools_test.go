package servers

import (
	"testing"

	"github.com/ryc2077/m365plus/pkg/client"
)

func backendSearchCall() []client.ToolCall {
	return []client.ToolCall{{
		ID:   "backend-search",
		Type: "function",
		Function: client.ToolCallFunction{
			Name:      "search",
			Arguments: `{"query":"latest Go version"}`,
		},
	}}
}

func TestBackendToolCallsAreDropped(t *testing.T) {
	calls, finish := withoutBackendToolCalls(backendSearchCall(), "tool_calls")
	if len(calls) != 0 {
		t.Fatalf("calls = %#v, want none", calls)
	}
	if finish != "stop" {
		t.Fatalf("finish reason = %q, want stop", finish)
	}
}

func TestBackendToolCallsResetAnthropicStopReason(t *testing.T) {
	_, finish := withoutBackendToolCalls(backendSearchCall(), "tool_use")
	if finish != "stop" {
		t.Fatalf("finish reason = %q, want stop", finish)
	}
}

func TestWithoutBackendToolCallsPreservesUnrelatedFinishReason(t *testing.T) {
	_, finish := withoutBackendToolCalls(backendSearchCall(), "length")
	if finish != "length" {
		t.Fatalf("finish reason = %q, want length", finish)
	}
	calls, finish := withoutBackendToolCalls(nil, "length")
	if calls != nil || finish != "length" {
		t.Fatalf("empty turn = (%#v, %q), want (nil, length)", calls, finish)
	}
}
