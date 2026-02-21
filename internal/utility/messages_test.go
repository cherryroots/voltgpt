package utility

import (
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestSplitParagraph(t *testing.T) {
	tests := []struct {
		name          string
		message       string
		wantFirstLen  int // approximate check on first part length
		wantFirstPart string
		wantLastPart  string
	}{
		{
			name:          "split on double newline",
			message:       strings.Repeat("a", 100) + "\n\n" + strings.Repeat("b", 100),
			wantFirstPart: strings.Repeat("a", 100),
			wantLastPart:  strings.Repeat("b", 100),
		},
		{
			name:          "split on single newline when no double",
			message:       strings.Repeat("a", 100) + "\n" + strings.Repeat("b", 100),
			wantFirstPart: strings.Repeat("a", 100),
			wantLastPart:  strings.Repeat("b", 100),
		},
		{
			name:          "no newline forces split at 1990",
			message:       strings.Repeat("a", 2500),
			wantFirstPart: strings.Repeat("a", 1990),
			wantLastPart:  strings.Repeat("a", 510),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			first, last := SplitParagraph(tt.message)
			if tt.wantFirstPart != "" && first != tt.wantFirstPart {
				t.Errorf("SplitParagraph() first = %q, want %q", first, tt.wantFirstPart)
			}
			if tt.wantLastPart != "" && last != tt.wantLastPart {
				t.Errorf("SplitParagraph() last = %q, want %q", last, tt.wantLastPart)
			}
		})
	}
}

func TestSplitParagraphCodeBlock(t *testing.T) {
	// When first part has odd number of ``` it should add closing ```
	// and prepend the language to the last part
	message := strings.Repeat("a", 100) + "\n\n" + "```go\n" + strings.Repeat("x", 100) + "\n\n" + strings.Repeat("y", 100) + "\n```"
	first, last := SplitParagraph(message)

	// first part should end with ``` to close the open code block
	if !strings.HasSuffix(first, "```") {
		t.Errorf("SplitParagraph() first part should end with ``` to close code block, got: %q", first)
	}
	// last part should start with the language identifier
	if !strings.HasPrefix(last, "```go") {
		t.Errorf("SplitParagraph() last part should start with language code, got: %q", last)
	}
}

func TestSplitMessageSlices(t *testing.T) {
	t.Run("short message returns single slice", func(t *testing.T) {
		msg := "short message"
		parts := SplitMessageSlices(msg)
		if len(parts) != 1 {
			t.Errorf("SplitMessageSlices() len = %d, want 1", len(parts))
		}
		if parts[0] != msg {
			t.Errorf("SplitMessageSlices() = %q, want %q", parts[0], msg)
		}
	})

	t.Run("message at exactly 1800 chars returns single slice", func(t *testing.T) {
		msg := strings.Repeat("a", 1800)
		parts := SplitMessageSlices(msg)
		if len(parts) != 1 {
			t.Errorf("SplitMessageSlices() len = %d, want 1", len(parts))
		}
	})

	t.Run("message over 1800 chars splits", func(t *testing.T) {
		msg := strings.Repeat("a", 1000) + "\n\n" + strings.Repeat("b", 1000)
		parts := SplitMessageSlices(msg)
		if len(parts) < 2 {
			t.Errorf("SplitMessageSlices() expected at least 2 parts, got %d", len(parts))
		}
		// Verify no part is empty
		for i, part := range parts {
			if part == "" {
				t.Errorf("SplitMessageSlices() part %d is empty", i)
			}
		}
	})

	t.Run("all parts fit within discord limit", func(t *testing.T) {
		// Build a large message with multiple paragraph breaks
		var builder strings.Builder
		for i := 0; i < 20; i++ {
			builder.WriteString(strings.Repeat("x", 200))
			builder.WriteString("\n\n")
		}
		msg := builder.String()

		parts := SplitMessageSlices(msg)
		for i, part := range parts {
			if len(part) > 2000 {
				t.Errorf("SplitMessageSlices() part %d has length %d > 2000", i, len(part))
			}
		}
	})

	t.Run("code block closing preserved on split", func(t *testing.T) {
		// Create a message that will split in the middle of a code block
		pre := strings.Repeat("a", 100) + "\n\n"
		codeBlock := "```go\n" + strings.Repeat("x", 1800) + "\n```"
		msg := pre + codeBlock

		parts := SplitMessageSlices(msg)
		if len(parts) < 2 {
			// If it doesn't need splitting, that's OK
			return
		}
		// If the first part has a code block opening, it should have a closing too
		for i, part := range parts {
			count := strings.Count(part, "```")
			if count%2 != 0 {
				t.Errorf("SplitMessageSlices() part %d has odd number of ``` (%d)", i, count)
			}
		}
	})

	t.Run("empty message returns empty slice", func(t *testing.T) {
		parts := SplitMessageSlices("")
		if len(parts) != 1 || parts[0] != "" {
			t.Errorf("SplitMessageSlices(\"\") = %v, want [\"\"]", parts)
		}
	})
}

func TestCheckCache(t *testing.T) {
	msg1 := &discordgo.Message{ID: "111"}
	msg2 := &discordgo.Message{ID: "222"}
	msg3 := &discordgo.Message{ID: "333"}
	cache := []*discordgo.Message{msg1, msg2, msg3}

	t.Run("found in cache", func(t *testing.T) {
		got := checkCache(cache, "222")
		if got != msg2 {
			t.Errorf("checkCache() = %v, want %v", got, msg2)
		}
	})

	t.Run("not found returns nil", func(t *testing.T) {
		got := checkCache(cache, "999")
		if got != nil {
			t.Errorf("checkCache() = %v, want nil", got)
		}
	})

	t.Run("first element found", func(t *testing.T) {
		got := checkCache(cache, "111")
		if got != msg1 {
			t.Errorf("checkCache() = %v, want %v", got, msg1)
		}
	})

	t.Run("last element found", func(t *testing.T) {
		got := checkCache(cache, "333")
		if got != msg3 {
			t.Errorf("checkCache() = %v, want %v", got, msg3)
		}
	})

	t.Run("empty cache returns nil", func(t *testing.T) {
		got := checkCache([]*discordgo.Message{}, "111")
		if got != nil {
			t.Errorf("checkCache() = %v, want nil", got)
		}
	})

	t.Run("nil cache returns nil", func(t *testing.T) {
		got := checkCache(nil, "111")
		if got != nil {
			t.Errorf("checkCache() = %v, want nil", got)
		}
	})
}
