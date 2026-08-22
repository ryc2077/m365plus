package servers

import (
	"strings"
	"testing"

	"github.com/ryc2077/m365plus/pkg/client"
	"github.com/ryc2077/m365plus/pkg/toolcalling"
)

func TestResponsesCustomToolUsesStableItemIDAndInputEvents(t *testing.T) {
	types := responsesToolTypes([]toolcalling.ToolDef{{Type: "custom", Name: "run_script"}})
	call := client.ToolCall{ID: "call_abc", Type: "function", Function: client.ToolCallFunction{Name: "run_script", Arguments: "echo hi"}}
	added := buildResponsesToolCallItem("call_abc", call, types, "in_progress")
	done := buildResponsesToolCallItem("call_abc", call, types, "completed")
	if added["id"] != done["id"] || !strings.HasPrefix(added["id"].(string), "ctc_") {
		t.Fatalf("unstable custom item id: added=%v done=%v", added["id"], done["id"])
	}
	events := responsesToolInputEvents("call_abc", call, types, 3)
	if len(events) != 2 || events[0].name != "response.custom_tool_call_input.delta" || events[1].name != "response.custom_tool_call_input.done" {
		t.Fatalf("unexpected custom input events: %#v", events)
	}
	for _, event := range events {
		if event.data["item_id"] != added["id"] || event.data["output_index"] != 3 {
			t.Fatalf("event does not match announced item: %#v", event)
		}
	}
}

func TestResponsesFunctionAndSearchToolInputEvents(t *testing.T) {
	functionTypes := responsesToolTypes([]toolcalling.ToolDef{{Type: "function", Name: "weather"}})
	call := client.ToolCall{ID: "call_fn", Type: "function", Function: client.ToolCallFunction{Name: "weather", Arguments: `{"city":"Istanbul"}`}}
	events := responsesToolInputEvents("call_fn", call, functionTypes, 0)
	if len(events) != 2 || events[0].name != "response.function_call_arguments.delta" || events[1].name != "response.function_call_arguments.done" {
		t.Fatalf("unexpected function input events: %#v", events)
	}
	searchTypes := responsesToolTypes([]toolcalling.ToolDef{{Type: "tool_search", Name: "search"}})
	call.Function.Name = "search"
	if events := responsesToolInputEvents("call_search", call, searchTypes, 0); len(events) != 0 {
		t.Fatalf("tool_search emitted input events: %#v", events)
	}
}
