# Reminder Feature Design

**Date:** 2026-02-22

## Overview

Add a reminder feature to Volt-仙女. Users mention the bot with a reminder phrase; the bot stores the reminder, schedules it, and pings the user in the same channel when it fires. A `/reminders` slash command lists pending reminders with a delete select menu.

## Requirements

- Any server member can set reminders
- Delivery: public ping in the channel where the reminder was set
- Interaction: mention-style (`@Volt remind me ...`) — does not conflict with LLM handler
- Time input: relative offset or absolute datetime, with optional timezone
- Images attached to the reminder message are stored and re-sent when the reminder fires
- A `/reminders` slash command lists pending reminders ephemerally with a delete dropdown

## Data Model

New `reminders` table in SQLite (added in `internal/db/db.go`):

```sql
CREATE TABLE IF NOT EXISTS reminders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT    NOT NULL,
    channel_id TEXT    NOT NULL,
    guild_id   TEXT    NOT NULL,
    message    TEXT    NOT NULL,
    images     TEXT,              -- JSON array of base64-encoded image strings, nullable
    fire_at    INTEGER NOT NULL   -- Unix timestamp (seconds), set by Go
)
```

`fire_at` is stored as a Unix integer (not SQLite DATETIME) to avoid the UTC/local timezone mismatch that affects `CURRENT_TIMESTAMP`.

## Architecture

### Scheduler: `time.AfterFunc` with SQLite backing (Approach B)

- On `Init(db, s)`: load all rows from `reminders` where `fire_at > now`, call `time.AfterFunc(fire_at - now, fire)` for each.
- On new reminder: insert to SQLite, then call `time.AfterFunc`.
- On fire: send ping message (with images if any), delete row from SQLite.
- Survives restarts cleanly — SQLite is the source of truth.

### Package Structure

```
internal/reminder/
  reminder.go   — Init, Reminder struct, Add, fire, mutex, in-memory timer map
  parse.go      — ParseTime: offset parser + absolute datetime parser
```

Follows the same `Init(db)` pattern as `gamble` and `hasher`.

### Time Parsing (`parse.go`)

**Offset format** — `in <duration>`, composable units, short and long forms:

| Short | Long forms |
|-------|------------|
| `y`   | `year`, `years` |
| `mo`  | `month`, `months` |
| `w`, `wk` | `week`, `weeks` |
| `d`   | `day`, `days` |
| `h`, `hr` | `hour`, `hours` |
| `m`, `min` | `minute`, `minutes` |
| `s`, `sec` | `second`, `seconds` |

Examples: `in 2h30m`, `in 4 years`, `in 1y2mo3d`

**Absolute format** — `at <datetime> [timezone]`:

- `at 2026-03-01 15:04`
- `at 2026-03-01 15:04 EST`
- `at 2026-03-01 15:04 America/New_York`
- `at 15:04` — time-only; assumed today if still in the future, tomorrow if already past
- `at 15:04 UTC`

Timezone lookup: try `time.LoadLocation` (IANA names), then a short-name map for common abbreviations (UTC, EST, PST, CET, etc.). Default: UTC.

### Message Handler Integration (`handler/messages.go`)

At the top of the message handler, after confirming the message mentions the bot:

1. Check if content matches reminder trigger: `remind me`, `reminder`, or `remind` (case-insensitive, after stripping the mention).
2. If matched: extract time expression and reminder text, call `reminder.Add(...)`.
3. Memory extraction still fires for the message (reminder messages contain useful context for facts).
4. Return early — Gemini never processes reminder messages.

**Trigger examples:**
- `@Volt remind me in 2h to check the oven`
- `@Volt reminder at 15:00 EST: movie night`
- `@Volt remind me at 2026-03-01 09:00 to renew subscription`

### Confirmation Reply (public, in channel)

```
⏰ @Alice I'll remind you <t:UNIX:R>: check the oven
```

`<t:UNIX:R>` renders as a Discord relative timestamp ("in 2 hours", "in 5 days") adjusted to each viewer's local timezone.

### Reminder Fire Message (public, in channel)

```
⏰ @Alice Reminder: check the oven
```

Images are re-posted as Discord file attachments (decoded from base64).

## `/reminders` Slash Command

- **Ephemeral** — only visible to the invoking user.
- Lists all pending reminders for that user, ordered by `fire_at`:

```
Your reminders:
1. <t:UNIX:R> — check the oven [1 image]
2. <t:UNIX:R> — movie night
```

- Below the list: a `StringSelect` component ("Delete a reminder…") with each reminder as an option (value = DB `id`).
- On selection: component handler (`reminder-delete-<id>`) deletes from SQLite, cancels the timer, edits the message to confirm.

## Files Modified / Created

| File | Change |
|------|--------|
| `internal/db/db.go` | Add `reminders` table to `createTables()` |
| `internal/reminder/reminder.go` | New package: Init, Add, fire, types |
| `internal/reminder/parse.go` | New: ParseTime (offset + absolute + timezone) |
| `internal/config/commands.go` | Add `/reminders` slash command definition |
| `internal/handler/commands.go` | Add `/reminders` handler |
| `internal/handler/components.go` | Add `reminder-delete` component handler |
| `internal/handler/messages.go` | Add reminder pattern check before Gemini dispatch |
| `main.go` | Call `reminder.Init(db.DB, s)` in init chain |

## Out of Scope

- Recurring reminders
- DM delivery
- Admin management of other users' reminders
- Snooze functionality
