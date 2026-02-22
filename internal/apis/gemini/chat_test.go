package gemini

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"google.golang.org/genai"

	"voltgpt/internal/config"
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

// minimalPNG returns bytes for a 1×1 RGBA PNG, usable as test image data.
func minimalPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestCreateContent_TextOnly(t *testing.T) {
	got := CreateContent(nil, "user", config.RequestContent{Text: "hello world"})
	if got.Role != "user" {
		t.Errorf("role: got %q, want %q", got.Role, "user")
	}
	if len(got.Parts) != 1 {
		t.Fatalf("parts: got %d, want 1", len(got.Parts))
	}
	if got.Parts[0].Text != "hello world" {
		t.Errorf("text: got %q, want %q", got.Parts[0].Text, "hello world")
	}
}

func TestCreateContent_ImageURL(t *testing.T) {
	pngData := minimalPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngData)
	}))
	defer srv.Close()

	got := CreateContent(nil, "user", config.RequestContent{
		Images: []string{srv.URL + "/test.png"},
	})
	if len(got.Parts) != 1 {
		t.Fatalf("parts: got %d, want 1", len(got.Parts))
	}
	if got.Parts[0].InlineData == nil {
		t.Fatal("expected InlineData part, got nil")
	}
	if got.Parts[0].InlineData.MIMEType != "image/png" {
		t.Errorf("MIME: got %q, want %q", got.Parts[0].InlineData.MIMEType, "image/png")
	}
}

func TestCreateContent_ThoughtSignature(t *testing.T) {
	pngData := minimalPNG(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(pngData)
	}))
	defer srv.Close()

	// thought_signature.png with role="model" → ThoughtSignature part, not InlineData.
	got := CreateContent(nil, "model", config.RequestContent{
		Images: []string{srv.URL + "/thought_signature.png"},
	})
	if len(got.Parts) != 1 {
		t.Fatalf("parts: got %d, want 1", len(got.Parts))
	}
	if got.Parts[0].ThoughtSignature == nil {
		t.Fatal("expected ThoughtSignature bytes, got nil")
	}
}

func TestCreateContent_YouTubeURL(t *testing.T) {
	ytURL := "https://www.youtube.com/watch?v=testID"
	got := CreateContent(nil, "user", config.RequestContent{
		YTURLs: []string{ytURL},
	})
	if len(got.Parts) != 1 {
		t.Fatalf("parts: got %d, want 1", len(got.Parts))
	}
	if got.Parts[0].FileData == nil {
		t.Fatal("expected FileData part, got nil")
	}
	if got.Parts[0].FileData.FileURI != ytURL {
		t.Errorf("URI: got %q, want %q", got.Parts[0].FileData.FileURI, ytURL)
	}
}

func TestCreateContent_BadImageURL_Skipped(t *testing.T) {
	// An unreachable URL should be silently skipped — no panic, no part.
	got := CreateContent(nil, "user", config.RequestContent{
		Images: []string{"http://localhost:0/bad.png"},
	})
	if len(got.Parts) != 0 {
		t.Errorf("parts: got %d, want 0 for bad URL", len(got.Parts))
	}
}
