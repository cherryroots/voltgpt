# No-Memory Emoji Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Skip background fact retrieval when the user's message contains 🚫.

**Architecture:** Single guard in `internal/handler/messages.go` wraps the `memory.RetrieveMultiUser` call; `backgroundFacts` defaults to `""` so `{BACKGROUND_FACTS}` is still replaced unconditionally in the system prompt.

**Tech Stack:** Go, `strings.Contains`

---

### Task 1: Add the 🚫 guard and test it

**Files:**
- Modify: `internal/handler/messages.go:134`
- Test: `internal/handler/messages_test.go` (create if it doesn't exist; add to it if it does)

**Step 1: Write the failing test**

Open (or create) `internal/handler/messages_test.go`. Add:

```go
package handler

import "testing"

func TestSkipMemoryEmoji(t *testing.T) {
    cases := []struct {
        content string
        skip    bool
    }{
        {"hello", false},
        {"hello 🚫", true},
        {"🚫 what is the capital of France?", true},
        {"just a normal message", false},
    }
    for _, tc := range cases {
        got := strings.Contains(tc.content, "🚫")
        if got != tc.skip {
            t.Errorf("content=%q: want skip=%v, got %v", tc.content, tc.skip, got)
        }
    }
}
```

This tests the predicate logic in isolation before wiring it in.

**Step 2: Run test to verify it fails (or passes already)**

```bash
/usr/local/go/bin/go test ./internal/handler/... -run TestSkipMemoryEmoji -v
```

Expected: PASS (the predicate is just `strings.Contains` — this is a logic test, not a compile test).

**Step 3: Apply the guard in `messages.go`**

In `internal/handler/messages.go`, find this block (around line 134):

```go
backgroundFacts := memory.RetrieveMultiUser(m.Content, users)
```

Replace with:

```go
var backgroundFacts string
if !strings.Contains(m.Content, "🚫") {
    backgroundFacts = memory.RetrieveMultiUser(m.Content, users)
}
```

`strings` is already imported.

**Step 4: Build to confirm no compile errors**

```bash
/usr/local/go/bin/go build ./...
```

Expected: exits 0, no output.

**Step 5: Run all tests**

```bash
/usr/local/go/bin/go test ./... -timeout 60s
```

Expected: all pass.

**Step 6: Commit**

```bash
git add internal/handler/messages.go internal/handler/messages_test.go
git commit -m "feat: skip memory retrieval when message contains 🚫"
```
