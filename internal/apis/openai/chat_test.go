package openai

import (
	"context"
	"errors"
	"testing"

	"github.com/openai/openai-go/v3/responses"
)

func TestSafeBaseURLForLog_RedactsCredentialsAndQuery(t *testing.T) {
	got := safeBaseURLForLog("https://user:secret@example.com/v1?token=secret")
	if got != "https://example.com" {
		t.Fatalf("safeBaseURLForLog() = %q, want %q", got, "https://example.com")
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

	got := GeneratedArtifacts(response)
	want := []Artifact{{ContainerID: "container-1", FileID: "file-1", Filename: "report.csv"}}
	if len(got) != len(want) {
		t.Fatalf("GeneratedArtifacts() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("GeneratedArtifacts()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func TestArtifactFilename(t *testing.T) {
	tests := []struct {
		name     string
		artifact Artifact
		want     string
	}{
		{name: "plain", artifact: Artifact{FileID: "file-1", Filename: "report.csv"}, want: "report.csv"},
		{name: "unix path", artifact: Artifact{FileID: "file-1", Filename: "/mnt/data/report.csv"}, want: "report.csv"},
		{name: "windows path", artifact: Artifact{FileID: "file-1", Filename: `C:\\data\\report.csv`}, want: "report.csv"},
		{name: "missing", artifact: Artifact{FileID: "file-1"}, want: "file-1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.artifact.Name(); got != tt.want {
				t.Errorf("Name() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestStreamChat_CanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := StreamChat(ctx, nil, ChatRequest{}, func(string) {})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StreamChat() error = %v, want context.Canceled", err)
	}
}

func TestInputMessage_DropsAssistantImages(t *testing.T) {
	images := []Image{{MIME: "image/png", Data: "AAAA"}}

	user := InputMessage("user", "hi", images)
	if got := len(user.OfMessage.Content.OfInputItemContentList); got != 2 {
		t.Fatalf("user message parts = %d, want 2", got)
	}

	assistant := InputMessage("assistant", "hi", images)
	if got := len(assistant.OfMessage.Content.OfInputItemContentList); got != 1 {
		t.Fatalf("assistant message parts = %d, want 1", got)
	}
}
