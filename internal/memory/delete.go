package memory

import (
	"context"
	"fmt"
	"log"
)

func DeleteUserMemory(guildID, discordID string) error {
	if database == nil {
		return fmt.Errorf("memory system not initialized")
	}

	user, err := getUserIdentityByDiscordID(discordID)
	if err != nil || user == nil {
		return err
	}

	purgeBufferedUserMessages(guildID, discordID)

	if err := DeleteGuildUserProfile(guildID, discordID); err != nil {
		return err
	}

	noteIDs, err := getAffectedNoteIDsForUser(guildID, user.UserID)
	if err != nil {
		return err
	}

	affectedDays := make(map[string]struct{})
	affectedUsers := make(map[int64]struct{})
	for _, noteID := range noteIDs {
		note, err := getNoteByID(noteID)
		if err != nil {
			return err
		}
		if note == nil {
			continue
		}
		affectedDays[note.NoteDate] = struct{}{}

		survivors := make([]int64, 0, len(note.ParticipantUserIDs))
		for _, participantID := range note.ParticipantUserIDs {
			if participantID == user.UserID {
				continue
			}
			survivors = append(survivors, participantID)
			affectedUsers[participantID] = struct{}{}
		}

		if len(survivors) == 0 {
			if err := deleteNoteAndVector(note.ID); err != nil {
				return err
			}
			continue
		}

		if _, err := database.Exec(`
			DELETE FROM note_participants
			WHERE note_id = ? AND participant_user_id = ?
		`, note.ID, user.UserID); err != nil {
			return err
		}
		if err := redactMixedParticipantNote(*note); err != nil {
			return err
		}
	}

	for participantID := range affectedUsers {
		if err := markProfileDirty(guildID, participantID); err != nil {
			log.Printf("memory: failed to mark profile dirty for user %d: %v", participantID, err)
		}
	}

	for date := range affectedDays {
		if err := invalidateClusterOutput(guildID, date); err != nil {
			return err
		}
	}

	return nil
}

func DeleteAllGuildMemory(guildID string) error {
	if database == nil {
		return fmt.Errorf("memory system not initialized")
	}

	purgeGuildBuffers(guildID)

	rows, err := database.Query("SELECT id FROM interaction_notes WHERE guild_id = ?", guildID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var noteIDs []int64
	for rows.Next() {
		var noteID int64
		if err := rows.Scan(&noteID); err != nil {
			return err
		}
		noteIDs = append(noteIDs, noteID)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	for _, noteID := range noteIDs {
		if _, err := tx.Exec("DELETE FROM vec_notes WHERE note_id = ?", noteID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM interaction_notes WHERE guild_id = ?", guildID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM guild_user_profiles WHERE guild_id = ?", guildID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM channel_buffers WHERE guild_id = ?", guildID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM memory_job_runs WHERE guild_id = ?", guildID); err != nil {
		return err
	}

	return tx.Commit()
}

func redactMixedParticipantNote(note InteractionNote) error {
	title := "Redacted conversation"
	summary := "A multi-participant conversation note was partially redacted after one participant's memory was deleted."
	return rewriteNote(note.ID, title, summary)
}

func rewriteNote(noteID int64, title, summary string) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE interaction_notes
		SET title = ?, summary = ?
		WHERE id = ?
	`, title, summary, noteID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM vec_notes WHERE note_id = ?", noteID); err != nil {
		return err
	}

	embedding, err := embedText(context.Background(), title+"\n"+summary)
	if err == nil {
		if _, err := tx.Exec(
			"INSERT INTO vec_notes (note_id, embedding) VALUES (?, ?)",
			noteID,
			serializeFloat32(embedding),
		); err != nil {
			return err
		}
	}

	return tx.Commit()
}

func invalidateClusterOutput(guildID, date string) error {
	if err := deleteStaleClusterNotes(guildID, date); err != nil {
		return err
	}
	_, err := database.Exec(`
		DELETE FROM memory_job_runs
		WHERE guild_id = ? AND job_date = ? AND phase = ?
	`, guildID, date, jobPhaseCluster)
	return err
}
