package db

import "testing"

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

	_, err = DB.Exec("INSERT INTO game_state (id, data) VALUES (2, '{}')")
	if err == nil {
		t.Error("expected error when inserting game_state with id != 1, got nil")
	}
}

func TestGameStateInsertOrReplace(t *testing.T) {
	Open(":memory:")
	defer Close()

	_, err := DB.Exec("INSERT OR REPLACE INTO game_state (id, data) VALUES (1, '{\"rounds\":[]}')")
	if err != nil {
		t.Fatalf("initial game_state insert failed: %v", err)
	}

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

func TestUsersTableColumns(t *testing.T) {
	Open(":memory:")
	defer Close()

	_, err := DB.Exec("INSERT INTO users (discord_id, username, display_name, preferred_name) VALUES ('999', 'migrated', 'Display Name', 'Preferred')")
	if err != nil {
		t.Fatalf("users table missing expected columns: %v", err)
	}

	var displayName, preferredName string
	err = DB.QueryRow("SELECT display_name, preferred_name FROM users WHERE discord_id = '999'").Scan(&displayName, &preferredName)
	if err != nil {
		t.Fatalf("failed to query user columns: %v", err)
	}
	if displayName != "Display Name" {
		t.Errorf("display_name = %q, want %q", displayName, "Display Name")
	}
	if preferredName != "Preferred" {
		t.Errorf("preferred_name = %q, want %q", preferredName, "Preferred")
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

func TestVecNotesQueryable(t *testing.T) {
	Open(":memory:")
	defer Close()

	if _, err := DB.Exec("INSERT INTO interaction_notes (guild_id, note_type, title, summary, note_date) VALUES ('g1', 'conversation', 'Note', 'Summary', '2026-02-25')"); err != nil {
		t.Fatalf("failed to insert note: %v", err)
	}

	zeroVec := make([]byte, 1536*4)
	if _, err := DB.Exec("INSERT INTO vec_notes (note_id, embedding) VALUES (1, ?)", zeroVec); err != nil {
		t.Fatalf("failed to insert vec_notes row: %v", err)
	}

	var noteID int
	if err := DB.QueryRow("SELECT note_id FROM vec_notes WHERE note_id = 1").Scan(&noteID); err != nil {
		t.Fatalf("failed to query vec_notes: %v", err)
	}
	if noteID != 1 {
		t.Errorf("vec_notes note_id = %d, want 1", noteID)
	}
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
