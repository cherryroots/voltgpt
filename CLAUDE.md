# CLAUDE.md

## Overview

Go Discord bot ("Volt-仙女") providing multimodal AI chat (Gemini, OpenRouter), image generation/editing (Wavespeed), video creation, audio transcription, perceptual image hashing for duplicate detection, and a movie wheel betting game.

## Build & Run

```bash
/usr/local/go/bin/go build -o voltgpt   # build
./voltgpt                               # run (reads .env automatically)
/usr/local/go/bin/go vet ./...          # static analysis
```

SQLite database (`voltgpt.db`) is created automatically on first run with WAL mode enabled.

## Claude Code Automations

- **Hook**: PostToolUse runs `go build ./...` after any `.go` edit; PreToolUse blocks edits to `.env`
- **Skill**: `/go-check` — runs vet + build and reports results
- **Subagent**: `memory-reviewer` — auto-triggered when modifying `internal/memory/` files
- **MCP**: `sqlite` server connected to `voltgpt.db` for direct DB queries during debugging

The `backup/` directory holds legacy `.gob` snapshots from before the SQLite migration; safe to ignore.

## Environment Variables

Copy `sample.env` to `.env` and add missing tokens manually (sample.env is incomplete). Required:
- `DISCORD_TOKEN` — bot authentication

Optional (features degrade gracefully without these):
- `GEMINI_TOKEN` — Google Gemini API (message handler chat)
- `MEMORY_GEMINI_TOKEN` — separate Gemini key for the memory/embedding system (independent of `GEMINI_TOKEN`)
- `WAVESPEED_TOKEN` — Wavespeed API (image generation, video creation)

## Project Structure

```
main.go                        # Entry point, Discord session, handler registration
internal/
  apis/
    gemini/chat.go             # Gemini streaming chat with tool use
    wavespeed/request.go       # Wavespeed image/video generation
    wavespeed/structs.go       # Wavespeed API types
  config/
    commands.go                # Discord slash command definitions
    config.go                  # Constants, system prompts, admin IDs, types
  db/db.go                     # SQLite init, schema, WAL config
  discord/discord.go           # Response helpers, error logging utilities
  handler/
    commands.go                # Slash command handlers
    components.go              # Button/select menu handlers
    messages.go                # Message event handler (Gemini chat)
    modals.go                  # Modal submission handlers
  gamble/gamble.go             # Movie wheel game: rounds, bets, players
  hasher/hasher.go             # Perceptual image hashing, duplicate detection
  memory/                      # Vector-backed long-term memory (fact extraction, RAG via sqlite-vec)
    consolidate.go             # Deduplicates/merges similar facts via semantic similarity
    extract.go                 # Extracts facts from Discord messages using Gemini
    memory.go                  # Init, embedding storage, sqlite-vec queries
    retrieve.go                # Retrieves relevant facts for RAG context injection
  utility/
    discord.go               # Discord message formatting, content extraction, admin
    messages.go              # Message splitting, sending, and retrieval
    url.go                   # URL parsing, media type detection, downloading
    image.go                 # Image processing, base64 encoding, PNG grids
    video.go                 # FFmpeg video frame extraction
    strings.go               # Generic string helpers
```

## Architecture

### Initialization chain
`init()` in `main.go` loads `.env`, opens SQLite, then calls `Init(db)` on hasher, gamble, and memory packages. Each loads its data from SQLite into in-memory structures.

### Handler dispatch
Handlers are registered as maps (`handler.Commands`, `handler.Components`, `handler.Modals`) mapping string keys to handler functions. All handlers are dispatched in goroutines from the main Discord event listener. Component and modal custom IDs are split on `-` to extract the handler key.

### Persistence pattern
Data lives in memory (protected by `sync.RWMutex`) and is periodically written back to SQLite using `INSERT OR REPLACE` with JSON-serialized payloads. Tables: `image_hashes`, `game_state`, `users`, `facts`.

### System prompt templating
`config.SystemMessage` uses `{TIME}`, `{CHANNEL}`, and `{BACKGROUND_FACTS}` placeholders replaced at request time in `gemini/chat.go`. Always replace `{BACKGROUND_FACTS}` unconditionally (empty string when no facts) — never leave the placeholder literal in the prompt.

### Memory system
- `vec_facts` virtual table uses `distance_metric=cosine`; `distanceThreshold=0.35` and `retrievalDistanceThreshold=0.6` are both cosine distance values
- `config.MemoryBlacklist` and `config.MainServer` gate which channels/guilds get memory extraction
- Facts are extracted in a 30s sliding buffer per user, then consolidated via Gemini before insert
- `sqlite-vec` vec0 tables support full-table scans but ANN queries require the `MATCH` + `k=` syntax

### Multimodal content
`config.RequestContent` carries text, image URLs, video URLs, PDF URLs, and YouTube URLs through the processing pipeline. The utility package handles downloading, resizing, and format conversion (relies on FFmpeg for video).

## Testing

- **`go mod tidy` drops unreferenced deps** — a test-only `go get` won't survive tidy until at least one `_test.go` file imports it; add the import first, then tidy
- **Testing URL-downloading functions** — use `httptest.NewServer` serving in-memory bytes; suffix the URL path with the right extension (e.g., `/test.png`) so `UrlToExt` routes correctly; no real network needed
- **Testing video functions** — `getVideoDuration` and `extractVideoFrameAtTime` take file paths directly; generate `internal/utility/testdata/test.mp4` via `ffmpeg -f lavfi -i color=c=blue:size=64x64:rate=5 -t 1 -pix_fmt yuv420p -y testdata/test.mp4`
- **Testing Discord message functions** — `*discordgo.Message` structs can be constructed directly for tests that don't call the Discord API; only `CleanMessage` (reads `s.State.User.ID`) needs a mock session
- **Testing `GetMessageMediaURL` with attachments** — requires `Width > 0 && Height > 0` on each `*discordgo.MessageAttachment`; zero-value structs are silently skipped, returning no URLs
- **Testing `formatFactsXML` output** — the `<note>` element contains literal `<user>` and `<general>` text; use `</general>` and `<user name=` as check strings to avoid false-positive substring matches
- **Testing hasher package** — `hashStore.m` is global; reset with `hashStore.m = make(map[string]*discordgo.Message)` between tests; `writeHash` (and any code path with `Store: true`) panics if `database` is nil, so those tests need an in-memory DB via `db.Open(":memory:")`
- **Testing memory package** — white-box tests (`package memory`) can set `database = db.DB` directly after `db.Open(":memory:")`; Gemini-backed tests skip cleanly when `MEMORY_GEMINI_TOKEN` is unset; load token for local runs via `export $(grep MEMORY_GEMINI_TOKEN .env | xargs)`
- **Run tests**: `/usr/local/go/bin/go test ./... -timeout 60s` (video tests need the timeout; they use ffmpeg)

## Conventions

- **Package organization**: one package per feature domain under `internal/`
- **No ORM**: raw `database/sql` with `go-sqlite3` driver
- **Error handling**: `log.Fatal` for startup failures; non-fatal errors logged and surfaced to Discord users via `discord.ErrorResponse()`
- **Concurrency**: `sync.RWMutex` on shared maps, `sync.WaitGroup` for parallel operations
- **Naming**: exported `CamelCase`, unexported `camelCase`, struct fields tagged with `json:` for serialization
- **Commands**: defined as `[]*discordgo.ApplicationCommand` in `config/commands.go`, auto-registered per guild on startup, stale commands auto-deleted

## Key Dependencies

| Package | Purpose |
|---|---|
| `bwmarrin/discordgo` | Discord API |
| `google.golang.org/genai` | Google Gemini API |
| `mattn/go-sqlite3` | SQLite driver |
| `asg017/sqlite-vec-go-bindings` | Vector search extension for SQLite (used by memory package) |
| `u2takey/ffmpeg-go` | FFmpeg media processing |
| `corona10/goimagehash` | Perceptual image hashing |
| `joho/godotenv` | .env file loading |
| `ewohltman/discordgo-mock` | Mock Discord sessions for unit tests (import as `.../mocksession`, `.../mockstate`, `.../mockuser` — no `/pkg/` segment) |
