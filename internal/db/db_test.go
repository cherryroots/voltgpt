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

	// Verify vec_facts virtual table exists by inserting a zero vector (768 floats)
	// sqlite-vec expects a JSON array for inserts
	zeroVec := make([]byte, 768*4) // 768 float32s = 3072 bytes
	_, err = DB.Exec("INSERT INTO vec_facts (fact_id, embedding) VALUES (1, ?)", zeroVec)
	if err != nil {
		t.Fatalf("vec_facts table not created: %v", err)
	}
}
