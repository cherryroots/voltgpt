package openai

import (
	"context"
	"errors"
	"testing"
)

func TestStreamer_HasVisibleOutput(t *testing.T) {
	s := newStreamer(nil, nil)

	s.Update(" \n\t ")
	if s.HasVisibleOutput() {
		t.Fatal("HasVisibleOutput() = true, want false for whitespace-only output")
	}

	s.Update("hello")
	if !s.HasVisibleOutput() {
		t.Fatal("HasVisibleOutput() = false, want true after visible output")
	}
}

func TestStreamMessageResponse_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := StreamMessageResponse(ctx, nil, nil, nil, nil, "", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StreamMessageResponse() error = %v, want context.Canceled", err)
	}
}
