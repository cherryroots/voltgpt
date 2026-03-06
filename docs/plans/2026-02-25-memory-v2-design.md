# Memory System v2 Design

## Overview

Memory v2 fully replaces the old flat-fact system (`facts`, `vec_facts`).
There is no migration or backfill from v1. Existing v1 memory is discarded.

The v2 model has three layers:

1. `interaction_notes`: canonical record of what happened
2. `topic clusters`: derived guild-level summaries built from conversation notes
3. `guild_user_profiles`: derived guild-scoped user summaries built from conversation notes

Conversation notes are the source of truth. Topic clusters and profiles are caches that can
be deleted and rebuilt from notes.

## Goals

- Guild-scoped recall only. No cross-guild memory injection.
- Better conversational grounding than flat facts, with real episodic context.
- Lower token cost than "rebuild every profile from all notes every time."
- Safe restart behavior for in-flight channel buffers.
- Safe deletion semantics when notes are the source of truth.
- A clean cold cutover from v1 with no legacy-memory compatibility burden.

## Non-Goals

- Preserving or migrating old v1 memory
- Cross-guild identity stitching
- Using topic clusters as the authoritative source for user profiles
- Retaining raw Discord messages as the long-term source of truth

## Core Model

### Conversation notes are canonical

The durable memory record is a stream of channel or thread summaries:

- one note per quiet period or max-age flush
- stored with guild, channel or thread, note date, participants, title, summary
- embedded into `vec_notes` for retrieval

Everything else is derived from these notes.

### Topic clusters are derived guild context

Topic clusters are lossy summaries of many conversation notes from the same guild and day.
They exist to improve broad retrieval, compression, and admin recap features.

They are useful for:

- "what has the server been talking about lately?"
- prompt compression
- admin digests

They are not used as the authoritative input for profile creation.

### Guild user profiles are cached summaries

Profiles are not canonical memory. They are a cached materialization for one user in one guild.

- Keyed by `(guild_id, user_id)`
- Updated incrementally from new conversation notes
- Rebuilt from notes only when needed
- Safe to delete and regenerate

This avoids cross-guild leakage and keeps token usage bounded.

## Why Profiles Are Incremental

Rebuilding a profile from all of a user's notes on every new note does not scale well in tokens.
The default update path is therefore incremental:

1. write the new conversation note
2. read the current cached profile for that guild and user
3. update the profile using only the current profile plus the new note

Full rebuilds are rare maintenance operations, not the normal write path.

Full rebuilds are triggered only when needed:

- note deletion or redaction
- model or schema changes
- profile drift, contradiction, or oversize
- failed incremental updates
- scheduled maintenance for very active users

Because notes remain canonical, profile repair is always possible.

## Schema

### `guild_user_profiles`

One row per `(guild_id, user_id)`.

Each section is stored as a JSON array of:

```json
{"text":"Lives in Austin.","source_note_ids":[12,18]}
```

`source_note_ids` is an array, not a single note ID, because profile facts may be merged across
multiple notes over time.

`is_dirty` marks profiles whose cached summary should be rebuilt from notes.

```sql
CREATE TABLE guild_user_profiles (
    guild_id             TEXT NOT NULL,
    user_id              INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    bio                  TEXT NOT NULL DEFAULT '[]',
    interests            TEXT NOT NULL DEFAULT '[]',
    skills               TEXT NOT NULL DEFAULT '[]',
    opinions             TEXT NOT NULL DEFAULT '[]',
    relationships        TEXT NOT NULL DEFAULT '[]',
    other                TEXT NOT NULL DEFAULT '[]',
    is_dirty             INTEGER NOT NULL DEFAULT 0,
    updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    last_full_rebuild_at DATETIME,
    PRIMARY KEY (guild_id, user_id)
)
```

### `interaction_notes`

Stores both conversation notes and topic-cluster notes.

`source_note_ids` is only populated for `topic_cluster` rows.

```sql
CREATE TABLE interaction_notes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    guild_id        TEXT NOT NULL,
    channel_id      TEXT,
    note_type       TEXT NOT NULL,   -- 'conversation' | 'topic_cluster'
    title           TEXT NOT NULL,
    summary         TEXT NOT NULL,
    source_note_ids TEXT NOT NULL DEFAULT '[]',
    note_date       DATE NOT NULL,
    created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)
```

### `note_participants`

Normalized participant membership for joins, retrieval expansion, deletion, and rebuilds.
This is the source of truth for note participants.

```sql
CREATE TABLE note_participants (
    note_id              INTEGER NOT NULL REFERENCES interaction_notes(id) ON DELETE CASCADE,
    participant_user_id  INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (note_id, participant_user_id)
)
```

### `vec_notes`

Embeddings for `interaction_notes`.

```sql
CREATE VIRTUAL TABLE vec_notes USING vec0(
    note_id   INTEGER PRIMARY KEY,
    embedding float[1536] distance_metric=cosine
)
```

### `channel_buffers`

Persists active channel or thread buffers so ongoing conversations survive restarts.

`messages` stores JSON objects:

```json
{
  "discord_id": "...",
  "username": "...",
  "display_name": "...",
  "text": "...",
  "message_id": "..."
}
```

```sql
CREATE TABLE channel_buffers (
    channel_id   TEXT PRIMARY KEY,
    guild_id     TEXT NOT NULL,
    messages     TEXT NOT NULL,
    started_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
)
```

### `memory_job_runs`

Tracks scheduled work per guild, day, and phase.

Phases:

- `cluster`
- `profile_maintenance`

```sql
CREATE TABLE memory_job_runs (
    guild_id     TEXT NOT NULL,
    job_date     DATE NOT NULL,
    phase        TEXT NOT NULL,
    status       TEXT NOT NULL,      -- 'running' | 'completed' | 'failed'
    started_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at  DATETIME,
    PRIMARY KEY (guild_id, job_date, phase)
)
```

### Recommended indexes

```sql
CREATE INDEX idx_notes_guild_type_date
    ON interaction_notes(guild_id, note_type, note_date, created_at);

CREATE INDEX idx_notes_guild_channel_created
    ON interaction_notes(guild_id, channel_id, created_at);

CREATE INDEX idx_note_participants_user_note
    ON note_participants(participant_user_id, note_id);

CREATE INDEX idx_profiles_guild_dirty
    ON guild_user_profiles(guild_id, is_dirty, updated_at);
```

## Data Flow

### Online capture path

```text
Message arrives
  -> ignore bot messages
  -> ignore blacklisted channels
  -> if skip-memory guard is present, skip both capture and retrieval
  -> upsert channel_buffers row keyed by channel_id
  -> reset inactivity timer
  -> if buffer age >= max age, flush immediately before appending new message
```

### Buffer flush path

```text
Channel or thread goes quiet, or max age is reached
  -> generate one conversation note (title + summary)
  -> upsert users for all human participants
  -> insert interaction_notes row
  -> insert note_participants rows
  -> embed the note into vec_notes
  -> delete channel_buffers row
  -> for each participant:
       attempt incremental guild profile update using current profile + this note only
       if update fails or is low-confidence, mark profile dirty
```

### Daily maintenance path

Per guild, once per day:

1. `cluster` phase:
   build topic clusters from that day's conversation notes
2. `profile_maintenance` phase:
   rebuild dirty profiles and any scheduled maintenance profiles

Both phases are idempotent and tracked in `memory_job_runs`.

### Startup catch-up

On `memory.Init()`:

1. reload persisted `channel_buffers`
2. recompute remaining inactivity from `updated_at`
3. flush overdue buffers immediately
4. restart timers for the rest
5. rerun missing or failed maintenance phases for missed days

## Topic Clusters

Topic clusters sit beside profiles, not underneath them.

- built from conversation notes for one guild and one day
- stored in `interaction_notes` with `note_type = 'topic_cluster'`
- embedded into `vec_notes`
- participants are the union of source-note participants

Topic clusters are used for:

- broad topical retrieval
- server recaps
- compression when many notes are relevant

Topic clusters are not used as the primary input for profile creation.
Profiles are built from conversation notes involving the user.

If a source conversation note is deleted or redacted:

- affected cluster notes are deleted or marked stale by deleting the completed job record
- the next cluster phase rebuilds them from remaining notes

## Retrieval

Retrieval is request-scoped, not "query plus users" only.

The retrieval request must include:

- `GuildID`
- `ChannelID`
- `Query`
- direct conversation users
- explicitly mentioned users

### Retrieval algorithm

```text
Embed query once
  -> search topic clusters in the current guild
  -> search conversation notes in the current guild
       optionally bias toward current channel or thread and recency
  -> use a strict threshold, then one bounded relaxed threshold
  -> if nothing qualifies, inject nothing
```

### Profile injection

- Always include ready profiles for direct conversation participants
- Include ready profiles for explicitly mentioned users, up to a small cap
- Include ready profiles for a few additional users surfaced by relevant notes or clusters
- If a profile is missing or dirty, fall back to raw notes for that user and queue a rebuild

No unrestricted cross-user or cross-guild fact sweep exists in v2.

### Prompt format

The wrapper can stay named `<background_facts>` for compatibility, but its contents are now
structured memory context:

```xml
<background_facts>
<user name="Alex">
Bio:
- Lives in Austin [Gaming chat, Feb 23]

Interests:
- JRPGs [Gaming chat, Feb 23]

Skills:
- Python and JavaScript [Dev chat, Feb 19; Tooling thread, Feb 21]
</user>
<topics>
- [Feb 23] Weekly game discussion - Several people compared JRPG recommendations.
</topics>
<notes>
- [Feb 23] Gaming chat - Alex and Sam compared Baldur's Gate 3 builds.
</notes>
</background_facts>
```

Profile citations are resolved at render time from `source_note_ids`.

## Profile Maintenance Strategy

### Default path: incremental

Incremental updates are the normal path because they keep token usage bounded.

Input:

- current guild-scoped profile
- one new conversation note
- the target participant identity

Output:

- full updated profile sections for that one user

Rules:

- only add or merge facts about the target user
- do not attribute other participants' facts to the target user
- keep `source_note_ids`
- if uncertain, return unchanged profile and set `is_dirty = 1`

### Rare path: full rebuild

A full rebuild discards the cached profile summary and recreates it from all remaining
conversation notes for that user in that guild.

It is used for:

- deletion or redaction repair
- drift correction
- model or prompt changes
- periodic maintenance

Full rebuilds never use topic clusters as authoritative source data.

## Deletion and Retention

Because notes are canonical, delete flows must update both derived caches and note records.

Deleting one user's memory in one guild must:

1. delete that user's `guild_user_profiles` row
2. remove that user's `note_participants` rows in that guild
3. delete any note whose only participant was that user
4. redact mixed-participant notes so the deleted user is no longer represented in title or summary
5. delete orphaned `vec_notes` rows when notes are deleted
6. mark surviving participants' profiles dirty
7. invalidate topic clusters for affected guild and day so they are rebuilt

V2 does not include automatic pruning. Retention is explicit:

- conversation notes are kept until a separate pruning policy is designed
- topic clusters and profiles remain rebuildable caches

## Cutover Strategy

This is a cold cutover.

- Do not migrate or reinterpret `facts` or `vec_facts`
- New code reads only v2 tables
- Old memory is intentionally discarded
- After cutover, remove v1 code and drop v1 tables

## Model Split

| Task | Model |
|---|---|
| Conversation note generation | `gpt-5-mini` |
| Incremental profile update | `gpt-5-mini` |
| Topic clustering | `gpt-5.4` |
| Full profile rebuild | `gpt-5.4` |
| Embeddings | `text-embedding-3-small` |

All memory-model traffic uses `MEMORY_OPENAI_TOKEN`.

## Edge Cases

### Buffer reload after restart

Reload persisted channel buffers from SQLite, compute elapsed idle time from `updated_at`,
flush overdue buffers immediately, and restart only the timers that still have time remaining.

### Long conversations that never go quiet

Buffers enforce a hard max age. When a new message arrives on an over-age buffer, flush the
existing buffer first and then start a fresh one.

### Few-note days

If a guild has too few conversation notes on a given day to form useful topic clusters, skip
the cluster phase for that day instead of forcing low-quality output.

### Dirty profiles at retrieval time

If a direct participant's profile is dirty or missing, retrieval should still be useful by
including recent or relevant conversation notes for that user and queuing a rebuild.

### Bot message filtering

Bot-authored messages are excluded from buffer capture, note generation, clustering, and
profile maintenance.
