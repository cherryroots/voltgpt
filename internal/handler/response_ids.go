package handler

import (
	"database/sql"
	"fmt"

	"voltgpt/internal/db"
)

// lookupResponseID returns the OpenAI response ID stored for a bot message, or
// an empty string when none exists.
func lookupResponseID(discordMsgID string) (string, error) {
	if db.DB == nil {
		return "", fmt.Errorf("database is not initialized")
	}

	var responseID string
	err := db.DB.QueryRow(
		"SELECT openai_response_id FROM response_ids WHERE discord_message_id = ?",
		discordMsgID,
	).Scan(&responseID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return responseID, nil
}

func storeResponseID(discordMsgID, openaiResponseID string) error {
	if db.DB == nil {
		return fmt.Errorf("database is not initialized")
	}

	_, err := db.DB.Exec(
		`INSERT INTO response_ids (discord_message_id, openai_response_id)
		 VALUES (?, ?)
		 ON CONFLICT(discord_message_id) DO UPDATE SET openai_response_id = excluded.openai_response_id`,
		discordMsgID,
		openaiResponseID,
	)
	return err
}
