# Gemini Package Test Coverage Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add test coverage to `internal/apis/gemini/` with pure logic tests and one real-API integration test.

**Architecture:** White-box tests (`package gemini`) cover pure functions and early-exit buffer paths without needing a Discord session or Gemini API. One integration test for `SummarizeCleanText` calls the real API and skips when `GEMINI_TOKEN` is absent, matching the memory package pattern.

**Tech Stack:** Go testing stdlib, `net/http/httptest`, `image/png`, `google.golang.org/genai`, `voltgpt/internal/config`

---

### Task 1: Fix GEMINI_API_KEY → GEMINI_TOKEN bug

**Files:**
- Modify: `internal/apis/gemini/chat.go:223-225`

**Step 1: Edit the env var name**

In `SummarizeCleanText`, change the two references from `GEMINI_API_KEY` to `GEMINI_TOKEN`:

```go
apiKey := os.Getenv("GEMINI_TOKEN")
if apiKey == "" {
    log.Println("GEMINI_TOKEN is not set")
    return ""
}
```

**Step 2: Build to verify**

Run: `/usr/local/go/bin/go build ./...`
Expected: no errors

**Step 3: Commit**

```bash
git add internal/apis/gemini/chat.go
git commit -m "fix: align SummarizeCleanText env var to GEMINI_TOKEN"
```

---

### Task 2: Create chat_test.go — contentToString tests

**Files:**
- Create: `internal/apis/gemini/chat_test.go`

**Step 1: Write the failing tests**

Create `internal/apis/gemini/chat_test.go`:

```go
package gemini

import (
	"bytes"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
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
```

**Step 2: Run to verify tests pass**

Run: `/usr/local/go/bin/go test ./internal/apis/gemini/... -run TestContentToString -v`
Expected: all PASS (pure function, already implemented)

**Step 3: Commit**

```bash
git add internal/apis/gemini/chat_test.go
git commit -m "test(gemini): add contentToString unit tests"
```

---

### Task 3: Add CreateContent tests

**Files:**
- Modify: `internal/apis/gemini/chat_test.go`

**Step 1: Write the tests**

Append to `chat_test.go`:

```go
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
```

**Step 2: Run the tests**

Run: `/usr/local/go/bin/go test ./internal/apis/gemini/... -run TestCreateContent -v -timeout 30s`
Expected: all PASS

**Step 3: Commit**

```bash
git add internal/apis/gemini/chat_test.go
git commit -m "test(gemini): add CreateContent unit tests"
```

---

### Task 4: Add Streamer buffer tests

**Files:**
- Modify: `internal/apis/gemini/chat_test.go`

**Step 1: Write the tests**

Append to `chat_test.go`:

```go
func TestStreamer_Update(t *testing.T) {
	s := NewStreamer(nil, nil)
	s.Update("hello")
	s.Update(" world")
	if s.Buffer != "hello world" {
		t.Errorf("buffer: got %q, want %q", s.Buffer, "hello world")
	}
}

func TestStreamer_Flush_EmptyBuffer(t *testing.T) {
	// Flush on an empty buffer should be a no-op — no panic with nil Session/Message.
	s := NewStreamer(nil, nil)
	s.Flush() // must not panic
}

func TestStreamer_Flush_OnlyXMLTags(t *testing.T) {
	// Buffer containing only replacement-map strings cleans to empty → early return.
	// replacementMap: "<username>", "</username>", "<attachments>", "</attachments>", "..."
	s := NewStreamer(nil, nil)
	s.Buffer = "<username></username><attachments></attachments>..."
	s.Flush() // must not panic or call Discord
}
```

**Step 2: Run the tests**

Run: `/usr/local/go/bin/go test ./internal/apis/gemini/... -run TestStreamer -v`
Expected: all PASS

**Step 3: Commit**

```bash
git add internal/apis/gemini/chat_test.go
git commit -m "test(gemini): add Streamer buffer unit tests"
```

---

### Task 5: Add SummarizeCleanText integration test

**Files:**
- Modify: `internal/apis/gemini/chat_test.go`

**Step 1: Write the test**

Append to `chat_test.go`:

```go
func TestSummarizeCleanText_Integration(t *testing.T) {
	if os.Getenv("GEMINI_TOKEN") == "" {
		t.Skip("GEMINI_TOKEN not set")
	}
	result := SummarizeCleanText("# Hello\nThis is a short test document for summarization.")
	if result == "" {
		t.Error("expected non-empty summary from Gemini")
	}
}
```

**Step 2: Run without token (verify skip)**

Run: `/usr/local/go/bin/go test ./internal/apis/gemini/... -run TestSummarizeCleanText -v`
Expected: `--- SKIP: TestSummarizeCleanText_Integration (GEMINI_TOKEN not set)`

**Step 3: Run with token (verify live call)**

Run: `GEMINI_TOKEN=$(grep ^GEMINI_TOKEN .env | cut -d'=' -f2) /usr/local/go/bin/go test ./internal/apis/gemini/... -run TestSummarizeCleanText -v -timeout 30s`
Expected: PASS with non-empty summary

**Step 4: Commit**

```bash
git add internal/apis/gemini/chat_test.go
git commit -m "test(gemini): add SummarizeCleanText integration test"
```

---

### Task 6: Update CI workflow and set GitHub secret

**Files:**
- Modify: `.github/workflows/test.yml:22-26`

**Step 1: Add GEMINI_TOKEN to the workflow env block**

In `test.yml`, the `env:` block currently has:
```yaml
env:
  MEMORY_GEMINI_TOKEN: ${{ secrets.MEMORY_GEMINI_TOKEN }}
  CGO_ENABLED: "1"
```

Change it to:
```yaml
env:
  MEMORY_GEMINI_TOKEN: ${{ secrets.MEMORY_GEMINI_TOKEN }}
  GEMINI_TOKEN: ${{ secrets.GEMINI_TOKEN }}
  CGO_ENABLED: "1"
```

**Step 2: Commit the workflow change**

```bash
git add .github/workflows/test.yml
git commit -m "ci: add GEMINI_TOKEN secret to test workflow"
```

**Step 3: Set the GitHub secret**

Run (pipes value directly, never displays it):
```bash
gh secret set GEMINI_TOKEN --body "$(grep ^GEMINI_TOKEN .env | cut -d'=' -f2)"
```
Expected: `✓ Set Actions secret GEMINI_TOKEN for cherryroots/voltgpt`

**Step 4: Push and verify CI**

```bash
git push
```
Then check: `gh run watch` or visit GitHub Actions to confirm the integration test passes in CI.
