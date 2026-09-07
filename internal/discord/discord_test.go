package discord

import "testing"

func TestFormatMessageContentSandboxLinks(t *testing.T) {
	for _, tt := range []struct{ input, want string }{
		{"[Download your file](sandbox:/mnt/data/download.txt)", "[Download your file](sandbox:/mnt/data/download.txt)"},
		{"Normal text and https://example.com", "Normal text and <https://example.com>"},
	} {
		if got := formatMessageContent(tt.input); got != tt.want {
			t.Errorf("formatMessageContent(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

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
		{name: "parenthesized citation", content: "([learn.microsoft.com](https://learn.microsoft.com/en-us/powershell/module/microsoft.online.sharepoint.powershell/get-spom365agentaccessinsightsreport?view=sharepoint-ps))", want: "([learn.microsoft.com](<https://learn.microsoft.com/en-us/powershell/module/microsoft.online.sharepoint.powershell/get-spom365agentaccessinsightsreport?view=sharepoint-ps>))"},
		{name: "markdown link", content: "[Example](https://example.com/page)", want: "[Example](<https://example.com/page>)"},
		{name: "parenthesized bare link", content: "(https://example.com/page).", want: "(<https://example.com/page>)."},
		{name: "balanced URL parentheses", content: "([Wiki](https://en.wikipedia.org/wiki/Function_(mathematics)))", want: "([Wiki](<https://en.wikipedia.org/wiki/Function_(mathematics)>))"},
		{name: "text after destination", content: "[Example](https://example.com/page),more", want: "[Example](<https://example.com/page>),more"},
		{name: "already suppressed citation", content: "([Example](<https://example.com/page>))", want: "([Example](<https://example.com/page>))"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := suppressLinkEmbeds(tt.content); got != tt.want {
				t.Fatalf("suppressLinkEmbeds(%q) = %q, want %q", tt.content, got, tt.want)
			}
			if got := suppressLinkEmbeds(tt.want); got != tt.want {
				t.Fatalf("second pass = %q, want %q", got, tt.want)
			}
		})
	}
}
