package gemini

import (
	"testing"

	"google.golang.org/genai"
)

func TestContentToString_Empty(t *testing.T) {
	c := &genai.Content{Parts: []*genai.Part{}}
	if got := contentToString(c); got != "" {
		t.Errorf("got %q, want empty string", got)
	}
}

func TestContentToString_SinglePart(t *testing.T) {
	c := &genai.Content{Parts: []*genai.Part{{Text: "hello"}}}
	want := "hello\n"
	if got := contentToString(c); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestContentToString_MultipleParts(t *testing.T) {
	c := &genai.Content{Parts: []*genai.Part{
		{Text: "foo"},
		{Text: "bar"},
	}}
	want := "foo\nbar\n"
	if got := contentToString(c); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestContentToString_NonTextPartSkipped(t *testing.T) {
	// A part with empty Text (e.g. InlineData) should contribute nothing.
	c := &genai.Content{Parts: []*genai.Part{
		{Text: ""},
		{Text: "baz"},
	}}
	want := "baz\n"
	if got := contentToString(c); got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
