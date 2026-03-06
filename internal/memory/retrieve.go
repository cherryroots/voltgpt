package memory

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
)

// RetrievedFact holds a fact with its timestamp for temporal context.
type RetrievedFact struct {
	Text      string
	CreatedAt string
}

// GeneralFact holds a fact from any user for cross-user retrieval.
type GeneralFact struct {
	Username  string
	Text      string
	CreatedAt string
}

// UserFacts holds retrieved facts for a single user.
type UserFacts struct {
	Username string
	Facts    []RetrievedFact
}

// Retrieve fetches the top relevant active facts for a user,
// filtered to those within retrievalDistanceThreshold.
func Retrieve(query string, discordID string) []RetrievedFact {
	if !enabled || strings.TrimSpace(query) == "" {
		return nil
	}

	ctx := context.Background()

	embedding, err := embedFunc(ctx, query)
	if err != nil {
		log.Printf("memory: retrieval embedding failed: %v", err)
		return nil
	}

	return retrieveForUserWithEmbedding(discordID, embedding)
}

func retrieveForUserWithEmbedding(discordID string, embedding []float32) []RetrievedFact {
	facts, err := retrieveFactsForUserWithinDistance(discordID, embedding, retrievalDistanceThreshold, retrievalLimit)
	if err != nil {
		log.Printf("memory: retrieval query failed: %v", err)
		return nil
	}
	if len(facts) == 0 {
		facts, err = retrieveFactsForUserWithinDistance(discordID, embedding, retrievalFallbackThreshold, retrievalLimit)
		if err != nil {
			log.Printf("memory: retrieval fallback query failed: %v", err)
			return nil
		}
	}
	return facts
}

func retrieveFactsForUserWithinDistance(discordID string, embedding []float32, maxDistance float64, limit int) ([]RetrievedFact, error) {
	rows, err := database.Query(`
		SELECT f.fact_text, f.created_at, vec_distance_cosine(vf.embedding, ?) AS distance
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		JOIN users u ON u.id = f.user_id
		WHERE u.discord_id = ?
		  AND f.is_active = 1
		ORDER BY distance
		LIMIT ?
	`, serializeFloat32(embedding), discordID, limit*distanceQueryMultiplier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var facts []RetrievedFact
	for rows.Next() {
		var f RetrievedFact
		var distance float64
		if err := rows.Scan(&f.Text, &f.CreatedAt, &distance); err != nil {
			return nil, err
		}
		if distance > maxDistance {
			break
		}
		facts = append(facts, f)
		if len(facts) >= limit {
			break
		}
	}
	return facts, rows.Err()
}

// RetrieveGeneral fetches facts from any user that are semantically relevant
// to the query, excluding specified discord IDs to avoid duplicating per-user facts.
func RetrieveGeneral(query string, excludeDiscordIDs map[string]bool) []GeneralFact {
	if !enabled || strings.TrimSpace(query) == "" {
		return nil
	}

	ctx := context.Background()

	embedding, err := embedFunc(ctx, query)
	if err != nil {
		log.Printf("memory: general retrieval embedding failed: %v", err)
		return nil
	}

	return retrieveGeneralWithEmbedding(embedding, excludeDiscordIDs)
}

func retrieveGeneralWithEmbedding(embedding []float32, excludeDiscordIDs map[string]bool) []GeneralFact {
	facts, err := retrieveGeneralFactsWithinDistance(embedding, excludeDiscordIDs, retrievalDistanceThreshold, generalRetrievalLimit)
	if err != nil {
		log.Printf("memory: general retrieval query failed: %v", err)
		return nil
	}
	if len(facts) == 0 {
		facts, err = retrieveGeneralFactsWithinDistance(embedding, excludeDiscordIDs, retrievalFallbackThreshold, generalRetrievalLimit)
		if err != nil {
			log.Printf("memory: general retrieval fallback query failed: %v", err)
			return nil
		}
	}
	return facts
}

func retrieveGeneralFactsWithinDistance(embedding []float32, excludeDiscordIDs map[string]bool, maxDistance float64, limit int) ([]GeneralFact, error) {
	rows, err := database.Query(`
		SELECT u.username, u.discord_id, f.fact_text, f.created_at, vec_distance_cosine(vf.embedding, ?) AS distance
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		JOIN users u ON u.id = f.user_id
		WHERE f.is_active = 1
		ORDER BY distance
		LIMIT ?
	`, serializeFloat32(embedding), (limit+len(excludeDiscordIDs))*distanceQueryMultiplier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var facts []GeneralFact
	for rows.Next() {
		var gf GeneralFact
		var discordID string
		var distance float64
		if err := rows.Scan(&gf.Username, &discordID, &gf.Text, &gf.CreatedAt, &distance); err != nil {
			return nil, err
		}
		if distance > maxDistance {
			break // results are ordered by distance; no point scanning further
		}
		if !excludeDiscordIDs[discordID] {
			facts = append(facts, gf)
			if len(facts) >= limit {
				break
			}
		}
	}
	return facts, rows.Err()
}

// RetrieveMultiUser fetches facts for multiple users concurrently and formats
// them into an XML block for injection into the system prompt.
func RetrieveMultiUser(query string, users map[string]string) string {
	if !enabled || len(users) == 0 || strings.TrimSpace(query) == "" {
		return ""
	}

	ctx := context.Background()
	embedding, err := embedFunc(ctx, query)
	if err != nil {
		log.Printf("memory: retrieval embedding failed: %v", err)
		return ""
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var userFacts []UserFacts
	var generalFacts []GeneralFact

	// Per-user retrieval
	for discordID, username := range users {
		wg.Add(1)
		go func(did, uname string) {
			defer wg.Done()
			facts := retrieveForUserWithEmbedding(did, embedding)
			if len(facts) > 0 {
				mu.Lock()
				userFacts = append(userFacts, UserFacts{Username: uname, Facts: facts})
				mu.Unlock()
			}
		}(discordID, username)
	}

	// General cross-user retrieval (concurrent with per-user)
	excludeIDs := make(map[string]bool, len(users))
	for discordID := range users {
		excludeIDs[discordID] = true
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		gf := retrieveGeneralWithEmbedding(embedding, excludeIDs)
		if len(gf) > 0 {
			mu.Lock()
			generalFacts = gf
			mu.Unlock()
		}
	}()

	wg.Wait()

	if len(userFacts) == 0 && len(generalFacts) == 0 {
		return ""
	}

	return formatFactsXML(userFacts, generalFacts)
}

// safeDate returns the YYYY-MM-DD prefix of a datetime string, or the full string if shorter.
func safeDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

// formatFactsXML formats user and general facts into the XML block for system prompt injection.
// Each fact includes a date prefix so the chat model has temporal context.
func formatFactsXML(userFacts []UserFacts, generalFacts []GeneralFact) string {
	var sb strings.Builder
	sb.WriteString("<background_facts>\n")
	sb.WriteString("<note>The <user> sections contain facts about the people you are currently talking to. " +
		"The <general> section contains facts about OTHER people that may be topically relevant — " +
		"never attribute general facts to the person you are speaking with.</note>\n")
	for _, uf := range userFacts {
		sb.WriteString(fmt.Sprintf("<user name=%q>\n", uf.Username))
		for _, fact := range uf.Facts {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", safeDate(fact.CreatedAt), fact.Text))
		}
		sb.WriteString("</user>\n")
	}
	if len(generalFacts) > 0 {
		sb.WriteString("<general>\n")
		for _, gf := range generalFacts {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", safeDate(gf.CreatedAt), gf.Text))
		}
		sb.WriteString("</general>\n")
	}
	sb.WriteString("</background_facts>")
	return sb.String()
}
