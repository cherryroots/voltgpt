package handler

import (
	"strings"
	"testing"
)

func TestEditResolution(t *testing.T) {
	tests := []struct {
		aspect float64
		want   string
	}{
		{1, "2048*2048"},
		{1.5, "3072*2048"},
		{3, "4096*1365"},
		{0.5, "2048*4096"},
		{0.25, "1024*4096"},
	}
	for _, tt := range tests {
		if got := editResolution(tt.aspect); got != tt.want {
			t.Errorf("editResolution(%v) = %q, want %q", tt.aspect, got, tt.want)
		}
	}
}

func TestTruncateMessage(t *testing.T) {
	if got := truncateMessage("short"); got != "short" {
		t.Errorf("truncateMessage(short) = %q", got)
	}
	got := truncateMessage(strings.Repeat("a", 2500))
	if len(got) != 2000 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncateMessage(long) has length %d, suffix %q", len(got), got[len(got)-3:])
	}
}
