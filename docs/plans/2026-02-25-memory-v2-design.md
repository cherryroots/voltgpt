# Memory System v2 Design

## Overview

Full replacement of the current flat-facts memory system (`facts`, `vec_facts`) with a
two-layer structured memory: **user profiles** (stable identity — who people are) and
**interaction notes** (episodic memory — what happened in the server).

The existing `facts` and `vec_facts` tables are soft-deprecated: left in place for
migration safety but ignored by all new code.

---

## Schema

### `user_profiles`

One row per user. Each section is a JSON array of `{"text": "...", "note_id": N}` objects,
allowing per-fact source tracing at retrieval time.

```sql
CREATE TABLE user_profiles (
    user_id       INTEGER PRIMARY KEY REFERENCES users(id),
    bio           TEXT NOT NULL DEFAULT '[]',
    interests     TEXT NOT NULL DEFAULT '[]',
    skills        TEXT NOT NULL DEFAULT '[]',
    opinions      TEXT NOT NULL DEFAULT '[]',
    relationships TEXT NOT NULL DEFAULT '[]',
    other         TEXT NOT NULL DEFAULT '[]',
    updated_at    DATETIME DEFAULT CURRENT_TIMESTAMP
)
```

### `interaction_notes`

Stores both granular conversation notes (written throughout the day) and daily topic
cluster notes (written by the midnight job). Both types are embedded and searchable.

```sql
CREATE TABLE interaction_notes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id        TEXT NOT NULL,
    channel_id      TEXT NOT NULL,
    note_type       TEXT NOT NULL,   -- 'conversation' | 'topic_cluster'
    participants    TEXT NOT NULL,   -- JSON array of discord IDs
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL,
    source_note_ids TEXT,            -- JSON array; populated for topic_cluster only
    note_date       DATE NOT NULL,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
)
```

### `vec_notes`

sqlite-vec virtual table. Mirrors the existing `vec_facts` pattern.

```sql
CREATE VIRTUAL TABLE vec_notes USING vec0(
    note_id   INTEGER PRIMARY KEY,
    embedding float[768] distance_metric=cosine
)
```

### `channel_buffers`

Persists active channel/thread buffers to SQLite so in-progress conversations survive
bot restarts.

```sql
CREATE TABLE channel_buffers (
    channel_id   TEXT PRIMARY KEY,
    guild_id     TEXT NOT NULL,
    messages     TEXT NOT NULL,   -- JSON array of {discord_id, username, display_name, text, message_id}
    started_at   DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME DEFAULT CURRENT_TIMESTAMP
)
```

### `memory_job_log`

Tracks when the midnight job last ran, enabling startup catch-up.

```sql
CREATE TABLE memory_job_log (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    last_run   DATETIME NOT NULL
)
```

---

## Data Flow

### Throughout the day — channel/thread buffers

```
Message arrives (human only — bot messages excluded)
  └─ channel_buffers row upserted (keyed by channel_id)
  └─ inactivity timer reset (20–30 min)
  └─ if buffer age >= 2 hours → force flush (max window)

Channel/thread goes quiet (inactivity timer fires)
  └─ Gemini flash: generate title + summary from buffered messages
  └─ Conversation note written to interaction_notes
  └─ Note embedded and stored in vec_notes
  └─ channel_buffers row deleted
  └─ For each human participant in the note:
       Gemini flash: compare note to user's existing profile
                     → update relevant sections with new facts
                     → each new fact carries the note's ID as source
```

Threads have their own `channel_id` in Discord — handled identically to channels,
no special casing. Archived threads simply go quiet and trigger the inactivity flush
naturally.

### Midnight job — two sequential passes

**Pass 1 — Topic clustering (per guild):**

```
Fetch all conversation notes from today for this guild
Gemini pro: cluster into 3–8 topic cluster notes
  Each cluster: title, summary, list of source_note_ids, participants union
Each cluster stored in interaction_notes + embedded in vec_notes
```

**Pass 2 — Profile consolidation (per active user):**

```
For each user who appears as a participant in today's notes:
  Single Gemini pro call:
    Input:  current profile (all sections as JSON facts) +
            today's interaction notes the user participated in
    Output: updated, consolidated profile sections
            (new facts added, redundancies removed, contradictions resolved,
             each fact carrying the source note_id)
  Profile written back to user_profiles
```

**Startup catch-up:**

On `memory.Init()`, query `memory_job_log`. If `last_run` is more than 24 hours ago
(or the row doesn't exist), run the midnight job immediately before completing init.

---

## Retrieval

```
Embed current message query
Search vec_notes → top N semantically relevant notes
                   (matches both conversation and topic_cluster types)

Profile injection:
  Always:     full profiles for all users directly in the conversation
  @mentions:  full profiles for mentioned users (up to 3 additional)
  From notes: full profiles for participants in retrieved notes (up to 3, deduped)
```

### System prompt format

Profile sections are formatted with inline source references so Vivy can answer
"where did you learn that?" without any additional lookup:

```xml
<background_facts>
<user name="Alex">
Bio:
- Lives in Austin, works at Dell  [Gaming session, Feb 23]

Interests:
- RPG games  [Gaming discussion, Feb 23]
- Anime      [Weekly recs, Feb 20]

Skills:
- Python, JavaScript  [Dev chat, Feb 19]

Opinions:
- Prefers Linux over Windows  [OS debate, Feb 21]

Relationships:
- Dog named Bento  [Pet photos, Feb 18]
</user>
<notes>
- [Feb 23] Gaming discussion — Alex, Sam, and Jake debated JRPG recommendations...
- [Feb 21] OS debate — A heated thread about Linux vs Windows...
</notes>
</background_facts>
```

---

## Model Split

| Task | Model | Frequency |
|---|---|---|
| Conversation note generation | `gemini-2.0-flash` (flash) | Per channel inactivity |
| Per-note profile update | flash | Per conversation note |
| Topic clustering | `gemini-2.5-pro` (pro) | Once daily per guild |
| Profile consolidation | pro | Once per active user daily |

Two model constants replace the current single `generationModel`:

```go
const (
    flashModel         = "gemini-2.0-flash"
    consolidationModel = "gemini-2.5-pro"
)
```

---

## Edge Cases

### Buffer loss on restart
`channel_buffers` is persisted to SQLite. On startup, any rows in `channel_buffers`
are loaded back into memory and their inactivity timers are restarted.

### Long conversations that never go quiet
Channel buffers enforce a 2-hour maximum window. If a buffer's `started_at` is more
than 2 hours ago when a new message arrives, the buffer is flushed immediately before
the new message is appended (creating a new buffer from scratch).

### Midnight job unavailability
`memory_job_log` tracks the last successful run. On startup, if the last run was
more than 24 hours ago, the job runs immediately. This handles nightly restarts,
crashes, and deployments without requiring a cron scheduler.

### Bot message filtering
The channel buffer only accepts messages where the author is not the bot's own user ID.
Vivy's responses are excluded from all conversation notes and profile updates.

---

## What's Removed

| Removed | Replaced by |
|---|---|
| `facts` table | `user_profiles` + `interaction_notes` |
| `vec_facts` virtual table | `vec_notes` |
| Per-user 30s message buffer (in-memory) | Per-channel buffer persisted to `channel_buffers` |
| `extractFacts()` | Per-note profile update pass |
| `consolidateAndStore()`, `findSimilarFacts()`, `decideAction()` | Daily profile consolidation pass |
| `GetUserFacts()`, `DeleteUserFacts()` | Profile equivalents |
| `ReinforceFact()`, `reinforceFact()` | Removed entirely — consolidation handles this |
| `RefreshFactNames()`, `RefreshAllFactNames()` | Profiles are Gemini-maintained text; no name prefix hacks |

## What Carries Over

- `users` table — unchanged
- `embed()`, `serializeFloat32()` — reused verbatim
- `upsertUser()` — reused verbatim
- `shouldSkipMemory` guard in message handler — applies to both note capture and retrieval
- `RetrieveMultiUser` function signature — implementation rewritten, signature compatible
- `config.MemoryBlacklist`, `config.MainServer` gating — unchanged
