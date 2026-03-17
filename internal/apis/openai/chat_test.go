package openai

import "testing"

func TestStreamer_HasVisibleOutput(t *testing.T) {
	s := NewStreamer(nil, nil)

	s.Update(" \n\t ")
	if s.HasVisibleOutput() {
		t.Fatal("HasVisibleOutput() = true, want false for whitespace-only output")
	}

	s.Update("hello")
	if !s.HasVisibleOutput() {
		t.Fatal("HasVisibleOutput() = false, want true after visible output")
	}
}
