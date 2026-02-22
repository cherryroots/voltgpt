# Memory Package Coverage Improvement

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Improve `internal/memory` test coverage from 36.8% to ~75%+ by adding tests for uncovered DB functions and Gemini-backed pipelines.

**Architecture:** All changes are in `internal/memory/memory_test.go`. No production code changes. Pure DB tests use `setupTestDB`. Gemini-backed tests call `setupGemini(t)` which skips cleanly without a token. Tests are in `package memory` (white-box) so unexported functions are accessible.

**Tech Stack:** Go test, sqlite-vec (already loaded by `db.Open`), Gemini API (optional, skips cleanly)

---

### Task 1: Pure DB tests — DeleteAllFacts, RefreshAllFactNames, GetRecentFacts

**Files:**
- Modify: `internal/memory/memory_test.go`

**Step 1: Add the three tests**

Append to `memory_test.go` after the existing `TestRefreshFactNames`:

```go
func TestDeleteAllFacts(t *testing.T) {
	setupTestDB(t)

	id1, _, _ := upsertUser("da1", "u1", "U1")
	id2, _, _ := upsertUser("da2", "u2", "U2")
	embedding := make([]float32, embeddingDimensions)
	insertFact(id1, "m1", "fact one", embedding)
	insertFact(id2, "m2", "fact two", embedding)

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

	id1, _, _ := upsertUser("rb1", "alice", "Alice")
	id2, _, _ := upsertUser("rb2", "bob", "Bob")
	embedding := make([]float32, embeddingDimensions)
	insertFact(id1, "m1", "Alice likes tea.", embedding)
	insertFact(id2, "m2", "Bob plays chess.", embedding)

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

	id, _, _ := upsertUser("gr1", "alice", "Alice")
	embedding := make([]float32, embeddingDimensions)
	insertFact(id, "m1", "Alice likes coffee.", embedding)
	insertFact(id, "m2", "Alice plays piano.", embedding)
	insertFact(id, "m3", "Alice lives in Paris.", embedding)

	got := GetRecentFacts(2)
	if len(got) != 2 {
		t.Errorf("GetRecentFacts(2) len = %d, want 2", len(got))
	}
	// Returned items must have non-empty username and fact text.
	for _, f := range got {
		if f.Username == "" || f.FactText == "" {
			t.Errorf("incomplete recent fact: %+v", f)
		}
	}
}
```

**Step 2: Run and verify all three pass**

```
/usr/local/go/bin/go test ./internal/memory/... -run 'TestDeleteAllFacts|TestRefreshAllFactNames|TestGetRecentFacts' -v -timeout 30s
```

Expected: 3 tests PASS.

**Step 3: Commit**

```bash
git add internal/memory/memory_test.go
git commit -m "test(memory): add DB-only tests for DeleteAllFacts, RefreshAllFactNames, GetRecentFacts"
```

---

### Task 2: findSimilarFacts — vector threshold test

**Files:**
- Modify: `internal/memory/memory_test.go`

**Step 1: Add the test**

The strategy: insert a fact with a known unit vector (dim 0 = 1.0, rest 0). Query with the identical vector → distance 0, result returned. Query with an orthogonal vector (dim 1 = 1.0) → distance 1.0, above `distanceThreshold` (0.35), no result.

```go
func TestFindSimilarFacts(t *testing.T) {
	setupTestDB(t)

	id, _, _ := upsertUser("fs1", "alice", "Alice")

	// Unit vector in dimension 0.
	stored := make([]float32, embeddingDimensions)
	stored[0] = 1.0
	if err := insertFact(id, "m1", "Alice likes hiking.", stored); err != nil {
		t.Fatalf("insertFact: %v", err)
	}

	// Identical vector → distance 0, should be returned.
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
```

**Step 2: Run and verify it passes**

```
/usr/local/go/bin/go test ./internal/memory/... -run TestFindSimilarFacts -v -timeout 30s
```

Expected: PASS.

**Step 3: Commit**

```bash
git add internal/memory/memory_test.go
git commit -m "test(memory): add findSimilarFacts vector threshold test"
```

---

### Task 3: consolidateAndStore — all four action paths (Gemini-gated)

**Files:**
- Modify: `internal/memory/memory_test.go`

Each sub-test calls both `setupTestDB(t)` and `setupGemini(t)`. All skip without `MEMORY_GEMINI_TOKEN`.

**Step 1: Add the four tests**

```go
func TestConsolidateAndStore_NoSimilar(t *testing.T) {
	setupTestDB(t)
	setupGemini(t)

	id, _, _ := upsertUser("cs1", "alice", "Alice")
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
	setupGemini(t)

	id, _, _ := upsertUser("cs2", "alice", "Alice")
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
	setupGemini(t)

	id, _, _ := upsertUser("cs3", "alice", "Alice")
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
	setupGemini(t)

	id, _, _ := upsertUser("cs4", "alice", "Alice")
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
```

**Step 2: Run with token**

```
MEMORY_GEMINI_TOKEN=$(grep MEMORY_GEMINI_TOKEN .env | cut -d= -f2) \
  /usr/local/go/bin/go test ./internal/memory/... \
  -run 'TestConsolidateAndStore' -v -timeout 60s
```

Expected: 4 PASS (or 4 SKIP if token absent).

**Step 3: Commit**

```bash
git add internal/memory/memory_test.go
git commit -m "test(memory): add consolidateAndStore tests for all four action paths"
```

---

### Task 4: Retrieve, RetrieveGeneral, RetrieveMultiUser — disabled guards + Gemini-gated happy paths

**Files:**
- Modify: `internal/memory/memory_test.go`

**Step 1: Add the tests**

```go
func TestRetrieve_Disabled(t *testing.T) {
	// No setupGemini — enabled stays false.
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
	setupGemini(t)

	id, _, _ := upsertUser("rv1", "alice", "Alice")
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
	setupGemini(t)

	ctx := context.Background()
	idA, _, _ := upsertUser("rg1", "alice", "Alice")
	idB, _, _ := upsertUser("rg2", "bob", "Bob")
	_ = idB

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
	setupGemini(t)

	ctx := context.Background()
	idA, _, _ := upsertUser("rm1", "alice", "Alice")
	idB, _, _ := upsertUser("rm2", "bob", "Bob")
	_ = idB

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
```

**Step 2: Run**

```
/usr/local/go/bin/go test ./internal/memory/... \
  -run 'TestRetrieve|TestRetrieveGeneral|TestRetrieveMultiUser' -v -timeout 60s
```

Expected: disabled tests PASS immediately; Gemini-backed tests PASS or SKIP.

**Step 3: Commit**

```bash
git add internal/memory/memory_test.go
git commit -m "test(memory): add Retrieve, RetrieveGeneral, RetrieveMultiUser tests"
```

---

### Task 5: flushBuffer integration test (Gemini-gated)

**Files:**
- Modify: `internal/memory/memory_test.go`

**Step 1: Add the test**

`flushBuffer` is called by the 30s timer in `Extract`. We can call it directly after manually seeding the `buffers` map.

```go
func TestFlushBuffer(t *testing.T) {
	setupTestDB(t)
	setupGemini(t)

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
```

**Step 2: Run**

```
/usr/local/go/bin/go test ./internal/memory/... -run TestFlushBuffer -v -timeout 60s
```

Expected: PASS or SKIP.

**Step 3: Commit**

```bash
git add internal/memory/memory_test.go
git commit -m "test(memory): add flushBuffer integration test"
```

---

### Task 6: Verify final coverage

**Step 1: Run full suite with coverage**

```
/usr/local/go/bin/go test ./internal/memory/... -cover -timeout 60s
```

Expected: coverage ≥ 55% without token (pure DB + disabled-guard tests cover the gap). With token: ≥ 75%.

**Step 2: Check per-function breakdown**

```
/usr/local/go/bin/go test ./internal/memory/... -coverprofile=/tmp/mem.out -timeout 60s && \
  /usr/local/go/bin/go tool cover -func=/tmp/mem.out
```

Functions that should now show > 0%: `DeleteAllFacts`, `RefreshAllFactNames`, `GetRecentFacts`, `findSimilarFacts`, `consolidateAndStore`, `Retrieve`, `RetrieveGeneral`, `RetrieveMultiUser`, `flushBuffer`.
