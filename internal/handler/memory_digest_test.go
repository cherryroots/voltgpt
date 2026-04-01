package handler

import (
	"strings"
	"testing"

	"voltgpt/internal/memory"
)

func TestBuildMemoryDigestFields(t *testing.T) {
	fields := buildMemoryDigestFields([]memory.InteractionNote{
		{
			NoteType: "conversation",
			Title:    "Late night game planning",
			Summary:  "Picked a map rotation and decided to start after dinner.",
			NoteDate: "2026-03-28",
		},
		{
			NoteType: "topic_cluster",
			Title:    strings.Repeat("x", 220),
			Summary:  strings.Repeat("y", 1100),
			NoteDate: "2026-03-27",
		},
	})

	if len(fields) != 2 {
		t.Fatalf("len(fields) = %d, want 2", len(fields))
	}

	if !strings.Contains(fields[0].Name, "Conversation") {
		t.Fatalf("first field name = %q, want Conversation label", fields[0].Name)
	}
	if !strings.Contains(fields[0].Name, "Mar 28, 2026") {
		t.Fatalf("first field name = %q, want formatted date", fields[0].Name)
	}
	if fields[0].Value != "Picked a map rotation and decided to start after dinner." {
		t.Fatalf("first field value = %q", fields[0].Value)
	}

	if !strings.Contains(fields[1].Name, "Topic") {
		t.Fatalf("second field name = %q, want Topic label", fields[1].Name)
	}
	if len(fields[1].Name) > 256 {
		t.Fatalf("second field name len = %d, want <= 256", len(fields[1].Name))
	}
	if len(fields[1].Value) > 1024 {
		t.Fatalf("second field value len = %d, want <= 1024", len(fields[1].Value))
	}
	if !strings.HasSuffix(fields[1].Value, "...") {
		t.Fatalf("second field value = %q, want ellipsis truncation", fields[1].Value)
	}
}

func TestParseMemoryDigestPage(t *testing.T) {
	if got := parseMemoryDigestPage("memorydigest-3"); got != 3 {
		t.Fatalf("parseMemoryDigestPage valid = %d, want 3", got)
	}
	if got := parseMemoryDigestPage("memorydigest-nope"); got != 1 {
		t.Fatalf("parseMemoryDigestPage invalid = %d, want 1", got)
	}
	if got := parseMemoryDigestPage("memorydigest"); got != 1 {
		t.Fatalf("parseMemoryDigestPage missing = %d, want 1", got)
	}
}
