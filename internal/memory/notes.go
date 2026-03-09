package memory

import (
	"database/sql"
	"fmt"
	"strings"
)

type sqlScanner interface {
	Scan(dest ...any) error
}

func insertNote(note InteractionNote, participantUserIDs []int64, embedding []float32) (int64, error) {
	if database == nil {
		return 0, fmt.Errorf("memory system not initialized")
	}

	tx, err := database.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	var channelValue any
	if strings.TrimSpace(note.ChannelID) != "" {
		channelValue = note.ChannelID
	}

	res, err := tx.Exec(`
		INSERT INTO interaction_notes (guild_id, channel_id, note_type, title, summary, source_note_ids, note_date)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, note.GuildID, channelValue, note.NoteType, note.Title, note.Summary, marshalInt64Slice(note.SourceNoteIDs), note.NoteDate)
	if err != nil {
		return 0, err
	}

	noteID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	for _, userID := range dedupeInt64s(participantUserIDs) {
		if _, err := tx.Exec(
			"INSERT INTO note_participants (note_id, participant_user_id) VALUES (?, ?)",
			noteID, userID,
		); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(
		"INSERT INTO vec_notes (note_id, embedding) VALUES (?, ?)",
		noteID, serializeFloat32(embedding),
	); err != nil {
		return 0, err
	}

	return noteID, tx.Commit()
}

func getNoteByID(noteID int64) (*InteractionNote, error) {
	row := database.QueryRow(`
		SELECT id, guild_id, COALESCE(channel_id, ''), note_type, title, summary, source_note_ids, note_date, created_at
		FROM interaction_notes
		WHERE id = ?
	`, noteID)
	note, err := scanInteractionNote(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	note.ParticipantUserIDs, err = listNoteParticipants(noteID)
	if err != nil {
		return nil, err
	}
	return &note, nil
}

func scanInteractionNote(scanner sqlScanner) (InteractionNote, error) {
	var (
		note          InteractionNote
		sourceNoteIDs string
	)
	err := scanner.Scan(
		&note.ID,
		&note.GuildID,
		&note.ChannelID,
		&note.NoteType,
		&note.Title,
		&note.Summary,
		&sourceNoteIDs,
		&note.NoteDate,
		&note.CreatedAt,
	)
	if err != nil {
		return InteractionNote{}, err
	}
	note.SourceNoteIDs, err = parseInt64Slice(sourceNoteIDs)
	if err != nil {
		return InteractionNote{}, err
	}
	note.NoteDate = safeDate(note.NoteDate)
	return note, nil
}

func listNoteParticipants(noteID int64) ([]int64, error) {
	rows, err := database.Query(`
		SELECT participant_user_id
		FROM note_participants
		WHERE note_id = ?
		ORDER BY participant_user_id
	`, noteID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var participantIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		participantIDs = append(participantIDs, userID)
	}
	return participantIDs, rows.Err()
}

func deleteNoteAndVector(noteID int64) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM vec_notes WHERE note_id = ?", noteID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM interaction_notes WHERE id = ?", noteID); err != nil {
		return err
	}

	return tx.Commit()
}

func getNotesByIDs(ids []int64) (map[int64]InteractionNote, error) {
	ids = dedupeInt64s(ids)
	if len(ids) == 0 {
		return map[int64]InteractionNote{}, nil
	}

	query, args := inClause(`
		SELECT id, guild_id, COALESCE(channel_id, ''), note_type, title, summary, source_note_ids, note_date, created_at
		FROM interaction_notes
		WHERE id IN (%s)
	`, ids)
	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	notes := make(map[int64]InteractionNote, len(ids))
	for rows.Next() {
		note, err := scanInteractionNote(rows)
		if err != nil {
			return nil, err
		}
		notes[note.ID] = note
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for noteID, note := range notes {
		participantIDs, err := listNoteParticipants(noteID)
		if err != nil {
			return nil, err
		}
		note.ParticipantUserIDs = participantIDs
		notes[noteID] = note
	}
	return notes, nil
}

func getRecentConversationNotesForUser(guildID string, userID int64, limit int) ([]InteractionNote, error) {
	if limit <= 0 {
		limit = recentUserFallbackNoteLimit
	}

	rows, err := database.Query(`
		SELECT n.id, n.guild_id, COALESCE(n.channel_id, ''), n.note_type, n.title, n.summary, n.source_note_ids, n.note_date, n.created_at
		FROM interaction_notes n
		JOIN note_participants np ON np.note_id = n.id
		WHERE n.guild_id = ?
		  AND n.note_type = ?
		  AND np.participant_user_id = ?
		ORDER BY n.note_date DESC, n.created_at DESC
		LIMIT ?
	`, guildID, noteTypeConversation, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []InteractionNote
	for rows.Next() {
		note, err := scanInteractionNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attachNoteParticipants(notes)
}

func GetRecentConversationNotesForUser(guildID, discordID string, limit int) ([]InteractionNote, error) {
	user, err := getUserIdentityByDiscordID(discordID)
	if err != nil || user == nil {
		return nil, err
	}
	return getRecentConversationNotesForUser(guildID, user.UserID, limit)
}

func getConversationNotesForGuildDate(guildID, date string) ([]InteractionNote, error) {
	rows, err := database.Query(`
		SELECT id, guild_id, COALESCE(channel_id, ''), note_type, title, summary, source_note_ids, note_date, created_at
		FROM interaction_notes
		WHERE guild_id = ?
		  AND note_type = ?
		  AND note_date = ?
		ORDER BY created_at ASC
	`, guildID, noteTypeConversation, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []InteractionNote
	for rows.Next() {
		note, err := scanInteractionNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attachNoteParticipants(notes)
}

func GetRecentGuildNotes(guildID string, limit int) ([]InteractionNote, error) {
	rows, err := database.Query(`
		SELECT id, guild_id, COALESCE(channel_id, ''), note_type, title, summary, source_note_ids, note_date, created_at
		FROM interaction_notes
		WHERE guild_id = ?
		ORDER BY note_date DESC, created_at DESC
		LIMIT ?
	`, guildID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var notes []InteractionNote
	for rows.Next() {
		note, err := scanInteractionNote(rows)
		if err != nil {
			return nil, err
		}
		notes = append(notes, note)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return attachNoteParticipants(notes)
}

func CountGuildNotes(guildID string) int {
	if database == nil {
		return 0
	}
	var count int
	_ = database.QueryRow("SELECT COUNT(*) FROM interaction_notes WHERE guild_id = ?", guildID).Scan(&count)
	return count
}

func RenderNotesMarkdown(notes []InteractionNote) string {
	var sb strings.Builder
	for _, note := range notes {
		label := "Conversation"
		if note.NoteType == noteTypeTopicCluster {
			label = "Topic"
		}
		sb.WriteString(fmt.Sprintf("**%s** [%s] %s\n", label, safeDate(note.NoteDate), note.Title))
		sb.WriteString("- " + note.Summary + "\n\n")
	}
	return strings.TrimSpace(sb.String())
}

func attachNoteParticipants(notes []InteractionNote) ([]InteractionNote, error) {
	for i := range notes {
		participantIDs, err := listNoteParticipants(notes[i].ID)
		if err != nil {
			return nil, err
		}
		notes[i].ParticipantUserIDs = participantIDs
	}
	return notes, nil
}

func getAffectedNoteIDsForUser(guildID string, userID int64) ([]int64, error) {
	rows, err := database.Query(`
		SELECT n.id
		FROM interaction_notes n
		JOIN note_participants np ON np.note_id = n.id
		WHERE n.guild_id = ?
		  AND np.participant_user_id = ?
	`, guildID, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var noteIDs []int64
	for rows.Next() {
		var noteID int64
		if err := rows.Scan(&noteID); err != nil {
			return nil, err
		}
		noteIDs = append(noteIDs, noteID)
	}
	return dedupeInt64s(noteIDs), rows.Err()
}

func inClause(format string, ids []int64) (string, []any) {
	parts := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		parts[i] = "?"
		args[i] = id
	}
	return fmt.Sprintf(format, strings.Join(parts, ",")), args
}
