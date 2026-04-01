package memory

import (
	"fmt"
	"testing"
)

func TestGetRecentGuildNotesPage(t *testing.T) {
	setupTestDB(t)

	userID, _, err := upsertUser("discord-page-user", "pageuser", "Page User")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	for i := 0; i < 4; i++ {
		if _, err := insertNote(InteractionNote{
			GuildID:            "guild-1",
			NoteType:           noteTypeConversation,
			Title:              fmt.Sprintf("Conversation %c", 'A'+i),
			Summary:            "Summary",
			NoteDate:           fmt.Sprintf("2026-03-%02d", 4-i),
			ParticipantUserIDs: []int64{userID},
		}, []int64{userID}, testEmbedding()); err != nil {
			t.Fatalf("insertNote(%d): %v", i, err)
		}
	}

	page1, err := GetRecentGuildNotesPage("guild-1", 2, 0)
	if err != nil {
		t.Fatalf("GetRecentGuildNotesPage page1: %v", err)
	}
	page2, err := GetRecentGuildNotesPage("guild-1", 2, 2)
	if err != nil {
		t.Fatalf("GetRecentGuildNotesPage page2: %v", err)
	}

	if len(page1) != 2 {
		t.Fatalf("page1 len = %d, want 2", len(page1))
	}
	if len(page2) != 2 {
		t.Fatalf("page2 len = %d, want 2", len(page2))
	}

	if page1[0].Title != "Conversation A" || page1[1].Title != "Conversation B" {
		t.Fatalf("page1 titles = %q, %q; want Conversation A, Conversation B", page1[0].Title, page1[1].Title)
	}
	if page2[0].Title != "Conversation C" || page2[1].Title != "Conversation D" {
		t.Fatalf("page2 titles = %q, %q; want Conversation C, Conversation D", page2[0].Title, page2[1].Title)
	}
}
