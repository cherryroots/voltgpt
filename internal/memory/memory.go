// Package memory provides a vector-backed long-term memory system.
// It extracts facts from Discord messages, consolidates them via
// semantic similarity, and retrieves relevant facts for RAG.
package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"log"
	"math"
	"os"

	"google.golang.org/genai"
)

const (
	embeddingModel    = "text-embedding-004"
	generationModel   = "gemini-2.0-flash"
	similarityLimit   = 3
	retrievalLimit    = 5
	minMessageLength  = 10
	distanceThreshold = float64(0.35)
)

var (
	database *sql.DB
	client   *genai.Client
	enabled  bool
)

func Init(db *sql.DB) {
	database = db

	apiKey := os.Getenv("MEMORY_GEMINI_TOKEN")
	if apiKey == "" {
		log.Println("MEMORY_GEMINI_TOKEN is not set — memory system disabled")
		return
	}

	ctx := context.Background()
	var err error
	client, err = genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		log.Printf("Failed to create memory Gemini client: %v", err)
		return
	}

	enabled = true
	log.Println("Memory system initialized")
}

// embed calls the Gemini embedding API and returns a float32 vector.
func embed(ctx context.Context, text string) ([]float32, error) {
	resp, err := client.Models.EmbedContent(ctx, embeddingModel, genai.Text(text), nil)
	if err != nil {
		return nil, err
	}
	if len(resp.Embeddings) == 0 {
		return nil, fmt.Errorf("embedding API returned no embeddings")
	}
	return resp.Embeddings[0].Values, nil
}

// serializeFloat32 converts a float32 slice to a little-endian byte slice
// for sqlite-vec queries.
func serializeFloat32(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

// upsertUser inserts a user if they don't exist and returns their ID.
func upsertUser(discordID, username string) (int64, error) {
	_, err := database.Exec(
		"INSERT OR IGNORE INTO users (discord_id, username) VALUES (?, ?)",
		discordID, username,
	)
	if err != nil {
		return 0, err
	}

	// Update username in case it changed
	_, err = database.Exec(
		"UPDATE users SET username = ? WHERE discord_id = ?",
		username, discordID,
	)
	if err != nil {
		return 0, err
	}

	var id int64
	err = database.QueryRow("SELECT id FROM users WHERE discord_id = ?", discordID).Scan(&id)
	return id, err
}

// insertFact inserts a new fact and its embedding in a transaction.
func insertFact(userID int64, messageID, factText string, embedding []float32) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	res, err := tx.Exec(
		"INSERT INTO facts (user_id, original_message_id, fact_text) VALUES (?, ?, ?)",
		userID, messageID, factText,
	)
	if err != nil {
		return err
	}

	factID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		"INSERT INTO vec_facts (fact_id, embedding) VALUES (?, ?)",
		factID, serializeFloat32(embedding),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// replaceFact atomically deactivates an old fact and inserts a new one with its embedding.
func replaceFact(oldFactID, userID int64, messageID, factText string, embedding []float32) error {
	tx, err := database.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("UPDATE facts SET is_active = 0 WHERE id = ?", oldFactID)
	if err != nil {
		return err
	}

	res, err := tx.Exec(
		"INSERT INTO facts (user_id, original_message_id, fact_text) VALUES (?, ?, ?)",
		userID, messageID, factText,
	)
	if err != nil {
		return err
	}

	factID, err := res.LastInsertId()
	if err != nil {
		return err
	}

	_, err = tx.Exec(
		"INSERT INTO vec_facts (fact_id, embedding) VALUES (?, ?)",
		factID, serializeFloat32(embedding),
	)
	if err != nil {
		return err
	}

	return tx.Commit()
}

// TotalFacts returns the count of active facts for logging at startup.
func TotalFacts() int {
	if database == nil {
		return 0
	}
	var count int
	database.QueryRow("SELECT COUNT(*) FROM facts WHERE is_active = 1").Scan(&count)
	return count
}
