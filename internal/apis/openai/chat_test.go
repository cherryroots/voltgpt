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

func TestSafeBaseURLForLog_RedactsCredentialsAndQuery(t *testing.T) {
	got := safeBaseURLForLog("https://user:secret@example.com/v1?token=secret")
	if got != "https://example.com" {
		t.Fatalf("safeBaseURLForLog() = %q, want %q", got, "https://example.com")
	}
}

func TestStreamer_StopWaitsForTicker(t *testing.T) {
	s := newStreamer(nil, nil)
	s.Start()

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
