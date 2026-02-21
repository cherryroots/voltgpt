# Reminder Feature Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add a mention-style reminder system (`@Volt remind me in 2h ...`) that pings users in channel, stores reminders in SQLite, and survives bot restarts.

**Architecture:** `time.AfterFunc` timers backed by a `reminders` SQLite table (fire_at as Unix int). On startup `reminder.Init(db, session)` loads all pending rows and re-schedules them. The message handler intercepts reminder-shaped mentions before Gemini sees them. A `/reminders` slash command lists pending reminders ephemerally with a select-menu delete.

**Tech Stack:** Go, discordgo, database/sql (go-sqlite3), encoding/base64, encoding/json, regexp, time.AfterFunc

---

### Task 1: Add `reminders` table to DB

**Files:**
- Modify: `internal/db/db.go`

**Step 1: Add the table definition**

In `createTables()`, append a new entry to the `tables` slice (after the `facts` entry):

```go
`CREATE TABLE IF NOT EXISTS reminders (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT    NOT NULL,
    channel_id TEXT    NOT NULL,
    guild_id   TEXT    NOT NULL,
    message    TEXT    NOT NULL,
    images     TEXT,
    fire_at    INTEGER NOT NULL
)`,
```

**Step 2: Build to verify no compile errors**

Run: `/usr/local/go/bin/go build ./...`
Expected: no output (success)

**Step 3: Commit**

```bash
git add internal/db/db.go
git commit -m "feat: add reminders table to SQLite schema"
```

---

### Task 2: Time parser (`parse.go`) — TDD

**Files:**
- Create: `internal/reminder/parse.go`
- Create: `internal/reminder/parse_test.go`

**Step 1: Write the failing tests**

Create `internal/reminder/parse_test.go`:

```go
package reminder

import (
	"testing"
	"time"
)

// testNow is a fixed reference point for all parse tests: 2026-02-22 12:00 UTC.
var testNow = time.Date(2026, 2, 22, 12, 0, 0, 0, time.UTC)

func TestParseTimeOffset(t *testing.T) {
	tests := []struct {
		input     string
		wantDelta time.Duration
		wantMsg   string
	}{
		{"in 2h to check the oven", 2 * time.Hour, "check the oven"},
		{"in 30m: buy groceries", 30 * time.Minute, "buy groceries"},
		{"in 2h30m meeting", 2*time.Hour + 30*time.Minute, "meeting"},
		{"in 1 hour dentist", time.Hour, "dentist"},
		{"in 30 minutes call mom", 30 * time.Minute, "call mom"},
		{"in 7 days workout", 7 * 24 * time.Hour, "workout"},
		{"in 1y update CV", 365 * 24 * time.Hour, "update CV"},
		{"in 2 years tax", 2 * 365 * 24 * time.Hour, "tax"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, remaining, err := ParseTime(tt.input, testNow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			delta := got.Sub(testNow)
			if delta != tt.wantDelta {
				t.Errorf("delta: got %v, want %v", delta, tt.wantDelta)
			}
			if remaining != tt.wantMsg {
				t.Errorf("remaining: got %q, want %q", remaining, tt.wantMsg)
			}
		})
	}
}

func TestParseTimeAbsolute(t *testing.T) {
	est := time.FixedZone("EST", -5*3600)
	tests := []struct {
		input   string
		wantT   time.Time
		wantMsg string
	}{
		{
			"at 2026-03-01 15:04 check it",
			time.Date(2026, 3, 1, 15, 4, 0, 0, time.UTC),
			"check it",
		},
		{
			"at 2026-03-01 15:04 EST check it",
			time.Date(2026, 3, 1, 15, 4, 0, 0, est),
			"check it",
		},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, remaining, err := ParseTime(tt.input, testNow)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !got.Equal(tt.wantT) {
				t.Errorf("time: got %v, want %v", got, tt.wantT)
			}
			if remaining != tt.wantMsg {
				t.Errorf("remaining: got %q, want %q", remaining, tt.wantMsg)
			}
		})
	}
}

func TestParseTimeTimeOnly(t *testing.T) {
	// testNow is 12:00 UTC — "at 15:00" is still in the future today.
	got, _, err := ParseTime("at 15:00 reminder", testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := time.Date(2026, 2, 22, 15, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("future today: got %v, want %v", got, want)
	}

	// "at 10:00" is in the past today → should roll to tomorrow.
	got2, _, err := ParseTime("at 10:00 reminder", testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want2 := time.Date(2026, 2, 23, 10, 0, 0, 0, time.UTC)
	if !got2.Equal(want2) {
		t.Errorf("past → tomorrow: got %v, want %v", got2, want2)
	}
}

func TestParseTimeErrors(t *testing.T) {
	if _, _, err := ParseTime("remind me something", testNow); err == nil {
		t.Error("expected error for missing 'in'/'at' prefix")
	}
	if _, _, err := ParseTime("in nothing", testNow); err == nil {
		t.Error("expected error for no valid duration units")
	}
	if _, _, err := ParseTime("at invalid-date here", testNow); err == nil {
		t.Error("expected error for unparseable absolute time")
	}
}
```

**Step 2: Run tests — expect compile failure**

Run: `/usr/local/go/bin/go test ./internal/reminder/... -v`
Expected: `cannot find package` or `undefined: ParseTime`

**Step 3: Implement `parse.go`**

Create `internal/reminder/parse.go`:

```go
package reminder

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// unitRe matches one duration component: a number followed by a unit.
// Longer unit names (e.g. "months") must precede shorter prefixes (e.g. "mo", "m")
// to avoid greedy mismatches.
var unitRe = regexp.MustCompile(
	`(?i)^(\d+)\s*` +
		`(years?|yr|months?|mo|weeks?|wk|days?|hours?|hr|minutes?|mins?|seconds?|secs?|[ymwdhs])`,
)

// tzAbbrevs maps common timezone abbreviations to fixed-offset locations.
// time.LoadLocation handles IANA names (e.g. "America/New_York"); this map
// covers short abbreviations that LoadLocation does not recognise.
var tzAbbrevs = map[string]*time.Location{
	"UTC":  time.UTC,
	"GMT":  time.UTC,
	"EST":  time.FixedZone("EST", -5*3600),
	"EDT":  time.FixedZone("EDT", -4*3600),
	"CST":  time.FixedZone("CST", -6*3600),
	"CDT":  time.FixedZone("CDT", -5*3600),
	"MST":  time.FixedZone("MST", -7*3600),
	"MDT":  time.FixedZone("MDT", -6*3600),
	"PST":  time.FixedZone("PST", -8*3600),
	"PDT":  time.FixedZone("PDT", -7*3600),
	"CET":  time.FixedZone("CET", 1*3600),
	"CEST": time.FixedZone("CEST", 2*3600),
	"BST":  time.FixedZone("BST", 1*3600),
	"JST":  time.FixedZone("JST", 9*3600),
	"AEST": time.FixedZone("AEST", 10*3600),
}

// ParseTime parses a time expression from the start of s.
// s must begin with "in " (relative offset) or "at " (absolute datetime).
// Returns the resolved fire time, the remainder of s as the reminder message,
// and any parse error.
func ParseTime(s string, now time.Time) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	lower := strings.ToLower(s)
	switch {
	case strings.HasPrefix(lower, "in "):
		return parseOffset(s[3:], now)
	case strings.HasPrefix(lower, "at "):
		return parseAbsolute(s[3:], now)
	default:
		return time.Time{}, s, fmt.Errorf("time expression must start with 'in' or 'at'")
	}
}

// parseOffset handles relative offsets: "2h30m to check the oven", "4 years tax".
// Multiple unit tokens are consumed greedily until no more unit tokens are found.
func parseOffset(s string, now time.Time) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	t := now
	matched := false

	for {
		m := unitRe.FindStringSubmatch(s)
		if m == nil {
			break
		}
		n, _ := strconv.Atoi(m[1])
		unit := strings.ToLower(m[2])
		switch {
		case strings.HasPrefix(unit, "y"):
			t = t.AddDate(n, 0, 0)
		case strings.HasPrefix(unit, "mo"):
			t = t.AddDate(0, n, 0)
		case strings.HasPrefix(unit, "w"):
			t = t.AddDate(0, 0, n*7)
		case strings.HasPrefix(unit, "d"):
			t = t.AddDate(0, 0, n)
		case strings.HasPrefix(unit, "h"):
			t = t.Add(time.Duration(n) * time.Hour)
		case strings.HasPrefix(unit, "mi"), unit == "m":
			t = t.Add(time.Duration(n) * time.Minute)
		case strings.HasPrefix(unit, "s"):
			t = t.Add(time.Duration(n) * time.Second)
		}
		s = strings.TrimSpace(s[len(m[0]):])
		matched = true
	}

	if !matched {
		return time.Time{}, s, fmt.Errorf("no valid duration found in %q", s)
	}
	return t, cleanRemaining(s), nil
}

// parseAbsolute handles "2026-03-01 15:04 EST: text" and "15:04 UTC text".
// It tries combining 1 or 2 leading words as the datetime string, optionally
// followed by a timezone word.
func parseAbsolute(s string, now time.Time) (time.Time, string, error) {
	s = strings.TrimSpace(s)
	words := strings.Fields(s)
	if len(words) == 0 {
		return time.Time{}, s, fmt.Errorf("empty absolute time expression")
	}

	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"15:04:05",
		"15:04",
	}

	// Try 2-word then 1-word datetime strings.
	for numTimeWords := 2; numTimeWords >= 1; numTimeWords-- {
		if numTimeWords > len(words) {
			continue
		}
		timeStr := strings.Join(words[:numTimeWords], " ")

		// Check whether the next word is a recognized timezone.
		loc := time.UTC
		tzWords := 0
		if numTimeWords < len(words) {
			candidate := words[numTimeWords]
			if l, err := time.LoadLocation(candidate); err == nil {
				loc = l
				tzWords = 1
			} else if l, ok := tzAbbrevs[strings.ToUpper(candidate)]; ok {
				loc = l
				tzWords = 1
			}
		}

		for _, layout := range layouts {
			t, err := time.ParseInLocation(layout, timeStr, loc)
			if err != nil {
				continue
			}

			// Time-only input: anchor to today; roll forward to tomorrow if past.
			if !strings.Contains(timeStr, "-") {
				y, mo, d := now.In(loc).Date()
				t = time.Date(y, mo, d, t.Hour(), t.Minute(), t.Second(), 0, loc)
				if !t.After(now) {
					t = t.AddDate(0, 0, 1)
				}
			}

			consumed := numTimeWords + tzWords
			remaining := ""
			if consumed < len(words) {
				remaining = strings.Join(words[consumed:], " ")
			}
			return t, cleanRemaining(remaining), nil
		}
	}

	return time.Time{}, s, fmt.Errorf("could not parse time from %q", s)
}

// cleanRemaining strips leading punctuation and optional "to " prefix from
// the remainder string, which becomes the reminder message text.
func cleanRemaining(s string) string {
	s = strings.TrimLeft(s, ":-, \t")
	if strings.HasPrefix(strings.ToLower(s), "to ") {
		s = s[3:]
	}
	return strings.TrimSpace(s)
}
```

**Step 4: Run tests — expect pass**

Run: `/usr/local/go/bin/go test ./internal/reminder/... -v -run TestParse`
Expected: all `TestParse*` tests PASS

**Step 5: Build check**

Run: `/usr/local/go/bin/go build ./...`
Expected: no output

**Step 6: Commit**

```bash
git add internal/reminder/parse.go internal/reminder/parse_test.go
git commit -m "feat: add reminder time parser with offset and absolute datetime support"
```

---

### Task 3: Reminder core (`reminder.go`) — TDD

**Files:**
- Create: `internal/reminder/reminder.go`
- Create: `internal/reminder/reminder_test.go`

**Step 1: Write failing tests**

Create `internal/reminder/reminder_test.go`:

```go
package reminder

import (
	"testing"
	"time"

	"voltgpt/internal/db"
)

func setupDB(t *testing.T) {
	t.Helper()
	db.Open(":memory:")
	database = db.DB
	timers = map[int64]*time.Timer{}
}

func TestAddAndGetUserReminders(t *testing.T) {
	setupDB(t)

	fireAt := time.Now().Add(1 * time.Hour)
	if err := Add("user1", "chan1", "guild1", "check the oven", nil, fireAt); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	reminders, err := GetUserReminders("user1")
	if err != nil {
		t.Fatalf("GetUserReminders failed: %v", err)
	}
	if len(reminders) != 1 {
		t.Fatalf("expected 1 reminder, got %d", len(reminders))
	}
	if reminders[0].Message != "check the oven" {
		t.Errorf("message: got %q, want %q", reminders[0].Message, "check the oven")
	}
	if reminders[0].UserID != "user1" {
		t.Errorf("userID: got %q, want %q", reminders[0].UserID, "user1")
	}
}

func TestAddWithImages(t *testing.T) {
	setupDB(t)

	imgs := []Image{{Filename: "photo.png", Data: "abc123"}}
	fireAt := time.Now().Add(1 * time.Hour)
	if err := Add("user1", "chan1", "guild1", "msg", imgs, fireAt); err != nil {
		t.Fatalf("Add failed: %v", err)
	}

	reminders, _ := GetUserReminders("user1")
	if len(reminders[0].Images) != 1 {
		t.Fatalf("expected 1 image, got %d", len(reminders[0].Images))
	}
	if reminders[0].Images[0].Filename != "photo.png" {
		t.Errorf("filename: got %q, want %q", reminders[0].Images[0].Filename, "photo.png")
	}
}

func TestDelete(t *testing.T) {
	setupDB(t)

	fireAt := time.Now().Add(1 * time.Hour)
	Add("user1", "chan1", "guild1", "test", nil, fireAt)

	reminders, _ := GetUserReminders("user1")
	if len(reminders) != 1 {
		t.Fatal("expected 1 reminder before delete")
	}

	if ok := Delete(reminders[0].ID); !ok {
		t.Error("Delete returned false for existing reminder")
	}

	after, _ := GetUserReminders("user1")
	if len(after) != 0 {
		t.Errorf("expected 0 reminders after delete, got %d", len(after))
	}
}

func TestDeleteNonExistent(t *testing.T) {
	setupDB(t)

	if ok := Delete(99999); ok {
		t.Error("Delete returned true for non-existent reminder")
	}
}

func TestGetUserRemindersExcludesPast(t *testing.T) {
	setupDB(t)

	// Insert a reminder with fire_at in the past directly via DB.
	db.DB.Exec(
		"INSERT INTO reminders (user_id, channel_id, guild_id, message, fire_at) VALUES (?, ?, ?, ?, ?)",
		"user1", "chan1", "guild1", "old", time.Now().Add(-1*time.Hour).Unix(),
	)

	reminders, _ := GetUserReminders("user1")
	if len(reminders) != 0 {
		t.Errorf("expected past reminder excluded, got %d", len(reminders))
	}
}

func TestTotalActive(t *testing.T) {
	setupDB(t)

	before := TotalActive()

	fireAt := time.Now().Add(1 * time.Hour)
	Add("user1", "chan1", "guild1", "test", nil, fireAt)

	if TotalActive() != before+1 {
		t.Errorf("expected TotalActive to increase by 1")
	}
}
```

**Step 2: Run tests — expect compile failure**

Run: `/usr/local/go/bin/go test ./internal/reminder/... -v -run TestAdd`
Expected: `undefined: Add`, `undefined: Reminder`, etc.

**Step 3: Implement `reminder.go`**

Create `internal/reminder/reminder.go`:

```go
// Package reminder manages scheduled Discord reminders backed by SQLite.
package reminder

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

var (
	database *sql.DB
	session  *discordgo.Session
	mu       sync.Mutex
	timers   = map[int64]*time.Timer{}
)

// Image is an attached image stored with a reminder.
type Image struct {
	Filename string `json:"filename"`
	Data     string `json:"data"` // base64-encoded bytes
}

// Reminder is a scheduled reminder row loaded from SQLite.
type Reminder struct {
	ID        int64
	UserID    string
	ChannelID string
	GuildID   string
	Message   string
	Images    []Image
	FireAt    int64 // Unix timestamp (seconds)
}

// Init loads all pending reminders from SQLite and schedules them.
// Must be called from main() after dg.Open(), because it needs the session.
func Init(db *sql.DB, s *discordgo.Session) {
	database = db
	session = s
	loadAndSchedule()
}

func loadAndSchedule() {
	rows, err := database.Query(
		"SELECT id, user_id, channel_id, guild_id, message, images, fire_at FROM reminders WHERE fire_at > ?",
		time.Now().Unix(),
	)
	if err != nil {
		log.Printf("reminder: failed to load pending reminders: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var r Reminder
		var imagesJSON sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.ChannelID, &r.GuildID, &r.Message, &imagesJSON, &r.FireAt); err != nil {
			log.Printf("reminder: scan error: %v", err)
			continue
		}
		if imagesJSON.Valid && imagesJSON.String != "" {
			json.Unmarshal([]byte(imagesJSON.String), &r.Images)
		}
		schedule(r)
	}
}

func schedule(r Reminder) {
	delay := time.Until(time.Unix(r.FireAt, 0))
	if delay < 0 {
		delay = 0
	}
	t := time.AfterFunc(delay, func() { fire(r) })
	mu.Lock()
	timers[r.ID] = t
	mu.Unlock()
}

func fire(r Reminder) {
	mu.Lock()
	delete(timers, r.ID)
	mu.Unlock()

	msg := fmt.Sprintf("⏰ <@%s> Reminder: %s", r.UserID, r.Message)

	var sendErr error
	if len(r.Images) == 0 {
		_, sendErr = session.ChannelMessageSend(r.ChannelID, msg)
	} else {
		files := make([]*discordgo.File, 0, len(r.Images))
		for _, img := range r.Images {
			data, err := base64.StdEncoding.DecodeString(img.Data)
			if err != nil {
				log.Printf("reminder: decode image %q: %v", img.Filename, err)
				continue
			}
			files = append(files, &discordgo.File{
				Name:   img.Filename,
				Reader: bytes.NewReader(data),
			})
		}
		_, sendErr = session.ChannelMessageSendComplex(r.ChannelID, &discordgo.MessageSend{
			Content: msg,
			Files:   files,
		})
	}
	if sendErr != nil {
		log.Printf("reminder: failed to send reminder %d: %v", r.ID, sendErr)
	}

	if _, err := database.Exec("DELETE FROM reminders WHERE id = ?", r.ID); err != nil {
		log.Printf("reminder: failed to delete reminder %d after firing: %v", r.ID, err)
	}
}

// Add inserts a new reminder into SQLite and schedules its timer.
func Add(userID, channelID, guildID, message string, images []Image, fireAt time.Time) error {
	var imagesJSON sql.NullString
	if len(images) > 0 {
		b, err := json.Marshal(images)
		if err != nil {
			return fmt.Errorf("marshal images: %w", err)
		}
		imagesJSON = sql.NullString{String: string(b), Valid: true}
	}

	result, err := database.Exec(
		"INSERT INTO reminders (user_id, channel_id, guild_id, message, images, fire_at) VALUES (?, ?, ?, ?, ?, ?)",
		userID, channelID, guildID, message, imagesJSON, fireAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert reminder: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	schedule(Reminder{
		ID:        id,
		UserID:    userID,
		ChannelID: channelID,
		GuildID:   guildID,
		Message:   message,
		Images:    images,
		FireAt:    fireAt.Unix(),
	})
	return nil
}

// Delete cancels and removes a reminder by ID. Returns true if a row was deleted.
func Delete(id int64) bool {
	mu.Lock()
	if t, ok := timers[id]; ok {
		t.Stop()
		delete(timers, id)
	}
	mu.Unlock()

	res, err := database.Exec("DELETE FROM reminders WHERE id = ?", id)
	if err != nil {
		log.Printf("reminder: delete %d: %v", id, err)
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// GetUserReminders returns all future reminders for userID, ordered by fire time.
func GetUserReminders(userID string) ([]Reminder, error) {
	rows, err := database.Query(
		"SELECT id, user_id, channel_id, guild_id, message, images, fire_at FROM reminders WHERE user_id = ? AND fire_at > ? ORDER BY fire_at ASC",
		userID, time.Now().Unix(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []Reminder
	for rows.Next() {
		var r Reminder
		var imagesJSON sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.ChannelID, &r.GuildID, &r.Message, &imagesJSON, &r.FireAt); err != nil {
			log.Printf("reminder: scan: %v", err)
			continue
		}
		if imagesJSON.Valid && imagesJSON.String != "" {
			json.Unmarshal([]byte(imagesJSON.String), &r.Images)
		}
		reminders = append(reminders, r)
	}
	return reminders, nil
}

// TotalActive returns the count of in-memory scheduled timers.
func TotalActive() int {
	mu.Lock()
	defer mu.Unlock()
	return len(timers)
}
```

**Step 4: Run tests — expect pass**

Run: `/usr/local/go/bin/go test ./internal/reminder/... -v`
Expected: all tests PASS

**Step 5: Commit**

```bash
git add internal/reminder/reminder.go internal/reminder/reminder_test.go
git commit -m "feat: add reminder package with SQLite persistence and time.AfterFunc scheduling"
```

---

### Task 4: Message handler — intercept reminder mentions

**Files:**
- Modify: `internal/handler/messages.go`

**Context:** `CleanMessage` strips the bot mention so that after line 87-88 in `HandleMessage`, `m.Message.Content` is the raw message text without the `<@BOT_ID>` prefix. E.g. `@Volt remind me in 2h check the oven` becomes `remind me in 2h check the oven`.

**Step 1: Add the reminder check after line 88**

In `HandleMessage`, after the two lines:
```go
m.Message = utility.CleanMessage(s, m.Message)
m.Message.Content = utility.ResolveMentions(m.Message.Content, m.Mentions)
```

Insert:

```go
if triggerLen, ok := reminderTrigger(m.Message.Content); ok {
    handleReminder(s, m.Message, triggerLen)
    return
}
```

**Step 2: Add imports**

Add to the import block in `messages.go`:
```go
"encoding/base64"
"net/http"
"io"

"voltgpt/internal/reminder"
```

**Step 3: Add helper functions at the bottom of `messages.go`**

```go
// reminderTrigger reports whether content starts with a reminder trigger phrase.
// Returns the byte length of the trigger prefix so the caller can slice past it.
func reminderTrigger(content string) (int, bool) {
	lower := strings.ToLower(strings.TrimSpace(content))
	for _, trigger := range []string{"remind me ", "reminder ", "remind "} {
		if strings.HasPrefix(lower, trigger) {
			return len(trigger), true
		}
	}
	return 0, false
}

// handleReminder parses and stores a reminder from a Discord message.
func handleReminder(s *discordgo.Session, m *discordgo.Message, triggerLen int) {
	after := strings.TrimSpace(m.Content[triggerLen:])
	fireAt, msg, err := reminder.ParseTime(after, time.Now().UTC())
	if err != nil {
		discord.LogSendErrorMessage(s, m, "Couldn't parse reminder time — try: remind me in 2h30m do the thing")
		return
	}

	var images []reminder.Image
	for _, att := range m.Attachments {
		if att.Width > 0 && att.Height > 0 {
			data, err := downloadBytes(att.URL)
			if err != nil {
				log.Printf("reminder: download attachment %s: %v", att.URL, err)
				continue
			}
			images = append(images, reminder.Image{
				Filename: att.Filename,
				Data:     base64.StdEncoding.EncodeToString(data),
			})
		}
	}

	if err := reminder.Add(m.Author.ID, m.ChannelID, m.GuildID, msg, images, fireAt); err != nil {
		discord.LogSendErrorMessage(s, m, fmt.Sprintf("Couldn't save reminder: %v", err))
		return
	}

	reply := fmt.Sprintf("⏰ <@%s> I'll remind you <t:%d:R>: %s", m.Author.ID, fireAt.Unix(), msg)
	if _, err := discord.SendMessage(s, m, reply); err != nil {
		log.Println(err)
	}
}

// downloadBytes fetches the bytes at url.
func downloadBytes(url string) ([]byte, error) {
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
```

**Step 4: Build**

Run: `/usr/local/go/bin/go build ./...`
Expected: no output

**Step 5: Smoke test manually**

Start the bot and send `@Volt remind me in 5s test`. The bot should reply with a confirmation. After 5 seconds it should post "⏰ @You Reminder: test" in the same channel.

**Step 6: Commit**

```bash
git add internal/handler/messages.go
git commit -m "feat: intercept reminder mentions in message handler before Gemini dispatch"
```

---

### Task 5: `/reminders` slash command

**Files:**
- Modify: `internal/config/commands.go`
- Modify: `internal/handler/commands.go`

**Step 1: Register the command**

In `config/commands.go`, append to the `Commands` slice:

```go
{
    Name:                     "reminders",
    Description:              "List your pending reminders",
    DefaultMemberPermissions: &writePermission,
    DMPermission:             &dmPermission,
},
```

**Step 2: Add the handler**

In `handler/commands.go`, add to the `Commands` map. Add these imports if not already present:
```go
"strconv"
"voltgpt/internal/reminder"
```

Handler:

```go
"reminders": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
    log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)

    reminders, err := reminder.GetUserReminders(i.Interaction.Member.User.ID)
    if err != nil || len(reminders) == 0 {
        s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
            Type: discordgo.InteractionResponseChannelMessageWithSource,
            Data: &discordgo.InteractionResponseData{
                Content: "You have no pending reminders!",
                Flags:   discordgo.MessageFlagsEphemeral,
            },
        })
        return
    }

    var sb strings.Builder
    sb.WriteString("**Your reminders:**\n")
    options := make([]discordgo.SelectMenuOption, 0, len(reminders))
    for idx, r := range reminders {
        imageNote := ""
        if len(r.Images) > 0 {
            imageNote = fmt.Sprintf(" [%d image(s)]", len(r.Images))
        }
        sb.WriteString(fmt.Sprintf("%d. <t:%d:R> — %s%s\n", idx+1, r.FireAt, r.Message, imageNote))

        label := fmt.Sprintf("%d. %s", idx+1, r.Message)
        if len(label) > 100 {
            label = label[:97] + "..."
        }
        options = append(options, discordgo.SelectMenuOption{
            Label: label,
            Value: strconv.FormatInt(r.ID, 10),
        })
    }

    err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
        Type: discordgo.InteractionResponseChannelMessageWithSource,
        Data: &discordgo.InteractionResponseData{
            Content: sb.String(),
            Flags:   discordgo.MessageFlagsEphemeral,
            Components: []discordgo.MessageComponent{
                discordgo.ActionsRow{
                    Components: []discordgo.MessageComponent{
                        discordgo.SelectMenu{
                            CustomID:    "reminder",
                            Placeholder: "Delete a reminder…",
                            Options:     options,
                        },
                    },
                },
            },
        },
    })
    if err != nil {
        log.Println(err)
    }
},
```

**Step 3: Build**

Run: `/usr/local/go/bin/go build ./...`
Expected: no output

**Step 4: Commit**

```bash
git add internal/config/commands.go internal/handler/commands.go
git commit -m "feat: add /reminders slash command with ephemeral list and delete select menu"
```

---

### Task 6: Delete component handler

**Files:**
- Modify: `internal/handler/components.go`

**Context:** The dispatcher in `main.go` splits the component CustomID on `-` and uses `split[0]` as the map key. The select menu has `CustomID: "reminder"`, so the handler key is `"reminder"`. The selected reminder ID comes from `i.MessageComponentData().Values[0]`.

**Step 1: Add imports to `components.go`**

Add to the import block:
```go
"strconv"
"voltgpt/internal/reminder"
```

**Step 2: Add the component handler**

In the `Components` map, add:

```go
"reminder": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
    log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

    values := i.MessageComponentData().Values
    if len(values) == 0 {
        discord.UpdateResponse(s, i, "No reminder selected.")
        return
    }

    id, err := strconv.ParseInt(values[0], 10, 64)
    if err != nil {
        discord.UpdateResponse(s, i, "Invalid reminder ID.")
        return
    }

    if reminder.Delete(id) {
        discord.UpdateResponse(s, i, "✅ Reminder deleted!")
    } else {
        discord.UpdateResponse(s, i, "Reminder not found (it may have already fired).")
    }
},
```

**Step 3: Build**

Run: `/usr/local/go/bin/go build ./...`
Expected: no output

**Step 4: Commit**

```bash
git add internal/handler/components.go
git commit -m "feat: add reminder delete component handler"
```

---

### Task 7: Wire `reminder.Init` into `main.go`

**Files:**
- Modify: `main.go`

**Context:** `reminder.Init` needs the Discord session, which is only available in `main()` after `discordgo.New(...)`. It cannot go in `init()`. Place the call after `dg.Open()`.

**Step 1: Add import**

Add to the import block in `main.go`:
```go
"voltgpt/internal/reminder"
```

**Step 2: Call `reminder.Init` after `dg.Open()`**

After:
```go
err = dg.Open()
if err != nil {
    log.Fatal("error opening connection,", err)
    return
}
```

Add:
```go
reminder.Init(db.DB, dg)
```

**Step 3: Add to the Ready handler log**

In the `dg.AddHandler(func(s *discordgo.Session, _ *discordgo.Ready)` block, add:
```go
log.Printf("Active reminders: %d", reminder.TotalActive())
```

**Step 4: Build**

Run: `/usr/local/go/bin/go build ./...`
Expected: no output

**Step 5: Commit**

```bash
git add main.go
git commit -m "feat: wire reminder.Init into main after session open"
```

---

### Task 8: Full test run and manual smoke test

**Step 1: Run all tests**

Run: `/usr/local/go/bin/go test ./... -timeout 60s`
Expected: all packages PASS, no failures

**Step 2: Run vet**

Run: `/usr/local/go/bin/go vet ./...`
Expected: no output

**Step 3: Manual smoke test checklist**

Start the bot (`./voltgpt`) and verify:

- [ ] `@Volt remind me in 10s test offset` → confirmation reply with relative timestamp; reminder fires ~10s later in the same channel
- [ ] `@Volt reminder at 15:04 test absolute` (pick a time ~1min in future) → confirmation; fires at that time
- [ ] `@Volt remind me in 5s` (no message text) → fires with empty message (or verify cleanRemaining returns "")
- [ ] `/reminders` → ephemeral list showing pending reminders with relative timestamps
- [ ] Select a reminder from the dropdown → "✅ Reminder deleted!"
- [ ] `/reminders` again → deleted reminder is gone
- [ ] Restart the bot mid-reminder → reminder still fires after restart
