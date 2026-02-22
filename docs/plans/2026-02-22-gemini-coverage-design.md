# Gemini Package Test Coverage Design

**Date:** 2026-02-22

## Goal

Add test coverage to `internal/apis/gemini/` using two layers: pure logic tests (no network) and one real-API integration test (skipped when token is absent), matching the pattern already established by the memory package.

## Bug Fix (prerequisite)

`SummarizeCleanText` reads `GEMINI_API_KEY` but the rest of the codebase uses `GEMINI_TOKEN`. Rename to `GEMINI_TOKEN` before writing tests so the integration test and CI use a consistent variable.

## Layer 1: Pure Logic Tests

File: `internal/apis/gemini/chat_test.go` (package `gemini`, white-box)

### `contentToString`
- empty content → `""`
- single text part → string returned
- multiple text parts → concatenated with newlines

### `CreateContent`
- text-only → single `Text` part, correct role assigned
- image URL via `httptest.NewServer` (serve PNG bytes at `/test.png`) → `InlineData` part with correct MIME type
- `thought_signature.png` URL with `role="model"` → `ThoughtSignature` part, not `InlineData`
- YouTube URL → part created via `NewPartFromURI`
- unreachable URL → skipped silently, no panic

### `Streamer` buffer behavior
- `Update` appends content to buffer correctly under mutex
- `Flush` on empty buffer → no-op (returns before any Discord call)
- `Flush` on buffer containing only XML tags (`<username>`, `</username>`, etc.) → cleans to empty, no-op

The two flush early-exit cases need no Discord session — they return before `utility.SplitSend` is reached.

**Out of scope:** `Flush` with real content, `StreamMessageResponse`, `PrependReplyMessages` — all require a live Discord session and add complexity disproportionate to their value.

## Layer 2: Integration Test

File: `internal/apis/gemini/chat_test.go`

```go
func TestSummarizeCleanText_Integration(t *testing.T) {
    if os.Getenv("GEMINI_TOKEN") == "" {
        t.Skip("GEMINI_TOKEN not set")
    }
    result := SummarizeCleanText("# Hello\nThis is a test document.")
    if result == "" {
        t.Error("expected non-empty summary")
    }
}
```

## CI Updates

### `test.yml` env block
Add:
```yaml
GEMINI_TOKEN: ${{ secrets.GEMINI_TOKEN }}
```

### GitHub Secret
```bash
gh secret set GEMINI_TOKEN --body "$(grep ^GEMINI_TOKEN .env | cut -d'=' -f2)"
```

## Implementation Order

1. Fix `GEMINI_API_KEY` → `GEMINI_TOKEN` in `SummarizeCleanText`
2. Write `chat_test.go` with pure logic tests
3. Add integration test for `SummarizeCleanText`
4. Update `test.yml`
5. Set GitHub secret
6. Run tests locally to verify
