package db

import (
	"math"
	"testing"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
)

func TestCreateMemoryV2Tables(t *testing.T) {
	Open(":memory:")
	defer Close()

	if _, err := DB.Exec("INSERT INTO users (discord_id, username, display_name, preferred_name) VALUES ('123', 'testuser', 'Test User', 'Tester')"); err != nil {
		t.Fatalf("users table not created: %v", err)
	}

	if _, err := DB.Exec(`
		INSERT INTO interaction_notes (guild_id, channel_id, note_type, title, summary, note_date)
		VALUES ('guild-1', 'channel-1', 'conversation', 'Test note', 'Summary', '2026-02-25')
	`); err != nil {
		t.Fatalf("interaction_notes table not created: %v", err)
	}

	if _, err := DB.Exec("INSERT INTO note_participants (note_id, participant_user_id) VALUES (1, 1)"); err != nil {
		t.Fatalf("note_participants table not created: %v", err)
	}

	zeroVec := make([]byte, 1536*4)
	if _, err := DB.Exec("INSERT INTO vec_notes (note_id, embedding) VALUES (1, ?)", zeroVec); err != nil {
		t.Fatalf("vec_notes table not created: %v", err)
	}

	if _, err := DB.Exec(`
		INSERT INTO guild_user_profiles (guild_id, user_id, bio)
		VALUES ('guild-1', 1, '[{"text":"Lives in Austin.","source_note_ids":[1]}]')
	`); err != nil {
		t.Fatalf("guild_user_profiles table not created: %v", err)
	}

	if _, err := DB.Exec(`
		INSERT INTO channel_buffers (channel_id, guild_id, messages)
		VALUES ('channel-1', 'guild-1', '[]')
	`); err != nil {
		t.Fatalf("channel_buffers table not created: %v", err)
	}

	if _, err := DB.Exec(`
		INSERT INTO memory_job_runs (guild_id, job_date, phase, status)
		VALUES ('guild-1', '2026-02-25', 'cluster', 'completed')
	`); err != nil {
		t.Fatalf("memory_job_runs table not created: %v", err)
	}
}

func TestGameStateTable(t *testing.T) {
	Open(":memory:")
	defer Close()

	_, err := DB.Exec("INSERT INTO game_state (id, data) VALUES (1, '{\"rounds\":[]}')")
	if err != nil {
		t.Fatalf("game_state table not created: %v", err)
	}

	_, err = DB.Exec("INSERT INTO game_state (id, data) VALUES (2, '{}')")
	if err == nil {
		t.Error("expected error when inserting game_state with id != 1, got nil")
	}
}

func TestUserDiscordIDUnique(t *testing.T) {
	Open(":memory:")
	defer Close()

	_, err := DB.Exec("INSERT INTO users (discord_id, username) VALUES ('dup', 'user1')")
	if err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	_, err = DB.Exec("INSERT INTO users (discord_id, username) VALUES ('dup', 'user2')")
	if err == nil {
		t.Error("expected UNIQUE constraint error on duplicate discord_id, got nil")
	}
}

func TestCloseNilSafe(t *testing.T) {
	DB = nil
	Close()
}

func TestOpenMemoryIsolatedAcrossCalls(t *testing.T) {
	Open(":memory:")

	if _, err := DB.Exec("INSERT INTO users (discord_id, username) VALUES ('isolated', 'first')"); err != nil {
		t.Fatalf("failed to insert into first in-memory DB: %v", err)
	}

	Open(":memory:")
	defer Close()

	var count int
	if err := DB.QueryRow("SELECT COUNT(*) FROM users WHERE discord_id = 'isolated'").Scan(&count); err != nil {
		t.Fatalf("failed to query second in-memory DB: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected reopened in-memory DB to be empty, got %d matching rows", count)
	}
}

func TestImageHashesPrimaryKey(t *testing.T) {
	Open(":memory:")
	defer Close()

	if _, err := DB.Exec("INSERT INTO image_hashes (hash, message_json) VALUES ('abc123', '{\"id\":\"1\"}')"); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}
	if _, err := DB.Exec("INSERT INTO image_hashes (hash, message_json) VALUES ('abc123', '{\"id\":\"2\"}')"); err == nil {
		t.Error("expected PRIMARY KEY error on duplicate hash, got nil")
	}

	// The hasher persists with INSERT OR REPLACE, which only upserts because
	// hash is the primary key. Without it this would leave two rows.
	if _, err := DB.Exec("INSERT OR REPLACE INTO image_hashes (hash, message_json) VALUES ('abc123', '{\"id\":\"3\"}')"); err != nil {
		t.Fatalf("replace failed: %v", err)
	}
	var count int
	var messageJSON string
	if err := DB.QueryRow("SELECT COUNT(*), MAX(message_json) FROM image_hashes").Scan(&count, &messageJSON); err != nil {
		t.Fatalf("failed to query image_hashes: %v", err)
	}
	if count != 1 || messageJSON != `{"id":"3"}` {
		t.Errorf("after replace got %d rows with %q, want 1 row with %q", count, messageJSON, `{"id":"3"}`)
	}
}

func TestGameStateInsertOrReplace(t *testing.T) {
	Open(":memory:")
	defer Close()

	for _, data := range []string{`{"rounds":[]}`, `{"rounds":[1,2]}`} {
		if _, err := DB.Exec("INSERT OR REPLACE INTO game_state (id, data) VALUES (1, ?)", data); err != nil {
			t.Fatalf("game_state upsert failed: %v", err)
		}
	}

	var count int
	var data string
	if err := DB.QueryRow("SELECT COUNT(*), MAX(data) FROM game_state").Scan(&count, &data); err != nil {
		t.Fatalf("failed to query game_state: %v", err)
	}
	if count != 1 || data != `{"rounds":[1,2]}` {
		t.Errorf("game_state has %d rows with %q, want 1 row with %q", count, data, `{"rounds":[1,2]}`)
	}
}

func TestUsersOptionalNameDefaults(t *testing.T) {
	Open(":memory:")
	defer Close()

	// Callers insert users with only discord_id and username, then scan the
	// name columns into plain strings, so they must default to '' not NULL.
	if _, err := DB.Exec("INSERT INTO users (discord_id, username) VALUES ('999', 'someone')"); err != nil {
		t.Fatalf("insert without optional names failed: %v", err)
	}

	var id int
	var displayName, preferredName string
	if err := DB.QueryRow("SELECT id, display_name, preferred_name FROM users WHERE discord_id = '999'").Scan(&id, &displayName, &preferredName); err != nil {
		t.Fatalf("failed to read user defaults: %v", err)
	}
	if id != 1 || displayName != "" || preferredName != "" {
		t.Errorf("got id=%d display_name=%q preferred_name=%q, want id=1 and empty names", id, displayName, preferredName)
	}

	if _, err := DB.Exec("INSERT INTO users (discord_id, username, display_name) VALUES ('1000', 'other', NULL)"); err == nil {
		t.Error("expected NOT NULL error for display_name, got nil")
	}
}

func TestForeignKeysEnforcedAndCascade(t *testing.T) {
	Open(":memory:")
	defer Close()

	if _, err := DB.Exec("INSERT INTO note_participants (note_id, participant_user_id) VALUES (42, 42)"); err == nil {
		t.Fatal("expected FOREIGN KEY error for participant of missing note and user, got nil")
	}

	stmts := []string{
		"INSERT INTO users (discord_id, username) VALUES ('u1', 'one'), ('u2', 'two')",
		"INSERT INTO interaction_notes (guild_id, note_type, title, summary, note_date) VALUES ('g1', 'conversation', 'A', 'a', '2026-02-25'), ('g1', 'conversation', 'B', 'b', '2026-02-25')",
		"INSERT INTO note_participants (note_id, participant_user_id) VALUES (1, 1), (1, 2), (2, 1)",
		"INSERT INTO guild_user_profiles (guild_id, user_id) VALUES ('g1', 1), ('g1', 2)",
	}
	for _, stmt := range stmts {
		if _, err := DB.Exec(stmt); err != nil {
			t.Fatalf("seed %q failed: %v", stmt, err)
		}
	}

	// Deleting note 1 drops (1,1) and (1,2); deleting user 1 then drops (2,1)
	// and user 1's profile. Only user 2's profile should be left.
	if _, err := DB.Exec("DELETE FROM interaction_notes WHERE id = 1"); err != nil {
		t.Fatalf("delete note failed: %v", err)
	}
	if _, err := DB.Exec("DELETE FROM users WHERE id = 1"); err != nil {
		t.Fatalf("delete user failed: %v", err)
	}

	var participants, profiles, profileUser int
	if err := DB.QueryRow("SELECT COUNT(*) FROM note_participants").Scan(&participants); err != nil {
		t.Fatalf("count participants: %v", err)
	}
	if err := DB.QueryRow("SELECT COUNT(*), MAX(user_id) FROM guild_user_profiles").Scan(&profiles, &profileUser); err != nil {
		t.Fatalf("count profiles: %v", err)
	}
	if participants != 0 {
		t.Errorf("note_participants rows = %d, want 0 after cascades", participants)
	}
	if profiles != 1 || profileUser != 2 {
		t.Errorf("guild_user_profiles = %d rows (max user %d), want 1 row for user 2", profiles, profileUser)
	}
}

func testVec(components map[int]float32) []byte {
	v := make([]float32, 1536)
	for i, x := range components {
		v[i] = x
	}
	b, err := sqlite_vec.SerializeFloat32(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestVecNotesUsesCosineDistance(t *testing.T) {
	Open(":memory:")
	defer Close()

	// Note 1 points the same way as the query but is 10x longer: cosine
	// distance 0, L2 distance 9. Note 2 is at 45 degrees: cosine distance
	// 1 - 1/sqrt(2) ~= 0.2929, L2 distance 1. Note 3 is orthogonal: cosine
	// distance 1. Under L2 the order of notes 1 and 2 would flip.
	vectors := map[int][]byte{
		1: testVec(map[int]float32{0: 10}),
		2: testVec(map[int]float32{0: 1, 1: 1}),
		3: testVec(map[int]float32{1: 1}),
	}
	for id := 1; id <= 3; id++ {
		if _, err := DB.Exec("INSERT INTO vec_notes (note_id, embedding) VALUES (?, ?)", id, vectors[id]); err != nil {
			t.Fatalf("insert vec_notes %d: %v", id, err)
		}
	}

	rows, err := DB.Query("SELECT note_id, distance FROM vec_notes WHERE embedding MATCH ? AND k = 3 ORDER BY distance", testVec(map[int]float32{0: 1}))
	if err != nil {
		t.Fatalf("KNN query failed: %v", err)
	}
	defer rows.Close()

	want := []struct {
		id   int
		dist float64
	}{{1, 0}, {2, 1 - 1/math.Sqrt2}, {3, 1}}
	i := 0
	for rows.Next() {
		var id int
		var dist float64
		if err := rows.Scan(&id, &dist); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if i >= len(want) {
			t.Fatalf("got more than %d rows", len(want))
		}
		if id != want[i].id || math.Abs(dist-want[i].dist) > 1e-4 {
			t.Errorf("row %d = (note %d, distance %.4f), want (note %d, distance %.4f)", i, id, dist, want[i].id, want[i].dist)
		}
		i++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows: %v", err)
	}
	if i != len(want) {
		t.Errorf("got %d rows, want %d", i, len(want))
	}
}

func TestEnsureVecNotesTableMigratesOldDimension(t *testing.T) {
	Open(":memory:")
	defer Close()

	if _, err := DB.Exec("DROP TABLE vec_notes"); err != nil {
		t.Fatalf("drop vec_notes: %v", err)
	}
	if _, err := DB.Exec("CREATE VIRTUAL TABLE vec_notes USING vec0(note_id INTEGER PRIMARY KEY, embedding float[768] distance_metric=cosine)"); err != nil {
		t.Fatalf("create legacy vec_notes: %v", err)
	}
	if _, err := DB.Exec("INSERT INTO vec_notes (note_id, embedding) VALUES (1, ?)", make([]byte, 768*4)); err != nil {
		t.Fatalf("insert legacy vector: %v", err)
	}

	ensureVecNotesTable()

	var count int
	if err := DB.QueryRow("SELECT COUNT(*) FROM vec_notes").Scan(&count); err != nil {
		t.Fatalf("count vec_notes: %v", err)
	}
	if count != 0 {
		t.Errorf("vec_notes has %d rows after migration, want 0", count)
	}
	if _, err := DB.Exec("INSERT INTO vec_notes (note_id, embedding) VALUES (1, ?)", make([]byte, 1536*4)); err != nil {
		t.Errorf("1536-dim insert after migration failed: %v", err)
	}

	// A table that is already 1536-dim must be left alone, keeping its rows.
	ensureVecNotesTable()
	if err := DB.QueryRow("SELECT COUNT(*) FROM vec_notes").Scan(&count); err != nil {
		t.Fatalf("count vec_notes: %v", err)
	}
	if count != 1 {
		t.Errorf("vec_notes has %d rows after no-op ensure, want 1", count)
	}
}
