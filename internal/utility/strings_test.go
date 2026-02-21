package utility

import (
	"testing"
)

func TestMatchMultiple(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		matches []string
		want    bool
	}{
		{
			name:    "exact match found",
			input:   "hello",
			matches: []string{"world", "hello", "foo"},
			want:    true,
		},
		{
			name:    "no match",
			input:   "hello",
			matches: []string{"world", "foo", "bar"},
			want:    false,
		},
		{
			name:    "empty input",
			input:   "",
			matches: []string{"world", "foo"},
			want:    false,
		},
		{
			name:    "empty matches slice",
			input:   "hello",
			matches: []string{},
			want:    false,
		},
		{
			name:    "nil matches slice",
			input:   "hello",
			matches: nil,
			want:    false,
		},
		{
			name:    "single element match",
			input:   "tenor",
			matches: []string{"tenor"},
			want:    true,
		},
		{
			name:    "case sensitive no match",
			input:   "Hello",
			matches: []string{"hello"},
			want:    false,
		},
		{
			name:    "match last element",
			input:   "baz",
			matches: []string{"foo", "bar", "baz"},
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchMultiple(tt.input, tt.matches)
			if got != tt.want {
				t.Errorf("MatchMultiple(%q, %v) = %v, want %v", tt.input, tt.matches, got, tt.want)
			}
		})
	}
}

func TestReplaceMultiple(t *testing.T) {
	tests := []struct {
		name       string
		str        string
		oldStrings []string
		newString  string
		want       string
	}{
		{
			name:       "replace single occurrence",
			str:        "hello world",
			oldStrings: []string{"world"},
			newString:  "earth",
			want:       "hello earth",
		},
		{
			name:       "replace multiple different strings",
			str:        "foo bar baz",
			oldStrings: []string{"foo", "baz"},
			newString:  "X",
			want:       "X bar X",
		},
		{
			name:       "replace with empty string",
			str:        "hello world",
			oldStrings: []string{"world"},
			newString:  "",
			want:       "hello ",
		},
		{
			name:       "empty oldStrings returns original",
			str:        "hello world",
			oldStrings: []string{},
			newString:  "X",
			want:       "hello world",
		},
		{
			name:       "empty input returns empty",
			str:        "",
			oldStrings: []string{"foo"},
			newString:  "bar",
			want:       "",
		},
		{
			name:       "nil oldStrings returns original",
			str:        "hello",
			oldStrings: nil,
			newString:  "X",
			want:       "hello",
		},
		{
			name:       "replace multiple occurrences of same string",
			str:        "aaa",
			oldStrings: []string{"a"},
			newString:  "b",
			want:       "bbb",
		},
		{
			name:       "no match returns original",
			str:        "hello world",
			oldStrings: []string{"xyz"},
			newString:  "abc",
			want:       "hello world",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ReplaceMultiple(tt.str, tt.oldStrings, tt.newString)
			if got != tt.want {
				t.Errorf("ReplaceMultiple(%q, %v, %q) = %q, want %q", tt.str, tt.oldStrings, tt.newString, got, tt.want)
			}
		})
	}
}

func TestExtractPairText(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		lookup string
		want   string
	}{
		{
			name:   "extract simple pair",
			text:   "before ***middle*** after",
			lookup: "***",
			want:   "middle",
		},
		{
			name:   "extract with backtick pairs",
			text:   "text `code` more",
			lookup: "`",
			want:   "code",
		},
		{
			name:   "no pair found returns empty",
			text:   "no pair here",
			lookup: "***",
			want:   "",
		},
		{
			name:   "odd occurrences returns empty",
			text:   "one *** two *** three *** end",
			lookup: "***",
			want:   "",
		},
		{
			name:   "empty text returns empty",
			text:   "",
			lookup: "***",
			want:   "",
		},
		{
			name:   "extract multichar delimiter",
			text:   "start ```block``` end",
			lookup: "```",
			want:   "block",
		},
		{
			name:   "extract from beginning",
			text:   "***content*** rest",
			lookup: "***",
			want:   "content",
		},
		{
			name:   "extract from end",
			text:   "rest ***content***",
			lookup: "***",
			want:   "content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractPairText(tt.text, tt.lookup)
			if got != tt.want {
				t.Errorf("ExtractPairText(%q, %q) = %q, want %q", tt.text, tt.lookup, got, tt.want)
			}
		})
	}
}

func TestContainsPair(t *testing.T) {
	tests := []struct {
		name   string
		text   string
		lookup string
		want   bool
	}{
		{
			name:   "even count pair",
			text:   "a***b***c",
			lookup: "***",
			want:   true,
		},
		{
			name:   "odd count no pair",
			text:   "a***b***c***d",
			lookup: "***",
			want:   false,
		},
		{
			name:   "not present",
			text:   "no lookup here",
			lookup: "***",
			want:   false,
		},
		{
			name:   "exactly two occurrences",
			text:   "***middle***",
			lookup: "***",
			want:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := containsPair(tt.text, tt.lookup)
			if got != tt.want {
				t.Errorf("containsPair(%q, %q) = %v, want %v", tt.text, tt.lookup, got, tt.want)
			}
		})
	}
}
