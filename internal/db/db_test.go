package db

import (
	"testing"
)

func TestCreateMemoryTables(t *testing.T) {
	// Open an in-memory DB using the same Open logic
	Open(":memory:")
	defer Close()

	// Verify users table exists
	_, err := DB.Exec("INSERT INTO users (discord_id, username) VALUES ('123', 'testuser')")
	if err != nil {
		t.Fatalf("users table not created: %v", err)
	}

	// Verify facts table exists
	_, err = DB.Exec("INSERT INTO facts (user_id, original_message_id, fact_text) VALUES (1, 'msg1', 'likes cats')")
	if err != nil {
		t.Fatalf("facts table not created: %v", err)
	}

	// Verify vec_facts virtual table exists by inserting a zero vector
	// sqlite-vec accepts raw little-endian float32 bytes
	zeroVec := make([]byte, 768*4) // 768 float32s = 3072 bytes
	_, err = DB.Exec("INSERT INTO vec_facts (fact_id, embedding) VALUES (1, ?)", zeroVec)
	if err != nil {
		t.Fatalf("vec_facts table not created: %v", err)
	}
}

func TestImageHashesTable(t *testing.T) {
	Open(":memory:")
	defer Close()

	_, err := DB.Exec("INSERT INTO image_hashes (hash, message_json) VALUES ('abc123', '{}')")
	if err != nil {
		t.Fatalf("image_hashes table not created: %v", err)
	}

	var hash string
	err = DB.QueryRow("SELECT hash FROM image_hashes WHERE hash = 'abc123'").Scan(&hash)
	if err != nil {
		t.Fatalf("failed to query image_hashes: %v", err)
	}
	if hash != "abc123" {
		t.Errorf("retrieved hash = %q, want %q", hash, "abc123")
	}
}

func TestGameStateTable(t *testing.T) {
	Open(":memory:")
	defer Close()

	_, err := DB.Exec("INSERT INTO game_state (id, data) VALUES (1, '{\"rounds\":[]}')")
	if err != nil {
		t.Fatalf("game_state table not created: %v", err)
	}

	// Only one row allowed (CHECK id = 1)
	_, err = DB.Exec("INSERT INTO game_state (id, data) VALUES (2, '{}')")
	if err == nil {
		t.Error("expected error when inserting game_state with id != 1, got nil")
	}
}

func TestGameStateInsertOrReplace(t *testing.T) {
	Open(":memory:")
	defer Close()

	// Insert initial state
	_, err := DB.Exec("INSERT OR REPLACE INTO game_state (id, data) VALUES (1, '{\"rounds\":[]}')")
	if err != nil {
		t.Fatalf("initial game_state insert failed: %v", err)
	}

	// Replace with new state
	_, err = DB.Exec("INSERT OR REPLACE INTO game_state (id, data) VALUES (1, '{\"rounds\":[1,2]}')")
	if err != nil {
		t.Fatalf("game_state replace failed: %v", err)
	}

	var data string
	err = DB.QueryRow("SELECT data FROM game_state WHERE id = 1").Scan(&data)
	if err != nil {
		t.Fatalf("failed to query game_state: %v", err)
	}
	if data != `{"rounds":[1,2]}` {
		t.Errorf("game_state data = %q, want %q", data, `{"rounds":[1,2]}`)
	}
}

func TestUsersTableMigrationColumns(t *testing.T) {
	Open(":memory:")
	defer Close()

	// Verify migrated columns exist by using them
	_, err := DB.Exec("INSERT INTO users (discord_id, username, display_name, preferred_name) VALUES ('999', 'migrated', 'Display Name', 'Preferred')")
	if err != nil {
		t.Fatalf("users table missing migrated columns: %v", err)
	}

	var displayName, preferredName string
	err = DB.QueryRow("SELECT display_name, preferred_name FROM users WHERE discord_id = '999'").Scan(&displayName, &preferredName)
	if err != nil {
		t.Fatalf("failed to query migrated columns: %v", err)
	}
	if displayName != "Display Name" {
		t.Errorf("display_name = %q, want %q", displayName, "Display Name")
	}
	if preferredName != "Preferred" {
		t.Errorf("preferred_name = %q, want %q", preferredName, "Preferred")
	}
}

func TestFactsTableDefaultValues(t *testing.T) {
	Open(":memory:")
	defer Close()

	_, err := DB.Exec("INSERT INTO users (discord_id, username) VALUES ('u1', 'testuser')")
	if err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}

	_, err = DB.Exec("INSERT INTO facts (user_id, original_message_id, fact_text) VALUES (1, 'msg_default', 'test fact')")
	if err != nil {
		t.Fatalf("failed to insert fact: %v", err)
	}

	var isActive, reinforcementCount int
	err = DB.QueryRow("SELECT is_active, reinforcement_count FROM facts WHERE original_message_id = 'msg_default'").Scan(&isActive, &reinforcementCount)
	if err != nil {
		t.Fatalf("failed to query fact defaults: %v", err)
	}
	if isActive != 1 {
		t.Errorf("is_active default = %d, want 1", isActive)
	}
	if reinforcementCount != 0 {
		t.Errorf("reinforcement_count default = %d, want 0", reinforcementCount)
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
	// Verify Close is safe when DB is nil
	DB = nil
	Close() // should not panic
}

func TestVecFactsQueryable(t *testing.T) {
	Open(":memory:")
	defer Close()

	// Insert user and fact first
	_, err := DB.Exec("INSERT INTO users (discord_id, username) VALUES ('v1', 'vecuser')")
	if err != nil {
		t.Fatalf("failed to insert user: %v", err)
	}
	_, err = DB.Exec("INSERT INTO facts (user_id, original_message_id, fact_text) VALUES (1, 'vmsg', 'vector fact')")
	if err != nil {
		t.Fatalf("failed to insert fact: %v", err)
	}

	// Insert embedding for the fact
	zeroVec := make([]byte, 768*4)
	_, err = DB.Exec("INSERT INTO vec_facts (fact_id, embedding) VALUES (1, ?)", zeroVec)
	if err != nil {
		t.Fatalf("failed to insert vec_facts row: %v", err)
	}

	// Verify we can query it back
	var factID int
	err = DB.QueryRow("SELECT fact_id FROM vec_facts WHERE fact_id = 1").Scan(&factID)
	if err != nil {
		t.Fatalf("failed to query vec_facts: %v", err)
	}
	if factID != 1 {
		t.Errorf("vec_facts fact_id = %d, want 1", factID)
	}
}
