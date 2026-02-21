package memory

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"strings"
	"testing"

	"google.golang.org/genai"
	"voltgpt/internal/db"
)

// setupTestDB opens a fresh in-memory SQLite database and wires it to the
// package-level database variable. Both are cleaned up after the test.
func setupTestDB(t *testing.T) {
	t.Helper()
	db.Open(":memory:")
	database = db.DB
	t.Cleanup(func() {
		db.Close()
		database = nil
	})
}

// setupGemini initialises the package-level Gemini client from
// MEMORY_GEMINI_TOKEN. The test is skipped when the token is absent.
func setupGemini(t *testing.T) {
	t.Helper()
	apiKey := os.Getenv("MEMORY_GEMINI_TOKEN")
	if apiKey == "" {
		t.Skip("MEMORY_GEMINI_TOKEN not set")
	}
	ctx := context.Background()
	var err error
	client, err = genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		t.Fatalf("setupGemini: failed to create client: %v", err)
	}
	t.Cleanup(func() { client = nil })
}

// ── Pure functions ────────────────────────────────────────────────────────────

func TestSafeDate(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"2024-01-15 10:30:00", "2024-01-15"},
		{"2024-01-15", "2024-01-15"},
		{"2024-01", "2024-01"}, // shorter than 10 chars — returned as-is
		{"short", "short"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := safeDate(tt.input); got != tt.want {
			t.Errorf("safeDate(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEffectiveName(t *testing.T) {
	tests := []struct {
		preferred   string
		displayName string
		username    string
		want        string
	}{
		{"Pref", "Display", "user", "Pref"},
		{"", "Display", "user", "Display"},
		{"", "", "user", "user"},
		{"Pref", "", "user", "Pref"},
	}
	for _, tt := range tests {
		got := effectiveName(tt.preferred, tt.displayName, tt.username)
		if got != tt.want {
			t.Errorf("effectiveName(%q, %q, %q) = %q, want %q",
				tt.preferred, tt.displayName, tt.username, got, tt.want)
		}
	}
}

func TestSerializeFloat32(t *testing.T) {
	if got := serializeFloat32(nil); len(got) != 0 {
		t.Errorf("serializeFloat32(nil) len = %d, want 0", len(got))
	}

	v := []float32{1.0, -1.0, 0.5}
	got := serializeFloat32(v)
	if len(got) != len(v)*4 {
		t.Fatalf("serializeFloat32 len = %d, want %d", len(got), len(v)*4)
	}

	// Round-trip: decode bytes back to float32 and compare.
	for i, want := range v {
		bits := binary.LittleEndian.Uint32(got[i*4:])
		if f := math.Float32frombits(bits); f != want {
			t.Errorf("round-trip [%d]: got %f, want %f", i, f, want)
		}
	}
}

func TestFormatFactsXML(t *testing.T) {
	t.Run("user facts only", func(t *testing.T) {
		userFacts := []UserFacts{
			{
				Username: "Alice",
				Facts: []RetrievedFact{
					{Text: "Alice likes hiking.", CreatedAt: "2024-01-15 10:00:00"},
				},
			},
		}
		got := formatFactsXML(userFacts, nil)
		if !strings.Contains(got, `<user name="Alice">`) {
			t.Errorf("missing user element: %s", got)
		}
		if !strings.Contains(got, "Alice likes hiking.") {
			t.Errorf("missing fact text: %s", got)
		}
		if !strings.Contains(got, "[2024-01-15]") {
			t.Errorf("missing truncated date: %s", got)
		}
		if strings.Contains(got, "</general>") {
			t.Errorf("unexpected <general> section: %s", got)
		}
	})

	t.Run("general facts only", func(t *testing.T) {
		generalFacts := []GeneralFact{
			{Username: "Bob", Text: "Bob owns a dog.", CreatedAt: "2024-02-01"},
		}
		got := formatFactsXML(nil, generalFacts)
		if !strings.Contains(got, "</general>") {
			t.Errorf("missing <general> section: %s", got)
		}
		if !strings.Contains(got, "Bob owns a dog.") {
			t.Errorf("missing general fact text: %s", got)
		}
		if strings.Contains(got, `<user name=`) {
			t.Errorf("unexpected <user> section: %s", got)
		}
	})

	t.Run("wraps in background_facts root", func(t *testing.T) {
		got := formatFactsXML(
			[]UserFacts{{Username: "A", Facts: []RetrievedFact{{Text: "fact", CreatedAt: "2024-01-01"}}}},
			[]GeneralFact{{Text: "other", CreatedAt: "2024-01-02"}},
		)
		if !strings.HasPrefix(got, "<background_facts>") {
			t.Errorf("should start with <background_facts>: %s", got)
		}
		if !strings.HasSuffix(got, "</background_facts>") {
			t.Errorf("should end with </background_facts>: %s", got)
		}
	})

	t.Run("short date string returned as-is", func(t *testing.T) {
		userFacts := []UserFacts{
			{Username: "Alice", Facts: []RetrievedFact{{Text: "fact", CreatedAt: "2024-01"}}},
		}
		got := formatFactsXML(userFacts, nil)
		if !strings.Contains(got, "[2024-01]") {
			t.Errorf("short date should appear as-is: %s", got)
		}
	})
}

// ── Database operations ───────────────────────────────────────────────────────

func TestUpsertUser(t *testing.T) {
	setupTestDB(t)

	id1, name1, err := upsertUser("discord1", "alice", "Alice Smith")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	if name1 != "Alice Smith" {
		t.Errorf("name = %q, want %q", name1, "Alice Smith")
	}

	// Second call with same discord ID must return the same row ID.
	id2, _, err := upsertUser("discord1", "alice_updated", "Alice Updated")
	if err != nil {
		t.Fatalf("upsertUser second call: %v", err)
	}
	if id1 != id2 {
		t.Errorf("id changed on second upsert: got %d, want %d", id2, id1)
	}
}

func TestUpsertUserPreferredNamePriority(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("discord2", "bob", "Bobby")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	database.Exec("UPDATE users SET preferred_name = 'B-Dog' WHERE id = ?", id)

	_, name, err := upsertUser("discord2", "bob", "Bobby")
	if err != nil {
		t.Fatalf("upsertUser after preferred set: %v", err)
	}
	if name != "B-Dog" {
		t.Errorf("name = %q, want %q", name, "B-Dog")
	}
}

func TestInsertFactAndGetUserFacts(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("discord3", "carol", "Carol")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	if err := insertFact(id, "msg1", "Carol enjoys painting.", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	facts := GetUserFacts("discord3")
	if len(facts) != 1 {
		t.Fatalf("GetUserFacts len = %d, want 1", len(facts))
	}
	if facts[0].FactText != "Carol enjoys painting." {
		t.Errorf("FactText = %q, want %q", facts[0].FactText, "Carol enjoys painting.")
	}
}

func TestReinforceFact(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("discord4", "dave", "Dave")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	if err := insertFact(id, "msg1", "Dave likes coffee.", make([]float32, embeddingDimensions)); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	facts := GetUserFacts("discord4")
	if facts[0].ReinforcementCount != 0 {
		t.Fatalf("initial reinforcement_count = %d, want 0", facts[0].ReinforcementCount)
	}
	if err := reinforceFact(facts[0].ID); err != nil {
		t.Fatalf("reinforceFact: %v", err)
	}

	facts = GetUserFacts("discord4")
	if facts[0].ReinforcementCount != 1 {
		t.Errorf("after reinforce, count = %d, want 1", facts[0].ReinforcementCount)
	}
}

func TestReplaceFact(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("discord5", "eve", "Eve")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	if err := insertFact(id, "msg1", "Eve lives in Paris.", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	facts := GetUserFacts("discord5")
	if err := replaceFact(facts[0].ID, id, "msg2", "Eve lives in Tokyo.", embedding); err != nil {
		t.Fatalf("replaceFact: %v", err)
	}

	facts = GetUserFacts("discord5")
	if len(facts) != 1 {
		t.Fatalf("after replace, len = %d, want 1", len(facts))
	}
	if facts[0].FactText != "Eve lives in Tokyo." {
		t.Errorf("FactText = %q, want %q", facts[0].FactText, "Eve lives in Tokyo.")
	}
}

func TestTotalFacts(t *testing.T) {
	setupTestDB(t)

	if n := TotalFacts(); n != 0 {
		t.Errorf("TotalFacts on empty DB = %d, want 0", n)
	}

	id, _, err := upsertUser("discord6", "frank", "Frank")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	insertFact(id, "m1", "fact 1", embedding)
	insertFact(id, "m2", "fact 2", embedding)

	if n := TotalFacts(); n != 2 {
		t.Errorf("TotalFacts = %d, want 2", n)
	}
}

func TestDeleteUserFacts(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("discord7", "grace", "Grace")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	insertFact(id, "m1", "Grace likes tea.", embedding)
	insertFact(id, "m2", "Grace plays piano.", embedding)

	n, err := DeleteUserFacts("discord7")
	if err != nil {
		t.Fatalf("DeleteUserFacts: %v", err)
	}
	if n != 2 {
		t.Errorf("deleted count = %d, want 2", n)
	}
	if facts := GetUserFacts("discord7"); len(facts) != 0 {
		t.Errorf("after delete, GetUserFacts len = %d, want 0", len(facts))
	}
}

func TestSetAndGetPreferredName(t *testing.T) {
	setupTestDB(t)

	// User doesn't exist yet — SetPreferredName should create them.
	if err := SetPreferredName("discord8", "henry", "Hank"); err != nil {
		t.Fatalf("SetPreferredName (new user): %v", err)
	}
	if got := GetPreferredName("discord8"); got != "Hank" {
		t.Errorf("GetPreferredName = %q, want %q", got, "Hank")
	}

	// Update existing user's preferred name.
	if err := SetPreferredName("discord8", "henry", "H-Dog"); err != nil {
		t.Fatalf("SetPreferredName (existing user): %v", err)
	}
	if got := GetPreferredName("discord8"); got != "H-Dog" {
		t.Errorf("GetPreferredName after update = %q, want %q", got, "H-Dog")
	}
}

func TestRefreshFactNames(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("discord9", "irene", "Irene")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	insertFact(id, "m1", "Irene writes Go code.", embedding)
	insertFact(id, "m2", "Irene owns two cats.", embedding)

	// Set a preferred name different from the current display/username.
	database.Exec("UPDATE users SET preferred_name = 'Irie' WHERE id = ?", id)

	n, err := RefreshFactNames("discord9")
	if err != nil {
		t.Fatalf("RefreshFactNames: %v", err)
	}
	if n != 2 {
		t.Errorf("RefreshFactNames count = %d, want 2", n)
	}
	for _, f := range GetUserFacts("discord9") {
		if !strings.HasPrefix(f.FactText, "Irie ") {
			t.Errorf("fact not renamed: %q", f.FactText)
		}
	}
}

// ── Gemini API ────────────────────────────────────────────────────────────────

func TestExtractFacts(t *testing.T) {
	setupGemini(t)

	tests := []struct {
		name     string
		username string
		messages string
		wantAny  bool // true: expect at least one fact; false: expect empty
	}{
		{
			name:     "clear possession statement",
			username: "Alex",
			messages: "I just got a Mass 2 monitor, it's so crispy",
			wantAny:  true,
		},
		{
			name:     "filler with no factual content",
			username: "Alex",
			messages: "lol yeah brb",
			wantAny:  false,
		},
		{
			name:     "statement about someone else",
			username: "Alex",
			messages: "he was using the onboard intel gpu instead of his dedicated gpu",
			wantAny:  false,
		},
		{
			name:     "biographical information",
			username: "Alex",
			messages: "I moved to Austin last year and started working at Dell",
			wantAny:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			facts, err := extractFacts(ctx, tt.username, tt.messages)
			if err != nil {
				t.Fatalf("extractFacts: %v", err)
			}
			if tt.wantAny && len(facts) == 0 {
				t.Error("expected at least one fact, got none")
			}
			if !tt.wantAny && len(facts) > 0 {
				t.Errorf("expected no facts, got %v", facts)
			}
		})
	}
}

func TestDecideAction(t *testing.T) {
	setupGemini(t)

	tests := []struct {
		name       string
		oldFact    string
		newFact    string
		wantAction string
	}{
		// REINFORCE vs KEEP boundary: same tool rephrased vs two unrelated facts.
		{
			name:       "reinforce: same tool rephrased",
			oldFact:    "Alice uses VSCode.",
			newFact:    "Alice codes in VSCode.",
			wantAction: "REINFORCE",
		},
		{
			name:       "keep: unrelated topics",
			oldFact:    "Alice uses VSCode.",
			newFact:    "Alice owns a dog.",
			wantAction: "KEEP",
		},
		// INVALIDATE vs MERGE boundary: city move vs complementary gaming consoles.
		{
			name:       "invalidate: city move",
			oldFact:    "Alice lives in Tokyo.",
			newFact:    "Alice moved to Berlin.",
			wantAction: "INVALIDATE",
		},
		{
			name:       "merge: complementary consoles",
			oldFact:    "Alice owns a PS5.",
			newFact:    "Alice bought an Xbox.",
			wantAction: "MERGE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			action, err := decideAction(ctx, tt.oldFact, tt.newFact)
			if err != nil {
				t.Fatalf("decideAction: %v", err)
			}
			if action.Action != tt.wantAction {
				t.Errorf("action = %q, want %q (old: %q, new: %q)",
					action.Action, tt.wantAction, tt.oldFact, tt.newFact)
			}
		})
	}
}
