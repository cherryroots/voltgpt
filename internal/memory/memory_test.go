package memory

import (
	"bytes"
	"context"
	"encoding/binary"
	"log"
	"strconv"
	"strings"
	"testing"
	"time"

	"voltgpt/internal/db"
)

func setupTestDB(t *testing.T) {
	t.Helper()
	db.Open(":memory:")
	database = db.DB
	enabled = true
	stopMaintenanceScheduler()
	stopAllBufferTimers()
	lifecycleMu.Lock()
	maintenanceSweepRunning = false
	lifecycleMu.Unlock()
	t.Cleanup(func() {
		stopMaintenanceScheduler()
		stopAllBufferTimers()
		lifecycleMu.Lock()
		maintenanceSweepRunning = false
		lifecycleMu.Unlock()
		db.Close()
		database = nil
		enabled = false
	})
}

func setEmbedText(t *testing.T, fn func(context.Context, string) ([]float32, error)) {
	t.Helper()
	previous := embedText
	embedText = fn
	t.Cleanup(func() { embedText = previous })
}

func setConversationNoteGenerator(t *testing.T, fn func(context.Context, string, string, []bufMsg) (generatedConversationNote, error)) {
	t.Helper()
	previous := generateConversationNote
	generateConversationNote = fn
	t.Cleanup(func() { generateConversationNote = previous })
}

func setIncrementalProfileUpdater(t *testing.T, fn func(context.Context, GuildUserProfile, InteractionNote, userIdentity) (profileUpdateResult, error)) {
	t.Helper()
	previous := incrementalProfileUpdate
	incrementalProfileUpdate = fn
	t.Cleanup(func() { incrementalProfileUpdate = previous })
}

func setClusterer(t *testing.T, fn func(context.Context, string, string, []InteractionNote) ([]clusterResult, error)) {
	t.Helper()
	previous := clusterGuildDay
	clusterGuildDay = fn
	t.Cleanup(func() { clusterGuildDay = previous })
}

func setProfileRebuilder(t *testing.T, fn func(context.Context, string, userIdentity, []InteractionNote) (GuildUserProfile, error)) {
	t.Helper()
	previous := rebuildGuildProfile
	rebuildGuildProfile = fn
	t.Cleanup(func() { rebuildGuildProfile = previous })
}

func setTimeNow(t *testing.T, fn func() time.Time) {
	t.Helper()
	previous := timeNow
	timeNow = fn
	t.Cleanup(func() { timeNow = previous })
}

func testEmbedding() []float32 {
	vec := make([]float32, embeddingDimensions)
	vec[0] = 1
	return vec
}

func TestSerializeFloat32(t *testing.T) {
	v := []float32{1, -1, 0.5}
	got := serializeFloat32(v)
	if len(got) != len(v)*4 {
		t.Fatalf("len = %d, want %d", len(got), len(v)*4)
	}
	if binary.LittleEndian.Uint32(got[:4]) == 0 {
		t.Fatal("expected first encoded float32 to be non-zero")
	}
}

func TestEffectiveName(t *testing.T) {
	if got := effectiveName("Preferred", "Display", "user"); got != "Preferred" {
		t.Fatalf("preferred name priority broken: %q", got)
	}
	if got := effectiveName("", "Display", "user"); got != "Display" {
		t.Fatalf("display name priority broken: %q", got)
	}
	if got := effectiveName("", "", "user"); got != "user" {
		t.Fatalf("username fallback broken: %q", got)
	}
}

func TestUpsertUserAndPreferredName(t *testing.T) {
	setupTestDB(t)

	userID, name, err := upsertUser("discord-1", "alice", "Alice")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	if userID == 0 || name != "Alice" {
		t.Fatalf("unexpected upsert result: id=%d name=%q", userID, name)
	}

	if err := SetPreferredName("discord-1", "alice", "Al"); err != nil {
		t.Fatalf("SetPreferredName: %v", err)
	}
	if got := GetPreferredName("discord-1"); got != "Al" {
		t.Fatalf("GetPreferredName = %q, want %q", got, "Al")
	}
}

func TestInsertNoteAndProfileRoundTrip(t *testing.T) {
	setupTestDB(t)

	userID, _, err := upsertUser("discord-2", "bob", "Bob")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	noteID, err := insertNote(InteractionNote{
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		NoteType:  noteTypeConversation,
		Title:     "Gaming chat",
		Summary:   "Bob talked about Baldur's Gate 3 builds.",
		NoteDate:  "2026-02-25",
	}, []int64{userID}, testEmbedding())
	if err != nil {
		t.Fatalf("insertNote: %v", err)
	}

	note, err := getNoteByID(noteID)
	if err != nil {
		t.Fatalf("getNoteByID: %v", err)
	}
	if note == nil || note.Title != "Gaming chat" {
		t.Fatalf("unexpected note: %+v", note)
	}

	profile := emptyProfile("guild-1", userID)
	profile.Bio = []ProfileFact{{Text: "Lives in Austin.", SourceNoteIDs: []int64{noteID}}}
	if err := writeGuildUserProfile(profile); err != nil {
		t.Fatalf("writeGuildUserProfile: %v", err)
	}

	loaded, err := GetGuildUserProfile("guild-1", "discord-2")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if loaded == nil || len(loaded.Bio) != 1 {
		t.Fatalf("unexpected loaded profile: %+v", loaded)
	}
}

func TestWriteGuildUserProfileCompactsOversizedProfile(t *testing.T) {
	setupTestDB(t)

	userID, _, err := upsertUser("discord-compact", "compact", "Compact")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	profile := emptyProfile("guild-compact", userID)
	longFact := strings.TrimSpace(strings.Repeat("word ", profileMaxFactWords+5))
	for i := 0; i < profileMaxBioFacts+1; i++ {
		profile.Bio = append(profile.Bio, ProfileFact{
			Text:          longFact,
			SourceNoteIDs: []int64{1, 2, 3, 4, 5, 5},
		})
	}

	if err := writeGuildUserProfile(profile); err != nil {
		t.Fatalf("writeGuildUserProfile: %v", err)
	}

	loaded, err := GetGuildUserProfile("guild-compact", "discord-compact")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if loaded == nil {
		t.Fatal("expected compacted profile")
	}
	if got := len(loaded.Bio); got != profileMaxBioFacts {
		t.Fatalf("bio facts after compaction = %d, want %d", got, profileMaxBioFacts)
	}
	if got := len(strings.Fields(loaded.Bio[0].Text)); got != profileMaxFactWords {
		t.Fatalf("word count after compaction = %d, want %d", got, profileMaxFactWords)
	}
	if got := len(loaded.Bio[0].SourceNoteIDs); got != profileMaxSourceNoteIDs {
		t.Fatalf("source note IDs after compaction = %d, want %d", got, profileMaxSourceNoteIDs)
	}
}

func TestMarkGuildUserProfileDirtyCreatesDirtyPlaceholder(t *testing.T) {
	setupTestDB(t)

	if err := MarkGuildUserProfileDirty("guild-dirty", "discord-dirty", "dirtyuser", "Dirty User"); err != nil {
		t.Fatalf("MarkGuildUserProfileDirty: %v", err)
	}

	profile, err := GetGuildUserProfile("guild-dirty", "discord-dirty")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if profile == nil || !profile.IsDirty {
		t.Fatalf("expected dirty placeholder profile, got %+v", profile)
	}
	if profileHasContent(profile) {
		t.Fatalf("expected empty placeholder profile, got %+v", profile)
	}
}

func TestMarkAllGuildProfilesDirty(t *testing.T) {
	setupTestDB(t)

	userA, _, err := upsertUser("discord-dirty-a", "dirtya", "Dirty A")
	if err != nil {
		t.Fatalf("upsertUser A: %v", err)
	}
	userB, _, err := upsertUser("discord-dirty-b", "dirtyb", "Dirty B")
	if err != nil {
		t.Fatalf("upsertUser B: %v", err)
	}
	userOther, _, err := upsertUser("discord-dirty-other", "dirtyother", "Dirty Other")
	if err != nil {
		t.Fatalf("upsertUser other: %v", err)
	}

	profileA := emptyProfile("guild-dirty-all", userA)
	profileA.Bio = []ProfileFact{{Text: "Lives in Austin.", SourceNoteIDs: []int64{1}}}
	if err := writeGuildUserProfile(profileA); err != nil {
		t.Fatalf("writeGuildUserProfile A: %v", err)
	}

	profileB := emptyProfile("guild-dirty-all", userB)
	profileB.Other = []ProfileFact{{Text: "Builds keyboards.", SourceNoteIDs: []int64{2}}}
	if err := writeGuildUserProfile(profileB); err != nil {
		t.Fatalf("writeGuildUserProfile B: %v", err)
	}

	profileOther := emptyProfile("guild-untouched", userOther)
	profileOther.Other = []ProfileFact{{Text: "Plays RTS games.", SourceNoteIDs: []int64{3}}}
	if err := writeGuildUserProfile(profileOther); err != nil {
		t.Fatalf("writeGuildUserProfile other: %v", err)
	}

	count, err := MarkAllGuildProfilesDirty("guild-dirty-all")
	if err != nil {
		t.Fatalf("MarkAllGuildProfilesDirty: %v", err)
	}
	if count != 2 {
		t.Fatalf("MarkAllGuildProfilesDirty count = %d, want 2", count)
	}

	dirtyA, err := GetGuildUserProfile("guild-dirty-all", "discord-dirty-a")
	if err != nil {
		t.Fatalf("GetGuildUserProfile A: %v", err)
	}
	if dirtyA == nil || !dirtyA.IsDirty {
		t.Fatalf("expected guild-dirty-all profile A to be dirty, got %+v", dirtyA)
	}

	dirtyB, err := GetGuildUserProfile("guild-dirty-all", "discord-dirty-b")
	if err != nil {
		t.Fatalf("GetGuildUserProfile B: %v", err)
	}
	if dirtyB == nil || !dirtyB.IsDirty {
		t.Fatalf("expected guild-dirty-all profile B to be dirty, got %+v", dirtyB)
	}

	untouched, err := GetGuildUserProfile("guild-untouched", "discord-dirty-other")
	if err != nil {
		t.Fatalf("GetGuildUserProfile other: %v", err)
	}
	if untouched == nil || untouched.IsDirty {
		t.Fatalf("expected guild-untouched profile to remain clean, got %+v", untouched)
	}
}

func TestBufferFlushCreatesConversationNoteAndProfile(t *testing.T) {
	setupTestDB(t)

	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})
	setConversationNoteGenerator(t, func(context.Context, string, string, []bufMsg) (generatedConversationNote, error) {
		return generatedConversationNote{
			Title:   "Hardware chat",
			Summary: "Alice talked about building a new PC.",
		}, nil
	})
	setIncrementalProfileUpdater(t, func(_ context.Context, current GuildUserProfile, note InteractionNote, target userIdentity) (profileUpdateResult, error) {
		current.GuildID = note.GuildID
		current.UserID = target.UserID
		current.Other = []ProfileFact{{Text: "Building a new PC.", SourceNoteIDs: []int64{note.ID}}}
		return profileUpdateResult{Profile: current}, nil
	})

	err := flushBufferData(&channelBuffer{
		ChannelID: "channel-1",
		GuildID:   "guild-1",
		StartedAt: contextDeadlineTime(),
		UpdatedAt: contextDeadlineTime(),
		Messages: []bufMsg{
			{
				DiscordID:   "discord-3",
				Username:    "alice",
				DisplayName: "Alice",
				Text:        "I'm building a new PC with a 5090 and trying to optimize airflow, noise, and thermals before I lock the parts list.",
				MessageID:   "m1",
			},
			{
				DiscordID:   "discord-3",
				Username:    "alice",
				DisplayName: "Alice",
				Text:        "I'm also comparing cases and coolers because I want this build to stay quiet under load without giving up performance.",
				MessageID:   "m2",
			},
		},
	})
	if err != nil {
		t.Fatalf("flushBufferData: %v", err)
	}

	if got := TotalNotes(); got != 1 {
		t.Fatalf("TotalNotes = %d, want 1", got)
	}

	profile, err := GetGuildUserProfile("guild-1", "discord-3")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if profile == nil || len(profile.Other) != 1 {
		t.Fatalf("unexpected profile after flush: %+v", profile)
	}
}

func TestBufferFlushMarksProfileDirtyWhenIncrementalResultExceedsBudget(t *testing.T) {
	setupTestDB(t)

	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})
	setConversationNoteGenerator(t, func(context.Context, string, string, []bufMsg) (generatedConversationNote, error) {
		return generatedConversationNote{
			Title:   "Budget chat",
			Summary: "Alice generated too many profile facts.",
		}, nil
	})
	setIncrementalProfileUpdater(t, func(_ context.Context, current GuildUserProfile, note InteractionNote, target userIdentity) (profileUpdateResult, error) {
		current.GuildID = note.GuildID
		current.UserID = target.UserID
		for i := 0; i < profileMaxOtherFacts+profileHysteresisExtraFacts+1; i++ {
			current.Other = append(current.Other, ProfileFact{
				Text:          "Keeps adding profile facts.",
				SourceNoteIDs: []int64{note.ID},
			})
		}
		return profileUpdateResult{Profile: current}, nil
	})

	err := flushBufferData(&channelBuffer{
		ChannelID: "channel-budget",
		GuildID:   "guild-budget",
		StartedAt: contextDeadlineTime(),
		UpdatedAt: contextDeadlineTime(),
		Messages: []bufMsg{
			{
				DiscordID:   "discord-budget",
				Username:    "alice",
				DisplayName: "Alice",
				Text:        "This message is long enough to create a conversation note and trigger the oversized profile path without any ambiguity in the test setup.",
				MessageID:   "budget-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("flushBufferData: %v", err)
	}

	profile, err := GetGuildUserProfile("guild-budget", "discord-budget")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if profile == nil || !profile.IsDirty {
		t.Fatalf("expected dirty profile marker after oversized incremental update, got %+v", profile)
	}
	if got := len(profile.Other); got != 0 {
		t.Fatalf("expected no oversized facts to be written, got %d", got)
	}
}

func TestBufferFlushCompactsSlightlyOversizedIncrementalResultWithinHysteresis(t *testing.T) {
	setupTestDB(t)

	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})
	setConversationNoteGenerator(t, func(context.Context, string, string, []bufMsg) (generatedConversationNote, error) {
		return generatedConversationNote{
			Title:   "Compact chat",
			Summary: "Alice generated a slightly oversized profile.",
		}, nil
	})
	setIncrementalProfileUpdater(t, func(_ context.Context, current GuildUserProfile, note InteractionNote, target userIdentity) (profileUpdateResult, error) {
		current.GuildID = note.GuildID
		current.UserID = target.UserID
		for i := 0; i < profileMaxOtherFacts+1; i++ {
			current.Other = append(current.Other, ProfileFact{
				Text:          "Keeps adding profile facts.",
				SourceNoteIDs: []int64{note.ID},
			})
		}
		return profileUpdateResult{Profile: current}, nil
	})

	err := flushBufferData(&channelBuffer{
		ChannelID: "channel-budget-compact",
		GuildID:   "guild-budget-compact",
		StartedAt: contextDeadlineTime(),
		UpdatedAt: contextDeadlineTime(),
		Messages: []bufMsg{
			{
				DiscordID:   "discord-budget-compact",
				Username:    "alice",
				DisplayName: "Alice",
				Text:        "This message is long enough to create a conversation note and verify that a slight overflow is compacted rather than forcing a rebuild.",
				MessageID:   "budget-compact-1",
			},
		},
	})
	if err != nil {
		t.Fatalf("flushBufferData: %v", err)
	}

	profile, err := GetGuildUserProfile("guild-budget-compact", "discord-budget-compact")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if profile == nil || profile.IsDirty {
		t.Fatalf("expected compacted clean profile, got %+v", profile)
	}
	if got := len(profile.Other); got != profileMaxOtherFacts {
		t.Fatalf("compacted other facts = %d, want %d", got, profileMaxOtherFacts)
	}
}

func TestBufferMessageFlushesAtMaxMessages(t *testing.T) {
	setupTestDB(t)

	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})
	setConversationNoteGenerator(t, func(context.Context, string, string, []bufMsg) (generatedConversationNote, error) {
		return generatedConversationNote{
			Title:   "Long channel session",
			Summary: "Alice kept a long-running thread going.",
		}, nil
	})
	setIncrementalProfileUpdater(t, func(_ context.Context, current GuildUserProfile, note InteractionNote, target userIdentity) (profileUpdateResult, error) {
		current.GuildID = note.GuildID
		current.UserID = target.UserID
		current.Other = []ProfileFact{{Text: "Participated in a long-running thread.", SourceNoteIDs: []int64{note.ID}}}
		return profileUpdateResult{Profile: current}, nil
	})

	for i := 0; i < bufferMaxMessages; i++ {
		BufferMessage(
			"channel-cap",
			"guild-cap",
			"discord-cap",
			"alice",
			"Alice",
			"This is a buffer message that should count toward the max message flush limit.",
			"msg-"+strconv.Itoa(i),
		)
	}

	if got := TotalNotes(); got != 1 {
		t.Fatalf("TotalNotes after max-message flush = %d, want 1", got)
	}

	buffersMu.Lock()
	_, exists := buffers["channel-cap"]
	buffersMu.Unlock()
	if exists {
		t.Fatal("expected channel buffer to be cleared after max-message flush")
	}
}

func TestBuildPromptContextGuildScoped(t *testing.T) {
	setupTestDB(t)

	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})

	userID, _, err := upsertUser("discord-4", "sam", "Sam")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	noteID, err := insertNote(InteractionNote{
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		NoteType:  noteTypeConversation,
		Title:     "Music talk",
		Summary:   "Sam compared synth plugins.",
		NoteDate:  "2026-02-25",
	}, []int64{userID}, testEmbedding())
	if err != nil {
		t.Fatalf("insertNote guild-1: %v", err)
	}

	if _, err := insertNote(InteractionNote{
		GuildID:   "guild-2",
		ChannelID: "channel-2",
		NoteType:  noteTypeConversation,
		Title:     "Other guild",
		Summary:   "This should not appear.",
		NoteDate:  "2026-02-25",
	}, []int64{userID}, testEmbedding()); err != nil {
		t.Fatalf("insertNote guild-2: %v", err)
	}

	profile := emptyProfile("guild-1", userID)
	profile.Interests = []ProfileFact{{Text: "Synth plugins.", SourceNoteIDs: []int64{noteID}}}
	if err := writeGuildUserProfile(profile); err != nil {
		t.Fatalf("writeGuildUserProfile: %v", err)
	}

	got := BuildPromptContext(RetrieveRequest{
		GuildID:           "guild-1",
		ChannelID:         "channel-1",
		Query:             "synth plugins",
		ConversationUsers: map[string]string{"discord-4": "sam"},
	})
	if !strings.Contains(got, "Synth plugins.") {
		t.Fatalf("expected profile fact in prompt context: %s", got)
	}
	if strings.Contains(got, "This should not appear.") {
		t.Fatalf("prompt context leaked another guild: %s", got)
	}
}

func TestBuildPromptContextLogsDuration(t *testing.T) {
	setupTestDB(t)

	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})

	userID, _, err := upsertUser("discord-5", "casey", "Casey")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	noteID, err := insertNote(InteractionNote{
		GuildID:   "guild-1",
		ChannelID: "channel-1",
		NoteType:  noteTypeConversation,
		Title:     "Prompt context log",
		Summary:   "Casey talked about duration logging.",
		NoteDate:  "2026-02-25",
	}, []int64{userID}, testEmbedding())
	if err != nil {
		t.Fatalf("insertNote: %v", err)
	}

	profile := emptyProfile("guild-1", userID)
	profile.Other = []ProfileFact{{Text: "Asked for duration logging.", SourceNoteIDs: []int64{noteID}}}
	if err := writeGuildUserProfile(profile); err != nil {
		t.Fatalf("writeGuildUserProfile: %v", err)
	}

	var logBuf bytes.Buffer
	previousWriter := log.Writer()
	log.SetOutput(&logBuf)
	t.Cleanup(func() { log.SetOutput(previousWriter) })

	got := BuildPromptContext(RetrieveRequest{
		GuildID:           "guild-1",
		ChannelID:         "channel-1",
		Query:             "duration logging",
		ConversationUsers: map[string]string{"discord-5": "casey"},
	})
	if got == "" {
		t.Fatal("expected prompt context, got empty string")
	}
	if !strings.Contains(logBuf.String(), "duration_ms=") {
		t.Fatalf("expected duration_ms in prompt_context log, got %q", logBuf.String())
	}
}

func TestClusterAndDeleteFlows(t *testing.T) {
	setupTestDB(t)

	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})
	setClusterer(t, func(_ context.Context, guildID, date string, notes []InteractionNote) ([]clusterResult, error) {
		return []clusterResult{{
			Title:         "Weekly games",
			Summary:       "People compared co-op games.",
			SourceNoteIDs: []int64{notes[0].ID, notes[1].ID, notes[2].ID},
		}}, nil
	})
	setProfileRebuilder(t, func(_ context.Context, guildID string, target userIdentity, notes []InteractionNote) (GuildUserProfile, error) {
		profile := emptyProfile(guildID, target.UserID)
		profile.Other = []ProfileFact{{Text: "Co-op games.", SourceNoteIDs: []int64{notes[0].ID}}}
		return profile, nil
	})

	u1, _, _ := upsertUser("discord-a", "a", "A")
	u2, _, _ := upsertUser("discord-b", "b", "B")

	for idx := 0; idx < 3; idx++ {
		if _, err := insertNote(InteractionNote{
			GuildID:   "guild-1",
			ChannelID: "channel-1",
			NoteType:  noteTypeConversation,
			Title:     "Chat",
			Summary:   "A and B discussed co-op games.",
			NoteDate:  "2026-02-25",
		}, []int64{u1, u2}, testEmbedding()); err != nil {
			t.Fatalf("insertNote %d: %v", idx, err)
		}
	}

	if err := runClusterPhase("guild-1", "2026-02-25"); err != nil {
		t.Fatalf("runClusterPhase: %v", err)
	}
	if err := markProfileDirty("guild-1", u1); err != nil {
		t.Fatalf("markProfileDirty: %v", err)
	}
	if err := rebuildDirtyProfiles("guild-1"); err != nil {
		t.Fatalf("rebuildDirtyProfiles: %v", err)
	}
	if err := DeleteUserMemory("guild-1", "discord-a"); err != nil {
		t.Fatalf("DeleteUserMemory: %v", err)
	}

	profile, err := GetGuildUserProfile("guild-1", "discord-a")
	if err != nil {
		t.Fatalf("GetGuildUserProfile after delete: %v", err)
	}
	if profile != nil {
		t.Fatalf("expected deleted profile to be gone, got %+v", profile)
	}
}

func TestRebuildDirtyProfilesCompactsOversizedRebuildOutput(t *testing.T) {
	setupTestDB(t)

	userID, _, err := upsertUser("discord-rebuild-compact", "rebuildcompact", "Rebuild Compact")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}

	noteID, err := insertNote(InteractionNote{
		GuildID:   "guild-rebuild-compact",
		ChannelID: "channel-1",
		NoteType:  noteTypeConversation,
		Title:     "Compact chat",
		Summary:   "Rebuild compaction should keep profiles bounded.",
		NoteDate:  "2026-03-08",
	}, []int64{userID}, testEmbedding())
	if err != nil {
		t.Fatalf("insertNote: %v", err)
	}

	setProfileRebuilder(t, func(_ context.Context, guildID string, target userIdentity, notes []InteractionNote) (GuildUserProfile, error) {
		profile := emptyProfile(guildID, target.UserID)
		longFact := strings.TrimSpace(strings.Repeat("detail ", profileMaxFactWords+4))
		for i := 0; i < profileMaxOtherFacts+2; i++ {
			profile.Other = append(profile.Other, ProfileFact{
				Text:          longFact,
				SourceNoteIDs: []int64{noteID, noteID + 1, noteID + 2, noteID + 3, noteID + 4},
			})
		}
		return profile, nil
	})

	if err := markProfileDirty("guild-rebuild-compact", userID); err != nil {
		t.Fatalf("markProfileDirty: %v", err)
	}
	if err := rebuildDirtyProfiles("guild-rebuild-compact"); err != nil {
		t.Fatalf("rebuildDirtyProfiles: %v", err)
	}

	profile, err := GetGuildUserProfile("guild-rebuild-compact", "discord-rebuild-compact")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if profile == nil || profile.IsDirty {
		t.Fatalf("expected rebuilt clean compacted profile, got %+v", profile)
	}
	if got := len(profile.Other); got != profileMaxOtherFacts {
		t.Fatalf("other facts after rebuild compaction = %d, want %d", got, profileMaxOtherFacts)
	}
	if got := len(strings.Fields(profile.Other[0].Text)); got != profileMaxFactWords {
		t.Fatalf("rebuilt fact word count = %d, want %d", got, profileMaxFactWords)
	}
	if got := len(profile.Other[0].SourceNoteIDs); got != profileMaxSourceNoteIDs {
		t.Fatalf("rebuilt fact source note IDs = %d, want %d", got, profileMaxSourceNoteIDs)
	}
}

func TestDeleteUserMemoryPurgesLiveBuffers(t *testing.T) {
	setupTestDB(t)

	BufferMessage(
		"channel-live-delete",
		"guild-live-delete",
		"discord-delete-me",
		"deleteme",
		"Delete Me",
		"This buffered message is long enough to survive the minimum content threshold by itself and should be removed.",
		"msg-live-1",
	)
	BufferMessage(
		"channel-live-delete",
		"guild-live-delete",
		"discord-keep-me",
		"keepme",
		"Keep Me",
		"This second buffered message is also long enough and should remain in the live buffer after deletion runs.",
		"msg-live-2",
	)

	if _, _, err := upsertUser("discord-delete-me", "deleteme", "Delete Me"); err != nil {
		t.Fatalf("upsertUser delete target: %v", err)
	}
	if _, _, err := upsertUser("discord-keep-me", "keepme", "Keep Me"); err != nil {
		t.Fatalf("upsertUser survivor: %v", err)
	}

	if err := DeleteUserMemory("guild-live-delete", "discord-delete-me"); err != nil {
		t.Fatalf("DeleteUserMemory: %v", err)
	}

	buffersMu.Lock()
	buf, exists := buffers["channel-live-delete"]
	buffersMu.Unlock()
	if !exists {
		t.Fatal("expected live buffer to remain for surviving participants")
	}
	if len(buf.Messages) != 1 {
		t.Fatalf("buffer message count after delete = %d, want 1", len(buf.Messages))
	}
	if buf.Messages[0].DiscordID != "discord-keep-me" {
		t.Fatalf("remaining buffered user = %q, want %q", buf.Messages[0].DiscordID, "discord-keep-me")
	}

	if err := DeleteAllGuildMemory("guild-live-delete"); err != nil {
		t.Fatalf("DeleteAllGuildMemory: %v", err)
	}
	buffersMu.Lock()
	_, exists = buffers["channel-live-delete"]
	buffersMu.Unlock()
	if exists {
		t.Fatal("expected guild-wide delete to purge the live buffer")
	}
}

func TestRunScheduledMaintenanceSweep(t *testing.T) {
	setupTestDB(t)

	yesterday := time.Date(2026, 3, 8, 10, 0, 0, 0, time.UTC)
	setTimeNow(t, func() time.Time { return yesterday.Add(24 * time.Hour) })
	setEmbedText(t, func(context.Context, string) ([]float32, error) {
		return testEmbedding(), nil
	})
	clusterCalls := 0
	setClusterer(t, func(_ context.Context, guildID, date string, notes []InteractionNote) ([]clusterResult, error) {
		clusterCalls++
		return []clusterResult{{
			Title:         "Yesterday topic",
			Summary:       "People compared game settings.",
			SourceNoteIDs: []int64{notes[0].ID, notes[1].ID, notes[2].ID},
		}}, nil
	})
	setProfileRebuilder(t, func(_ context.Context, guildID string, target userIdentity, notes []InteractionNote) (GuildUserProfile, error) {
		profile := emptyProfile(guildID, target.UserID)
		profile.Other = []ProfileFact{{Text: "Tweaks game settings.", SourceNoteIDs: []int64{notes[0].ID}}}
		return profile, nil
	})

	u1, _, _ := upsertUser("discord-s1", "s1", "S1")
	u2, _, _ := upsertUser("discord-s2", "s2", "S2")
	for idx := 0; idx < 3; idx++ {
		if _, err := insertNote(InteractionNote{
			GuildID:   "guild-scheduler",
			ChannelID: "channel-1",
			NoteType:  noteTypeConversation,
			Title:     "Chat",
			Summary:   "S1 and S2 discussed settings.",
			NoteDate:  yesterday.Format(time.DateOnly),
		}, []int64{u1, u2}, testEmbedding()); err != nil {
			t.Fatalf("insertNote %d: %v", idx, err)
		}
	}
	if err := markProfileDirty("guild-scheduler", u1); err != nil {
		t.Fatalf("markProfileDirty: %v", err)
	}

	days, err := listConversationGuildDaysBefore(timeNow().Format(time.DateOnly))
	if err != nil {
		t.Fatalf("listConversationGuildDaysBefore: %v", err)
	}
	if len(days) != 1 {
		t.Fatalf("conversation guild-days = %d, want 1", len(days))
	}
	notes, err := getConversationNotesForGuildDate("guild-scheduler", yesterday.Format(time.DateOnly))
	if err != nil {
		t.Fatalf("getConversationNotesForGuildDate: %v", err)
	}
	if len(notes) != 3 {
		t.Fatalf("conversation notes for scheduler test = %d, want 3", len(notes))
	}
	var existingJobRuns int
	if err := database.QueryRow("SELECT COUNT(*) FROM memory_job_runs").Scan(&existingJobRuns); err != nil {
		t.Fatalf("memory_job_runs count: %v", err)
	}
	if existingJobRuns != 0 {
		t.Fatalf("expected no job runs before sweep, got %d", existingJobRuns)
	}

	if err := runScheduledMaintenanceSweep(); err != nil {
		t.Fatalf("runScheduledMaintenanceSweep: %v", err)
	}
	if clusterCalls == 0 {
		t.Fatal("expected scheduled maintenance sweep to call clusterer")
	}

	profile, err := GetGuildUserProfile("guild-scheduler", "discord-s1")
	if err != nil {
		t.Fatalf("GetGuildUserProfile: %v", err)
	}
	if profile == nil || profile.IsDirty {
		t.Fatalf("expected rebuilt clean profile, got %+v", profile)
	}

	clusterNotes, err := getConversationNotesForGuildDate("guild-scheduler", yesterday.Format(time.DateOnly))
	if err != nil {
		t.Fatalf("getConversationNotesForGuildDate: %v", err)
	}
	if len(clusterNotes) != 3 {
		t.Fatalf("conversation notes changed unexpectedly: %d", len(clusterNotes))
	}

	var topicCount int
	if err := database.QueryRow(`
		SELECT COUNT(*)
		FROM interaction_notes
		WHERE guild_id = ? AND note_type = ?
	`, "guild-scheduler", noteTypeTopicCluster).Scan(&topicCount); err != nil {
		t.Fatalf("topic count query: %v", err)
	}
	if topicCount != 1 {
		t.Fatalf("topic cluster count = %d, want 1", topicCount)
	}

	status, err := getJobStatus("guild-scheduler", timeNow().Format(time.DateOnly), jobPhaseProfileMaintenance)
	if err != nil {
		t.Fatalf("getJobStatus profile maintenance: %v", err)
	}
	if status != jobStatusCompleted {
		t.Fatalf("profile maintenance job status = %q, want %q", status, jobStatusCompleted)
	}
}

func TestRunScheduledMaintenanceSweepSkipsOverlap(t *testing.T) {
	setupTestDB(t)

	setTimeNow(t, func() time.Time {
		return time.Date(2026, 3, 9, 10, 0, 0, 0, time.UTC)
	})
	called := 0
	setProfileRebuilder(t, func(_ context.Context, guildID string, target userIdentity, notes []InteractionNote) (GuildUserProfile, error) {
		called++
		return emptyProfile(guildID, target.UserID), nil
	})

	userID, _, err := upsertUser("discord-overlap", "overlap", "Overlap")
	if err != nil {
		t.Fatalf("upsertUser: %v", err)
	}
	if _, err := insertNote(InteractionNote{
		GuildID:   "guild-overlap",
		ChannelID: "channel-1",
		NoteType:  noteTypeConversation,
		Title:     "Overlap chat",
		Summary:   "Overlap discussed settings.",
		NoteDate:  "2026-03-08",
	}, []int64{userID}, testEmbedding()); err != nil {
		t.Fatalf("insertNote: %v", err)
	}
	if err := markProfileDirty("guild-overlap", userID); err != nil {
		t.Fatalf("markProfileDirty: %v", err)
	}

	lifecycleMu.Lock()
	maintenanceSweepRunning = true
	lifecycleMu.Unlock()
	t.Cleanup(func() {
		lifecycleMu.Lock()
		maintenanceSweepRunning = false
		lifecycleMu.Unlock()
	})

	if err := runScheduledMaintenanceSweep(); err != nil {
		t.Fatalf("runScheduledMaintenanceSweep: %v", err)
	}
	if called != 0 {
		t.Fatalf("expected overlapping sweep to skip work, called=%d", called)
	}
}

func contextDeadlineTime() time.Time {
	return time.Now().UTC()
}
