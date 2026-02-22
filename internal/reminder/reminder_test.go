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
