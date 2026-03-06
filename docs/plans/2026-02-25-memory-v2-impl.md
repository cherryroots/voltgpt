# Memory System v2 Implementation Plan

## Goal

Replace the v1 flat-fact memory system with Memory v2:

- canonical `interaction_notes`
- derived `topic clusters`
- derived guild-scoped `guild_user_profiles`

There is no migration from v1. Old memory is discarded.

## Design Authority

This plan implements:

- [2026-02-25-memory-v2-design.md](./2026-02-25-memory-v2-design.md)
- [2026-03-05-openai-migration-design.md](./2026-03-05-openai-migration-design.md)

If the two documents overlap:

- memory shape and lifecycle come from the memory v2 design
- provider and model choices come from the OpenAI migration design

## Non-Negotiable Constraints

- Keep the build green throughout.
- Add new v2 code before removing v1 code.
- Do not migrate or backfill old `facts` or `vec_facts`.
- Do not keep compatibility with the old retrieval API if it conflicts with guild-scoped retrieval.
- Unit tests must not require live OpenAI tokens.
- Use normalized participant joins, never JSON `LIKE` queries.
- Retrieval must be guild-scoped and use one query embedding per request.
- Similarity fallback must be bounded. Never inject arbitrary nearest neighbors.

## Models

| Task | Model |
|---|---|
| Note generation | `gpt-5-mini` |
| Incremental profile update | `gpt-5-mini` |
| Topic clustering | `gpt-5.4` |
| Full profile rebuild | `gpt-5.4` |
| Embeddings | `text-embedding-3-small` |

## Testing Strategy

Default package tests must use stubs or overridable function variables for:

- embedding calls
- note generation
- incremental profile update
- topic clustering
- full profile rebuild

Optional live integration tests may exist behind an env var, but they are not part of the
default suite. `go test ./... -timeout 60s` must pass without external tokens.

## Implementation Order

1. Schema and indexes
2. Shared types, constants, and provider seams
3. Note storage helpers
4. Per-channel buffer management and persistence
5. Conversation note generation and flush pipeline
6. Guild-scoped profile cache and incremental updates
7. Retrieval rewrite and caller API change
8. Topic clustering job and job-run tracking
9. Dirty-profile rebuild job
10. Delete and redact flows
11. Handler, command, and startup integration
12. Remove v1 code and drop v1 data

This order preserves forward progress while keeping the repo buildable.

## Task 1: Add v2 schema and indexes

**Files**

- Modify: `internal/db/db.go`
- Add or update tests in: `internal/db/db_test.go`

**Add tables**

- `guild_user_profiles`
- `interaction_notes`
- `note_participants`
- `vec_notes`
- `channel_buffers`
- `memory_job_runs`

**Recommended indexes**

- notes by guild, type, and date
- notes by guild, channel, and created time
- participant membership by user and note
- dirty profiles by guild

**Keep for now**

- `users`
- existing non-memory tables

**Do not add**

- any migration from `facts`
- any v1-to-v2 backfill

**Acceptance**

- fresh DB creates all v2 tables and indexes
- `vec_notes` is 1536-dimensional
- existing tests for unrelated tables still pass

## Task 2: Add shared memory types and test seams

**Files**

- Modify: `internal/memory/memory.go`
- Add or update tests in: `internal/memory/memory_test.go`

**Add constants**

- embedding model and dimensions
- note generation model
- incremental update model
- clustering model
- full rebuild model
- retrieval thresholds
- buffer inactivity and max-age windows

**Add types**

- `ProfileFact`
- `GuildUserProfile`
- `InteractionNote`
- `bufMsg`
- `RetrieveRequest`

Suggested request shape:

```go
type RetrieveRequest struct {
    GuildID           string
    ChannelID         string
    Query             string
    ConversationUsers map[string]string
    MentionedUsers    map[string]string
}
```

**Add seams**

Expose overridable package-level functions for:

- `embedText`
- `generateConversationNote`
- `incrementalProfileUpdate`
- `clusterGuildDay`
- `rebuildGuildProfile`

The production implementations can call OpenAI. Tests should stub these directly.

**Acceptance**

- compile-only tests for new types and constants
- stubs can replace model calls in tests

## Task 3: Implement note persistence helpers

**Files**

- Add: `internal/memory/notes.go`
- Add: `internal/memory/notes_test.go`

**Implement**

- `insertNote(...)`
- `getNoteByID(...)`
- helper to list note participants through `note_participants`
- helper to delete a note and its vector row transactionally

`insertNote` must:

1. insert into `interaction_notes`
2. insert `note_participants` rows
3. insert into `vec_notes`
4. commit as one transaction

For `topic_cluster` notes:

- populate `source_note_ids`
- also write union participants to `note_participants`

**Acceptance**

- inserting a conversation note creates:
  - one `interaction_notes` row
  - N `note_participants` rows
  - one `vec_notes` row
- participants are never read from JSON blobs

## Task 4: Build per-channel buffer management and SQLite persistence

**Files**

- Add: `internal/memory/buffer.go`
- Add: `internal/memory/buffer_test.go`

**Implement**

- `BufferMessage(channelID, guildID, discordID, username, displayName, text, messageID string)`
- `saveChannelBuffer(...)`
- `loadChannelBuffers()`
- `deleteChannelBuffer(channelID)`
- `loadAndRestartBuffers()`

**Behavior**

- buffers are keyed by channel or thread ID
- each buffer is persisted to `channel_buffers`
- inactivity timer resets on every message
- max age forces flush before appending the new message
- reload path respects elapsed idle time from `updated_at`
- overdue buffers flush immediately on startup

**Important**

The skip-memory guard must skip capture as well as retrieval.

**Acceptance**

- new and append-to-existing buffer tests pass
- stale buffers force flush on max age
- persisted buffers reload correctly
- overdue reloaded buffers flush immediately instead of getting a fresh full timer

## Task 5: Implement note-generation flush pipeline

**Files**

- Modify: `internal/memory/buffer.go`
- Add or update tests in: `internal/memory/buffer_test.go`

**Implement**

- `flushChannelBuffer(channelID string)`

**Pipeline**

1. read and remove in-memory buffer
2. delete persisted `channel_buffers` row
3. skip if below minimum content threshold
4. generate conversation note title and summary
5. upsert participants in `users`
6. insert note and participant membership
7. embed note into `vec_notes`
8. attempt incremental profile updates for participants
9. mark profiles dirty on failure or low-confidence update

**Guidance**

- note generation should summarize the whole channel or thread buffer
- profile updates must be user-specific, not generic note summarization
- profile update failure must never lose the canonical note

**Acceptance**

- flush creates one conversation note with participants and vector row
- participant profiles are updated or marked dirty
- note creation still succeeds if profile update fails

## Task 6: Add guild-scoped profile cache and incremental update path

**Files**

- Add: `internal/memory/profiles.go`
- Add: `internal/memory/profiles_test.go`

**Implement**

- `GetGuildUserProfile(guildID, discordID string) (*GuildUserProfile, error)`
- `writeGuildUserProfile(...)`
- `markProfileDirty(guildID string, userID int64) error`
- `clearProfileDirty(guildID string, userID int64) error`
- `DeleteGuildUserProfile(guildID, discordID string) error`
- `DeleteAllGuildProfiles(guildID string) (int64, error)`

**Incremental profile update**

The normal update path is:

- current cached profile
- one new conversation note
- target participant identity
- output full updated profile sections

The implementation must:

- keep `source_note_ids`
- avoid cross-participant misattribution
- leave the profile unchanged and mark it dirty if uncertain

**Acceptance**

- empty, existing, dirty, and delete cases are covered
- incremental updates operate on `(guild_id, user_id)`, not global user rows

## Task 7: Rewrite retrieval around a request object

**Files**

- Replace: `internal/memory/retrieve.go`
- Add: `internal/memory/retrieve_test.go`
- Update callers later in handler task

**Public API**

Replace the old multi-user query helper with:

- `BuildPromptContext(req RetrieveRequest) string`

Do not keep `RetrieveMultiUser(query, users)` for compatibility. It cannot satisfy the
guild-scoping requirements cleanly.

**Retrieval flow**

1. embed the query once
2. search `topic_cluster` notes in the current guild
3. search `conversation` notes in the current guild
4. apply strict threshold, then one bounded fallback threshold
5. bias toward current channel or thread and recency where helpful
6. fetch direct participant profiles
7. fetch mentioned-user profiles up to a cap
8. fetch a few extra profiles from relevant note participants
9. if a profile is missing or dirty, include notes for that user and queue rebuild
10. render the XML block

**Do not implement**

- any global cross-guild note sweep
- any arbitrary-nearest fallback

**Acceptance**

- only one embedding call per request
- retrieval never returns notes from another guild
- dirty or missing profiles still yield usable raw-note context
- prompt renderer includes profiles, topics, and notes when available

## Task 8: Add topic-clustering job and job-run tracking

**Files**

- Add: `internal/memory/jobs.go`
- Add: `internal/memory/jobs_test.go`

**Implement**

- `runClusterPhase(guildID, date string) error`
- helpers to read and write `memory_job_runs`
- helpers to delete stale cluster notes for a guild-day before rebuild

**Rules**

- input is that guild-day's conversation notes only
- output is zero or more `topic_cluster` notes
- if there are too few notes for useful clusters, skip cleanly
- cluster rebuild is idempotent
- participants are unioned and written to `note_participants`

**Acceptance**

- cluster phase creates only guild-scoped cluster notes
- rerunning the phase does not duplicate cluster output
- failed phases are detectable and retryable

## Task 9: Add dirty-profile rebuild job

**Files**

- Modify: `internal/memory/jobs.go`
- Add or update tests in: `internal/memory/jobs_test.go`

**Implement**

- `runProfileMaintenancePhase(guildID, date string) error`
- `rebuildDirtyProfiles(guildID string) error`
- `rebuildOneGuildProfile(guildID string, userID int64) error`

**Rebuild flow**

1. list dirty profiles in the guild
2. load all remaining conversation notes for that user in that guild
3. rebuild the profile from notes only
4. write the fresh profile
5. clear dirty flag

This is also where periodic maintenance rebuilds can run for high-activity users.

**Important**

- full rebuild uses conversation notes only
- full rebuild does not use topic clusters as source of truth

**Acceptance**

- dirty profiles can be repaired from notes
- clean profiles are skipped
- rebuild logic is guild-scoped

## Task 10: Implement delete and redact flows

**Files**

- Add: `internal/memory/delete.go`
- Add: `internal/memory/delete_test.go`

**Implement**

- `DeleteUserMemory(guildID, discordID string) error`
- `DeleteAllGuildMemory(guildID string) error`
- helpers to:
  - find affected notes
  - delete single-participant notes
  - redact mixed-participant notes
  - delete orphaned vectors
  - mark affected profiles dirty
  - invalidate cluster output for affected guild-days

**Delete semantics**

For one user in one guild:

1. delete the cached profile row
2. remove that user from `note_participants`
3. delete notes that only belonged to that user
4. redact mixed notes so the deleted user is not reintroduced later
5. delete orphaned `vec_notes` rows
6. mark surviving participants' profiles dirty
7. delete completed cluster job rows for affected days so the cluster phase reruns

**Acceptance**

- deleted users cannot be reconstructed from retained mixed notes
- affected profiles and clusters are repairable from remaining notes

## Task 11: Wire handlers, commands, and startup

**Files**

- Modify: `internal/handler/messages.go`
- Modify: `internal/handler/commands.go`
- Modify: `main.go`
- Modify: `internal/memory/memory.go`

**Message handler**

- replace `Extract(...)` with `BufferMessage(...)`
- gate capture with the skip-memory guard
- replace old retrieval call with `BuildPromptContext(RetrieveRequest{...})`
- pass guild and channel context into retrieval

**Startup**

- init OpenAI memory client
- reload persisted buffers
- flush overdue buffers immediately
- run missed or failed maintenance phases on startup catch-up

**Commands**

Update memory commands to use v2 concepts:

- self/admin view -> guild-scoped profile display
- admin digest -> recent conversation notes or cluster digests
- admin delete -> delete guild-scoped user memory or all guild memory
- refresh-names -> remove or repurpose; it is not meaningful in v2

**Logging**

- replace `TotalFacts()` startup logging with `TotalNotes()` or equivalent v2 metric

**Acceptance**

- handlers compile against the new retrieval API
- commands no longer reference v1 fact helpers

## Task 12: Remove v1 code and drop v1 data

**Files**

- Delete: `internal/memory/extract.go`
- Delete: `internal/memory/consolidate.go`
- Replace or trim: `internal/memory/memory.go`
- Update tests across `internal/memory/`
- Modify: `internal/db/db.go` to remove v1 table creation once cutover is complete

**Remove**

- `facts`
- `vec_facts`
- v1 retrieval helpers
- v1 admin helpers
- v1 tests

**Database cleanup**

After all code has switched to v2:

- drop `vec_facts`
- drop `facts`

There is no v1 data migration. Old memory is intentionally lost.

**Acceptance**

- repo contains no references to v1 memory helpers
- default test suite passes
- build passes

## Suggested File Layout After Cutover

```text
internal/memory/
  memory.go
  notes.go
  buffer.go
  profiles.go
  retrieve.go
  jobs.go
  delete.go
```

## Verification Commands

Run these throughout implementation:

```bash
go build ./...
go test ./internal/db -timeout 60s
go test ./internal/memory -timeout 60s
go test ./... -timeout 60s
go vet ./...
```

## Definition of Done

Memory v2 is complete when all of the following are true:

- v1 fact memory code is gone
- old memory data is not migrated
- conversation notes are canonical
- profiles are guild-scoped cached summaries
- topic clusters are derived retrieval context, not profile source
- retrieval is guild-scoped and request-based
- channel buffers survive restarts correctly
- deletion and redaction semantics are safe
- topic-cluster and dirty-profile maintenance are idempotent
- default tests pass without live model tokens
