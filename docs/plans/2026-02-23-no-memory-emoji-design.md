# No-Memory Emoji Design

**Date:** 2026-02-23
**Status:** Approved

## Summary

If a user includes 🚫 in their message, background fact retrieval is skipped entirely and no memory context is injected into the system prompt.

## Motivation

Users sometimes want to ask the bot a question without their stored facts influencing the response — e.g. when testing, asking general questions, or when the stored context is known to be unhelpful for a particular query.

## Design

Single change in `internal/handler/messages.go`.

Before:
```go
backgroundFacts := memory.RetrieveMultiUser(m.Content, users)
```

After:
```go
var backgroundFacts string
if !strings.Contains(m.Content, "🚫") {
    backgroundFacts = memory.RetrieveMultiUser(m.Content, users)
}
```

`backgroundFacts` defaults to `""`. The `{BACKGROUND_FACTS}` placeholder in `config.SystemMessage` is always replaced unconditionally, so an empty string is already the correct "no facts" value.

## Trade-offs

- **Skip retrieval entirely** (chosen): avoids the DB/embedding lookup cost.
- Alternative — retrieve but don't inject: wastes the lookup; rejected.

## Scope

- One file modified: `internal/handler/messages.go`
- No changes to `gemini`, `memory`, or `config` packages.
