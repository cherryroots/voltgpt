# Memory System v2 Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace the flat per-user fact system with a two-layer episodic memory: per-channel conversation buffers → interaction notes → user profiles.

**Architecture:** Channel buffers collect messages and flush on inactivity (25 min) or max age (2 h) via Gemini flash into conversation notes stored in `interaction_notes` + `vec_notes`. Each flush also updates profiles for note participants. A nightly job clusters today's notes and runs full profile consolidation via Gemini pro.

**Tech Stack:** Go, SQLite (`database/sql`), `asg017/sqlite-vec-go-bindings`, `google.golang.org/genai`, `bwmarrin/discordgo`

**Design doc:** `docs/plans/2026-02-25-memory-v2-design.md` — read it before touching any file.

**Build check:** After every `.go` edit the PostToolUse hook runs `go build ./...`. The build must never break. Add new code before removing old code.

**Run tests:** `/usr/local/go/bin/go test ./... -timeout 60s`

**Run single test:** `/usr/local/go/bin/go test ./internal/memory/ -run TestFoo -v -timeout 30s`

---

## Implementation Order

Tasks are ordered to keep the build green throughout:
1–2: Schema + types (additive)
3–8: New memory package code (additive)
9: Rewrite retrieve.go (compatible signature)
10–11: Midnight job + Init wiring
12–13: Update callers (messages.go, commands.go, main.go)
14: Delete old code

---

### Task 1: Add new DB tables

**Files:**
- Modify: `internal/db/db.go`

**Step 1: Write the failing test**

Create `internal/db/db_test.go`:

```go
package db

import (
	"testing"
)

func TestNewTablesCreated(t *testing.T) {
	Open(":memory:")
	defer Close()

	tables := []string{
		"user_profiles",
		"interaction_notes",
		"vec_notes",
		"channel_buffers",
		"memory_job_log",
	}

	for _, name := range tables {
		var found string
		err := DB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type IN ('table','shadow') AND name = ?", name,
		).Scan(&found)
		if err != nil || found != name {
			t.Errorf("table %q not created: err=%v", name, err)
		}
	}
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/db/ -run TestNewTablesCreated -v
```

Expected: FAIL — tables don't exist yet.

**Step 3: Implement — add 5 new table DDLs to `createTables()` in `internal/db/db.go`**

Append to the `tables` slice (before the `for` loop):

```go
`CREATE TABLE IF NOT EXISTS user_profiles (
    user_id       INTEGER PRIMARY KEY REFERENCES users(id),
    bio           TEXT NOT NULL DEFAULT '[]',
    interests     TEXT NOT NULL DEFAULT '[]',
    skills        TEXT NOT NULL DEFAULT '[]',
    opinions      TEXT NOT NULL DEFAULT '[]',
    relationships TEXT NOT NULL DEFAULT '[]',
    other         TEXT NOT NULL DEFAULT '[]',
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS interaction_notes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id        TEXT NOT NULL,
    channel_id      TEXT NOT NULL,
    note_type       TEXT NOT NULL,
    participants    TEXT NOT NULL,
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL,
    source_note_ids TEXT,
    note_date       DATE NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS channel_buffers (
    channel_id   TEXT PRIMARY KEY,
    guild_id     TEXT NOT NULL,
    messages     TEXT NOT NULL,
    started_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
)`,
`CREATE TABLE IF NOT EXISTS memory_job_log (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    last_run   DATETIME NOT NULL
)`,
```

Then separately create `vec_notes` — it's a virtual table, which needs the same pattern as `vec_facts`:

```go
var vecNotes string
err = DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='vec_notes'").Scan(&vecNotes)
if err == sql.ErrNoRows {
    _, err = DB.Exec(`CREATE VIRTUAL TABLE vec_notes USING vec0(
        note_id   INTEGER PRIMARY KEY,
        embedding float[768] distance_metric=cosine
    )`)
    if err != nil {
        log.Fatalf("Failed to create vec_notes table: %v", err)
    }
} else if err != nil {
    log.Fatalf("Failed to check vec_notes table: %v", err)
}
```

Add this block immediately after the existing `vec_facts` block (around line 93).

**Step 4: Run — verify it passes**

```bash
/usr/local/go/bin/go test ./internal/db/ -run TestNewTablesCreated -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go
git commit -m "feat(db): add user_profiles, interaction_notes, vec_notes, channel_buffers, memory_job_log tables"
```

---

### Task 2: New types and constants in memory.go

No new behavior — just types and constants that the rest of the package will use.

**Files:**
- Modify: `internal/memory/memory.go`

**Step 1: Write a trivial compile test**

In `internal/memory/memory_test.go`, add at the top of the file (after package declaration):

```go
// TestNewConstantsCompile verifies the new model constants exist.
func TestNewConstantsCompile(t *testing.T) {
    if flashModel == "" || consolidationModel == "" {
        t.Error("model constants must not be empty")
    }
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestNewConstantsCompile -v
```

Expected: FAIL (compile error — `flashModel` undefined).

**Step 3: Implement — add to `internal/memory/memory.go`**

Replace the `generationModel` constant line with:

```go
const (
    embeddingModel          = "gemini-embedding-001"
    embeddingDimensions     = 768
    flashModel              = "gemini-2.0-flash"
    consolidationModel      = "gemini-2.5-pro"
    noteRetrievalLimit      = 5
    profilesFromNotes       = 3 // max additional profiles from note participants
    bufferInactivity        = 25 * time.Minute
    bufferMaxAge            = 2 * time.Hour
    // v1 constants — kept until extract.go / consolidate.go are removed
    similarityLimit         = 3
    retrievalLimit          = 5
    generalRetrievalLimit   = 10
    minMessageLength        = 10
    distanceThreshold       = float64(0.35)
    retrievalDistanceThreshold = float64(0.6)
)
```

Add the `time` import if not already present.

Add new types after the `var` block:

```go
// ProfileFact is a single item in a user profile section,
// carrying the interaction_notes ID it was sourced from.
type ProfileFact struct {
    Text   string `json:"text"`
    NoteID int64  `json:"note_id"`
}

// UserProfile mirrors the user_profiles table.
type UserProfile struct {
    UserID        int64
    Bio           []ProfileFact
    Interests     []ProfileFact
    Skills        []ProfileFact
    Opinions      []ProfileFact
    Relationships []ProfileFact
    Other         []ProfileFact
    UpdatedAt     string
}

// bufMsg is one message in a channel buffer, stored as JSON in channel_buffers.
type bufMsg struct {
    DiscordID   string `json:"discord_id"`
    Username    string `json:"username"`
    DisplayName string `json:"display_name"`
    Text        string `json:"text"`
    MessageID   string `json:"message_id"`
}
```

Also add `TotalNotes()` function (replaces `TotalFacts` for main.go logging):

```go
// TotalNotes returns the count of interaction notes for startup logging.
func TotalNotes() int {
    if database == nil {
        return 0
    }
    var count int
    database.QueryRow("SELECT COUNT(*) FROM interaction_notes").Scan(&count)
    return count
}
```

**Step 4: Run — verify it passes**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestNewConstantsCompile -v
```

Expected: PASS. Also verify full build:

```bash
/usr/local/go/bin/go build ./...
```

**Step 5: Commit**

```bash
git add internal/memory/memory.go internal/memory/memory_test.go
git commit -m "feat(memory): add v2 types, model constants, and TotalNotes"
```

---

### Task 3: insertNote — store a conversation note and its vec embedding

**Files:**
- Create: `internal/memory/notes.go`

**Step 1: Write the failing test**

Create `internal/memory/notes_test.go`:

```go
package memory

import (
    "encoding/json"
    "testing"
)

func TestInsertNote(t *testing.T) {
    setupTestDB(t)

    note := &interactionNote{
        GuildID:      "guild1",
        ChannelID:    "chan1",
        NoteType:     "conversation",
        Participants: []string{"user1", "user2"},
        Title:        "Gaming chat",
        Summary:      "They talked about RPGs.",
        NoteDate:     "2026-02-25",
    }
    embedding := make([]float32, embeddingDimensions)
    embedding[0] = 1.0

    id, err := insertNote(note, embedding)
    if err != nil {
        t.Fatalf("insertNote: %v", err)
    }
    if id == 0 {
        t.Fatal("expected non-zero note ID")
    }

    // Verify interaction_notes row
    var title, noteType string
    var participantsJSON string
    err = database.QueryRow(
        "SELECT title, note_type, participants FROM interaction_notes WHERE id = ?", id,
    ).Scan(&title, &noteType, &participantsJSON)
    if err != nil {
        t.Fatalf("query: %v", err)
    }
    if title != "Gaming chat" {
        t.Errorf("title = %q, want %q", title, "Gaming chat")
    }
    if noteType != "conversation" {
        t.Errorf("note_type = %q, want %q", noteType, "conversation")
    }
    var participants []string
    json.Unmarshal([]byte(participantsJSON), &participants)
    if len(participants) != 2 {
        t.Errorf("participants len = %d, want 2", len(participants))
    }

    // Verify vec_notes row
    var vecNoteID int64
    err = database.QueryRow("SELECT note_id FROM vec_notes WHERE note_id = ?", id).Scan(&vecNoteID)
    if err != nil {
        t.Fatalf("vec_notes row missing: %v", err)
    }
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestInsertNote -v
```

Expected: FAIL (compile error — `interactionNote` and `insertNote` undefined).

**Step 3: Create `internal/memory/notes.go`**

```go
package memory

import (
    "encoding/json"
)

// interactionNote maps to the interaction_notes table.
type interactionNote struct {
    ID             int64
    GuildID        string
    ChannelID      string
    NoteType       string   // "conversation" | "topic_cluster"
    Participants   []string // discord IDs
    Title          string
    Summary        string
    SourceNoteIDs  []int64  // populated for topic_cluster only
    NoteDate       string   // YYYY-MM-DD
}

// insertNote writes an interaction note and its vec embedding in a transaction.
// Returns the new note ID.
func insertNote(note *interactionNote, embedding []float32) (int64, error) {
    participantsJSON, err := json.Marshal(note.Participants)
    if err != nil {
        return 0, err
    }

    sourceJSON, err := json.Marshal(note.SourceNoteIDs)
    if err != nil {
        return 0, err
    }

    tx, err := database.Begin()
    if err != nil {
        return 0, err
    }
    defer tx.Rollback()

    res, err := tx.Exec(`
        INSERT INTO interaction_notes
            (guild_id, channel_id, note_type, participants, title, summary, source_note_ids, note_date)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
    `, note.GuildID, note.ChannelID, note.NoteType,
        string(participantsJSON), note.Title, note.Summary,
        string(sourceJSON), note.NoteDate)
    if err != nil {
        return 0, err
    }

    noteID, err := res.LastInsertId()
    if err != nil {
        return 0, err
    }

    _, err = tx.Exec(
        "INSERT INTO vec_notes (note_id, embedding) VALUES (?, ?)",
        noteID, serializeFloat32(embedding),
    )
    if err != nil {
        return 0, err
    }

    return noteID, tx.Commit()
}
```

**Step 4: Run — verify it passes**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestInsertNote -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/memory/notes.go internal/memory/notes_test.go
git commit -m "feat(memory): add interactionNote type and insertNote function"
```

---

### Task 4: generateConversationNote — Gemini flash: buffer → note

**Files:**
- Modify: `internal/memory/notes.go`

**Step 1: Add a Gemini test to `notes_test.go`**

```go
func TestGenerateConversationNote(t *testing.T) {
    setupGemini(t)

    msgs := []bufMsg{
        {DiscordID: "u1", Username: "Alice", DisplayName: "Alice", Text: "anyone else playing BG3?"},
        {DiscordID: "u2", Username: "Bob", DisplayName: "Bob", Text: "yeah I'm on act 3, it's amazing"},
        {DiscordID: "u1", Username: "Alice", DisplayName: "Alice", Text: "I just started, what class are you?"},
        {DiscordID: "u2", Username: "Bob", DisplayName: "Bob", Text: "paladin, really fun so far"},
    }

    title, summary, err := generateConversationNote("chan1", "guild1", msgs)
    if err != nil {
        t.Fatalf("generateConversationNote: %v", err)
    }
    if title == "" {
        t.Error("expected non-empty title")
    }
    if summary == "" {
        t.Error("expected non-empty summary")
    }
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestGenerateConversationNote -v
```

Expected: FAIL (compile error — `generateConversationNote` undefined). If Gemini token absent, test skips.

**Step 3: Add `generateConversationNote` to `notes.go`**

```go
import (
    "context"
    "encoding/json"
    "fmt"
    "strings"

    "google.golang.org/genai"
)

const noteGenerationPrompt = `You summarize Discord channel conversations into a short note.

Output a JSON object with:
- "title": a 3-8 word title describing the main topic (e.g. "BG3 class discussion", "Linux vs Windows debate")
- "summary": 1-3 sentences summarizing what was discussed, who participated, and any key points

Focus on what was actually said. Use participant usernames naturally.
Return only valid JSON.`

type noteOutput struct {
    Title   string `json:"title"`
    Summary string `json:"summary"`
}

// generateConversationNote calls Gemini flash to produce a title and summary
// from a slice of buffered channel messages.
func generateConversationNote(channelID, guildID string, msgs []bufMsg) (title, summary string, err error) {
    if len(msgs) == 0 {
        return "", "", fmt.Errorf("no messages to summarize")
    }

    var sb strings.Builder
    for _, m := range msgs {
        name := m.DisplayName
        if name == "" {
            name = m.Username
        }
        sb.WriteString(fmt.Sprintf("%s: %s\n", name, m.Text))
    }

    ctx := context.Background()
    t := float32(0.2)
    resp, err := client.Models.GenerateContent(ctx, flashModel,
        genai.Text(sb.String()),
        &genai.GenerateContentConfig{
            SystemInstruction: genai.NewContentFromText(noteGenerationPrompt, genai.RoleModel),
            Temperature:       &t,
            ResponseMIMEType:  "application/json",
            ResponseSchema: &genai.Schema{
                Type: genai.TypeObject,
                Properties: map[string]*genai.Schema{
                    "title":   {Type: genai.TypeString},
                    "summary": {Type: genai.TypeString},
                },
                Required: []string{"title", "summary"},
            },
        },
    )
    if err != nil {
        return "", "", err
    }
    if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
        return "", "", fmt.Errorf("empty response from Gemini")
    }

    var text string
    for _, part := range resp.Candidates[0].Content.Parts {
        if part.Text != "" {
            text += part.Text
        }
    }

    var out noteOutput
    if err := json.Unmarshal([]byte(text), &out); err != nil {
        return "", "", fmt.Errorf("unmarshal note: %w", err)
    }
    return out.Title, out.Summary, nil
}
```

**Step 4: Run — verify it passes**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestGenerateConversationNote -v -timeout 30s
```

Expected: PASS (or SKIP if no token).

**Step 5: Commit**

```bash
git add internal/memory/notes.go internal/memory/notes_test.go
git commit -m "feat(memory): add generateConversationNote with Gemini flash"
```

---

### Task 5: Channel buffer — BufferMessage with inactivity timer and max age

**Files:**
- Create: `internal/memory/buffer.go`

This replaces `extract.go`'s per-user buffer with a per-channel buffer. The flush logic (note generation + profile update) comes in Task 6. This task just covers the buffer management.

**Step 1: Write the failing test**

Create `internal/memory/buffer_test.go`:

```go
package memory

import (
    "testing"
    "time"
)

func TestBufferMessage_NewBuffer(t *testing.T) {
    // Reset global state
    channelBuffersMu.Lock()
    channelBuffers = make(map[string]*channelBuffer)
    channelBuffersMu.Unlock()

    BufferMessage("chan1", "guild1", "u1", "alice", "Alice", "Hello!", "msg1")

    channelBuffersMu.Lock()
    buf, ok := channelBuffers["chan1"]
    channelBuffersMu.Unlock()

    if !ok {
        t.Fatal("expected buffer for chan1")
    }
    if len(buf.messages) != 1 {
        t.Errorf("messages len = %d, want 1", len(buf.messages))
    }
    if buf.messages[0].Text != "Hello!" {
        t.Errorf("text = %q, want %q", buf.messages[0].Text, "Hello!")
    }
    if buf.timer == nil {
        t.Error("inactivity timer must be set")
    }
    buf.timer.Stop() // cleanup
}

func TestBufferMessage_AppendToExisting(t *testing.T) {
    channelBuffersMu.Lock()
    channelBuffers = make(map[string]*channelBuffer)
    channelBuffersMu.Unlock()

    BufferMessage("chan2", "guild1", "u1", "alice", "Alice", "first", "m1")
    BufferMessage("chan2", "guild1", "u2", "bob", "Bob", "second", "m2")

    channelBuffersMu.Lock()
    buf := channelBuffers["chan2"]
    channelBuffersMu.Unlock()

    if len(buf.messages) != 2 {
        t.Fatalf("messages len = %d, want 2", len(buf.messages))
    }
    buf.timer.Stop()
}

func TestBufferMessage_MaxAge_ForcesFlush(t *testing.T) {
    // When a buffer is older than bufferMaxAge and a new message arrives,
    // the old buffer should be flushed (synchronously in test via a replaced
    // flush func) and a new buffer started.
    channelBuffersMu.Lock()
    channelBuffers = make(map[string]*channelBuffer)
    channelBuffersMu.Unlock()

    // Manually insert a stale buffer (started 3 hours ago)
    stale := &channelBuffer{
        channelID: "chan3",
        guildID:   "guild1",
        messages:  []bufMsg{{Text: "old message"}},
        startedAt: time.Now().Add(-3 * time.Hour),
    }
    // Use a no-op timer so it doesn't fire
    stale.timer = time.AfterFunc(24*time.Hour, func() {})

    channelBuffersMu.Lock()
    channelBuffers["chan3"] = stale
    channelBuffersMu.Unlock()

    var flushedChannelID string
    origFlush := testFlushFn
    testFlushFn = func(chID string) { flushedChannelID = chID }
    defer func() { testFlushFn = origFlush }()

    BufferMessage("chan3", "guild1", "u1", "alice", "Alice", "new message", "m2")

    if flushedChannelID != "chan3" {
        t.Errorf("expected chan3 to be force-flushed on max age, got %q", flushedChannelID)
    }

    channelBuffersMu.Lock()
    buf := channelBuffers["chan3"]
    channelBuffersMu.Unlock()

    if len(buf.messages) != 1 || buf.messages[0].Text != "new message" {
        t.Errorf("expected new buffer with 1 message 'new message', got %+v", buf.messages)
    }
    buf.timer.Stop()
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestBufferMessage -v
```

Expected: FAIL (compile errors).

**Step 3: Create `internal/memory/buffer.go`**

```go
package memory

import (
    "log"
    "sync"
    "time"
)

// channelBuffer holds in-flight messages for one Discord channel or thread.
type channelBuffer struct {
    channelID string
    guildID   string
    messages  []bufMsg
    timer     *time.Timer
    startedAt time.Time
}

var (
    channelBuffers   = make(map[string]*channelBuffer)
    channelBuffersMu sync.Mutex
    // testFlushFn is overridden in tests to intercept flush calls.
    testFlushFn func(channelID string)
)

// BufferMessage appends a human message to the per-channel buffer.
// It resets the inactivity timer and force-flushes buffers older than bufferMaxAge.
func BufferMessage(channelID, guildID, discordID, username, displayName, text, messageID string) {
    if !enabled {
        return
    }

    msg := bufMsg{
        DiscordID:   discordID,
        Username:    username,
        DisplayName: displayName,
        Text:        text,
        MessageID:   messageID,
    }

    channelBuffersMu.Lock()
    buf, exists := channelBuffers[channelID]

    if exists && time.Since(buf.startedAt) >= bufferMaxAge {
        // Force flush before appending — prevents unbounded buffers.
        buf.timer.Stop()
        delete(channelBuffers, channelID)
        channelBuffersMu.Unlock()
        doFlush(channelID)
        channelBuffersMu.Lock()
        exists = false
    }

    if !exists {
        buf = &channelBuffer{
            channelID: channelID,
            guildID:   guildID,
            startedAt: time.Now(),
        }
        channelBuffers[channelID] = buf
    }

    buf.messages = append(buf.messages, msg)

    if buf.timer != nil {
        buf.timer.Stop()
    }
    buf.timer = time.AfterFunc(bufferInactivity, func() {
        doFlush(channelID)
    })
    channelBuffersMu.Unlock()

    saveChannelBuffer(channelID, guildID, buf.messages, buf.startedAt)
}

// doFlush removes the buffer from the map and triggers the flush pipeline.
// If testFlushFn is set (tests only), it is called instead of flushChannelBuffer.
func doFlush(channelID string) {
    if testFlushFn != nil {
        testFlushFn(channelID)
        return
    }
    flushChannelBuffer(channelID)
}

// flushChannelBuffer is defined in Task 6.
func flushChannelBuffer(channelID string) {
    channelBuffersMu.Lock()
    buf, exists := channelBuffers[channelID]
    if !exists {
        channelBuffersMu.Unlock()
        return
    }
    msgs := buf.messages
    guildID := buf.guildID
    delete(channelBuffers, channelID)
    channelBuffersMu.Unlock()

    deleteChannelBuffer(channelID)

    if len(msgs) == 0 {
        return
    }

    // Minimum content gate: skip if total text is too short.
    var totalLen int
    for _, m := range msgs {
        totalLen += len(m.Text)
    }
    if totalLen < minMessageLength {
        return
    }

    title, summary, err := generateConversationNote(channelID, guildID, msgs)
    if err != nil {
        log.Printf("memory: note generation failed for %s: %v", channelID, err)
        return
    }

    // Collect unique participant discord IDs and upsert them.
    seen := make(map[string]bool)
    var participants []string
    for _, m := range msgs {
        if !seen[m.DiscordID] {
            seen[m.DiscordID] = true
            participants = append(participants, m.DiscordID)
        }
    }

    today := time.Now().UTC().Format("2006-01-02")
    note := &interactionNote{
        GuildID:      guildID,
        ChannelID:    channelID,
        NoteType:     "conversation",
        Participants: participants,
        Title:        title,
        Summary:      summary,
        NoteDate:     today,
    }

    embedding, err := embed(nil, title+" "+summary)
    if err != nil {
        log.Printf("memory: note embedding failed for %s: %v", channelID, err)
        return
    }

    noteID, err := insertNote(note, embedding)
    if err != nil {
        log.Printf("memory: insertNote failed for %s: %v", channelID, err)
        return
    }

    // Update profiles for each participant.
    for _, discordID := range participants {
        if err := updateUserProfile(discordID, noteID, title, summary); err != nil {
            log.Printf("memory: profile update failed for %s: %v", discordID, err)
        }
    }
}
```

Note: `embed` takes a `context.Context`. Fix the call signature to pass `context.Background()`:
```go
import "context"

// in flushChannelBuffer:
ctx := context.Background()
embedding, err := embed(ctx, title+" "+summary)
```

**Step 4: Run — verify tests pass**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestBufferMessage -v
```

Expected: PASS

**Step 5: Commit**

```bash
git add internal/memory/buffer.go internal/memory/buffer_test.go
git commit -m "feat(memory): add per-channel BufferMessage with inactivity timer and max age"
```

---

### Task 6: Buffer persistence — save/load channel_buffers to/from SQLite

**Files:**
- Modify: `internal/memory/buffer.go`

**Step 1: Write the failing test**

Add to `buffer_test.go`:

```go
func TestSaveAndLoadChannelBuffer(t *testing.T) {
    setupTestDB(t)

    msgs := []bufMsg{
        {DiscordID: "u1", Username: "alice", Text: "hello"},
        {DiscordID: "u2", Username: "bob", Text: "world"},
    }
    startedAt := time.Now().Truncate(time.Second)

    if err := saveChannelBuffer("chan10", "guild1", msgs, startedAt); err != nil {
        t.Fatalf("saveChannelBuffer: %v", err)
    }

    loaded, err := loadChannelBuffers()
    if err != nil {
        t.Fatalf("loadChannelBuffers: %v", err)
    }
    if len(loaded) != 1 {
        t.Fatalf("loaded len = %d, want 1", len(loaded))
    }
    if loaded[0].channelID != "chan10" {
        t.Errorf("channelID = %q, want %q", loaded[0].channelID, "chan10")
    }
    if len(loaded[0].messages) != 2 {
        t.Errorf("messages len = %d, want 2", len(loaded[0].messages))
    }
}

func TestDeleteChannelBuffer(t *testing.T) {
    setupTestDB(t)

    msgs := []bufMsg{{Text: "hi"}}
    saveChannelBuffer("chan11", "guild1", msgs, time.Now())

    if err := deleteChannelBuffer("chan11"); err != nil {
        t.Fatalf("deleteChannelBuffer: %v", err)
    }

    loaded, _ := loadChannelBuffers()
    for _, b := range loaded {
        if b.channelID == "chan11" {
            t.Error("buffer chan11 should have been deleted")
        }
    }
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestSaveAndLoadChannelBuffer -v
```

Expected: FAIL (undefined functions).

**Step 3: Add persistence functions to `buffer.go`**

```go
import "encoding/json"

// saveChannelBuffer upserts the channel buffer to the channel_buffers table.
func saveChannelBuffer(channelID, guildID string, msgs []bufMsg, startedAt time.Time) error {
    if database == nil {
        return nil
    }
    data, err := json.Marshal(msgs)
    if err != nil {
        return err
    }
    _, err = database.Exec(`
        INSERT INTO channel_buffers (channel_id, guild_id, messages, started_at, updated_at)
        VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(channel_id) DO UPDATE SET
            messages   = excluded.messages,
            updated_at = CURRENT_TIMESTAMP
    `, channelID, guildID, string(data), startedAt.UTC().Format("2006-01-02 15:04:05"))
    return err
}

// deleteChannelBuffer removes a flushed buffer from SQLite.
func deleteChannelBuffer(channelID string) error {
    if database == nil {
        return nil
    }
    _, err := database.Exec("DELETE FROM channel_buffers WHERE channel_id = ?", channelID)
    return err
}

// loadChannelBuffers reads all persisted buffers from SQLite (called at startup).
func loadChannelBuffers() ([]*channelBuffer, error) {
    if database == nil {
        return nil, nil
    }
    rows, err := database.Query(
        "SELECT channel_id, guild_id, messages, started_at FROM channel_buffers",
    )
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var results []*channelBuffer
    for rows.Next() {
        var b channelBuffer
        var msgsJSON, startedAtStr string
        if err := rows.Scan(&b.channelID, &b.guildID, &msgsJSON, &startedAtStr); err != nil {
            log.Printf("memory: scan channel_buffers: %v", err)
            continue
        }
        if err := json.Unmarshal([]byte(msgsJSON), &b.messages); err != nil {
            log.Printf("memory: unmarshal channel_buffers messages: %v", err)
            continue
        }
        b.startedAt, _ = time.Parse("2006-01-02 15:04:05", startedAtStr)
        results = append(results, &b)
    }
    return results, rows.Err()
}
```

**Step 4: Run — verify tests pass**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestSaveAndLoadChannelBuffer -v
/usr/local/go/bin/go test ./internal/memory/ -run TestDeleteChannelBuffer -v
```

Expected: both PASS

**Step 5: Commit**

```bash
git add internal/memory/buffer.go internal/memory/buffer_test.go
git commit -m "feat(memory): persist channel buffers to SQLite for crash recovery"
```

---

### Task 7: User profile CRUD

**Files:**
- Create: `internal/memory/profiles.go`

This task implements: `updateUserProfile` (Gemini flash), `GetUserProfile`, `DeleteUserProfile`, and `GetRecentNotes`.

**Step 1: Write the failing tests**

Create `internal/memory/profiles_test.go`:

```go
package memory

import (
    "encoding/json"
    "testing"
)

func TestGetUserProfile_Empty(t *testing.T) {
    setupTestDB(t)

    // User has no profile row — should return an empty profile, not an error.
    profile, err := GetUserProfile("nonexistent")
    if err != nil {
        t.Fatalf("GetUserProfile: %v", err)
    }
    if profile != nil {
        t.Errorf("expected nil profile for unknown user, got %+v", profile)
    }
}

func TestGetUserProfile_WithData(t *testing.T) {
    setupTestDB(t)

    // Insert a user and a profile row manually.
    id, _, err := upsertUser("profile_u1", "alice", "Alice")
    if err != nil {
        t.Fatalf("upsertUser: %v", err)
    }

    bio := []ProfileFact{{Text: "Alice lives in Austin.", NoteID: 1}}
    bioJSON, _ := json.Marshal(bio)
    database.Exec(
        "INSERT INTO user_profiles (user_id, bio) VALUES (?, ?)", id, string(bioJSON),
    )

    profile, err := GetUserProfile("profile_u1")
    if err != nil {
        t.Fatalf("GetUserProfile: %v", err)
    }
    if profile == nil {
        t.Fatal("expected profile, got nil")
    }
    if len(profile.Bio) != 1 {
        t.Fatalf("bio len = %d, want 1", len(profile.Bio))
    }
    if profile.Bio[0].Text != "Alice lives in Austin." {
        t.Errorf("bio text = %q", profile.Bio[0].Text)
    }
}

func TestDeleteUserProfile(t *testing.T) {
    setupTestDB(t)

    id, _, err := upsertUser("profile_u2", "bob", "Bob")
    if err != nil {
        t.Fatalf("upsertUser: %v", err)
    }
    database.Exec("INSERT INTO user_profiles (user_id) VALUES (?)", id)

    if err := DeleteUserProfile("profile_u2"); err != nil {
        t.Fatalf("DeleteUserProfile: %v", err)
    }

    profile, _ := GetUserProfile("profile_u2")
    if profile != nil {
        t.Error("profile should be nil after deletion")
    }
}

func TestGetRecentNotes(t *testing.T) {
    setupTestDB(t)

    note := &interactionNote{
        GuildID:      "g1",
        ChannelID:    "c1",
        NoteType:     "conversation",
        Participants: []string{"u1"},
        Title:        "Test chat",
        Summary:      "A test.",
        NoteDate:     "2026-02-25",
    }
    embedding := make([]float32, embeddingDimensions)
    insertNote(note, embedding)

    notes := GetRecentNotes(5)
    if len(notes) != 1 {
        t.Fatalf("GetRecentNotes len = %d, want 1", len(notes))
    }
    if notes[0].Title != "Test chat" {
        t.Errorf("title = %q, want %q", notes[0].Title, "Test chat")
    }
}

func TestUpdateUserProfile(t *testing.T) {
    setupTestDB(t)
    setupGemini(t)

    id, _, err := upsertUser("profile_u3", "charlie", "Charlie")
    if err != nil {
        t.Fatalf("upsertUser: %v", err)
    }
    _ = id

    err = updateUserProfile("profile_u3", 1, "Gaming chat", "Charlie and others discussed Baldur's Gate 3.")
    if err != nil {
        t.Fatalf("updateUserProfile: %v", err)
    }

    profile, err := GetUserProfile("profile_u3")
    if err != nil {
        t.Fatalf("GetUserProfile after update: %v", err)
    }
    // A Gemini call may or may not find facts — just verify no panic and no error.
    _ = profile
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestGetUserProfile -v
```

Expected: FAIL (compile errors).

**Step 3: Create `internal/memory/profiles.go`**

```go
package memory

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    "log"

    "google.golang.org/genai"
)

// RecentNote is returned by GetRecentNotes for display in commands.
type RecentNote struct {
    Title    string
    Summary  string
    NoteDate string
}

// GetUserProfile returns the profile for a Discord user, or nil if none exists.
func GetUserProfile(discordID string) (*UserProfile, error) {
    if database == nil {
        return nil, nil
    }

    var userID int64
    err := database.QueryRow("SELECT id FROM users WHERE discord_id = ?", discordID).Scan(&userID)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    var p UserProfile
    p.UserID = userID

    var bio, interests, skills, opinions, relationships, other, updatedAt string
    err = database.QueryRow(`
        SELECT bio, interests, skills, opinions, relationships, other, updated_at
        FROM user_profiles WHERE user_id = ?
    `, userID).Scan(&bio, &interests, &skills, &opinions, &relationships, &other, &updatedAt)
    if err == sql.ErrNoRows {
        return nil, nil
    }
    if err != nil {
        return nil, err
    }

    json.Unmarshal([]byte(bio), &p.Bio)
    json.Unmarshal([]byte(interests), &p.Interests)
    json.Unmarshal([]byte(skills), &p.Skills)
    json.Unmarshal([]byte(opinions), &p.Opinions)
    json.Unmarshal([]byte(relationships), &p.Relationships)
    json.Unmarshal([]byte(other), &p.Other)
    p.UpdatedAt = updatedAt

    return &p, nil
}

// DeleteUserProfile removes a user's profile row (keeps the users row).
func DeleteUserProfile(discordID string) error {
    if database == nil {
        return nil
    }
    _, err := database.Exec(`
        DELETE FROM user_profiles
        WHERE user_id = (SELECT id FROM users WHERE discord_id = ?)
    `, discordID)
    return err
}

// GetRecentNotes returns the most recently created interaction notes.
func GetRecentNotes(limit int) []RecentNote {
    if database == nil {
        return nil
    }
    rows, err := database.Query(`
        SELECT title, summary, note_date
        FROM interaction_notes
        ORDER BY created_at DESC
        LIMIT ?
    `, limit)
    if err != nil {
        log.Printf("memory: GetRecentNotes: %v", err)
        return nil
    }
    defer rows.Close()

    var notes []RecentNote
    for rows.Next() {
        var n RecentNote
        if err := rows.Scan(&n.Title, &n.Summary, &n.NoteDate); err != nil {
            continue
        }
        notes = append(notes, n)
    }
    return notes
}

const profileUpdatePrompt = `You update a user's memory profile based on a new conversation note they participated in.

The profile has sections: bio, interests, skills, opinions, relationships, other.
Each section is a JSON array of {"text": "...", "note_id": N} objects.

Rules:
- Add new facts the profile doesn't already capture. Always set note_id to the provided note ID.
- If a fact contradicts an existing one, replace the old one.
- If a fact is already covered, skip it (don't duplicate).
- If there's nothing new to learn about this specific user from the note, return the profile unchanged.
- Return the complete updated profile as a JSON object with all six sections.
- Keep facts concise (one sentence each), in third person.`

type profileSections struct {
    Bio           []ProfileFact `json:"bio"`
    Interests     []ProfileFact `json:"interests"`
    Skills        []ProfileFact `json:"skills"`
    Opinions      []ProfileFact `json:"opinions"`
    Relationships []ProfileFact `json:"relationships"`
    Other         []ProfileFact `json:"other"`
}

// updateUserProfile calls Gemini flash to integrate a new conversation note
// into a user's profile, then writes the updated profile to user_profiles.
func updateUserProfile(discordID string, noteID int64, noteTitle, noteSummary string) error {
    if !enabled {
        return nil
    }

    // Upsert the user so we have a user_id.
    userID, _, err := upsertUser(discordID, discordID, "")
    if err != nil {
        return fmt.Errorf("upsertUser: %w", err)
    }

    // Read current profile (may be empty if first note).
    current, err := GetUserProfile(discordID)
    if err != nil {
        return err
    }

    var sections profileSections
    if current != nil {
        sections.Bio = current.Bio
        sections.Interests = current.Interests
        sections.Skills = current.Skills
        sections.Opinions = current.Opinions
        sections.Relationships = current.Relationships
        sections.Other = current.Other
    }

    currentJSON, _ := json.Marshal(sections)
    prompt := fmt.Sprintf(
        "Note ID: %d\nNote title: %s\nNote summary: %s\n\nCurrent profile:\n%s",
        noteID, noteTitle, noteSummary, string(currentJSON),
    )

    ctx := context.Background()
    t := float32(0.1)
    resp, err := client.Models.GenerateContent(ctx, flashModel,
        genai.Text(prompt),
        &genai.GenerateContentConfig{
            SystemInstruction: genai.NewContentFromText(profileUpdatePrompt, genai.RoleModel),
            Temperature:       &t,
            ResponseMIMEType:  "application/json",
            ResponseSchema: &genai.Schema{
                Type: genai.TypeObject,
                Properties: map[string]*genai.Schema{
                    "bio":           {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "interests":     {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "skills":        {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "opinions":      {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "relationships": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "other":         {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                },
            },
        },
    )
    if err != nil {
        return err
    }

    if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
        return nil // no update
    }

    var respText string
    for _, part := range resp.Candidates[0].Content.Parts {
        if part.Text != "" {
            respText += part.Text
        }
    }

    var updated profileSections
    if err := json.Unmarshal([]byte(respText), &updated); err != nil {
        return fmt.Errorf("unmarshal profile update: %w", err)
    }

    return writeUserProfile(userID, updated)
}

// writeUserProfile upserts all profile sections to user_profiles.
func writeUserProfile(userID int64, sections profileSections) error {
    bio, _ := json.Marshal(sections.Bio)
    interests, _ := json.Marshal(sections.Interests)
    skills, _ := json.Marshal(sections.Skills)
    opinions, _ := json.Marshal(sections.Opinions)
    relationships, _ := json.Marshal(sections.Relationships)
    other, _ := json.Marshal(sections.Other)

    _, err := database.Exec(`
        INSERT INTO user_profiles
            (user_id, bio, interests, skills, opinions, relationships, other, updated_at)
        VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
        ON CONFLICT(user_id) DO UPDATE SET
            bio           = excluded.bio,
            interests     = excluded.interests,
            skills        = excluded.skills,
            opinions      = excluded.opinions,
            relationships = excluded.relationships,
            other         = excluded.other,
            updated_at    = CURRENT_TIMESTAMP
    `, userID,
        string(bio), string(interests), string(skills),
        string(opinions), string(relationships), string(other),
    )
    return err
}
```

**Step 4: Run — verify tests pass**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run "TestGetUserProfile|TestDeleteUserProfile|TestGetRecentNotes|TestUpdateUserProfile" -v -timeout 30s
```

Expected: all PASS (or SKIP for Gemini test if no token).

**Step 5: Commit**

```bash
git add internal/memory/profiles.go internal/memory/profiles_test.go
git commit -m "feat(memory): add user profile CRUD and GetRecentNotes"
```

---

### Task 8: Rewrite retrieve.go — vec_notes search + profile injection

The public signature `RetrieveMultiUser(query string, users map[string]string) string` stays unchanged.

**Files:**
- Modify: `internal/memory/retrieve.go` (full rewrite)

**Step 1: Write the failing test**

Add to `notes_test.go` (or create `retrieve_test.go`):

Create `internal/memory/retrieve_test.go`:

```go
package memory

import (
    "strings"
    "testing"
)

func TestRetrieveMultiUser_Disabled(t *testing.T) {
    // No setupGemini — enabled stays false.
    result := RetrieveMultiUser("anything", map[string]string{"d1": "user"})
    if result != "" {
        t.Errorf("RetrieveMultiUser when disabled = %q, want empty", result)
    }
}

func TestRetrieveMultiUser_EmptyUsers(t *testing.T) {
    setupTestDB(t)
    setupGemini(t)
    result := RetrieveMultiUser("anything", map[string]string{})
    if result != "" {
        t.Errorf("expected empty for no users, got %q", result)
    }
}

func TestRetrieveMultiUser_WithNote(t *testing.T) {
    setupTestDB(t)
    setupGemini(t)

    // Insert a note about "u1".
    note := &interactionNote{
        GuildID:      "g1",
        ChannelID:    "c1",
        NoteType:     "conversation",
        Participants: []string{"u1"},
        Title:        "Piano discussion",
        Summary:      "Alice talked about playing piano for 10 years.",
        NoteDate:     "2026-02-25",
    }
    // Use a real embedding so ANN search can find it.
    embedding, err := embed(nil, "piano music instruments")
    // embed signature uses context — use context.Background() instead
    // (see note in Task 4 Step 3 about embed signature fix)
    if err != nil {
        t.Skipf("embed failed: %v", err)
    }
    insertNote(note, embedding)

    // Also upsert user so profile lookup works.
    upsertUser("u1", "alice", "Alice")

    result := RetrieveMultiUser("musical instruments piano", map[string]string{"u1": "Alice"})

    if result == "" {
        t.Error("expected non-empty XML")
    }
    if !strings.Contains(result, "<background_facts>") {
        t.Errorf("missing <background_facts> wrapper: %s", result)
    }
}
```

Note: `embed` currently takes `(ctx context.Context, text string)`. Update the test call to pass `context.Background()`.

**Step 2: Run — verify baseline (existing retrieve.go tests may still pass)**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestRetrieveMultiUser -v -timeout 30s
```

Note the output — some v1 tests may pass, some fail. We'll replace all of them.

**Step 3: Rewrite `internal/memory/retrieve.go`**

Replace the entire file:

```go
package memory

import (
    "context"
    "fmt"
    "log"
    "regexp"
    "strings"
    "sync"
    "time"
)

var mentionRe = regexp.MustCompile(`<@!?(\d+)>`)

// RetrieveMultiUser fetches relevant notes and profiles for the given users
// and formats them into an XML block for system prompt injection.
// Signature is unchanged from v1 for compatibility with messages.go.
func RetrieveMultiUser(query string, users map[string]string) string {
    if !enabled || len(users) == 0 || strings.TrimSpace(query) == "" {
        return ""
    }

    ctx := context.Background()

    embedding, err := embed(ctx, query)
    if err != nil {
        log.Printf("memory: retrieval embedding failed: %v", err)
        return ""
    }

    notes, err := searchNotes(embedding, noteRetrievalLimit)
    if err != nil {
        log.Printf("memory: note search failed: %v", err)
    }

    // Collect discord IDs whose profiles we need.
    profileIDs := make(map[string]bool)
    for id := range users {
        profileIDs[id] = true
    }
    // Add @mentioned users (up to 3 extra).
    extras := 0
    for _, m := range mentionRe.FindAllStringSubmatch(query, -1) {
        if len(m) > 1 && !profileIDs[m[1]] {
            profileIDs[m[1]] = true
            extras++
            if extras >= profilesFromNotes {
                break
            }
        }
    }
    // Add participants from retrieved notes (up to 3 extra).
    fromNotes := 0
    for _, n := range notes {
        for _, pid := range n.Participants {
            if !profileIDs[pid] {
                profileIDs[pid] = true
                fromNotes++
                if fromNotes >= profilesFromNotes {
                    break
                }
            }
        }
        if fromNotes >= profilesFromNotes {
            break
        }
    }

    // Fetch profiles concurrently.
    type profileResult struct {
        discordID string
        profile   *UserProfile
    }
    profileCh := make(chan profileResult, len(profileIDs))
    var wg sync.WaitGroup
    for id := range profileIDs {
        wg.Add(1)
        go func(did string) {
            defer wg.Done()
            p, err := GetUserProfile(did)
            if err != nil {
                log.Printf("memory: GetUserProfile %s: %v", did, err)
            }
            profileCh <- profileResult{discordID: did, profile: p}
        }(id)
    }
    wg.Wait()
    close(profileCh)

    profiles := make(map[string]*UserProfile)
    for r := range profileCh {
        if r.profile != nil {
            profiles[r.discordID] = r.profile
        }
    }

    if len(profiles) == 0 && len(notes) == 0 {
        return ""
    }

    return formatMemoryXML(users, profiles, notes)
}

// searchNotes queries vec_notes for the top k most relevant notes.
func searchNotes(embedding []float32, k int) ([]*interactionNote, error) {
    rows, err := database.Query(`
        SELECT n.id, n.guild_id, n.channel_id, n.note_type,
               n.participants, n.title, n.summary, n.note_date
        FROM vec_notes vn
        JOIN interaction_notes n ON n.id = vn.note_id
        WHERE vn.embedding MATCH ?
          AND k = ?
        ORDER BY vn.distance
    `, serializeFloat32(embedding), k)
    if err != nil {
        return nil, err
    }
    defer rows.Close()

    var notes []*interactionNote
    for rows.Next() {
        var n interactionNote
        var participantsJSON string
        if err := rows.Scan(
            &n.ID, &n.GuildID, &n.ChannelID, &n.NoteType,
            &participantsJSON, &n.Title, &n.Summary, &n.NoteDate,
        ); err != nil {
            log.Printf("memory: searchNotes scan: %v", err)
            continue
        }
        // json.Unmarshal participants
        import_json_unmarshal(&n, participantsJSON)
        notes = append(notes, &n)
    }
    return notes, rows.Err()
}
```

Note: replace the placeholder `import_json_unmarshal` with:
```go
import "encoding/json"
// ...
json.Unmarshal([]byte(participantsJSON), &n.Participants)
```

Add `formatMemoryXML`:

```go
// formatMemoryXML builds the <background_facts> block for system prompt injection.
// Format mirrors the v2 design doc with profile sections and source references.
func formatMemoryXML(
    conversationUsers map[string]string, // discordID → username
    profiles map[string]*UserProfile,
    notes []*interactionNote,
) string {
    var sb strings.Builder
    sb.WriteString("<background_facts>\n")

    // Write a <user> block for each profile we have.
    for discordID, p := range profiles {
        name := conversationUsers[discordID]
        if name == "" {
            name = discordID // fallback
        }
        sb.WriteString(fmt.Sprintf("<user name=%q>\n", name))

        writeSectionIfNonEmpty(&sb, "Bio", p.Bio)
        writeSectionIfNonEmpty(&sb, "Interests", p.Interests)
        writeSectionIfNonEmpty(&sb, "Skills", p.Skills)
        writeSectionIfNonEmpty(&sb, "Opinions", p.Opinions)
        writeSectionIfNonEmpty(&sb, "Relationships", p.Relationships)
        writeSectionIfNonEmpty(&sb, "Other", p.Other)

        sb.WriteString("</user>\n")
    }

    // Write retrieved notes.
    if len(notes) > 0 {
        sb.WriteString("<notes>\n")
        for _, n := range notes {
            date := n.NoteDate
            if len(date) >= 10 {
                t, err := time.Parse("2006-01-02", date[:10])
                if err == nil {
                    date = t.Format("Jan 2")
                }
            }
            sb.WriteString(fmt.Sprintf("- [%s] %s — %s\n", date, n.Title, n.Summary))
        }
        sb.WriteString("</notes>\n")
    }

    sb.WriteString("</background_facts>")
    return sb.String()
}

func writeSectionIfNonEmpty(sb *strings.Builder, label string, facts []ProfileFact) {
    if len(facts) == 0 {
        return
    }
    sb.WriteString(label + ":\n")
    for _, f := range facts {
        sb.WriteString(fmt.Sprintf("- %s\n", f.Text))
    }
}
```

Remove the old `Retrieve`, `RetrieveGeneral`, `formatFactsXML`, `safeDate` functions from the file — they are replaced.

**Step 4: Run the retrieve tests**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run "TestRetrieveMultiUser" -v -timeout 30s
```

Also run the full suite to check nothing else broke:

```bash
/usr/local/go/bin/go test ./internal/memory/ -v -timeout 60s
```

Expected: v2 retrieve tests PASS. Old `TestRetrieve`, `TestRetrieveGeneral` tests that reference removed functions will need to be deleted from `memory_test.go` — do that now. Also delete `TestFormatFactsXML` since `formatFactsXML` is removed.

**Step 5: Commit**

```bash
git add internal/memory/retrieve.go internal/memory/retrieve_test.go internal/memory/memory_test.go
git commit -m "feat(memory): rewrite RetrieveMultiUser with vec_notes + profile injection"
```

---

### Task 9: Midnight job — topic clustering and profile consolidation

**Files:**
- Create: `internal/memory/midnight.go`

**Step 1: Write the failing test**

Create `internal/memory/midnight_test.go`:

```go
package memory

import (
    "testing"
)

func TestRunMidnightJob_NoNotes(t *testing.T) {
    setupTestDB(t)
    setupGemini(t)

    // No notes in DB — job should complete without error.
    if err := runMidnightJob("guild1"); err != nil {
        t.Fatalf("runMidnightJob with no notes: %v", err)
    }
}

func TestRunMidnightJob_WithNotes(t *testing.T) {
    setupTestDB(t)
    setupGemini(t)

    today := "2026-02-25"

    // Insert a few conversation notes for guild1.
    for i, title := range []string{"Gaming discussion", "Movie picks", "Tech chat"} {
        upsertUser(fmt.Sprintf("u%d", i+1), fmt.Sprintf("user%d", i+1), fmt.Sprintf("User%d", i+1))
        note := &interactionNote{
            GuildID:      "guild1",
            ChannelID:    fmt.Sprintf("chan%d", i+1),
            NoteType:     "conversation",
            Participants: []string{fmt.Sprintf("u%d", i+1)},
            Title:        title,
            Summary:      fmt.Sprintf("Discussion about %s.", title),
            NoteDate:     today,
        }
        embedding := make([]float32, embeddingDimensions)
        insertNote(note, embedding)
    }

    if err := runMidnightJob("guild1"); err != nil {
        t.Fatalf("runMidnightJob: %v", err)
    }

    // Verify at least one topic_cluster note was created.
    var count int
    database.QueryRow(
        "SELECT COUNT(*) FROM interaction_notes WHERE note_type = 'topic_cluster' AND guild_id = 'guild1'",
    ).Scan(&count)
    if count == 0 {
        t.Error("expected at least one topic_cluster note, got 0")
    }
}
```

You'll need `fmt` imported in the test file.

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestRunMidnightJob -v -timeout 60s
```

Expected: FAIL (undefined `runMidnightJob`).

**Step 3: Create `internal/memory/midnight.go`**

```go
package memory

import (
    "context"
    "encoding/json"
    "fmt"
    "log"
    "strings"
    "time"

    "google.golang.org/genai"
)

const clusteringPrompt = `You cluster a list of conversation notes from a Discord server into thematic topic groups.

Each input note has an ID, title, participants, and summary.
Return 3–8 topic clusters. Each cluster should:
- Have a descriptive title (5–10 words)
- Have a summary paragraph (2–4 sentences) of what was discussed across all its source notes
- Include the list of source note IDs it covers
- Include the union of participants across its source notes

Return as a JSON array of cluster objects.`

type clusterOutput struct {
    Title         string   `json:"title"`
    Summary       string   `json:"summary"`
    SourceNoteIDs []int64  `json:"source_note_ids"`
    Participants  []string `json:"participants"`
}

const consolidationPrompt = `You consolidate a user's memory profile given today's interaction notes they participated in.

Profile sections: bio, interests, skills, opinions, relationships, other.
Each section is a JSON array of {"text": "...", "note_id": N} objects.

Rules:
- Merge redundant facts. Keep the most specific version.
- Update outdated facts (e.g. old location → new location).
- Add new facts from today's notes. Set note_id to the source note's ID.
- Return the complete consolidated profile as a JSON object with all six sections.
- Remove facts that have been superseded.
- Keep facts concise (one sentence), third person.`

// runMidnightJob executes both passes of the nightly memory job for a guild.
// Pass 1: topic clustering of today's conversation notes.
// Pass 2: profile consolidation for each active user.
func runMidnightJob(guildID string) error {
    today := time.Now().UTC().Format("2006-01-02")

    // Pass 1: topic clustering.
    if err := clusterTodaysNotes(guildID, today); err != nil {
        log.Printf("memory: clustering failed for guild %s: %v", guildID, err)
        // Non-fatal — continue to pass 2.
    }

    // Pass 2: profile consolidation.
    if err := consolidateProfiles(guildID, today); err != nil {
        log.Printf("memory: profile consolidation failed for guild %s: %v", guildID, err)
    }

    // Record last run.
    _, err := database.Exec(`
        INSERT INTO memory_job_log (id, last_run) VALUES (1, CURRENT_TIMESTAMP)
        ON CONFLICT(id) DO UPDATE SET last_run = CURRENT_TIMESTAMP
    `)
    return err
}

// clusterTodaysNotes fetches today's conversation notes for a guild and
// groups them into topic clusters via Gemini pro.
func clusterTodaysNotes(guildID, date string) error {
    rows, err := database.Query(`
        SELECT id, title, summary, participants
        FROM interaction_notes
        WHERE guild_id = ? AND note_type = 'conversation' AND note_date = ?
        ORDER BY created_at
    `, guildID, date)
    if err != nil {
        return err
    }
    defer rows.Close()

    type noteRow struct {
        ID           int64
        Title        string
        Summary      string
        Participants []string
    }
    var notes []noteRow
    for rows.Next() {
        var n noteRow
        var participantsJSON string
        if err := rows.Scan(&n.ID, &n.Title, &n.Summary, &participantsJSON); err != nil {
            continue
        }
        json.Unmarshal([]byte(participantsJSON), &n.Participants)
        notes = append(notes, n)
    }
    if err := rows.Err(); err != nil {
        return err
    }
    if len(notes) == 0 {
        return nil // nothing to cluster
    }

    // Build prompt input.
    var sb strings.Builder
    for _, n := range notes {
        sb.WriteString(fmt.Sprintf(
            "ID %d | Title: %s | Participants: %v | Summary: %s\n",
            n.ID, n.Title, n.Participants, n.Summary,
        ))
    }

    ctx := context.Background()
    t := float32(0.3)
    resp, err := client.Models.GenerateContent(ctx, consolidationModel,
        genai.Text(sb.String()),
        &genai.GenerateContentConfig{
            SystemInstruction: genai.NewContentFromText(clusteringPrompt, genai.RoleModel),
            Temperature:       &t,
            ResponseMIMEType:  "application/json",
            ResponseSchema: &genai.Schema{
                Type: genai.TypeArray,
                Items: &genai.Schema{
                    Type: genai.TypeObject,
                    Properties: map[string]*genai.Schema{
                        "title":           {Type: genai.TypeString},
                        "summary":         {Type: genai.TypeString},
                        "source_note_ids": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeInteger}},
                        "participants":    {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeString}},
                    },
                    Required: []string{"title", "summary", "source_note_ids", "participants"},
                },
            },
        },
    )
    if err != nil {
        return err
    }
    if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
        return nil
    }

    var respText string
    for _, part := range resp.Candidates[0].Content.Parts {
        if part.Text != "" {
            respText += part.Text
        }
    }

    var clusters []clusterOutput
    if err := json.Unmarshal([]byte(respText), &clusters); err != nil {
        return fmt.Errorf("unmarshal clusters: %w", err)
    }

    for _, c := range clusters {
        note := &interactionNote{
            GuildID:       guildID,
            ChannelID:     "",
            NoteType:      "topic_cluster",
            Participants:  c.Participants,
            Title:         c.Title,
            Summary:       c.Summary,
            SourceNoteIDs: c.SourceNoteIDs,
            NoteDate:      date,
        }
        embedding, err := embed(ctx, c.Title+" "+c.Summary)
        if err != nil {
            log.Printf("memory: cluster embed failed: %v", err)
            continue
        }
        if _, err := insertNote(note, embedding); err != nil {
            log.Printf("memory: insert cluster failed: %v", err)
        }
    }
    return nil
}

// consolidateProfiles runs profile consolidation for each user who participated
// in today's notes.
func consolidateProfiles(guildID, date string) error {
    // Find all unique discord IDs that appeared as participants today.
    rows, err := database.Query(`
        SELECT DISTINCT participants
        FROM interaction_notes
        WHERE guild_id = ? AND note_date = ? AND note_type = 'conversation'
    `, guildID, date)
    if err != nil {
        return err
    }
    defer rows.Close()

    activeUsers := make(map[string]bool)
    for rows.Next() {
        var participantsJSON string
        if err := rows.Scan(&participantsJSON); err != nil {
            continue
        }
        var ids []string
        json.Unmarshal([]byte(participantsJSON), &ids)
        for _, id := range ids {
            activeUsers[id] = true
        }
    }

    for discordID := range activeUsers {
        if err := consolidateUserProfile(discordID, guildID, date); err != nil {
            log.Printf("memory: consolidate profile %s: %v", discordID, err)
        }
    }
    return nil
}

// consolidateUserProfile calls Gemini pro to fully consolidate a user's profile
// given all of today's notes they participated in.
func consolidateUserProfile(discordID, guildID, date string) error {
    current, err := GetUserProfile(discordID)
    if err != nil || current == nil {
        return err
    }

    var sections profileSections
    sections.Bio = current.Bio
    sections.Interests = current.Interests
    sections.Skills = current.Skills
    sections.Opinions = current.Opinions
    sections.Relationships = current.Relationships
    sections.Other = current.Other

    currentJSON, _ := json.Marshal(sections)

    // Fetch all today's notes for this user.
    rows, err := database.Query(`
        SELECT id, title, summary
        FROM interaction_notes
        WHERE guild_id = ? AND note_date = ? AND note_type = 'conversation'
          AND participants LIKE ?
    `, guildID, date, "%"+discordID+"%")
    if err != nil {
        return err
    }
    defer rows.Close()

    var notesSummary strings.Builder
    for rows.Next() {
        var id int64
        var title, summary string
        rows.Scan(&id, &title, &summary)
        notesSummary.WriteString(fmt.Sprintf("Note %d — %s: %s\n", id, title, summary))
    }
    if notesSummary.Len() == 0 {
        return nil
    }

    prompt := fmt.Sprintf(
        "Current profile:\n%s\n\nToday's notes:\n%s",
        string(currentJSON), notesSummary.String(),
    )

    ctx := context.Background()
    t := float32(0.1)
    resp, err := client.Models.GenerateContent(ctx, consolidationModel,
        genai.Text(prompt),
        &genai.GenerateContentConfig{
            SystemInstruction: genai.NewContentFromText(consolidationPrompt, genai.RoleModel),
            Temperature:       &t,
            ResponseMIMEType:  "application/json",
            ResponseSchema: &genai.Schema{
                Type: genai.TypeObject,
                Properties: map[string]*genai.Schema{
                    "bio":           {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "interests":     {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "skills":        {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "opinions":      {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "relationships": {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                    "other":         {Type: genai.TypeArray, Items: &genai.Schema{Type: genai.TypeObject}},
                },
            },
        },
    )
    if err != nil {
        return err
    }
    if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
        return nil
    }

    var respText string
    for _, part := range resp.Candidates[0].Content.Parts {
        if part.Text != "" {
            respText += part.Text
        }
    }

    var updated profileSections
    if err := json.Unmarshal([]byte(respText), &updated); err != nil {
        return fmt.Errorf("unmarshal consolidation: %w", err)
    }

    return writeUserProfile(current.UserID, updated)
}
```

**Step 4: Run — verify tests pass**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestRunMidnightJob -v -timeout 60s
```

Expected: PASS (or SKIP if no Gemini token).

**Step 5: Commit**

```bash
git add internal/memory/midnight.go internal/memory/midnight_test.go
git commit -m "feat(memory): add midnight job (topic clustering + profile consolidation)"
```

---

### Task 10: Wire Init — buffer reload and midnight catch-up

**Files:**
- Modify: `internal/memory/memory.go`

**Step 1: Write the failing test**

Add to `memory_test.go`:

```go
func TestInit_LoadsChannelBuffers(t *testing.T) {
    db.Open(":memory:")
    defer db.Close()

    // Manually pre-populate a channel_buffers row.
    db.DB.Exec(`INSERT INTO channel_buffers (channel_id, guild_id, messages, started_at)
        VALUES ('chan_reload', 'guild1', '[{"discord_id":"u1","text":"hello"}]', CURRENT_TIMESTAMP)`)

    // Call Init without a Gemini token — memory stays disabled but buffers load.
    os.Unsetenv("MEMORY_GEMINI_TOKEN")
    Init(db.DB)

    // Even when disabled, in-memory buffers should have been loaded.
    // (When enabled, timers would be restarted — here we just check the load path.)
    channelBuffersMu.Lock()
    _, loaded := channelBuffers["chan_reload"]
    channelBuffersMu.Unlock()

    // Cleanup
    database = nil
    channelBuffers = make(map[string]*channelBuffer)

    if !loaded {
        t.Error("expected chan_reload buffer to be loaded from SQLite on Init")
    }
}
```

**Step 2: Run — verify it fails**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestInit_LoadsChannelBuffers -v
```

Expected: FAIL.

**Step 3: Update `Init()` in `memory.go`**

After the existing `enabled = true` line, add:

```go
// Load any channel buffers that survived a restart.
loadAndRestartBuffers()

// Run midnight job on startup if overdue.
go func() {
    if shouldRunMidnightJob() {
        log.Println("memory: midnight job overdue — running catch-up")
        // Run for MainServer guild; extend if multi-guild needed.
        if err := runMidnightJob(config.MainServer); err != nil {
            log.Printf("memory: catch-up midnight job failed: %v", err)
        }
    }
}()

log.Println("Memory system initialized")
```

Add `loadAndRestartBuffers` to `buffer.go`:

```go
// loadAndRestartBuffers is called from Init to reload persisted buffers
// and restart their inactivity timers.
func loadAndRestartBuffers() {
    bufs, err := loadChannelBuffers()
    if err != nil {
        log.Printf("memory: loadChannelBuffers: %v", err)
        return
    }

    channelBuffersMu.Lock()
    defer channelBuffersMu.Unlock()

    for _, b := range bufs {
        buf := b
        buf.timer = time.AfterFunc(bufferInactivity, func() {
            doFlush(buf.channelID)
        })
        channelBuffers[buf.channelID] = buf
    }
    if len(bufs) > 0 {
        log.Printf("memory: reloaded %d channel buffers", len(bufs))
    }
}
```

Add `shouldRunMidnightJob` to `midnight.go`:

```go
// shouldRunMidnightJob returns true if the midnight job hasn't run in the last 24 hours.
func shouldRunMidnightJob() bool {
    if database == nil {
        return false
    }
    var lastRun string
    err := database.QueryRow("SELECT last_run FROM memory_job_log WHERE id = 1").Scan(&lastRun)
    if err != nil {
        return true // no record — run immediately
    }
    t, err := time.Parse("2006-01-02 15:04:05", lastRun)
    if err != nil {
        return true
    }
    return time.Since(t) > 24*time.Hour
}
```

Also add `config` import to `memory.go` for `config.MainServer`.

**Step 4: Run — verify tests pass**

```bash
/usr/local/go/bin/go test ./internal/memory/ -run TestInit_LoadsChannelBuffers -v
/usr/local/go/bin/go build ./...
```

Expected: PASS, build succeeds.

**Step 5: Commit**

```bash
git add internal/memory/memory.go internal/memory/buffer.go internal/memory/midnight.go internal/memory/memory_test.go
git commit -m "feat(memory): reload channel buffers and catch-up midnight job on Init"
```

---

### Task 11: Update messages.go — Extract → BufferMessage

**Files:**
- Modify: `internal/handler/messages.go`

**Step 1: No new test needed** — `handler/messages_test.go` already tests `shouldSkipMemory`. The behavior change (Extract → BufferMessage) is integration-level; the build check verifies it compiles.

**Step 2: Update the call in `messages.go`**

Replace line 47:

```go
go memory.Extract(m.Author.ID, m.Author.Username, m.Author.GlobalName, m.ID, extractContent)
```

With:

```go
go memory.BufferMessage(m.ChannelID, m.Message.GuildID, m.Author.ID, m.Author.Username, m.Author.GlobalName, extractContent, m.ID)
```

**Step 3: Verify build**

```bash
/usr/local/go/bin/go build ./...
```

Expected: success. If `Extract` no longer has any callers, the compiler will warn if it becomes unreferenced — that's fine, we'll remove it in Task 13.

**Step 4: Commit**

```bash
git add internal/handler/messages.go
git commit -m "feat(handler): switch fact extraction from Extract to BufferMessage"
```

---

### Task 12: Update commands.go and main.go — v1 → v2 public API

**Files:**
- Modify: `internal/handler/commands.go`
- Modify: `main.go`

**Step 1: Verify build before starting**

```bash
/usr/local/go/bin/go build ./...
```

Note the current v1 calls:
- `memory.GetUserFacts(user.ID)` — replace with `memory.GetUserProfile(user.ID)`
- `memory.DeleteUserFacts(user.ID)` — replace with `memory.DeleteUserProfile(user.ID)`
- `memory.DeleteAllFacts()` — replace with delete all profiles (new helper)
- `memory.GetRecentFacts(20)` — replace with `memory.GetRecentNotes(20)`
- `memory.RefreshFactNames(user.ID)` — **removed**, repurpose command to show "no longer needed"
- `memory.RefreshAllFactNames()` — same
- `memory.TotalFacts()` in `main.go` — replace with `memory.TotalNotes()`

**Step 2: Add `DeleteAllProfiles` to `profiles.go`**

```go
// DeleteAllProfiles removes all user profile rows.
func DeleteAllProfiles() (int64, error) {
    if database == nil {
        return 0, nil
    }
    res, err := database.Exec("DELETE FROM user_profiles")
    if err != nil {
        return 0, err
    }
    return res.RowsAffected()
}
```

**Step 3: Update `main.go`**

Replace:

```go
log.Printf("Active facts: %d", memory.TotalFacts())
```

With:

```go
log.Printf("Interaction notes: %d", memory.TotalNotes())
```

**Step 4: Update `commands.go` — `memory_admin_view`**

Replace the `GetUserFacts` block with:

```go
profile, err := memory.GetUserProfile(user.ID)
if err != nil || profile == nil {
    _, err := discord.SendFollowup(s, i, fmt.Sprintf("No profile stored for %s.", user.Username))
    if err != nil {
        log.Println(err)
    }
    return
}

var sb strings.Builder
sb.WriteString(fmt.Sprintf("**Profile for %s:**\n", user.Username))
writeProfileSection(&sb, "Bio", profile.Bio)
writeProfileSection(&sb, "Interests", profile.Interests)
writeProfileSection(&sb, "Skills", profile.Skills)
writeProfileSection(&sb, "Opinions", profile.Opinions)
writeProfileSection(&sb, "Relationships", profile.Relationships)
writeProfileSection(&sb, "Other", profile.Other)
```

Add helper at the bottom of commands.go:

```go
func writeProfileSection(sb *strings.Builder, label string, facts []memory.ProfileFact) {
    if len(facts) == 0 {
        return
    }
    sb.WriteString(fmt.Sprintf("**%s:**\n", label))
    for _, f := range facts {
        sb.WriteString(fmt.Sprintf("- %s\n", f.Text))
    }
}
```

**Step 5: Update `commands.go` — `memory_admin_delete`**

Replace `DeleteUserFacts`/`DeleteAllFacts` calls:

```go
if user != nil {
    err = memory.DeleteUserProfile(user.ID)
    message = fmt.Sprintf("Deleted profile for %s.", user.Username)
} else {
    _, err = memory.DeleteAllProfiles()
    message = fmt.Sprintf("Deleted all user profiles.")
}
```

Remove the `count` variable (DeleteUserProfile returns only error).

**Step 6: Update `commands.go` — `memory_self`**

Replace `GetUserFacts` with `GetUserProfile` and update the display similarly to `memory_admin_view`.

**Step 7: Update `commands.go` — `memory_admin_digest`**

Replace `GetRecentFacts` with `GetRecentNotes`:

```go
recentNotes := memory.GetRecentNotes(20)
if len(recentNotes) == 0 {
    _, err := discord.SendFollowup(s, i, "No notes recorded recently!")
    // ...
    return
}

var sb strings.Builder
sb.WriteString("**Memory Digest** — Recent conversation notes:\n\n")
for _, n := range recentNotes {
    sb.WriteString(fmt.Sprintf("**[%s] %s**\n%s\n\n", n.NoteDate, n.Title, n.Summary))
}
```

**Step 8: Update `commands.go` — `memory_admin_refreshnames`**

Replace the body with a message that the command is no longer applicable:

```go
_, err := discord.SendFollowup(s, i, "The `/memory refresh-names` command is no longer needed — profiles are maintained by Gemini directly.")
if err != nil {
    log.Println(err)
}
```

**Step 9: Verify build**

```bash
/usr/local/go/bin/go build ./...
```

Expected: success.

**Step 10: Commit**

```bash
git add internal/handler/commands.go internal/memory/profiles.go main.go
git commit -m "feat(commands): update memory slash commands to v2 profile API"
```

---

### Task 13: Remove old code — extract.go, consolidate.go, v1 memory.go code

Now that all callers use v2, remove the v1 code.

**Files:**
- Delete: `internal/memory/extract.go`
- Delete: `internal/memory/consolidate.go`
- Modify: `internal/memory/memory.go` — remove v1-only functions
- Modify: `internal/memory/memory_test.go` — remove v1 tests

**Step 1: Check for remaining references**

```bash
/usr/local/go/bin/go build ./...
grep -r "memory\.Extract\b" --include="*.go" .
grep -r "GetUserFacts\|DeleteUserFacts\|DeleteAllFacts\|GetRecentFacts\|RefreshFactNames\|RefreshAllFactNames\|TotalFacts\|insertFact\|replaceFact\|reinforceFact" --include="*.go" .
```

All should return nothing (only definitions in memory package, which we're about to remove).

**Step 2: Delete `extract.go`**

```bash
rm internal/memory/extract.go
```

**Step 3: Delete `consolidate.go`**

```bash
rm internal/memory/consolidate.go
```

**Step 4: Remove v1 functions from `memory.go`**

Remove these functions entirely:
- `insertFact`
- `replaceFact`
- `reinforceFact`
- `TotalFacts`
- `GetUserFacts` and the `Fact` type
- `DeleteUserFacts`
- `DeleteAllFacts`
- `GetRecentFacts`
- `RefreshFactNames`
- `RefreshAllFactNames`
- Also remove v1 constants now that extract.go/consolidate.go are gone: `similarityLimit`, `retrievalLimit`, `generalRetrievalLimit`, `minMessageLength`, `distanceThreshold`, `retrievalDistanceThreshold`

Keep: `embed`, `serializeFloat32`, `upsertUser`, `effectiveName`, `SetPreferredName`, `GetPreferredName`, `TotalNotes`, `Init`, and the type definitions added in Task 2.

**Step 5: Remove v1 tests from `memory_test.go`**

Remove tests for deleted functions:
- `TestInsertFactAndGetUserFacts`
- `TestReinforceFact`
- `TestReplaceFact`
- `TestTotalFacts`
- `TestDeleteUserFacts`
- `TestDeleteAllFacts`
- `TestRefreshFactNames`
- `TestRefreshAllFactNames`
- `TestGetRecentFacts`
- `TestFindSimilarFacts` (and `TestFindSimilarFacts_UserScoped`)
- `TestConsolidateAndStore_*` (all 4 variants)
- `TestFlushBuffer` (old per-user buffer)
- `TestExtractFacts`
- `TestDecideAction`
- `TestFormatFactsXML` (old format, deleted in Task 8)
- `TestConsolidateAndStore_ReinforceDoesNotSkipInvalidate`
- Old `TestRetrieve`, `TestRetrieveGeneral`, old `TestRetrieveMultiUser`

Keep: `TestSafeDate` — wait, `safeDate` was removed too. Remove it. Keep:
- `TestEffectiveName`
- `TestSerializeFloat32`
- `TestUpsertUser`, `TestUpsertUserPreferredNamePriority`
- `TestSetAndGetPreferredName`
- `TestNewConstantsCompile`
- `TestInit_LoadsChannelBuffers`
- Plus all new tests added in Tasks 3–10.

**Step 6: Verify build and run tests**

```bash
/usr/local/go/bin/go build ./...
/usr/local/go/bin/go test ./... -timeout 60s
```

Expected: build succeeds, tests PASS.

**Step 7: Commit**

```bash
git add -A
git commit -m "refactor(memory): remove v1 fact extraction, consolidation, and related tests"
```

---

## Final Verification

After all 13 tasks, run:

```bash
/usr/local/go/bin/go vet ./...
/usr/local/go/bin/go test ./... -timeout 60s
/usr/local/go/bin/go build ./...
```

All should succeed. The bot should start, log `Interaction notes: 0` at first run, and begin accumulating notes from channel activity.
