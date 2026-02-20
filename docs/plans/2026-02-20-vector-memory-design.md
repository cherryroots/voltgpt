# Vector Memory System Design

## Summary

Add a local vector memory layer to the Discord bot using sqlite-vec. The system extracts long-term facts about users from all messages, consolidates them via a 3-state invalidation model, and retrieves relevant facts at chat time via RAG.

## Decisions

| Decision | Choice | Rationale |
|---|---|---|
| Database | Same `voltgpt.db` | Follows existing single-DB pattern, avoids managing two connections |
| Package structure | Single `internal/memory/` with multiple files | Balances separation of concerns with import simplicity |
| Extraction trigger | All non-bot messages | Builds the richest memory; requires `MessageContent` intent |
| API key | Separate `MEMORY_GEMINI_TOKEN` | Isolates memory traffic from chat traffic quota |
| Retrieval scope | All users in reply chain, fetched concurrently | Bot has context about every participant in the conversation |

## Database Schema

Added to existing `voltgpt.db` via `db.createTables()`. Requires `sqlite_vec.Auto()` before `sql.Open()`.

```sql
CREATE TABLE IF NOT EXISTS users (
    id INTEGER PRIMARY KEY,
    discord_id TEXT UNIQUE NOT NULL,
    username TEXT NOT NULL
)

CREATE TABLE IF NOT EXISTS facts (
    id INTEGER PRIMARY KEY,
    user_id INTEGER NOT NULL REFERENCES users(id),
    original_message_id TEXT NOT NULL,
    fact_text TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    is_active INTEGER DEFAULT 1
)

CREATE VIRTUAL TABLE IF NOT EXISTS vec_facts USING vec0(
    fact_id INTEGER PRIMARY KEY,
    embedding float[768]
)
```

## Package Layout

```
internal/memory/
  memory.go       # Init, Gemini client, shared types
  extract.go      # Pipeline 1: background fact extraction
  consolidate.go  # Pipeline 2: 3-state fact invalidation
  retrieve.go     # Pipeline 3: RAG retrieval
```

## Pipeline 1: Background Extraction (`extract.go`)

**Entry point:** `Extract(discordID, username, messageID, text string)` called as a goroutine from `handler/messages.go` for every non-bot message.

**Flow:**
1. Skip messages shorter than 10 characters.
2. Upsert user: `INSERT OR IGNORE INTO users (discord_id, username)`, fetch `id`.
3. Call Gemini 3 Flash with structured JSON output (`ResponseSchema: []string`).
4. For each extracted fact, call `consolidateAndStore()`.

**Extraction prompt:**
```
Extract long-term, third-person facts about the user from this message.
Ignore temporary states like current mood or what they're doing right now.
If no long-term facts can be extracted, return an empty array.
The user's name is {username}.
Message: {text}
```

**Graceful degradation:** If `MEMORY_GEMINI_TOKEN` is empty, `Init` logs a warning and `Extract` is a no-op.

## Pipeline 2: Consolidation (`consolidate.go`)

**Entry point:** `consolidateAndStore(userID int64, messageID, factText string)` called from extraction for each new fact.

**Flow:**
1. Embed the new fact via `gemini-embedding-001` (768-dim vector).
2. Search `vec_facts` joined with `facts` for similar active facts (same user, distance < 0.35 threshold, limit 3).
3. For each similar fact, call Gemini 3 Flash with structured JSON output to decide action.
4. Apply action: KEEP (insert new), INVALIDATE (soft-delete old + insert new), MERGE (soft-delete old + embed merged text + insert merged).
5. If no similar facts found, insert directly.
6. Fact + embedding insertions wrapped in a transaction.

**Consolidation system prompt:**
```
You are a memory consolidation AI. Your job is to compare a NEW fact with an OLD fact and decide how to update the database.

Rules:
1. INVALIDATE: Use this if the new fact completely replaces or contradicts the old fact (e.g., 'Lives in NY' vs 'Moved to LA').
2. MERGE: Use this if the facts are about the exact same topic/entity and can be combined into a single, richer sentence (e.g., 'Owns an Xbox' + 'Bought a PS5' -> 'Owns both an Xbox and a PS5').
3. KEEP: Use this if the facts are completely unrelated and should both exist independently.

If you choose MERGE, you must provide the newly combined fact. If you choose KEEP or INVALIDATE, leave the merged text blank.
```

**Response schema:** `{ "action": enum("KEEP", "INVALIDATE", "MERGE"), "merged_text": string }`

## Pipeline 3: Retrieval (`retrieve.go`)

**Entry point:** `Retrieve(query string, discordID string) []string` called synchronously before generating a chat response.

**Flow:**
1. Embed the query via `gemini-embedding-001`.
2. Search `vec_facts` for top 5 active facts for the given user.
3. Return fact texts as `[]string`.

**Multi-user retrieval (in handler):**
1. Collect distinct Discord user IDs from the chat history (reply chain).
2. Fan out `Retrieve()` calls concurrently via goroutines + `sync.WaitGroup`.
3. Merge results, format into attributed XML block:

```xml
<background_facts>
<user name="alice">
- Prefers dark mode interfaces
- Lives in Toronto
</user>
<user name="bob">
- Works as a software engineer
</user>
</background_facts>
```

4. Inject XML into the Gemini system prompt before generating the chat response.

## Integration Points

### `internal/db/db.go`
- Add `sqlite_vec.Auto()` import and call before `sql.Open()`
- Add `users`, `facts`, `vec_facts` table creation to `createTables()`

### `main.go`
- Add `memory.Init(db.DB)` to init chain
- Add `discordgo.IntentMessageContent` to intents

### `handler/messages.go`
- Call `go memory.Extract(...)` for every non-bot message (before the mention check)
- Before calling `gemini.StreamMessageResponse`, retrieve facts for all users in the reply chain
- Pass facts to the system prompt builder

### `internal/apis/gemini/chat.go`
- Accept optional facts parameter in `StreamMessageResponse` (or modify system prompt construction)
- Append `<background_facts>` XML to system instruction when facts are present

## Dependencies to Add

```
github.com/asg017/sqlite-vec-go-bindings/cgo
```

## Environment Variables

New optional variable:
- `MEMORY_GEMINI_TOKEN` — Separate Gemini API key for memory extraction/consolidation/embedding
