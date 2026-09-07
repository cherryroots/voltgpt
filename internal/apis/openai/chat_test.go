package openai

import (
	"context"
	"errors"
	"github.com/bwmarrin/discordgo"
	"testing"

	"github.com/openai/openai-go/v3/responses"
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

func TestGeneratedArtifacts(t *testing.T) {
	response := responses.Response{Output: []responses.ResponseOutputItemUnion{
		{
			Type: "reasoning",
		},
		{
			Type: "message",
			Content: []responses.ResponseOutputMessageContentUnion{
				{
					Type: "output_text",
					Annotations: []responses.ResponseOutputTextAnnotationUnion{
						{Type: "url_citation", URL: "https://example.com"},
						{Type: "container_file_citation", ContainerID: "container-1", FileID: "file-1", Filename: "report.csv"},
						{Type: "container_file_citation", ContainerID: "container-1", FileID: "file-1", Filename: "report.csv"},
						{Type: "container_file_citation", ContainerID: "", FileID: "file-2", Filename: "invalid.csv"},
					},
				},
			},
		},
	}}

	got := generatedArtifacts(response)
	want := []generatedArtifact{{ContainerID: "container-1", FileID: "file-1", Filename: "report.csv"}}
	if len(got) != len(want) {
		t.Fatalf("generatedArtifacts() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("generatedArtifacts()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestArtifactFilename(t *testing.T) {
	tests := []struct {
		name     string
		artifact generatedArtifact
		want     string
	}{
		{name: "plain", artifact: generatedArtifact{FileID: "file-1", Filename: "report.csv"}, want: "report.csv"},
		{name: "unix path", artifact: generatedArtifact{FileID: "file-1", Filename: "/mnt/data/report.csv"}, want: "report.csv"},
		{name: "windows path", artifact: generatedArtifact{FileID: "file-1", Filename: `C:\\data\\report.csv`}, want: "report.csv"},
		{name: "missing", artifact: generatedArtifact{FileID: "file-1"}, want: "file-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := artifactFilename(tt.artifact); got != tt.want {
				t.Errorf("artifactFilename() = %q, want %q", got, tt.want)
			}
		})
	}
}
