# OpenAI Responses API Migration Design

## Overview

Migrate VoltGPT's chat interface from Gemini to OpenAI's Responses API with streaming. The memory system (embeddings, extraction, consolidation) also moves to OpenAI. The existing `internal/apis/gemini/` package is kept intact; a new `internal/apis/openai/` package is added alongside it.

## Models

| Purpose | Model |
|---|---|
| Chat | `gpt-5.4` |
| Fact extraction + consolidation | `gpt-5-mini` |
| Embeddings | `text-embedding-3-small` (1536 dims) |

## Environment Variables

| Remove | Add |
|---|---|
| `GEMINI_TOKEN` | `OPENAI_TOKEN` |
| `MEMORY_GEMINI_TOKEN` | `MEMORY_OPENAI_TOKEN` |

## Stateful Conversation via `previous_response_id`

The core improvement over the Gemini approach: instead of rebuilding the full reply chain history on every message, the OpenAI response ID is stored after each bot response and reused on subsequent replies.

### SQLite Schema Addition

```sql
CREATE TABLE IF NOT EXISTS response_ids (
    discord_message_id TEXT PRIMARY KEY,
    openai_response_id TEXT NOT NULL
);
```

Added to `internal/db/db.go` alongside existing schema.

### Conversation Flow

```
Incoming mention
       |
       v
Is it a reply to a bot message?
       |
   +---+-------------------------------------------+
   |                                               |
   | Yes                                           | No / no stored ID
   |                                               |
   v                                               v
Look up previous_response_id              Build history from reply chain
from response_ids table                   via PrependReplyMessages()
   |                                               |
   +---+-------------------------------------------+
       |
       v
Call OpenAI Responses API (streaming)
       |
       v
Store bot_discord_msg_id -> openai_response_id
in response_ids table
```

**Case 1 — Direct mention, no reply:** Single-message input. No `previous_response_id`.

**Case 2 — Reply to bot message with stored ID:** Pass `previous_response_id`. Skip history rebuild entirely.

**Case 3 — Reply to non-bot message, or bot message with no stored ID:** Build full history from reply chain. No `previous_response_id`. This becomes the base turn; subsequent replies use Case 2.

## New Package: `internal/apis/openai/`

File: `chat.go`

Functions:
- `GetClient() (*openai.Client, error)` — singleton using `OPENAI_TOKEN`
- `StreamMessageResponse(s, client, m, input, previousResponseID, backgroundFacts) error`
- `PrependReplyMessages(s, originMember, message, cache, items)` — builds `[]openai.ResponseInputItemUnion`
- `CreateContent(role, content RequestContent) openai.ResponseInputItemUnion`
- `LookupResponseID(discordMsgID string) (string, error)` — queries SQLite
- `StoreResponseID(discordMsgID, openaiResponseID string) error` — writes to SQLite

Streamer: identical ticker-based logic as Gemini version. `ThoughtSignature` / PNG embedding removed entirely. No file attachments on bot responses.

## Handler Changes (`internal/handler/messages.go`)

- Replace Gemini client with OpenAI client
- After determining `isReply`: check if the referenced message ID has a stored `previous_response_id`
  - If yes: skip `PrependReplyMessages`, pass ID to `StreamMessageResponse`
  - If no: call `PrependReplyMessages` to build input array
- Remove `[]*genai.Content` usage; use `[]openai.ResponseInputItemUnion`
- Remove `genai` import; add `openai` package import

## Memory System Changes (`internal/memory/`)

- `memory.go`: replace Gemini embedding client with OpenAI client using `MEMORY_OPENAI_TOKEN`; call `text-embedding-3-small` for all embed operations
- `extract.go`: replace Gemini generation call with OpenAI chat completions (`gpt-5-mini`)
- `consolidate.go`: same — replace Gemini call with OpenAI chat completions (`gpt-5-mini`)
- **vec_facts table must be cleared** on first run after migration — embedding dimensions change from 768 (Gemini) to 1536 (OpenAI); stored vectors are incompatible

## Multimodal Handling

| Content type | Handling |
|---|---|
| Images | Download, send as base64 inline |
| Videos | Extract frames via existing FFmpeg utility, send as images |
| PDFs | Skipped (no change from intent) |
| YouTube URLs | Silently dropped |

## Dependency Changes

```
Add: github.com/openai/openai-go
Keep: google.golang.org/genai (gemini package retained)
```

## Out of Scope

- Removing the Gemini package
- Tool use / function calling
- PDF support
