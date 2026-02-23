# Channel Thread Context Window

**Date:** 2026-02-23
**Status:** Design approved

## Problem

When a user @mentions Vivy without using Discord's reply feature, she starts cold with no history of the channel. The reply chain mechanism works well for explicit threading, but in practice conversations drift — someone @mentions without replying, or asks a follow-up in a new message. The result is Vivy losing context she visibly had in the previous turn.

Additionally, even within a reply chain, Vivy has no awareness of concurrent conversations happening in the same channel.

## Solution

Inject a **channel thread context window** before every response — a structured snapshot of recent channel activity grouped by reply chain. This gives Vivy consistent background context (breadth) regardless of whether a reply chain is also present (depth). The two layers are complementary and deduplicated.

## Design

### Context Structure

Vivy receives two layers of context per response:

1. **Thread context window** — recent channel messages, grouped by reply chain, with reply-chain members filtered out to avoid duplication
2. **Reply chain** — the explicit focused conversation (unchanged, existing behavior)

For standalone @mentions with no reply chain, layer 2 is absent and layer 1 carries the full context.

### Thread Forest Construction

From the cached message list, build a forest by following `MessageReference` links:

- A message whose reference points to another cached message is a child of that message
- A message with no reference, or whose reference is outside the cache, is a root
- Each tree = one conversation thread
- Threads are sorted by the timestamp of their most recent message (newest first)
- A reasonable cap of ~5 threads (or ~20 messages total) keeps token usage bounded

### Deduplication

Messages already present in the reply chain are excluded from the thread context window. This prevents the same message appearing twice and ensures the layers are additive rather than redundant.

### Formatted Output

Each thread is presented as a labeled block:

```
[recent channel context]

thread (alice, bob):
  alice: I've been thinking about X...
  bob: What about Y though?
  alice: Yeah, also Z

thread (you, vivy):
  you: @Vivy what's your take on W?
  vivy: Here's what I think about W...

standalone (charlie):
  Anyone seen the new thing?
```

The bot's own messages are labeled `vivy` to match its identity. The block is injected as a `user`-role Gemini content turn before the reply chain and current message.

### Always-on Fetch

The message cache (100 messages) is now fetched unconditionally at the start of `HandleMessage`, not only for reply chains. This enables:
- Thread context window for standalone @mentions
- Consistent deduplication when a reply chain is also present

### 🚫 Guard

The `🚫` emoji flag that currently skips memory retrieval is extended to also skip the thread context window. Both are context injection mechanisms, and the user's intent with `🚫` is clearly "answer this in isolation."

`utility.ShouldSkipMemory` is renamed to `utility.ShouldSkipContext` to reflect this broader scope. The function signature and logic are unchanged.

## Code Changes

### `internal/handler/messages.go`

- Move cache fetch (`GetMessagesBefore`) outside the `isReply` branch so it always runs
- Call new `gemini.PrependChannelContext` before `PrependReplyMessages`, passing the reply-chain message IDs for deduplication
- Replace `utility.ShouldSkipMemory` → `utility.ShouldSkipContext` (also gates thread context)

### `internal/apis/gemini/chat.go`

New function:
```go
func PrependChannelContext(
    s *discordgo.Session,
    m *discordgo.Message,
    cache []*discordgo.Message,
    excludeIDs map[string]bool,
    chatMessages *[]*genai.Content,
)
```

- Builds thread forest from `cache`
- Filters messages in `excludeIDs`
- Caps output to ~5 threads / ~20 messages
- Formats as a single `user` content turn and prepends to `chatMessages`

### `internal/utility/messages.go`

- Rename `ShouldSkipMemory` → `ShouldSkipContext`

### `internal/handler/messages_test.go`

- Update test to call `ShouldSkipContext`

### New helper (in `gemini` or `utility`)

```go
// buildThreadForest groups a flat message list into reply-chain trees.
// Returns a slice of threads, each thread being a slice of messages
// in chronological order. Threads are ordered newest-first by their
// most recent message.
func buildThreadForest(messages []*discordgo.Message) [][]*discordgo.Message
```

## Non-Goals

- No changes to reply chain depth or fetch limit
- No per-channel or per-user configuration of window size (keep it simple)
- Thread context is not persisted or cached beyond the request lifetime
