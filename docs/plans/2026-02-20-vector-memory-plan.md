# Vector Memory System Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a local vector memory layer (sqlite-vec) that extracts facts from all Discord messages, consolidates them via 3-state invalidation, and retrieves relevant facts at chat time via RAG.

**Architecture:** Single `internal/memory/` package with 4 files (memory.go, extract.go, consolidate.go, retrieve.go). Integrates into the existing `voltgpt.db` SQLite database by loading `sqlite-vec` extension at startup. Uses a separate `MEMORY_GEMINI_TOKEN` for all memory-related Gemini API calls (extraction, consolidation, embedding).

**Tech Stack:** Go 1.26, sqlite-vec (CGO), google.golang.org/genai (Gemini 3 Flash + gemini-embedding-001)

---

### Task 1: Install sqlite-vec dependency

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

**Step 1: Install the sqlite-vec Go bindings**

```bash
cd /home/bot/dev/bots/voltgpt/.claude/worktrees/interesting-shirley
go get github.com/asg017/sqlite-vec-go-bindings/cgo
```

Expected: `go.mod` gains a new `require` entry for `github.com/asg017/sqlite-vec-go-bindings`.

**Step 2: Verify the dependency resolves**

```bash
go mod tidy
```

Expected: Clean exit, no errors. `go.sum` updated.

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "deps: add sqlite-vec-go-bindings for vector memory"
```

---

### Task 2: Add sqlite-vec and memory tables to db.go

**Files:**
- Modify: `internal/db/db.go` (lines 4-9 for imports, line 15 for Auto(), lines 34-47 for tables)

**Step 1: Write a test for the new tables**

Create `internal/db/db_test.go`:

```go
package db

import (
	"testing"
)

func TestCreateMemoryTables(t *testing.T) {
	// Open an in-memory DB using the same Open logic
	Open(":memory:")
	defer Close()

	// Verify users table exists
	_, err := DB.Exec("INSERT INTO users (discord_id, username) VALUES ('123', 'testuser')")
	if err != nil {
		t.Fatalf("users table not created: %v", err)
	}

	// Verify facts table exists
	_, err = DB.Exec("INSERT INTO facts (user_id, original_message_id, fact_text) VALUES (1, 'msg1', 'likes cats')")
	if err != nil {
		t.Fatalf("facts table not created: %v", err)
	}

	// Verify vec_facts virtual table exists by inserting a zero vector (768 floats)
	// sqlite-vec expects a JSON array for inserts
	zeroVec := make([]byte, 768*4) // 768 float32s = 3072 bytes
	_, err = DB.Exec("INSERT INTO vec_facts (fact_id, embedding) VALUES (1, ?)", zeroVec)
	if err != nil {
		t.Fatalf("vec_facts table not created: %v", err)
	}
}
```

**Step 2: Run the test — expect FAIL**

```bash
go test ./internal/db/ -run TestCreateMemoryTables -v
```

Expected: FAIL — tables `users`, `facts`, `vec_facts` do not exist yet.

**Step 3: Modify `internal/db/db.go`**

Add the `sqlite_vec` import alongside the existing `go-sqlite3` import (line 8). Call `sqlite_vec.Auto()` at the top of `Open()` (before `sql.Open`). Add three new table DDL strings to the `tables` slice in `createTables()`.

The full modified file should be:

```go
// Package db manages the SQLite database for persistent storage.
package db

import (
	"database/sql"
	"log"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func Open(path string) {
	sqlite_vec.Auto()

	var err error
	DB, err = sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	if err := DB.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	createTables()
}

func Close() {
	if DB != nil {
		DB.Close()
	}
}

func createTables() {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS image_hashes (
			hash TEXT PRIMARY KEY,
			message_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS game_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS transcriptions (
			content_url TEXT PRIMARY KEY,
			response_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			discord_id TEXT UNIQUE NOT NULL,
			username TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS facts (
			id INTEGER PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id),
			original_message_id TEXT NOT NULL,
			fact_text TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_active INTEGER DEFAULT 1
		)`,
	}

	for _, table := range tables {
		if _, err := DB.Exec(table); err != nil {
			log.Fatalf("Failed to create table: %v", err)
		}
	}

	// vec_facts uses a virtual table — CREATE VIRTUAL TABLE does not support IF NOT EXISTS
	// in all sqlite-vec versions, so check if it exists first.
	var name string
	err := DB.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='vec_facts'").Scan(&name)
	if err != nil {
		_, err = DB.Exec(`CREATE VIRTUAL TABLE vec_facts USING vec0(fact_id INTEGER PRIMARY KEY, embedding float[768])`)
		if err != nil {
			log.Fatalf("Failed to create vec_facts table: %v", err)
		}
	}
}
```

**Step 4: Run the test — expect PASS**

```bash
go test ./internal/db/ -run TestCreateMemoryTables -v
```

Expected: PASS

**Step 5: Run `go vet` to verify no issues**

```bash
go vet ./...
```

Expected: Clean.

**Step 6: Commit**

```bash
git add internal/db/db.go internal/db/db_test.go
git commit -m "feat: add sqlite-vec extension and memory tables (users, facts, vec_facts)"
```

---

### Task 3: Create memory.go — Init, client, types

**Files:**
- Create: `internal/memory/memory.go`

**Step 1: Create the directory**

```bash
mkdir -p internal/memory
```

**Step 2: Write `internal/memory/memory.go`**

This file provides `Init()`, the persistent Gemini client, shared types, and helper functions for embedding vectors.

```go
// Package memory provides a vector-backed long-term memory system.
// It extracts facts from Discord messages, consolidates them via
// semantic similarity, and retrieves relevant facts for RAG.
package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"log"
	"math"
	"os"

	"google.golang.org/genai"
)

const (
	embeddingModel    = "gemini-embedding-001"
	generationModel   = "gemini-2.0-flash"
	embeddingDim      = 768
	similarityLimit   = 3
	retrievalLimit    = 5
	minMessageLength  = 10
	distanceThreshold = float64(0.35)
)

var (
	database *sql.DB
	client   *genai.Client
	enabled  bool
)

func Init(db *sql.DB) {
	database = db

	apiKey := os.Getenv("MEMORY_GEMINI_TOKEN")
	if apiKey == "" {
		log.Println("MEMORY_GEMINI_TOKEN is not set — memory system disabled")
		return
	}

	ctx := context.Background()
	var err error
	client, err = genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Printf("Failed to create memory Gemini client: %v", err)
		return
	}

	enabled = true
	log.Println("Memory system initialized")
}

// embed calls the Gemini embedding API and returns a float32 vector.
func embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := client.Models.EmbedContent(ctx, embeddingModel, genai.Text(text), nil)
	if err != nil {
		return nil, err
	}
	return resp.Embeddings[0].Values, nil
}

// serializeFloat32 converts a float32 slice to a little-endian byte slice
// for sqlite-vec queries.
func serializeFloat32(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// upsertUser inserts a user if they don't exist and returns their ID.
func upsertUser(discordID, username string) (int64, error) {
	_, err := database.Exec(
		"INSERT OR IGNORE INTO users (discord_id, username) VALUES (?, ?)",
		discordID, username,
	)
	if err != nil {
		return 0, err
	}

	// Update username in case it changed
	_, err = database.Exec(
		"UPDATE users SET username = ? WHERE discord_id = ?",
		username, discordID,
	)
	if err != nil {
		return 0, err
	}

	var id int64
	err = database.QueryRow("SELECT id FROM users WHERE discord_id = ?", discordID).Scan(&id)
	return id, err
}

// insertFact inserts a new fact and its embedding in a transaction.
func insertFact(userID int64, messageID, factText string, embedding []float32) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO facts (user_id, original_message_id, fact_text) VALUES (?, ?, ?)",
		userID, messageID, factText,
	)
	if err != nil {
		return err
	}

	factID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		"INSERT INTO vec_facts (fact_id, embedding) VALUES (?, ?)",
		factID, serializeFloat32(embedding),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// deactivateFact soft-deletes a fact by setting is_active = 0.
func deactivateFact(factID int64) error {
	_, err := database.Exec("UPDATE facts SET is_active = 0 WHERE id = ?", factID)
	return err
}

// TotalFacts returns the count of active facts for logging at startup.
func TotalFacts() int {
	if database == nil {
		return 0
	}
	var count int
	database.QueryRow("SELECT COUNT(*) FROM facts WHERE is_active = 1").Scan(&count)
	return count
}
```

**Step 3: Verify it compiles**

```bash
go vet ./internal/memory/
```

Expected: Clean (no errors).

**Step 4: Commit**

```bash
git add internal/memory/memory.go
git commit -m "feat: add memory package with init, embedding, and DB helpers"
```

---

### Task 4: Create extract.go — Pipeline 1

**Files:**
- Create: `internal/memory/extract.go`

**Step 1: Write `internal/memory/extract.go`**

```go
package memory

import (
	"context"
	"encoding/json"
	"log"

	"google.golang.org/genai"
)

// Extract asynchronously extracts long-term facts from a message.
// Call this as a goroutine: go memory.Extract(...)
func Extract(discordID, username, messageID, text string) {
	if !enabled || len(text) < minMessageLength {
		return
	}

	ctx := context.Background()

	userID, err := upsertUser(discordID, username)
	if err != nil {
		log.Printf("memory: failed to upsert user %s: %v", discordID, err)
		return
	}

	facts, err := extractFacts(ctx, username, text)
	if err != nil {
		log.Printf("memory: extraction failed for message %s: %v", messageID, err)
		return
	}

	for _, fact := range facts {
		if err := consolidateAndStore(ctx, userID, messageID, fact); err != nil {
			log.Printf("memory: consolidation failed for fact %q: %v", fact, err)
		}
	}
}

// extractFacts calls Gemini to extract long-term facts from a message.
func extractFacts(ctx context.Context, username, text string) ([]string, error) {
	prompt := "Extract long-term, third-person facts about the user from this message. " +
		"Ignore temporary states like current mood or what they're doing right now. " +
		"If no long-term facts can be extracted, return an empty array.\n" +
		"The user's name is " + username + ".\n" +
		"Message: " + text

	t := float32(0.1)
	resp, err := client.Models.GenerateContent(ctx, generationModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			Temperature:      &t,
			ResponseMIMEType: "application/json",
			ResponseSchema: &genai.Schema{
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeString,
				},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, nil
	}

	// Parse the JSON array from the response text
	var responseText string
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			responseText += part.Text
		}
	}

	var facts []string
	if err := json.Unmarshal([]byte(responseText), &facts); err != nil {
		return nil, err
	}

	return facts, nil
}
```

**Step 2: Verify it compiles**

```bash
go vet ./internal/memory/
```

Expected: Clean. (The `consolidateAndStore` function doesn't exist yet — this will fail until Task 5. If it fails, move on to Task 5 and come back.)

**Step 3: Commit (defer until consolidate.go exists if needed)**

```bash
git add internal/memory/extract.go
git commit -m "feat: add fact extraction pipeline (Pipeline 1)"
```

---

### Task 5: Create consolidate.go — Pipeline 2

**Files:**
- Create: `internal/memory/consolidate.go`

**Step 1: Write `internal/memory/consolidate.go`**

```go
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"google.golang.org/genai"
)

type consolidationAction struct {
	Action     string `json:"action"`
	MergedText string `json:"merged_text"`
}

type similarFact struct {
	ID       int64
	FactText string
	Distance float64
}

const consolidationSystemPrompt = `You are a memory consolidation AI. Your job is to compare a NEW fact with an OLD fact and decide how to update the database.

Rules:
1. INVALIDATE: Use this if the new fact completely replaces or contradicts the old fact (e.g., 'Lives in NY' vs 'Moved to LA').
2. MERGE: Use this if the facts are about the exact same topic/entity and can be combined into a single, richer sentence (e.g., 'Owns an Xbox' + 'Bought a PS5' -> 'Owns both an Xbox and a PS5').
3. KEEP: Use this if the facts are completely unrelated and should both exist independently.

If you choose MERGE, you must provide the newly combined fact. If you choose KEEP or INVALIDATE, leave the merged text blank.`

// consolidateAndStore embeds a new fact, checks for similar existing facts,
// and either inserts, merges, or invalidates as appropriate.
func consolidateAndStore(ctx context.Context, userID int64, messageID, factText string) error {
	embedding, err := embed(ctx, factText)
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	similar, err := findSimilarFacts(userID, embedding)
	if err != nil {
		return fmt.Errorf("similarity search failed: %w", err)
	}

	// No similar facts — insert directly
	if len(similar) == 0 {
		return insertFact(userID, messageID, factText, embedding)
	}

	// Check each similar fact for consolidation
	for _, sf := range similar {
		action, err := decideAction(ctx, sf.FactText, factText)
		if err != nil {
			log.Printf("memory: consolidation decision failed for fact %d: %v", sf.ID, err)
			continue
		}

		switch action.Action {
		case "INVALIDATE":
			if err := deactivateFact(sf.ID); err != nil {
				return fmt.Errorf("deactivation failed: %w", err)
			}
			return insertFact(userID, messageID, factText, embedding)

		case "MERGE":
			if action.MergedText == "" {
				log.Printf("memory: MERGE action returned empty merged_text, falling back to KEEP")
				continue
			}
			if err := deactivateFact(sf.ID); err != nil {
				return fmt.Errorf("deactivation failed: %w", err)
			}
			mergedEmbedding, err := embed(ctx, action.MergedText)
			if err != nil {
				return fmt.Errorf("merge embedding failed: %w", err)
			}
			return insertFact(userID, messageID, action.MergedText, mergedEmbedding)

		case "KEEP":
			continue
		}
	}

	// All similar facts returned KEEP — insert as new
	return insertFact(userID, messageID, factText, embedding)
}

// findSimilarFacts queries vec_facts for active facts belonging to the same user
// that are within the distance threshold.
func findSimilarFacts(userID int64, embedding []float32) ([]similarFact, error) {
	rows, err := database.Query(`
		SELECT f.id, f.fact_text, vf.distance
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		WHERE vf.embedding MATCH ?
		  AND f.user_id = ?
		  AND f.is_active = 1
		ORDER BY vf.distance
		LIMIT ?
	`, serializeFloat32(embedding), userID, similarityLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []similarFact
	for rows.Next() {
		var sf similarFact
		if err := rows.Scan(&sf.ID, &sf.FactText, &sf.Distance); err != nil {
			return nil, err
		}
		if sf.Distance < distanceThreshold {
			results = append(results, sf)
		}
	}
	return results, rows.Err()
}

// decideAction calls Gemini to decide whether to KEEP, INVALIDATE, or MERGE two facts.
func decideAction(ctx context.Context, oldFact, newFact string) (*consolidationAction, error) {
	prompt := fmt.Sprintf("OLD: %q\nNEW: %q", oldFact, newFact)

	t := float32(0.1)
	resp, err := client.Models.GenerateContent(ctx, generationModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(consolidationSystemPrompt, genai.RoleModel),
			Temperature:       &t,
			ResponseMIMEType:  "application/json",
			ResponseSchema: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type: genai.TypeString,
						Enum: []string{"KEEP", "INVALIDATE", "MERGE"},
					},
					"merged_text": {
						Type: genai.TypeString,
					},
				},
				Required: []string{"action", "merged_text"},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return &consolidationAction{Action: "KEEP"}, nil
	}

	var responseText string
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			responseText += part.Text
		}
	}

	var action consolidationAction
	if err := json.Unmarshal([]byte(responseText), &action); err != nil {
		return nil, err
	}

	return &action, nil
}
```

**Step 2: Verify it compiles with extract.go**

```bash
go vet ./internal/memory/
```

Expected: Clean.

**Step 3: Commit**

```bash
git add internal/memory/extract.go internal/memory/consolidate.go
git commit -m "feat: add extraction and consolidation pipelines (Pipeline 1 & 2)"
```

---

### Task 6: Create retrieve.go — Pipeline 3

**Files:**
- Create: `internal/memory/retrieve.go`

**Step 1: Write `internal/memory/retrieve.go`**

```go
package memory

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
)

// UserFacts holds retrieved facts for a single user.
type UserFacts struct {
	Username string
	Facts    []string
}

// Retrieve fetches the top relevant active facts for a user.
func Retrieve(query string, discordID string) []string {
	if !enabled {
		return nil
	}

	ctx := context.Background()

	embedding, err := embed(ctx, query)
	if err != nil {
		log.Printf("memory: retrieval embedding failed: %v", err)
		return nil
	}

	rows, err := database.Query(`
		SELECT f.fact_text
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		JOIN users u ON u.id = f.user_id
		WHERE vf.embedding MATCH ?
		  AND u.discord_id = ?
		  AND f.is_active = 1
		ORDER BY vf.distance
		LIMIT ?
	`, serializeFloat32(embedding), discordID, retrievalLimit)
	if err != nil {
		log.Printf("memory: retrieval query failed: %v", err)
		return nil
	}
	defer rows.Close()

	var facts []string
	for rows.Next() {
		var fact string
		if err := rows.Scan(&fact); err != nil {
			log.Printf("memory: retrieval scan failed: %v", err)
			continue
		}
		facts = append(facts, fact)
	}

	return facts
}

// RetrieveMultiUser fetches facts for multiple users concurrently and formats
// them into an XML block for injection into the system prompt.
func RetrieveMultiUser(query string, users map[string]string) string {
	if !enabled || len(users) == 0 {
		return ""
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var allFacts []UserFacts

	for discordID, username := range users {
		wg.Add(1)
		go func(did, uname string) {
			defer wg.Done()
			facts := Retrieve(query, did)
			if len(facts) > 0 {
				mu.Lock()
				allFacts = append(allFacts, UserFacts{Username: uname, Facts: facts})
				mu.Unlock()
			}
		}(discordID, username)
	}

	wg.Wait()

	if len(allFacts) == 0 {
		return ""
	}

	return formatFactsXML(allFacts)
}

// formatFactsXML formats user facts into the XML block for system prompt injection.
func formatFactsXML(allFacts []UserFacts) string {
	var sb strings.Builder
	sb.WriteString("<background_facts>\n")
	for _, uf := range allFacts {
		sb.WriteString(fmt.Sprintf("<user name=%q>\n", uf.Username))
		for _, fact := range uf.Facts {
			sb.WriteString(fmt.Sprintf("- %s\n", fact))
		}
		sb.WriteString("</user>\n")
	}
	sb.WriteString("</background_facts>")
	return sb.String()
}
```

**Step 2: Verify it compiles**

```bash
go vet ./internal/memory/
```

Expected: Clean.

**Step 3: Commit**

```bash
git add internal/memory/retrieve.go
git commit -m "feat: add retrieval pipeline with multi-user concurrent fetch (Pipeline 3)"
```

---

### Task 7: Integrate into gemini/chat.go — accept facts in StreamMessageResponse

**Files:**
- Modify: `internal/apis/gemini/chat.go` (line 127 signature, line 146 system prompt)

**Step 1: Add `backgroundFacts string` parameter to `StreamMessageResponse`**

Change the function signature at line 127 from:

```go
func StreamMessageResponse(s *discordgo.Session, c *genai.Client, m *discordgo.Message, history []*genai.Content) error {
```

to:

```go
func StreamMessageResponse(s *discordgo.Session, c *genai.Client, m *discordgo.Message, history []*genai.Content, backgroundFacts string) error {
```

**Step 2: Inject facts into system prompt**

Change line 146 from:

```go
systemMessageText := fmt.Sprintf("System message: %s\n\nInstruction message: %s", config.SystemMessageMinimal, instructionMessage)
```

to:

```go
systemMessageText := fmt.Sprintf("System message: %s\n\nInstruction message: %s", config.SystemMessageMinimal, instructionMessage)
if backgroundFacts != "" {
    systemMessageText += "\n\n" + backgroundFacts
}
```

**Step 3: Verify it compiles**

```bash
go vet ./internal/apis/gemini/
```

Expected: FAIL — the call site in `handler/messages.go:102` doesn't pass the new argument yet. That's expected, we fix it in Task 8.

**Step 4: Commit**

```bash
git add internal/apis/gemini/chat.go
git commit -m "feat: accept background facts parameter in StreamMessageResponse"
```

---

### Task 8: Integrate into handler/messages.go — extraction + retrieval

**Files:**
- Modify: `internal/handler/messages.go` (imports, extraction call, retrieval call, StreamMessageResponse call)

**Step 1: Modify `internal/handler/messages.go`**

The modified file adds:
1. Import for `"voltgpt/internal/memory"`.
2. A `go memory.Extract(...)` call for every non-bot message (right after the bot-check on line 37-39, before the mention check).
3. Before calling `StreamMessageResponse`, collect user IDs from the chat history and call `memory.RetrieveMultiUser`.
4. Pass the facts XML to `StreamMessageResponse`.

Add to imports (after line 14):
```go
"voltgpt/internal/memory"
```

After line 39 (`return` for bot messages), before the `apiKey` check, insert:
```go
// Background fact extraction for all non-bot messages
go memory.Extract(m.Author.ID, m.Author.Username, m.ID, m.Content)
```

Before the `StreamMessageResponse` call (before line 102), add retrieval logic:
```go
// Retrieve memory facts for all users in the conversation
users := make(map[string]string)
users[m.Author.ID] = m.Author.Username
for _, msg := range chatMessages {
    for _, part := range msg.Parts {
        if part.Text != "" {
            // Extract username from <username>NAME</username> tags
            if start := strings.Index(part.Text, "<username>"); start != -1 {
                end := strings.Index(part.Text, "</username>")
                if end > start {
                    name := part.Text[start+len("<username>") : end]
                    // We need to find the discord ID for this user
                    // For now, just use the users we know about
                    _ = name
                }
            }
        }
    }
}
// For reply chain users, we can extract IDs from the cache
if cache != nil {
    for _, cached := range cache {
        if cached.Author != nil && !cached.Author.Bot && cached.Author.ID != s.State.User.ID {
            users[cached.Author.ID] = cached.Author.Username
        }
    }
}
backgroundFacts := memory.RetrieveMultiUser(m.Content, users)
```

Update line 102 from:
```go
err = gemini.StreamMessageResponse(s, c, m.Message, chatMessages)
```
to:
```go
err = gemini.StreamMessageResponse(s, c, m.Message, chatMessages, backgroundFacts)
```

**Step 2: Verify it compiles**

```bash
go vet ./...
```

Expected: Clean.

**Step 3: Commit**

```bash
git add internal/handler/messages.go
git commit -m "feat: integrate memory extraction and retrieval into message handler"
```

---

### Task 9: Update main.go — init memory, add MessageContent intent

**Files:**
- Modify: `main.go` (imports, init, intents, ready handler)

**Step 1: Add memory import**

Add to imports (after line 18):
```go
"voltgpt/internal/memory"
```

**Step 2: Add memory.Init to init()**

After line 31 (`transcription.Init(db.DB)`), add:
```go
memory.Init(db.DB)
```

**Step 3: Add MessageContent intent**

Change line 46 from:
```go
dg.Identify.Intents = discordgo.IntentGuildMessages
```
to:
```go
dg.Identify.Intents = discordgo.IntentGuildMessages | discordgo.IntentMessageContent
```

**Step 4: Add fact count to Ready handler**

After line 77 (`log.Printf("Transcripts in cache: %d", ...)`), add:
```go
log.Printf("Active facts: %d", memory.TotalFacts())
```

**Step 5: Verify full build**

```bash
go build -o voltgpt
```

Expected: Binary compiles successfully.

**Step 6: Run go vet**

```bash
go vet ./...
```

Expected: Clean.

**Step 7: Commit**

```bash
git add main.go
git commit -m "feat: initialize memory system at startup with MessageContent intent"
```

---

### Task 10: Final verification

**Step 1: Full build**

```bash
go build -o voltgpt
```

Expected: Success.

**Step 2: Run all tests**

```bash
go test ./...
```

Expected: All tests pass (db_test.go at minimum).

**Step 3: Run vet**

```bash
go vet ./...
```

Expected: Clean.

**Step 4: Review file structure**

```bash
ls -la internal/memory/
```

Expected:
```
memory.go
extract.go
consolidate.go
retrieve.go
```

**Step 5: Final commit (if any unstaged changes remain)**

```bash
git status
```

If clean, no action needed. Otherwise stage and commit remaining files.
