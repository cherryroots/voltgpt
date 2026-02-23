# Channel Thread Context Window Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Inject a structured snapshot of recent channel conversations (grouped by reply chain) into every Gemini request, so Vivy has channel context even without an explicit reply chain.

**Architecture:** Always fetch the 100-message cache unconditionally. Build a thread forest from it, exclude messages already in the reply chain, format as a labeled text block, and prepend as a `user` content turn. The `🚫` emoji skips both memory retrieval and the new context window (rename `ShouldSkipMemory` → `ShouldSkipContext`).

**Tech Stack:** Go, discordgo, google.golang.org/genai, internal utility package

---

### Task 1: Rename ShouldSkipMemory → ShouldSkipContext

**Files:**
- Modify: `internal/utility/strings.go:43-45`
- Modify: `internal/utility/strings_test.go:260-276`
- Modify: `internal/handler/messages.go:111`

**Step 1: Update the function name in strings.go**

In `internal/utility/strings.go`, change:
```go
// shouldSkipMemory reports whether background fact retrieval should be skipped
// for the given message content. Users can include 🚫 to opt out of memory context.
func ShouldSkipMemory(content string) bool {
	return strings.Contains(content, "🚫")
}
```
to:
```go
// ShouldSkipContext reports whether background context retrieval (memory facts and
// channel history) should be skipped. Users include 🚫 to opt out.
func ShouldSkipContext(content string) bool {
	return strings.Contains(content, "🚫")
}
```

**Step 2: Update the test in strings_test.go**

Change `TestSkipMemoryEmoji` to `TestSkipContextEmoji` and update the call:
```go
func TestSkipContextEmoji(t *testing.T) {
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
		got := ShouldSkipContext(tc.content)
		if got != tc.skip {
			t.Errorf("content=%q: want skip=%v, got %v", tc.content, tc.skip, got)
		}
	}
}
```

**Step 3: Update the call site in messages.go**

In `internal/handler/messages.go`, change:
```go
if !utility.ShouldSkipMemory(m.Content) {
```
to:
```go
if !utility.ShouldSkipContext(m.Content) {
```

**Step 4: Run tests**

```bash
/usr/local/go/bin/go test ./internal/utility/... ./internal/handler/... -timeout 60s
```
Expected: all tests pass.

**Step 5: Commit**

```bash
git add internal/utility/strings.go internal/utility/strings_test.go internal/handler/messages.go
git commit -m "refactor: rename ShouldSkipMemory to ShouldSkipContext"
```

---

### Task 2: Always fetch the message cache in HandleMessage

**Files:**
- Modify: `internal/handler/messages.go:56-70`

The cache fetch currently lives inside the `if m.Type == discordgo.MessageTypeReply && isMentioned` block. Move the variable declaration and fetch outside it so the cache is always available.

**Step 1: Restructure the cache fetch**

Replace this block (approximately lines 56-70):
```go
var chatMessages []*genai.Content
var cache []*discordgo.Message
var isMentioned, isReply bool

for _, mention := range m.Mentions {
    if mention.ID == s.State.User.ID {
        isMentioned = true
        break
    }
}

if m.Type == discordgo.MessageTypeReply && isMentioned {
    cache, _ = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
    isReply = true
}
```

with:
```go
var chatMessages []*genai.Content
var isMentioned, isReply bool

for _, mention := range m.Mentions {
    if mention.ID == s.State.User.ID {
        isMentioned = true
        break
    }
}

cache, _ := utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)

if m.Type == discordgo.MessageTypeReply && isMentioned {
    isReply = true
}
```

**Step 2: Build and verify no compilation errors**

```bash
/usr/local/go/bin/go build ./...
```
Expected: builds cleanly.

**Step 3: Commit**

```bash
git add internal/handler/messages.go
git commit -m "refactor: always fetch message cache in HandleMessage"
```

---

### Task 3: Implement buildThreadForest

**Files:**
- Modify: `internal/apis/gemini/chat.go` (add function at end of file)
- Modify: `internal/apis/gemini/chat_test.go` (add tests)

`buildThreadForest` groups a flat message slice into reply-chain trees. It is a pure function — no session, no API calls.

**Step 1: Write failing tests in chat_test.go**

Add to `internal/apis/gemini/chat_test.go`:
```go
func TestBuildThreadForest_StandaloneMessages(t *testing.T) {
    now := time.Now()
    m1 := &discordgo.Message{ID: "1", Author: &discordgo.User{Username: "alice"}, Content: "hello", Timestamp: now.Add(-2 * time.Minute)}
    m2 := &discordgo.Message{ID: "2", Author: &discordgo.User{Username: "bob"}, Content: "world", Timestamp: now.Add(-1 * time.Minute)}

    threads := buildThreadForest([]*discordgo.Message{m1, m2})

    if len(threads) != 2 {
        t.Fatalf("want 2 threads, got %d", len(threads))
    }
    // Newest thread first: m2 at -1min
    if threads[0][0].ID != "2" {
        t.Errorf("want newest thread first (id=2), got id=%s", threads[0][0].ID)
    }
}

func TestBuildThreadForest_ReplyChain(t *testing.T) {
    now := time.Now()
    m1 := &discordgo.Message{ID: "1", Author: &discordgo.User{Username: "alice"}, Content: "root", Timestamp: now.Add(-3 * time.Minute)}
    m2 := &discordgo.Message{
        ID: "2", Author: &discordgo.User{Username: "bob"}, Content: "reply",
        Timestamp:        now.Add(-2 * time.Minute),
        MessageReference: &discordgo.MessageReference{MessageID: "1"},
    }
    m3 := &discordgo.Message{ID: "3", Author: &discordgo.User{Username: "charlie"}, Content: "standalone", Timestamp: now.Add(-1 * time.Minute)}

    threads := buildThreadForest([]*discordgo.Message{m1, m2, m3})

    if len(threads) != 2 {
        t.Fatalf("want 2 threads, got %d", len(threads))
    }
    // charlie's standalone is newest
    if threads[0][0].ID != "3" {
        t.Errorf("want charlie's thread first, got id=%s", threads[0][0].ID)
    }
    // alice+bob thread: chronological order within thread
    if threads[1][0].ID != "1" || threads[1][1].ID != "2" {
        t.Errorf("want [1,2] in alice/bob thread, got [%s,%s]", threads[1][0].ID, threads[1][1].ID)
    }
}

func TestBuildThreadForest_ExternalReferenceIsRoot(t *testing.T) {
    now := time.Now()
    // Reference points to a message NOT in the cache — treated as root
    m1 := &discordgo.Message{
        ID: "1", Author: &discordgo.User{Username: "alice"}, Content: "reply to outside",
        Timestamp:        now,
        MessageReference: &discordgo.MessageReference{MessageID: "999"},
    }

    threads := buildThreadForest([]*discordgo.Message{m1})

    if len(threads) != 1 {
        t.Fatalf("want 1 thread, got %d", len(threads))
    }
    if threads[0][0].ID != "1" {
        t.Errorf("want message to be a root, got id=%s", threads[0][0].ID)
    }
}

func TestBuildThreadForest_CappedAtFiveThreads(t *testing.T) {
    now := time.Now()
    var msgs []*discordgo.Message
    for i := 0; i < 10; i++ {
        msgs = append(msgs, &discordgo.Message{
            ID:        fmt.Sprintf("%d", i),
            Author:    &discordgo.User{Username: "user"},
            Content:   "msg",
            Timestamp: now.Add(time.Duration(i) * time.Minute),
        })
    }

    threads := buildThreadForest(msgs)

    if len(threads) != 5 {
        t.Errorf("want 5 threads (cap), got %d", len(threads))
    }
}
```

Also add these imports to the test file if not already present:
```go
"fmt"
"time"
"github.com/bwmarrin/discordgo"
```

**Step 2: Run tests to verify they fail**

```bash
/usr/local/go/bin/go test ./internal/apis/gemini/... -run "TestBuildThreadForest" -v
```
Expected: compile error — `buildThreadForest` undefined.

**Step 3: Implement buildThreadForest in chat.go**

Add to the end of `internal/apis/gemini/chat.go`:
```go
// buildThreadForest groups a flat message slice into reply-chain trees.
// Each tree is a slice of messages in chronological order. Trees are
// returned newest-first (by most recent message). Capped at 5 threads.
func buildThreadForest(messages []*discordgo.Message) [][]*discordgo.Message {
	byID := make(map[string]*discordgo.Message, len(messages))
	for _, m := range messages {
		byID[m.ID] = m
	}

	children := make(map[string][]*discordgo.Message)
	var roots []*discordgo.Message
	for _, m := range messages {
		if m.MessageReference != nil && byID[m.MessageReference.MessageID] != nil {
			parentID := m.MessageReference.MessageID
			children[parentID] = append(children[parentID], m)
		} else {
			roots = append(roots, m)
		}
	}

	var threads [][]*discordgo.Message
	for _, root := range roots {
		var thread []*discordgo.Message
		queue := []*discordgo.Message{root}
		for len(queue) > 0 {
			curr := queue[0]
			queue = queue[1:]
			thread = append(thread, curr)
			queue = append(queue, children[curr.ID]...)
		}
		sort.Slice(thread, func(i, j int) bool {
			return thread[i].Timestamp.Before(thread[j].Timestamp)
		})
		threads = append(threads, thread)
	}

	sort.Slice(threads, func(i, j int) bool {
		latestI := threads[i][len(threads[i])-1].Timestamp
		latestJ := threads[j][len(threads[j])-1].Timestamp
		return latestI.After(latestJ)
	})

	if len(threads) > 5 {
		threads = threads[:5]
	}
	return threads
}
```

Also add `"sort"` to the imports in `chat.go`.

**Step 4: Run tests to verify they pass**

```bash
/usr/local/go/bin/go test ./internal/apis/gemini/... -run "TestBuildThreadForest" -v
```
Expected: all 4 tests PASS.

**Step 5: Commit**

```bash
git add internal/apis/gemini/chat.go internal/apis/gemini/chat_test.go
git commit -m "feat: implement buildThreadForest for channel context grouping"
```

---

### Task 4: Implement PrependChannelContext

**Files:**
- Modify: `internal/apis/gemini/chat.go` (add function)
- Modify: `internal/apis/gemini/chat_test.go` (add tests)

`PrependChannelContext` takes `botID` (instead of a session) so it stays testable. It uses `utility.ResolveMentions` on each message for clean content.

**Step 1: Write failing tests in chat_test.go**

```go
func TestPrependChannelContext_InjectsContext(t *testing.T) {
    now := time.Now()
    alice := &discordgo.Message{
        ID:        "1",
        Author:    &discordgo.User{ID: "u1", Username: "alice"},
        Content:   "hey how are you",
        Timestamp: now.Add(-2 * time.Minute),
    }
    bob := &discordgo.Message{
        ID:        "2",
        Author:    &discordgo.User{ID: "u2", Username: "bob"},
        Content:   "doing well",
        Timestamp: now.Add(-1 * time.Minute),
        MessageReference: &discordgo.MessageReference{MessageID: "1"},
    }

    var chatMessages []*genai.Content
    PrependChannelContext("bot-id", []*discordgo.Message{alice, bob}, nil, &chatMessages)

    if len(chatMessages) != 1 {
        t.Fatalf("want 1 prepended content, got %d", len(chatMessages))
    }
    text := chatMessages[0].Parts[0].Text
    if !strings.Contains(text, "alice") || !strings.Contains(text, "bob") {
        t.Errorf("expected participant names in context, got: %s", text)
    }
    if !strings.Contains(text, "hey how are you") {
        t.Errorf("expected message content in context, got: %s", text)
    }
}

func TestPrependChannelContext_ExcludesIDs(t *testing.T) {
    now := time.Now()
    m1 := &discordgo.Message{ID: "1", Author: &discordgo.User{ID: "u1", Username: "alice"}, Content: "excluded", Timestamp: now}
    m2 := &discordgo.Message{ID: "2", Author: &discordgo.User{ID: "u2", Username: "bob"}, Content: "included", Timestamp: now.Add(time.Second)}

    exclude := map[string]bool{"1": true}
    var chatMessages []*genai.Content
    PrependChannelContext("bot-id", []*discordgo.Message{m1, m2}, exclude, &chatMessages)

    if len(chatMessages) != 1 {
        t.Fatalf("want 1 content, got %d", len(chatMessages))
    }
    text := chatMessages[0].Parts[0].Text
    if strings.Contains(text, "excluded") {
        t.Errorf("excluded message should not appear in context")
    }
    if !strings.Contains(text, "included") {
        t.Errorf("non-excluded message should appear in context")
    }
}

func TestPrependChannelContext_EmptyAfterFilter_NoOp(t *testing.T) {
    m1 := &discordgo.Message{ID: "1", Author: &discordgo.User{ID: "u1", Username: "alice"}, Content: "msg", Timestamp: time.Now()}
    exclude := map[string]bool{"1": true}

    var chatMessages []*genai.Content
    PrependChannelContext("bot-id", []*discordgo.Message{m1}, exclude, &chatMessages)

    if len(chatMessages) != 0 {
        t.Errorf("expected no-op when all messages excluded, got %d content items", len(chatMessages))
    }
}

func TestPrependChannelContext_BotLabeledVivy(t *testing.T) {
    botMsg := &discordgo.Message{
        ID:        "1",
        Author:    &discordgo.User{ID: "bot-id", Username: "Vivy"},
        Content:   "I am the bot",
        Timestamp: time.Now(),
    }

    var chatMessages []*genai.Content
    PrependChannelContext("bot-id", []*discordgo.Message{botMsg}, nil, &chatMessages)

    if len(chatMessages) != 1 {
        t.Fatalf("want 1 content, got %d", len(chatMessages))
    }
    text := chatMessages[0].Parts[0].Text
    if !strings.Contains(text, "vivy") {
        t.Errorf("expected bot to be labeled 'vivy', got: %s", text)
    }
}
```

Add `"strings"` to test imports if not already present.

**Step 2: Run tests to verify they fail**

```bash
/usr/local/go/bin/go test ./internal/apis/gemini/... -run "TestPrependChannelContext" -v
```
Expected: compile error — `PrependChannelContext` undefined.

**Step 3: Implement PrependChannelContext in chat.go**

Add after `buildThreadForest`:
```go
// PrependChannelContext injects a structured snapshot of recent channel conversations
// as a user content turn prepended to chatMessages. Messages in excludeIDs are omitted
// (to avoid duplicating the active reply chain). No-ops if nothing remains after filtering.
func PrependChannelContext(
	botID string,
	cache []*discordgo.Message,
	excludeIDs map[string]bool,
	chatMessages *[]*genai.Content,
) {
	filtered := make([]*discordgo.Message, 0, len(cache))
	for _, m := range cache {
		if !excludeIDs[m.ID] {
			filtered = append(filtered, m)
		}
	}
	if len(filtered) == 0 {
		return
	}

	threads := buildThreadForest(filtered)
	if len(threads) == 0 {
		return
	}

	var sb strings.Builder
	sb.WriteString("[recent channel context]")

	for _, thread := range threads {
		seen := map[string]bool{}
		var participants []string
		for _, m := range thread {
			name := m.Author.Username
			if m.Author.ID == botID {
				name = "vivy"
			}
			if !seen[name] {
				seen[name] = true
				participants = append(participants, name)
			}
		}

		if len(participants) == 1 {
			sb.WriteString(fmt.Sprintf("\n\nstandalone (%s):", participants[0]))
		} else {
			sb.WriteString(fmt.Sprintf("\n\nthread (%s):", strings.Join(participants, ", ")))
		}

		for _, m := range thread {
			name := m.Author.Username
			if m.Author.ID == botID {
				name = "vivy"
			}
			text := strings.TrimSpace(utility.ResolveMentions(m.Content, m.Mentions))
			if text == "" {
				continue
			}
			sb.WriteString(fmt.Sprintf("\n  %s: %s", name, text))
		}
	}

	contextText := strings.TrimSpace(sb.String())
	if contextText == "" {
		return
	}

	*chatMessages = append([]*genai.Content{
		{Role: "user", Parts: []*genai.Part{{Text: contextText}}},
	}, *chatMessages...)
}
```

**Step 4: Run tests**

```bash
/usr/local/go/bin/go test ./internal/apis/gemini/... -run "TestPrependChannelContext" -v
```
Expected: all 4 tests PASS.

**Step 5: Build check**

```bash
/usr/local/go/bin/go build ./...
```
Expected: builds cleanly.

**Step 6: Commit**

```bash
git add internal/apis/gemini/chat.go internal/apis/gemini/chat_test.go
git commit -m "feat: implement PrependChannelContext for channel thread window"
```

---

### Task 5: Wire PrependChannelContext into HandleMessage

**Files:**
- Modify: `internal/handler/messages.go`

Add a `replyChainIDs` helper and call `PrependChannelContext` unconditionally (subject to the `🚫` guard).

**Step 1: Add the replyChainIDs helper in messages.go**

Add before `HandleMessage` (or after `handleReminder`):
```go
// replyChainIDs returns the set of message IDs reachable by following the reply
// chain from m, using the cache to avoid extra API calls where possible.
func replyChainIDs(s *discordgo.Session, m *discordgo.Message, cache []*discordgo.Message) map[string]bool {
	ids := map[string]bool{m.ID: true}
	ref := utility.GetReferencedMessage(s, m, cache)
	for ref != nil {
		ids[ref.ID] = true
		if ref.Type != discordgo.MessageTypeReply {
			break
		}
		ref = utility.GetReferencedMessage(s, ref, cache)
	}
	return ids
}
```

**Step 2: Call PrependChannelContext in HandleMessage**

The memory/context retrieval block currently reads:
```go
// Retrieve memory facts for users in the reply chain (not the whole channel)
users := map[string]string{m.Author.ID: m.Author.Username}
if isReply {
    maps.Copy(users, utility.ReplyChainUsers(s, m.Message, cache))
}
var backgroundFacts string
if !utility.ShouldSkipContext(m.Content) {
    backgroundFacts = memory.RetrieveMultiUser(m.Content, users)
}
```

Replace it with:
```go
// Retrieve memory facts for users in the reply chain (not the whole channel)
users := map[string]string{m.Author.ID: m.Author.Username}
if isReply {
    maps.Copy(users, utility.ReplyChainUsers(s, m.Message, cache))
}
var backgroundFacts string
if !utility.ShouldSkipContext(m.Content) {
    backgroundFacts = memory.RetrieveMultiUser(m.Content, users)
    excludeIDs := replyChainIDs(s, m.Message, cache)
    gemini.PrependChannelContext(s.State.User.ID, cache, excludeIDs, &chatMessages)
}
```

**Step 3: Build**

```bash
/usr/local/go/bin/go build ./...
```
Expected: builds cleanly.

**Step 4: Run full test suite**

```bash
/usr/local/go/bin/go test ./... -timeout 60s
```
Expected: all tests pass.

**Step 5: Commit**

```bash
git add internal/handler/messages.go
git commit -m "feat: wire channel thread context window into HandleMessage"
```

---

### Task 6: Final verification

**Step 1: Run full build and vet**

```bash
/usr/local/go/bin/go vet ./... && /usr/local/go/bin/go build ./...
```
Expected: no warnings, clean build.

**Step 2: Run all tests**

```bash
/usr/local/go/bin/go test ./... -timeout 60s
```
Expected: all pass.
