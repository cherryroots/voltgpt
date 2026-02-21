# Improve Test Coverage Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Increase test coverage in `gamble`, `utility/discord`, and `utility/url` packages by adding tests for uncovered pure-logic paths and Discord struct-based functions.

**Architecture:** Tests use table-driven patterns where sensible. `discordgo-mock` (ewohltman) is added as a test dependency for constructing mock sessions; all `*discordgo.Message` tests use direct struct construction. No HTTP calls are made in any test.

**Tech Stack:** Go stdlib `testing`, `github.com/ewohltman/discordgo-mock` (test only), `github.com/bwmarrin/discordgo`

---

### Task 1: Add discordgo-mock dependency

**Files:**
- Modify: `go.mod`, `go.sum` (via `go get`)

**Step 1: Add the dependency**

```bash
/usr/local/go/bin/go get github.com/ewohltman/discordgo-mock@latest
```

**Step 2: Verify it resolves**

```bash
/usr/local/go/bin/go mod tidy
/usr/local/go/bin/go build ./...
```

Expected: no errors.

**Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add ewohltman/discordgo-mock as test dependency"
```

---

### Task 2: gamble — playerTax, HasBet false case, wheelOptions fallback

**Files:**
- Modify: `internal/gamble/gamble_test.go`

Existing helpers `setupGame()` and `makePlayer()` are already in the file — reuse them.

**Step 1: Write the failing tests**

Append to `internal/gamble/gamble_test.go`:

```go
// TestPlayerTax verifies tax = playerMoney * 3 * (10 - betPct) / 100.
func TestPlayerTax(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddPlayer(alice)
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)

	GameState.AddRound()
	GameState.Rounds[0].AddClaim(alice) // alice has 100
	// Alice bets 0 → betPct = 0, tax = 100 * 3 * 10 / 100 = 30
	tax := GameState.playerTax(alice, GameState.Rounds[0])
	if tax != 30 {
		t.Errorf("playerTax() = %d, want 30", tax)
	}
}

// TestPlayerTaxAboveThreshold verifies zero tax when bet% >= 10.
func TestPlayerTaxAboveThreshold(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddPlayer(alice)
	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)

	GameState.AddRound()
	GameState.Rounds[0].AddClaim(alice) // alice has 100
	// Alice bets 10 on bob → betPct = 10, (10-10)=0, tax = 0
	GameState.Rounds[0].Bets = []Bet{{Amount: 10, By: alice, On: bob}}
	tax := GameState.playerTax(alice, GameState.Rounds[0])
	if tax != 0 {
		t.Errorf("playerTax() = %d, want 0 when bet%% >= 10", tax)
	}
}

// TestHasBetNotFound verifies false is returned when no matching bet exists.
func TestHasBetNotFound(t *testing.T) {
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")
	charlie := makePlayer("3", "Charlie")

	r := &round{ID: 0}
	r.Bets = []Bet{{Amount: 50, By: alice, On: charlie}}

	_, ok := r.HasBet(Bet{By: alice, On: bob}) // different target
	if ok {
		t.Error("HasBet() = true, want false for non-matching target")
	}

	_, ok2 := r.HasBet(Bet{By: bob, On: charlie}) // different bettor
	if ok2 {
		t.Error("HasBet() = true, want false for non-matching bettor")
	}
}

// TestWheelOptionsFallback verifies fallback to CurrentWheelOptions when round.ID >= len(Rounds).
func TestWheelOptionsFallback(t *testing.T) {
	setupGame()
	alice := makePlayer("1", "Alice")
	bob := makePlayer("2", "Bob")

	GameState.AddWheelOption(alice)
	GameState.AddWheelOption(bob)
	GameState.AddRound() // only 1 round (index 0)

	// Pass a round with ID == len(Rounds) to trigger fallback
	futureRound := round{ID: 1}
	opts := GameState.wheelOptions(futureRound)
	current := GameState.CurrentWheelOptions()

	if len(opts) != len(current) {
		t.Errorf("wheelOptions fallback len = %d, want %d (CurrentWheelOptions)", len(opts), len(current))
	}
}
```

**Step 2: Run to verify they fail**

```bash
/usr/local/go/bin/go test ./internal/gamble/ -run "TestPlayerTax|TestHasBetNotFound|TestWheelOptionsFallback" -v
```

Expected: FAIL (functions exist but tests are new — they should actually pass immediately since these are pure logic tests; if they pass, that's the goal).

**Step 3: Run all gamble tests to confirm no regressions**

```bash
/usr/local/go/bin/go test ./internal/gamble/ -v -cover
```

Expected: all PASS, coverage increases above 57.4%.

**Step 4: Commit**

```bash
git add internal/gamble/gamble_test.go
git commit -m "test: add playerTax, HasBet false case, and wheelOptions fallback coverage"
```

---

### Task 3: utility/url — invalid URL error paths

**Files:**
- Modify: `internal/utility/url_test.go`

**Step 1: Read the existing test file to find the right place to append**

Read `internal/utility/url_test.go` and note the last test function name.

**Step 2: Append the new tests**

```go
func TestUrlToExtInvalidURL(t *testing.T) {
	_, err := UrlToExt("://bad url")
	if err == nil {
		t.Error("UrlToExt() expected error for invalid URL, got nil")
	}
}

func TestIsImageURLInvalidURL(t *testing.T) {
	if IsImageURL("://bad url") {
		t.Error("IsImageURL() = true for invalid URL, want false")
	}
}

func TestIsVideoURLInvalidURL(t *testing.T) {
	if IsVideoURL("://bad url") {
		t.Error("IsVideoURL() = true for invalid URL, want false")
	}
}

func TestIsPDFURLInvalidURL(t *testing.T) {
	if IsPDFURL("://bad url") {
		t.Error("IsPDFURL() = true for invalid URL, want false")
	}
}

func TestMediaTypeUnknown(t *testing.T) {
	got := MediaType("https://example.com/file.xyz")
	if got != "" {
		t.Errorf("MediaType() = %q for unknown extension, want \"\"", got)
	}
}
```

**Step 3: Run to verify they pass**

```bash
/usr/local/go/bin/go test ./internal/utility/ -run "TestUrlToExtInvalid|TestIsImageURLInvalid|TestIsVideoURLInvalid|TestIsPDFURLInvalid|TestMediaTypeUnknown" -v
```

Expected: all PASS.

**Step 4: Run full utility suite**

```bash
/usr/local/go/bin/go test ./internal/utility/ -cover
```

**Step 5: Commit**

```bash
git add internal/utility/url_test.go
git commit -m "test: add invalid URL error path coverage for url utility functions"
```

---

### Task 4: utility/discord — MessageToEmbeds, AttachmentText empty, HasImageURL content branch

**Files:**
- Modify: `internal/utility/discord_test.go`

No session needed for these — only `*discordgo.Message` struct construction.

**Step 1: Read discord_test.go to understand existing imports and structure**

Read `internal/utility/discord_test.go` fully before editing.

**Step 2: Append tests**

```go
func TestMessageToEmbeds(t *testing.T) {
	m := &discordgo.Message{
		ID:        "msg1",
		ChannelID: "chan1",
		Content:   "hello",
		Author: &discordgo.User{
			ID:       "user1",
			Username: "Alice",
		},
	}
	embeds := MessageToEmbeds("guild1", m, 7)

	if len(embeds) == 0 {
		t.Fatal("MessageToEmbeds() returned no embeds")
	}
	first := embeds[0]
	if first.Title != "Message link" {
		t.Errorf("embed Title = %q, want \"Message link\"", first.Title)
	}
	if first.Description != "hello" {
		t.Errorf("embed Description = %q, want \"hello\"", first.Description)
	}
	if first.Footer == nil || !strings.Contains(first.Footer.Text, "7bit distance") {
		t.Errorf("embed Footer = %v, want text containing \"7bit distance\"", first.Footer)
	}
	wantURL := "https://discord.com/channels/guild1/chan1/msg1"
	if first.URL != wantURL {
		t.Errorf("embed URL = %q, want %q", first.URL, wantURL)
	}
}

func TestMessageToEmbedsIncludesMessageEmbeds(t *testing.T) {
	inner := &discordgo.MessageEmbed{Title: "inner"}
	m := &discordgo.Message{
		ID:        "msg2",
		ChannelID: "chan1",
		Author:    &discordgo.User{ID: "u1", Username: "Bob"},
		Embeds:    []*discordgo.MessageEmbed{inner},
	}
	embeds := MessageToEmbeds("guild1", m, 0)
	// First embed is the link embed; second should be the inner embed
	if len(embeds) != 2 {
		t.Fatalf("MessageToEmbeds() len = %d, want 2", len(embeds))
	}
	if embeds[1].Title != "inner" {
		t.Errorf("embeds[1].Title = %q, want \"inner\"", embeds[1].Title)
	}
}

func TestAttachmentTextEmpty(t *testing.T) {
	m := &discordgo.Message{}
	got := AttachmentText(m)
	if got != "" {
		t.Errorf("AttachmentText() = %q for no attachments, want \"\"", got)
	}
}

func TestHasImageURLFromContent(t *testing.T) {
	m := &discordgo.Message{
		Content: "check this out https://example.com/photo.png cool right",
	}
	if !HasImageURL(m) {
		t.Error("HasImageURL() = false for message with image URL in content, want true")
	}
}

func TestHasVideoURLFromContent(t *testing.T) {
	m := &discordgo.Message{
		Content: "https://example.com/clip.mp4",
	}
	if !HasVideoURL(m) {
		t.Error("HasVideoURL() = false for message with video URL in content, want true")
	}
}
```

**Step 3: Ensure `strings` is imported in discord_test.go** (add to import block if not present).

**Step 4: Run**

```bash
/usr/local/go/bin/go test ./internal/utility/ -run "TestMessageToEmbeds|TestAttachmentTextEmpty|TestHasImageURLFromContent|TestHasVideoURLFromContent" -v
```

Expected: all PASS.

**Step 5: Commit**

```bash
git add internal/utility/discord_test.go
git commit -m "test: add MessageToEmbeds, AttachmentText empty, and content URL coverage"
```

---

### Task 5: utility/discord — CleanMessage via discordgo-mock

**Files:**
- Modify: `internal/utility/discord_test.go`

**Step 1: Add mock imports**

Ensure `discord_test.go` imports:
```go
"github.com/ewohltman/discordgo-mock/pkg/mocksession"
"github.com/ewohltman/discordgo-mock/pkg/mockstate"
"github.com/ewohltman/discordgo-mock/pkg/mockuser"
```

**Step 2: Append the test**

```go
func TestCleanMessage(t *testing.T) {
	botUser := mockuser.New(
		mockuser.WithID("bot123"),
		mockuser.WithUsername("VoltBot"),
		mockuser.WithBotFlag(true),
	)
	state, err := mockstate.New(
		mockstate.WithUser(botUser),
	)
	if err != nil {
		t.Fatalf("mockstate.New() error: %v", err)
	}
	s, err := mocksession.New(
		mocksession.WithState(state),
	)
	if err != nil {
		t.Fatalf("mocksession.New() error: %v", err)
	}

	m := &discordgo.Message{
		Content: "  <@bot123> hello world  ",
	}
	result := CleanMessage(s, m)
	if result.Content != "hello world" {
		t.Errorf("CleanMessage() content = %q, want \"hello world\"", result.Content)
	}
}

func TestCleanMessageNoMention(t *testing.T) {
	botUser := mockuser.New(mockuser.WithID("bot123"))
	state, err := mockstate.New(mockstate.WithUser(botUser))
	if err != nil {
		t.Fatalf("mockstate.New() error: %v", err)
	}
	s, err := mocksession.New(mocksession.WithState(state))
	if err != nil {
		t.Fatalf("mocksession.New() error: %v", err)
	}

	m := &discordgo.Message{Content: "hello world"}
	result := CleanMessage(s, m)
	if result.Content != "hello world" {
		t.Errorf("CleanMessage() content = %q, want \"hello world\"", result.Content)
	}
}
```

**Step 3: Run**

```bash
/usr/local/go/bin/go test ./internal/utility/ -run "TestCleanMessage" -v
```

Expected: both PASS.

**Step 4: Run full suite with coverage**

```bash
/usr/local/go/bin/go test ./internal/gamble/ ./internal/utility/ -cover
```

Expected: gamble > 65%, utility > 45%.

**Step 5: Commit**

```bash
git add internal/utility/discord_test.go
git commit -m "test: add CleanMessage coverage using discordgo-mock session"
```

---

### Task 6: utility/discord — GetMessageMediaURL embed paths

**Files:**
- Modify: `internal/utility/discord_test.go`

**Step 1: Append embed coverage tests**

```go
func TestGetMessageMediaURLEmbedThumbnail(t *testing.T) {
	m := &discordgo.Message{
		Embeds: []*discordgo.MessageEmbed{
			{
				Thumbnail: &discordgo.MessageEmbedThumbnail{
					URL: "https://example.com/thumb.png",
				},
			},
		},
	}
	images, _, _, _ := GetMessageMediaURL(m)
	if len(images) == 0 {
		t.Error("GetMessageMediaURL() found no images from embed thumbnail, want 1")
	}
	if images[0] != "https://example.com/thumb.png" {
		t.Errorf("GetMessageMediaURL() image = %q, want embed thumbnail URL", images[0])
	}
}

func TestGetMessageMediaURLEmbedImage(t *testing.T) {
	m := &discordgo.Message{
		Embeds: []*discordgo.MessageEmbed{
			{
				Image: &discordgo.MessageEmbedImage{
					URL: "https://example.com/image.jpg",
				},
			},
		},
	}
	images, _, _, _ := GetMessageMediaURL(m)
	if len(images) == 0 {
		t.Error("GetMessageMediaURL() found no images from embed image field, want 1")
	}
}

func TestGetMessageMediaURLContentURLs(t *testing.T) {
	m := &discordgo.Message{
		Content: "look: https://example.com/pic.webp and https://example.com/video.mp4",
	}
	images, videos, _, _ := GetMessageMediaURL(m)
	if len(images) == 0 {
		t.Error("GetMessageMediaURL() found no images from content URL, want 1")
	}
	if len(videos) == 0 {
		t.Error("GetMessageMediaURL() found no videos from content URL, want 1")
	}
}

func TestGetMessageMediaURLDeduplication(t *testing.T) {
	// Same image URL appearing in two embed fields should be deduplicated
	m := &discordgo.Message{
		Embeds: []*discordgo.MessageEmbed{
			{
				Thumbnail: &discordgo.MessageEmbedThumbnail{
					URL:      "https://example.com/same.png",
					ProxyURL: "https://example.com/same.png",
				},
			},
		},
	}
	images, _, _, _ := GetMessageMediaURL(m)
	if len(images) != 1 {
		t.Errorf("GetMessageMediaURL() len = %d, want 1 (deduplicated)", len(images))
	}
}
```

**Step 2: Run**

```bash
/usr/local/go/bin/go test ./internal/utility/ -run "TestGetMessageMediaURL" -v
```

Expected: all PASS.

**Step 3: Final coverage check across all tested packages**

```bash
/usr/local/go/bin/go test ./internal/db/ ./internal/gamble/ ./internal/utility/ -cover
```

**Step 4: Commit**

```bash
git add internal/utility/discord_test.go
git commit -m "test: add GetMessageMediaURL embed and deduplication path coverage"
```
