package hasher

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/corona10/goimagehash"
	"voltgpt/internal/db"
)

// resetStore replaces hashStore.m with an empty map.
func resetStore(t *testing.T) {
	t.Helper()
	hashStore.Lock()
	hashStore.m = make(map[string]hashEntry)
	hashStore.Unlock()
}

// setupHasher resets the hash store and clears the database pointer.
// Use for tests that exercise in-memory store logic without DB writes.
func setupHasher(t *testing.T) {
	t.Helper()
	resetStore(t)
	database = nil
	t.Cleanup(func() { resetStore(t) })
}

// setupHasherWithDB opens an in-memory SQLite database and resets the store.
// Use for tests that exercise DB persistence (writeHash, loadFromDB, etc.).
func setupHasherWithDB(t *testing.T) {
	t.Helper()
	db.Open(":memory:")
	database = db.DB
	resetStore(t)
	t.Cleanup(func() {
		db.Close()
		database = nil
		resetStore(t)
	})
}

// makeTestImage returns a solid-colour RGBA image of the given dimensions.
func makeTestImage(w, h int, c color.RGBA) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	return img
}

// servePNG starts a test HTTP server that encodes img as PNG and serves it
// for any path request.
func servePNG(t *testing.T, img image.Image) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	data := buf.Bytes()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(data)
	}))
}

// ── Pure / store-only functions ───────────────────────────────────────────────

func TestUniqueHashResults(t *testing.T) {
	msg1 := &discordgo.Message{ID: "1"}
	msg2 := &discordgo.Message{ID: "2"}

	results := []hashResult{
		{distance: 5, message: msg1},
		{distance: 5, message: msg1}, // exact duplicate — same distance + same ID
		{distance: 3, message: msg1}, // same message, different distance → different key
		{distance: 5, message: msg2}, // different message → different key
	}

	got := uniqueHashResults(results)
	if len(got) != 3 {
		t.Errorf("uniqueHashResults len = %d, want 3", len(got))
	}
}

func TestUniqueHashResultsEmpty(t *testing.T) {
	if got := uniqueHashResults(nil); len(got) != 0 {
		t.Errorf("uniqueHashResults(nil) len = %d, want 0", len(got))
	}
}

func TestStringToHashRoundTrip(t *testing.T) {
	img := makeTestImage(64, 64, color.RGBA{R: 200, G: 100, B: 50, A: 255})
	hash, err := goimagehash.ExtAverageHash(img, 16, 16)
	if err != nil {
		t.Fatalf("ExtAverageHash: %v", err)
	}

	parsed := stringToHash(hash.ToString())
	dist, err := hash.Distance(parsed)
	if err != nil {
		t.Fatalf("Distance: %v", err)
	}
	if dist != 0 {
		t.Errorf("round-trip distance = %d, want 0", dist)
	}
}

func TestStringToHashInvalidInput(t *testing.T) {
	// Should log and return a zero-value hash rather than panicking.
	got := stringToHash("not-a-valid-hash-string")
	if got == nil {
		t.Error("stringToHash returned nil on invalid input, want non-nil zero hash")
	}
}

func TestTotalHashes(t *testing.T) {
	setupHasher(t)

	if n := TotalHashes(); n != 0 {
		t.Errorf("TotalHashes on empty store = %d, want 0", n)
	}

	hashStore.Lock()
	hashStore.m["h1"] = hashEntry{messageID: "1"}
	hashStore.m["h2"] = hashEntry{messageID: "2"}
	hashStore.Unlock()

	if n := TotalHashes(); n != 2 {
		t.Errorf("TotalHashes = %d, want 2", n)
	}
}

func TestCheckHash(t *testing.T) {
	setupHasher(t)

	if checkHash("missing") {
		t.Error("checkHash on empty store = true, want false")
	}

	hashStore.Lock()
	hashStore.m["exists"] = hashEntry{messageID: "msg"}
	hashStore.Unlock()

	if !checkHash("exists") {
		t.Error("checkHash for inserted key = false, want true")
	}
}

func TestOlderHash(t *testing.T) {
	setupHasher(t)

	// Hash not in store → always considered older (should be stored).
	if !olderHash("nonexistent", &discordgo.Message{ID: "x", Timestamp: time.Now()}) {
		t.Error("olderHash for missing key = false, want true")
	}

	base := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	hashStore.Lock()
	hashStore.m["key"] = hashEntry{messageID: "stored", timestamp: base}
	hashStore.Unlock()

	// New message predates the stored one → should replace it.
	if !olderHash("key", &discordgo.Message{ID: "older", Timestamp: base.Add(-time.Hour)}) {
		t.Error("olderHash with earlier timestamp = false, want true")
	}

	// New message is more recent → keep the stored one.
	if olderHash("key", &discordgo.Message{ID: "newer", Timestamp: base.Add(time.Hour)}) {
		t.Error("olderHash with later timestamp = true, want false")
	}
}

// ── DB persistence ────────────────────────────────────────────────────────────

func TestWriteAndReadHash(t *testing.T) {
	setupHasherWithDB(t)

	msg := &discordgo.Message{ID: "msg1", ChannelID: "ch1"}
	writeHash("testhash", msg)

	got, err := readHashFromDB("testhash")
	if err != nil {
		t.Fatalf("readHashFromDB: %v", err)
	}
	if got.ID != "msg1" {
		t.Errorf("message ID = %q, want %q", got.ID, "msg1")
	}
}

func TestLoadFromDB(t *testing.T) {
	setupHasherWithDB(t)

	// Pre-populate the DB row directly, bypassing the in-memory store.
	msg := &discordgo.Message{ID: "preloaded", ChannelID: "ch"}
	msgJSON, _ := json.Marshal(msg)
	db.DB.Exec("INSERT INTO image_hashes (hash, message_json) VALUES (?, ?)", "dbhash", string(msgJSON))

	// Reset store then reload from DB to simulate startup.
	resetStore(t)
	loadFromDB()

	if n := TotalHashes(); n != 1 {
		t.Errorf("TotalHashes after loadFromDB = %d, want 1", n)
	}
	got, err := readHashFromDB("dbhash")
	if err != nil || got.ID != "preloaded" {
		t.Errorf("loaded message ID = %v (err=%v), want %q", got, err, "preloaded")
	}
}

// ── Network / hashing ─────────────────────────────────────────────────────────

func TestGetFileSuccess(t *testing.T) {
	data := []byte("fake image bytes")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(data)
	}))
	defer srv.Close()

	buf, err := getFile(srv.URL + "/file.png")
	if err != nil {
		t.Fatalf("getFile: %v", err)
	}
	if !bytes.Equal(buf.Bytes(), data) {
		t.Errorf("getFile body = %q, want %q", buf.Bytes(), data)
	}
}

func TestGetFileNonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	_, err := getFile(srv.URL + "/missing.png")
	if err == nil {
		t.Error("expected error for 404 response, got nil")
	}
}

func TestHashAttachments(t *testing.T) {
	setupHasher(t)

	img := makeTestImage(64, 64, color.RGBA{R: 128, G: 128, B: 128, A: 255})
	srv := servePNG(t, img)
	defer srv.Close()

	msg := &discordgo.Message{
		ID: "testmsg",
		Attachments: []*discordgo.MessageAttachment{
			{URL: srv.URL + "/test.png", Width: 64, Height: 64},
		},
	}

	hashes, count := HashAttachments(msg, HashOptions{Store: false, Threshold: 10})
	if len(hashes) != 1 {
		t.Errorf("HashAttachments len = %d, want 1", len(hashes))
	}
	if count != 0 {
		t.Errorf("count = %d, want 0 (Store: false)", count)
	}
}

func TestHashAttachmentsIgnoreExtension(t *testing.T) {
	setupHasher(t)

	img := makeTestImage(64, 64, color.RGBA{R: 100, G: 100, B: 100, A: 255})
	srv := servePNG(t, img)
	defer srv.Close()

	msg := &discordgo.Message{
		ID: "testmsg",
		Attachments: []*discordgo.MessageAttachment{
			{URL: srv.URL + "/test.png", Width: 64, Height: 64},
		},
	}

	hashes, _ := HashAttachments(msg, HashOptions{
		Store:            false,
		IgnoreExtensions: []string{".png"},
	})
	if len(hashes) != 0 {
		t.Errorf("expected 0 hashes with .png ignored, got %d", len(hashes))
	}
}

func TestHashAttachmentsWithStore(t *testing.T) {
	setupHasherWithDB(t)

	img := makeTestImage(64, 64, color.RGBA{R: 200, G: 150, B: 100, A: 255})
	srv := servePNG(t, img)
	defer srv.Close()

	msg := &discordgo.Message{
		ID: "storemsg",
		Attachments: []*discordgo.MessageAttachment{
			{URL: srv.URL + "/test.png", Width: 64, Height: 64},
		},
	}

	hashes, count := HashAttachments(msg, HashOptions{Store: true, Threshold: 10})
	if len(hashes) != 1 {
		t.Errorf("HashAttachments len = %d, want 1", len(hashes))
	}
	if count != 1 {
		t.Errorf("count = %d, want 1 (Store: true, new hash)", count)
	}
	if TotalHashes() != 1 {
		t.Errorf("TotalHashes after store = %d, want 1", TotalHashes())
	}
}
