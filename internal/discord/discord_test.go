package discord

import "testing"

func TestSuppressLinkEmbeds(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "bare link", content: "See https://example.com/page", want: "See <https://example.com/page>"},
		{name: "multiple links", content: "https://one.test and http://two.test/x", want: "<https://one.test> and <http://two.test/x>"},
		{name: "already suppressed", content: "See <https://example.com/page>", want: "See <https://example.com/page>"},
		{name: "trailing punctuation", content: "See https://example.com/page, then continue.", want: "See <https://example.com/page>, then continue."},
		{name: "no links", content: "Nothing to preview", want: "Nothing to preview"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suppressLinkEmbeds(tt.content); got != tt.want {
				t.Fatalf("suppressLinkEmbeds(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}
