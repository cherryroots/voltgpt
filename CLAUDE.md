# CLAUDE.md

## Overview

Go Discord bot ("Volt-仙女") providing multimodal AI chat (Gemini, OpenRouter), image generation/editing (Wavespeed), video creation, audio transcription, perceptual image hashing for duplicate detection, and a movie wheel betting game.

## Build & Run

```bash
go build -o voltgpt   # build
./voltgpt             # run (reads .env automatically)
go vet ./...          # static analysis
```

SQLite database (`voltgpt.db`) is created automatically on first run with WAL mode enabled.

The `backup/` directory holds legacy `.gob` snapshots from before the SQLite migration; safe to ignore.

## Environment Variables

Copy `sample.env` to `.env` and add missing tokens manually (sample.env is incomplete). Required:
- `DISCORD_TOKEN` — bot authentication

Optional (features degrade gracefully without these):
- `GEMINI_TOKEN` — Google Gemini API (message handler chat)
- `OPENROUTER_TOKEN` — OpenRouter API (slash command chat, model: o3)
- `OPENAI_TOKEN` — OpenAI API (TTS transcription)
- `WAVESPEED_TOKEN` — Wavespeed API (image generation, video creation)
- `ANTHROPIC_TOKEN`, `STABILITY_TOKEN` — currently unused but defined in sample.env

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

### Multimodal content
`config.RequestContent` carries text, image URLs, video URLs, PDF URLs, and YouTube URLs through the processing pipeline. The utility package handles downloading, resizing, and format conversion (relies on FFmpeg for video).

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
