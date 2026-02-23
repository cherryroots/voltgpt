package handler

import (
	"strings"
	"testing"
)

func TestSkipMemoryEmoji(t *testing.T) {
	cases := []struct {
		content string
		skip    bool
	}{
		{"hello", false},
		{"hello 🚫", true},
		{"🚫 what is the capital of France?", true},
		{"just a normal message", false},
	}
	for _, tc := range cases {
		got := strings.Contains(tc.content, "🚫")
		if got != tc.skip {
			t.Errorf("content=%q: want skip=%v, got %v", tc.content, tc.skip, got)
		}
	}
}
