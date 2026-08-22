package servers

import "testing"

func TestAnthropicToolChoiceEnforcement(t *testing.T) {
	cases := []struct {
		name   string
		choice map[string]any
		want   string
	}{
		{"absent", nil, ""},
		{"auto", map[string]any{"type": "auto"}, "auto"},
		{"any", map[string]any{"type": "any"}, "any"},
		{"none", map[string]any{"type": "none"}, "none"},
		{"pinned", map[string]any{"type": "tool", "name": "get_weather"}, "get_weather"},
		{"pinned without name", map[string]any{"type": "tool"}, ""},
	}
	for _, testCase := range cases {
		if got := anthropicToolChoiceEnforcement(testCase.choice); got != testCase.want {
			t.Fatalf("%s: got %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

func TestToolChoiceStringResolvesPinnedFunction(t *testing.T) {
	cases := []struct {
		name   string
		choice any
		want   string
	}{
		{"absent", nil, ""},
		{"auto", "auto", "auto"},
		{"none", "none", "none"},
		{"required", "required", "required"},
		{"pinned", map[string]any{"type": "function", "function": map[string]any{"name": "send_email"}}, "send_email"},
	}
	for _, testCase := range cases {
		if got := toolChoiceString(testCase.choice); got != testCase.want {
			t.Fatalf("%s: got %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
