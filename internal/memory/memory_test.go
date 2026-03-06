package memory

import (
	"context"
	"encoding/binary"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"voltgpt/internal/db"

	"github.com/joho/godotenv"
	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
)

var (
	testOpenAIOnce   sync.Once
	testOpenAIClient *oa.Client
	testOpenAIErr    string
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

// setupOpenAIClient initialises a shared OpenAI client from MEMORY_OPENAI_TOKEN.
func setupOpenAIClient(t *testing.T) {
	t.Helper()
	testOpenAIOnce.Do(func() {
		godotenv.Load("../../.env") // no-op if already set or file absent
		token := strings.TrimSpace(os.Getenv("MEMORY_OPENAI_TOKEN"))
		if token == "" {
			testOpenAIErr = "MEMORY_OPENAI_TOKEN is not set"
			return
		}
		c := oa.NewClient(option.WithAPIKey(token))
		testOpenAIClient = &c
		client = testOpenAIClient
	})
	if testOpenAIErr != "" {
		t.Skip(testOpenAIErr)
	}
}

func setupOpenAIIntegration(t *testing.T) {
	t.Helper()
	setupOpenAIClient(t)
	enableMemory(t)
}

func enableMemory(t *testing.T) {
	t.Helper()
	previous := enabled
	enabled = true
	t.Cleanup(func() { enabled = previous })
}

func setEmbedFunc(t *testing.T, fn func(context.Context, string) ([]float32, error)) {
	t.Helper()
	previous := embedFunc
	embedFunc = fn
	t.Cleanup(func() { embedFunc = previous })
}

func setDecideActionFunc(t *testing.T, fn func(context.Context, string, string) (*consolidationAction, error)) {
	t.Helper()
	previous := decideActionFunc
	decideActionFunc = fn
	t.Cleanup(func() { decideActionFunc = previous })
}

func testVector(dim int, value float32) []float32 {
	v := make([]float32, embeddingDimensions)
	v[dim] = value
	return v
}

func runExtractFactsTest(t *testing.T, username, messages string, wantAny bool) {
	t.Helper()
	setupOpenAIClient(t)

	facts, err := extractFacts(context.Background(), username, messages)
	if err != nil {
		t.Fatalf("extractFacts: %v", err)
	}
	if wantAny && len(facts) == 0 {
		t.Fatal("expected at least one fact, got none")
	}
	if !wantAny && len(facts) > 0 {
		t.Fatalf("expected no facts, got %v", facts)
	}
}

func runDecideActionTest(t *testing.T, oldFact, newFact, wantAction string) {
	t.Helper()
	setupOpenAIClient(t)

	action, err := decideAction(context.Background(), oldFact, newFact)
	if err != nil {
		t.Fatalf("decideAction: %v", err)
	}
	if action.Action != wantAction {
		t.Fatalf("action = %q, want %q (old: %q, new: %q)",
			action.Action, wantAction, oldFact, newFact)
	}
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

// ── OpenAI API ────────────────────────────────────────────────────────────────

func TestExtractFacts_ClearPossessionStatement(t *testing.T) {
	t.Parallel()
	runExtractFactsTest(t, "Alex", "I just got a Mass 2 monitor, it's so crispy", true)
}

func TestExtractFacts_FillerWithNoFactualContent(t *testing.T) {
	t.Parallel()
	runExtractFactsTest(t, "Alex", "lol yeah brb", false)
}

func TestExtractFacts_StatementAboutSomeoneElse(t *testing.T) {
	t.Parallel()
	runExtractFactsTest(t, "Alex", "he was using the onboard intel gpu instead of his dedicated gpu", false)
}

func TestExtractFacts_BiographicalInformation(t *testing.T) {
	t.Parallel()
	runExtractFactsTest(t, "Alex", "I moved to Austin last year and started working at Dell", true)
}

func TestDecideAction_ReinforceSameToolRephrased(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice uses VSCode.", "Alice codes in VSCode.", "REINFORCE")
}

func TestDecideAction_KeepUnrelatedTopics(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice uses VSCode.", "Alice owns a dog.", "KEEP")
}

func TestDecideAction_InvalidateCityMove(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice lives in Tokyo.", "Alice moved to Berlin.", "INVALIDATE")
}

func TestDecideAction_MergeComplementaryConsoles(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice owns a PS5.", "Alice bought an Xbox.", "MERGE")
}

func TestDecideAction_KeepTemporaryVisit(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice lives in Tokyo.", "Alice is visiting Paris this week.", "KEEP")
}

func TestDecideAction_MergeSameDomainSkills(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice plays guitar.", "Alice plays piano.", "MERGE")
}

func TestDecideAction_ReinforceSameRoleRephrased(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice is a software engineer.", "Alice works as a developer.", "REINFORCE")
}

func TestDecideAction_KeepUnrelatedDomains(t *testing.T) {
	t.Parallel()
	runDecideActionTest(t, "Alice owns a cat.", "Alice works at Dell.", "KEEP")
}

func TestDeleteAllFacts(t *testing.T) {
	setupTestDB(t)

	id1, _, err := upsertUser("da1", "u1", "U1")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	id2, _, err := upsertUser("da2", "u2", "U2")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	if err := insertFact(id1, "m1", "fact one", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}
	if err := insertFact(id2, "m2", "fact two", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	if n := TotalFacts(); n != 2 {
		t.Fatalf("before delete: TotalFacts = %d, want 2", n)
	}

	n, err := DeleteAllFacts()
	if err != nil {
		t.Fatalf("DeleteAllFacts: %v", err)
	}
	if n != 2 {
		t.Errorf("rows affected = %d, want 2", n)
	}
	if n := TotalFacts(); n != 0 {
		t.Errorf("after delete: TotalFacts = %d, want 0", n)
	}
}

func TestRefreshAllFactNames(t *testing.T) {
	setupTestDB(t)

	id1, _, err := upsertUser("rb1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	id2, _, err := upsertUser("rb2", "bob", "Bob")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	if err := insertFact(id1, "m1", "Alice likes tea.", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}
	if err := insertFact(id2, "m2", "Bob plays chess.", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	database.Exec("UPDATE users SET preferred_name = 'Ali' WHERE id = ?", id1)
	database.Exec("UPDATE users SET preferred_name = 'Robert' WHERE id = ?", id2)

	n, err := RefreshAllFactNames()
	if err != nil {
		t.Fatalf("RefreshAllFactNames: %v", err)
	}
	if n != 2 {
		t.Errorf("updated count = %d, want 2", n)
	}

	facts1 := GetUserFacts("rb1")
	if len(facts1) != 1 || !strings.HasPrefix(facts1[0].FactText, "Ali ") {
		t.Errorf("user 1 fact not renamed: %q", facts1[0].FactText)
	}
	facts2 := GetUserFacts("rb2")
	if len(facts2) != 1 || !strings.HasPrefix(facts2[0].FactText, "Robert ") {
		t.Errorf("user 2 fact not renamed: %q", facts2[0].FactText)
	}
}

func TestGetRecentFacts(t *testing.T) {
	setupTestDB(t)

	if got := GetRecentFacts(5); len(got) != 0 {
		t.Errorf("empty DB: GetRecentFacts len = %d, want 0", len(got))
	}

	id, _, err := upsertUser("gr1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	embedding := make([]float32, embeddingDimensions)
	if err := insertFact(id, "m1", "Alice likes coffee.", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}
	if err := insertFact(id, "m2", "Alice plays piano.", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}
	if err := insertFact(id, "m3", "Alice lives in Paris.", embedding); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	got := GetRecentFacts(2)
	if len(got) != 2 {
		t.Errorf("GetRecentFacts(2) len = %d, want 2", len(got))
	}
	for _, f := range got {
		if f.Username == "" || f.FactText == "" {
			t.Errorf("incomplete recent fact: %+v", f)
		}
	}
}

func TestFindSimilarFacts(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("fs1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	// Unit vector in dimension 0.
	stored := make([]float32, embeddingDimensions)
	stored[0] = 1.0
	if err := insertFact(id, "m1", "Alice likes hiking.", stored); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	// Identical vector → distance ~0, should be returned.
	same := make([]float32, embeddingDimensions)
	same[0] = 1.0
	similar, err := findSimilarFacts(id, same)
	if err != nil {
		t.Fatalf("findSimilarFacts (identical): %v", err)
	}
	if len(similar) != 1 {
		t.Fatalf("identical vector: len = %d, want 1", len(similar))
	}
	if similar[0].FactText != "Alice likes hiking." {
		t.Errorf("FactText = %q, want %q", similar[0].FactText, "Alice likes hiking.")
	}
	if similar[0].Distance > 0.01 {
		t.Errorf("distance = %f, want ~0", similar[0].Distance)
	}

	// Orthogonal vector (dim 1) → cosine distance = 1.0, above threshold, filtered out.
	ortho := make([]float32, embeddingDimensions)
	ortho[1] = 1.0
	dissimilar, err := findSimilarFacts(id, ortho)
	if err != nil {
		t.Fatalf("findSimilarFacts (orthogonal): %v", err)
	}
	if len(dissimilar) != 0 {
		t.Errorf("orthogonal vector: expected 0 results, got %d", len(dissimilar))
	}
}

func TestConsolidateAndStore_NoSimilar(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	id, _, err := upsertUser("cs1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	ctx := context.Background()

	if err := consolidateAndStore(ctx, id, "m1", "Alice likes hiking."); err != nil {
		t.Fatalf("consolidateAndStore: %v", err)
	}

	facts := GetUserFacts("cs1")
	if len(facts) != 1 {
		t.Fatalf("len = %d, want 1", len(facts))
	}
	if facts[0].FactText != "Alice likes hiking." {
		t.Errorf("FactText = %q, want %q", facts[0].FactText, "Alice likes hiking.")
	}
}

func TestConsolidateAndStore_Reinforce(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	id, _, err := upsertUser("cs2", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	ctx := context.Background()

	if err := consolidateAndStore(ctx, id, "m1", "Alice uses VSCode."); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := consolidateAndStore(ctx, id, "m2", "Alice codes in VSCode."); err != nil {
		t.Fatalf("reinforce store: %v", err)
	}

	facts := GetUserFacts("cs2")
	if len(facts) != 1 {
		t.Fatalf("after reinforce: len = %d, want 1", len(facts))
	}
	if facts[0].ReinforcementCount != 1 {
		t.Errorf("reinforcement_count = %d, want 1", facts[0].ReinforcementCount)
	}
}

func TestConsolidateAndStore_Invalidate(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	id, _, err := upsertUser("cs3", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	ctx := context.Background()

	if err := consolidateAndStore(ctx, id, "m1", "Alice lives in Tokyo."); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := consolidateAndStore(ctx, id, "m2", "Alice moved to Berlin."); err != nil {
		t.Fatalf("invalidate store: %v", err)
	}

	facts := GetUserFacts("cs3")
	if len(facts) != 1 {
		t.Fatalf("after invalidate: len = %d, want 1", len(facts))
	}
	if !strings.Contains(facts[0].FactText, "Berlin") {
		t.Errorf("expected new fact mentioning Berlin, got %q", facts[0].FactText)
	}
}

func TestConsolidateAndStore_Merge(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	id, _, err := upsertUser("cs4", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	ctx := context.Background()

	if err := consolidateAndStore(ctx, id, "m1", "Alice owns a PS5."); err != nil {
		t.Fatalf("first store: %v", err)
	}
	if err := consolidateAndStore(ctx, id, "m2", "Alice bought an Xbox."); err != nil {
		t.Fatalf("merge store: %v", err)
	}

	facts := GetUserFacts("cs4")
	if len(facts) != 1 {
		t.Fatalf("after merge: len = %d, want 1", len(facts))
	}
	merged := facts[0].FactText
	if !strings.Contains(merged, "PS5") || !strings.Contains(merged, "Xbox") {
		t.Errorf("merged fact should mention both consoles: %q", merged)
	}
}

func TestRetrieve_Disabled(t *testing.T) {
	// No setupOpenAI — enabled stays false.
	if got := Retrieve("anything", "discord1"); got != nil {
		t.Errorf("Retrieve when disabled = %v, want nil", got)
	}
}

func TestRetrieveMultiUser_Disabled(t *testing.T) {
	result := RetrieveMultiUser("anything", map[string]string{"d1": "user"})
	if result != "" {
		t.Errorf("RetrieveMultiUser when disabled = %q, want empty", result)
	}
}

func TestRetrieve(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	id, _, err := upsertUser("rv1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	ctx := context.Background()
	if err := consolidateAndStore(ctx, id, "m1", "Alice loves hiking in the mountains."); err != nil {
		t.Fatalf("consolidateAndStore: %v", err)
	}

	facts := Retrieve("outdoor activities", "rv1")
	if len(facts) == 0 {
		t.Error("expected at least one fact, got none")
	}
}

func TestRetrieveGeneral(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	idA, _, err := upsertUser("rg1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser A: %v", err)
	}
	ctx := context.Background()
	if err := consolidateAndStore(ctx, idA, "m1", "Alice loves mountain hiking."); err != nil {
		t.Fatalf("store A: %v", err)
	}

	// Exclude A — only B's facts eligible (B has none, so result may be empty).
	// The important thing is it doesn't panic and returns a slice.
	_ = RetrieveGeneral("outdoor activities", map[string]bool{"rg1": true})

	// Now retrieve without any exclusion — should find Alice's fact.
	facts := RetrieveGeneral("outdoor activities", map[string]bool{})
	if len(facts) == 0 {
		t.Error("expected at least one general fact, got none")
	}
}

func TestRetrieveMultiUser(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	idA, _, err := upsertUser("rm1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser A: %v", err)
	}
	ctx := context.Background()
	if err := consolidateAndStore(ctx, idA, "m1", "Alice plays piano."); err != nil {
		t.Fatalf("store A: %v", err)
	}

	users := map[string]string{"rm1": "Alice", "rm2": "Bob"}
	result := RetrieveMultiUser("musical instruments", users)

	if result == "" {
		t.Error("expected non-empty XML output, got empty string")
	}
	if !strings.HasPrefix(result, "<background_facts>") {
		t.Errorf("expected XML wrapper, got: %s", result)
	}
}

func TestFlushBuffer(t *testing.T) {
	setupTestDB(t)
	setupOpenAIIntegration(t)

	buffersMu.Lock()
	buffers["fb1"] = &messageBuffer{
		discordID:   "fb1",
		username:    "frank",
		displayName: "Frank",
		messageID:   "mfb1",
		messages:    []string{"I just moved to Austin and started working at Dell."},
	}
	buffersMu.Unlock()

	flushBuffer("fb1")

	facts := GetUserFacts("fb1")
	if len(facts) == 0 {
		t.Error("expected facts after flush, got none")
	}
}

func TestFindSimilarFacts_UserScoped(t *testing.T) {
	// With k=3 (similarityLimit), inserting 4 user2 facts with identical
	// embeddings before user1's fact means user2's facts fill the ANN top-3
	// slots (lower rowids). The buggy MATCH+k= query then has nothing left
	// for user1 after the JOIN filter. The fixed full-table scan must always
	// return user1's fact regardless of how many other users have similar facts.
	setupTestDB(t)

	user1, _, err := upsertUser("sc_u1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser user1: %v", err)
	}
	user2, _, err := upsertUser("sc_u2", "bob", "Bob")
	if err != nil {
		t.Fatalf("upsertUser user2: %v", err)
	}

	embedding := make([]float32, embeddingDimensions)
	embedding[0] = 1.0

	// Insert k+1 facts for user2 first so they get lower rowids and
	// crowd user1's fact out of the ANN result set.
	for j := range similarityLimit + 1 {
		if err := insertFact(user2, "m2", "bob fact", embedding); err != nil {
			t.Fatalf("insertFact user2[%d]: %v", j, err)
		}
	}
	if err := insertFact(user1, "m1", "alice fact", embedding); err != nil {
		t.Fatalf("insertFact user1: %v", err)
	}

	results, err := findSimilarFacts(user1, embedding)
	if err != nil {
		t.Fatalf("findSimilarFacts: %v", err)
	}
	for _, r := range results {
		if r.FactText == "bob fact" {
			t.Error("findSimilarFacts returned a fact belonging to another user")
		}
	}
	if len(results) == 0 {
		t.Error("findSimilarFacts returned no results for user1 — likely crowded out by other-user facts")
	}
}

func TestConsolidateAndStore_ReinforceDoesNotSkipInvalidate(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("loop1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	reinforceVector := testVector(0, 1)
	invalidateVector := make([]float32, embeddingDimensions)
	invalidateVector[0] = 0.9
	invalidateVector[1] = 0.1

	if err := insertFact(id, "m1", "Alice uses VSCode.", reinforceVector); err != nil {
		t.Fatalf("insertFact reinforce candidate: %v", err)
	}
	if err := insertFact(id, "m2", "Alice lives in Tokyo.", invalidateVector); err != nil {
		t.Fatalf("insertFact invalidate candidate: %v", err)
	}

	setEmbedFunc(t, func(ctx context.Context, text string) ([]float32, error) {
		return reinforceVector, nil
	})
	setDecideActionFunc(t, func(ctx context.Context, oldFact, newFact string) (*consolidationAction, error) {
		switch oldFact {
		case "Alice uses VSCode.":
			return &consolidationAction{Action: "REINFORCE"}, nil
		case "Alice lives in Tokyo.":
			return &consolidationAction{Action: "INVALIDATE"}, nil
		default:
			return &consolidationAction{Action: "KEEP"}, nil
		}
	})

	if err := consolidateAndStore(context.Background(), id, "m3", "Alice moved to Berlin."); err != nil {
		t.Fatalf("consolidateAndStore: %v", err)
	}

	facts := GetUserFacts("loop1")
	if len(facts) != 2 {
		t.Fatalf("active facts len = %d, want 2", len(facts))
	}

	var gotVSCode, gotBerlin, gotTokyo bool
	for _, fact := range facts {
		gotVSCode = gotVSCode || fact.FactText == "Alice uses VSCode."
		gotBerlin = gotBerlin || fact.FactText == "Alice moved to Berlin."
		gotTokyo = gotTokyo || fact.FactText == "Alice lives in Tokyo."
		if fact.FactText == "Alice uses VSCode." && fact.ReinforcementCount != 1 {
			t.Fatalf("VSCode reinforcement_count = %d, want 1", fact.ReinforcementCount)
		}
	}
	if !gotVSCode || !gotBerlin || gotTokyo {
		t.Fatalf("unexpected active facts after reinforce+invalidate loop: %+v", facts)
	}
}

func TestConsolidateAndStore_RelaxedFallbackCanInvalidate(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("fallback1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	stored := make([]float32, embeddingDimensions)
	stored[0] = 1
	if err := insertFact(id, "m1", "Alice lives in Tokyo.", stored); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	originalEmbed := embedFunc
	originalDecide := decideActionFunc
	t.Cleanup(func() {
		embedFunc = originalEmbed
		decideActionFunc = originalDecide
	})

	embedFunc = func(ctx context.Context, text string) ([]float32, error) {
		switch text {
		case "Alice moved to Berlin.":
			// Similar enough for the relaxed fallback, but outside the strict threshold.
			v := make([]float32, embeddingDimensions)
			v[0] = 0.6
			v[1] = 0.8
			return v, nil
		default:
			return originalEmbed(ctx, text)
		}
	}
	decideActionFunc = func(ctx context.Context, oldFact, newFact string) (*consolidationAction, error) {
		if oldFact == "Alice lives in Tokyo." && newFact == "Alice moved to Berlin." {
			return &consolidationAction{Action: "INVALIDATE"}, nil
		}
		return &consolidationAction{Action: "KEEP"}, nil
	}

	if err := consolidateAndStore(context.Background(), id, "m2", "Alice moved to Berlin."); err != nil {
		t.Fatalf("consolidateAndStore: %v", err)
	}

	facts := GetUserFacts("fallback1")
	if len(facts) != 1 {
		t.Fatalf("after fallback invalidate: len = %d, want 1", len(facts))
	}
	if facts[0].FactText != "Alice moved to Berlin." {
		t.Errorf("FactText = %q, want %q", facts[0].FactText, "Alice moved to Berlin.")
	}
}

func TestConsolidateAndStore_UnrelatedFactDoesNotUseArbitraryNearestFallback(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("fallback2", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	stored := make([]float32, embeddingDimensions)
	stored[0] = 1
	if err := insertFact(id, "m1", "Alice lives in Tokyo.", stored); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	originalEmbed := embedFunc
	originalDecide := decideActionFunc
	t.Cleanup(func() {
		embedFunc = originalEmbed
		decideActionFunc = originalDecide
	})

	embedFunc = func(ctx context.Context, text string) ([]float32, error) {
		if text == "Alice bought a surfboard." {
			v := make([]float32, embeddingDimensions)
			v[1] = 1
			return v, nil
		}
		return originalEmbed(ctx, text)
	}

	decideCalled := false
	decideActionFunc = func(ctx context.Context, oldFact, newFact string) (*consolidationAction, error) {
		decideCalled = true
		return &consolidationAction{Action: "INVALIDATE"}, nil
	}

	if err := consolidateAndStore(context.Background(), id, "m2", "Alice bought a surfboard."); err != nil {
		t.Fatalf("consolidateAndStore: %v", err)
	}

	if decideCalled {
		t.Fatal("decideAction should not run for unrelated fallback candidates")
	}

	facts := GetUserFacts("fallback2")
	if len(facts) != 2 {
		t.Fatalf("after unrelated insert: len = %d, want 2", len(facts))
	}

	var gotTokyo, gotSurfboard bool
	for _, fact := range facts {
		gotTokyo = gotTokyo || fact.FactText == "Alice lives in Tokyo."
		gotSurfboard = gotSurfboard || fact.FactText == "Alice bought a surfboard."
	}
	if !gotTokyo || !gotSurfboard {
		t.Fatalf("expected both facts to remain active, got %+v", facts)
	}
}

func TestRetrieve_RelaxedFallbackDistance(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("retrieve_fallback", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	stored := make([]float32, embeddingDimensions)
	stored[0] = 1
	if err := insertFact(id, "m1", "Alice loves hiking in the mountains.", stored); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	originalEmbed := embedFunc
	t.Cleanup(func() { embedFunc = originalEmbed })
	embedFunc = func(ctx context.Context, text string) ([]float32, error) {
		v := make([]float32, embeddingDimensions)
		v[0] = 0.3
		v[1] = 0.9539392
		return v, nil
	}

	enabled = true
	t.Cleanup(func() { enabled = false })

	facts := Retrieve("outdoor activities", "retrieve_fallback")
	if len(facts) != 1 {
		t.Fatalf("fallback Retrieve len = %d, want 1", len(facts))
	}
	if facts[0].Text != "Alice loves hiking in the mountains." {
		t.Errorf("Text = %q, want %q", facts[0].Text, "Alice loves hiking in the mountains.")
	}
}

func TestRetrieve_DoesNotReturnArbitraryNearestFact(t *testing.T) {
	setupTestDB(t)

	id, _, err := upsertUser("retrieve_none", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	stored := make([]float32, embeddingDimensions)
	stored[0] = 1
	if err := insertFact(id, "m1", "Alice loves hiking in the mountains.", stored); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	originalEmbed := embedFunc
	t.Cleanup(func() { embedFunc = originalEmbed })
	embedFunc = func(ctx context.Context, text string) ([]float32, error) {
		v := make([]float32, embeddingDimensions)
		v[1] = 1
		return v, nil
	}

	enabled = true
	t.Cleanup(func() { enabled = false })

	facts := Retrieve("outdoor activities", "retrieve_none")
	if len(facts) != 0 {
		t.Fatalf("Retrieve returned unrelated nearest facts: %+v", facts)
	}
}

func TestRetrieveMultiUser_EmbedsQueryOnce(t *testing.T) {
	setupTestDB(t)

	aliceID, _, err := upsertUser("embed_once_a", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser alice: %v", err)
	}
	bobID, _, err := upsertUser("embed_once_b", "bob", "Bob")
	if err != nil {
		t.Fatalf("upsertUser bob: %v", err)
	}

	stored := make([]float32, embeddingDimensions)
	stored[0] = 1
	if err := insertFact(aliceID, "m1", "Alice plays piano.", stored); err != nil {
		t.Fatalf("insertFact alice: %v", err)
	}
	if err := insertFact(bobID, "m2", "Bob collects synths.", stored); err != nil {
		t.Fatalf("insertFact bob: %v", err)
	}

	originalEmbed := embedFunc
	t.Cleanup(func() { embedFunc = originalEmbed })

	var embedCalls atomic.Int32
	embedFunc = func(ctx context.Context, text string) ([]float32, error) {
		embedCalls.Add(1)
		v := make([]float32, embeddingDimensions)
		v[0] = 1
		return v, nil
	}

	enabled = true
	t.Cleanup(func() { enabled = false })

	result := RetrieveMultiUser("music gear", map[string]string{
		"embed_once_a": "Alice",
		"embed_once_b": "Bob",
	})
	if result == "" {
		t.Fatal("expected non-empty XML output")
	}
	if calls := embedCalls.Load(); calls != 1 {
		t.Fatalf("embedFunc calls = %d, want 1", calls)
	}
}
