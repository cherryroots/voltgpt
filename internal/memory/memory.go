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
	"strings"

	"google.golang.org/genai"
)

const (
	embeddingModel           = "gemini-embedding-001"
	embeddingDimensions      = 768
	generationModel          = "gemini-3-flash-preview"
	similarityLimit          = 3
	retrievalLimit           = 5
	generalRetrievalLimit    = 10
	minMessageLength         = 10
	distanceThreshold        = float64(0.35) // cosine distance; vec_facts uses distance_metric=cosine
	retrievalDistanceThreshold = float64(0.6)  // more permissive than deduplication threshold
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

	go func() {
		if n := reembedMissingFacts(ctx); n > 0 {
			log.Printf("memory: re-embedded %d facts after schema migration", n)
		}
	}()
}

// embed calls the Gemini embedding API and returns a float32 vector
// truncated to embeddingDimensions via the API's OutputDimensionality param.
func embed(ctx context.Context, text string) ([]float32, error) {
	dim := int32(embeddingDimensions)
	resp, err := client.Models.EmbedContent(ctx, embeddingModel, genai.Text(text), &genai.EmbedContentConfig{
		OutputDimensionality: &dim,
	})
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

// upsertUser returns an existing user's ID and their effective display name.
// Uses SELECT-first to avoid burning autoincrement IDs on INSERT OR IGNORE.
// Name priority: preferred_name > display_name > username.
func upsertUser(discordID, username, displayName string) (int64, string, error) {
	var id int64
	var preferredName string
	err := database.QueryRow("SELECT id, preferred_name FROM users WHERE discord_id = ?", discordID).Scan(&id, &preferredName)
	if err == nil {
		// Existing user — update username and display_name
		_, _ = database.Exec("UPDATE users SET username = ?, display_name = ? WHERE id = ?", username, displayName, id)
		return id, effectiveName(preferredName, displayName, username), nil
	}
	if err != sql.ErrNoRows {
		return 0, "", err
	}

	// New user — insert
	res, err := database.Exec(
		"INSERT INTO users (discord_id, username, display_name) VALUES (?, ?, ?)",
		discordID, username, displayName,
	)
	if err != nil {
		return 0, "", err
	}
	id, err = res.LastInsertId()
	return id, effectiveName("", displayName, username), err
}

// effectiveName returns the best available name: preferred > display > username.
func effectiveName(preferredName, displayName, username string) string {
	if preferredName != "" {
		return preferredName
	}
	if displayName != "" {
		return displayName
	}
	return username
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

// Fact represents a stored fact for display.
type Fact struct {
	ID                 int64
	FactText           string
	CreatedAt          string
	ReinforcementCount int
}

// GetUserFacts returns all active facts for a Discord user.
func GetUserFacts(discordID string) []Fact {
	if database == nil {
		return nil
	}

	rows, err := database.Query(`
		SELECT f.id, f.fact_text, f.created_at, f.reinforcement_count
		FROM facts f
		JOIN users u ON u.id = f.user_id
		WHERE u.discord_id = ? AND f.is_active = 1
		ORDER BY f.reinforcement_count DESC, f.created_at DESC
	`, discordID)
	if err != nil {
		log.Printf("memory: failed to get user facts: %v", err)
		return nil
	}
	defer rows.Close()

	var facts []Fact
	for rows.Next() {
		var f Fact
		if err := rows.Scan(&f.ID, &f.FactText, &f.CreatedAt, &f.ReinforcementCount); err != nil {
			log.Printf("memory: failed to scan fact: %v", err)
			continue
		}
		facts = append(facts, f)
	}
	if err := rows.Err(); err != nil {
		log.Printf("memory: GetUserFacts rows error: %v", err)
	}
	return facts
}

// DeleteUserFacts soft-deletes all facts for a Discord user.
func DeleteUserFacts(discordID string) (int64, error) {
	res, err := database.Exec(`
		UPDATE facts SET is_active = 0
		WHERE user_id = (SELECT id FROM users WHERE discord_id = ?)
		AND is_active = 1
	`, discordID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// DeleteAllFacts soft-deletes all facts.
func DeleteAllFacts() (int64, error) {
	res, err := database.Exec("UPDATE facts SET is_active = 0 WHERE is_active = 1")
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SetPreferredName sets the preferred display name for a user.
// If the user doesn't exist yet, they are created with the given discordID and username.
func SetPreferredName(discordID, username, preferredName string) error {
	if database == nil {
		return fmt.Errorf("memory system not initialized")
	}

	var id int64
	err := database.QueryRow("SELECT id FROM users WHERE discord_id = ?", discordID).Scan(&id)
	if err == sql.ErrNoRows {
		_, err = database.Exec(
			"INSERT INTO users (discord_id, username, preferred_name) VALUES (?, ?, ?)",
			discordID, username, preferredName,
		)
		return err
	}
	if err != nil {
		return err
	}

	_, err = database.Exec("UPDATE users SET preferred_name = ? WHERE id = ?", preferredName, id)
	return err
}

// GetPreferredName returns the current preferred name for a user, or empty string if unset.
func GetPreferredName(discordID string) string {
	if database == nil {
		return ""
	}
	var name string
	database.QueryRow("SELECT preferred_name FROM users WHERE discord_id = ?", discordID).Scan(&name)
	return name
}

// RefreshFactNames replaces the name prefix in all active facts for a user
// with their current effective name. Strips text up to the first space and
// prepends the new name. Embeddings are not recomputed since the semantic
// meaning is nearly identical with only a name change.
func RefreshFactNames(discordID string) (int64, error) {
	if database == nil {
		return 0, fmt.Errorf("memory system not initialized")
	}

	var userID int64
	var username, displayName, preferredName string
	err := database.QueryRow(
		"SELECT id, username, display_name, preferred_name FROM users WHERE discord_id = ?",
		discordID,
	).Scan(&userID, &username, &displayName, &preferredName)
	if err != nil {
		return 0, fmt.Errorf("user not found: %w", err)
	}

	newName := effectiveName(preferredName, displayName, username)

	rows, err := database.Query(
		"SELECT id, fact_text FROM facts WHERE user_id = ? AND is_active = 1",
		userID,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	type factRow struct {
		id   int64
		text string
	}
	var toUpdate []factRow
	for rows.Next() {
		var f factRow
		if err := rows.Scan(&f.id, &f.text); err != nil {
			continue
		}
		// Strip old name (everything before the first space)
		if idx := strings.IndexByte(f.text, ' '); idx != -1 {
			updated := newName + f.text[idx:]
			if updated != f.text {
				toUpdate = append(toUpdate, factRow{id: f.id, text: updated})
			}
		}
	}

	if len(toUpdate) == 0 {
		return 0, nil
	}

	tx, err := database.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare("UPDATE facts SET fact_text = ? WHERE id = ?")
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	var count int64
	for _, f := range toUpdate {
		if _, err := stmt.Exec(f.text, f.id); err != nil {
			log.Printf("memory: failed to update fact %d: %v", f.id, err)
			continue
		}
		count++
	}

	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return count, nil
}

// RefreshAllFactNames refreshes fact name prefixes for every user that has active facts.
func RefreshAllFactNames() (int64, error) {
	if database == nil {
		return 0, fmt.Errorf("memory system not initialized")
	}

	rows, err := database.Query("SELECT discord_id FROM users WHERE id IN (SELECT DISTINCT user_id FROM facts WHERE is_active = 1)")
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			continue
		}
		ids = append(ids, id)
	}

	var total int64
	for _, id := range ids {
		count, err := RefreshFactNames(id)
		if err != nil {
			log.Printf("memory: failed to refresh names for %s: %v", id, err)
			continue
		}
		total += count
	}
	return total, nil
}

// reembedMissingFacts finds active facts that have no entry in vec_facts and re-embeds them.
// Called at startup to recover from vec_facts schema migrations.
func reembedMissingFacts(ctx context.Context) int {
	rows, err := database.Query(`
		SELECT f.id, f.fact_text FROM facts f
		WHERE f.is_active = 1
		AND NOT EXISTS (SELECT 1 FROM vec_facts WHERE fact_id = f.id)
	`)
	if err != nil {
		log.Printf("memory: failed to query facts needing re-embedding: %v", err)
		return 0
	}
	defer rows.Close()

	type factRow struct {
		id   int64
		text string
	}
	var toEmbed []factRow
	for rows.Next() {
		var f factRow
		if err := rows.Scan(&f.id, &f.text); err != nil {
			continue
		}
		toEmbed = append(toEmbed, f)
	}

	var count int
	for _, f := range toEmbed {
		embedding, err := embed(ctx, f.text)
		if err != nil {
			log.Printf("memory: failed to re-embed fact %d: %v", f.id, err)
			continue
		}
		if _, err = database.Exec(
			"INSERT OR REPLACE INTO vec_facts (fact_id, embedding) VALUES (?, ?)",
			f.id, serializeFloat32(embedding),
		); err != nil {
			log.Printf("memory: failed to insert re-embedding for fact %d: %v", f.id, err)
			continue
		}
		count++
	}
	return count
}

// reinforceFact increments the reinforcement counter for a fact.
func reinforceFact(factID int64) error {
	_, err := database.Exec("UPDATE facts SET reinforcement_count = reinforcement_count + 1 WHERE id = ?", factID)
	return err
}

// GetRecentFacts returns recently created active facts for the digest.
func GetRecentFacts(limit int) []struct {
	Username string
	FactText string
} {
	if database == nil {
		return nil
	}

	rows, err := database.Query(`
		SELECT u.username, f.fact_text
		FROM facts f
		JOIN users u ON u.id = f.user_id
		WHERE f.is_active = 1
		ORDER BY f.created_at DESC
		LIMIT ?
	`, limit)
	if err != nil {
		log.Printf("memory: failed to get recent facts: %v", err)
		return nil
	}
	defer rows.Close()

	type recentFact struct {
		Username string
		FactText string
	}
	var facts []struct {
		Username string
		FactText string
	}
	for rows.Next() {
		var f struct {
			Username string
			FactText string
		}
		if err := rows.Scan(&f.Username, &f.FactText); err != nil {
			continue
		}
		facts = append(facts, f)
	}
	return facts
}
