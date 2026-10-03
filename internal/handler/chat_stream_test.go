package handler

import (
	"context"
	"errors"
	"testing"

	"github.com/bwmarrin/discordgo"
)

func TestLinkGeneratedArtifacts(t *testing.T) {
	attachments := []*discordgo.MessageAttachment{{Filename: "my report.txt", URL: "https://cdn.discordapp.com/attachments/report?ex=123&sig=456"}}
	for _, tt := range []struct{ input, want string }{
		{"[Download](sandbox:/mnt/data/my%20report.txt)", "[Download](https://cdn.discordapp.com/attachments/report?ex=123&sig=456)"},
		{"[Missing](sandbox:/mnt/data/missing.txt)", "[Missing](sandbox:/mnt/data/missing.txt)"},
		{"Normal text", "Normal text"},
	} {
		if got := linkGeneratedArtifacts(tt.input, attachments); got != tt.want {
			t.Errorf("linkGeneratedArtifacts(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

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

func TestStreamChatResponse_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := streamChatResponse(ctx, nil, nil, nil, nil, "", "")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("streamChatResponse() error = %v, want context.Canceled", err)
	}
}

func TestStreamer_StopWaitsForTicker(t *testing.T) {
	s := newStreamer(nil, nil)
	s.Start()

	if err := s.Stop(); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
}
