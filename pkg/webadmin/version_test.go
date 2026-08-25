package webadmin

import "testing"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"1.3.20", "1.3.19", 1},
		{"1.3.20", "1.3.20", 0},
		{"1.3.19", "1.3.20", -1},
		{"2.0.0", "1.99.99", 1},
		{"1.3.20-beta.1", "1.3.19", 1},
	}
	for _, test := range tests {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Fatalf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}
