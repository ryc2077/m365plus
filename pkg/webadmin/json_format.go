package webadmin

import "strings"

func normalizeJSONText(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
		if before, ok := strings.CutSuffix(s, "```"); ok {
			s = strings.TrimSpace(before)
		}
	}
	return s
}
