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

// Retrieve fetches the top relevant active facts for a user.
func Retrieve(query string, discordID string) []RetrievedFact {
	if !enabled {
		return nil
	}

	ctx := context.Background()

	embedding, err := embed(ctx, query)
	if err != nil {
		log.Printf("memory: retrieval embedding failed: %v", err)
		return nil
	}

	rows, err := database.Query(`
		SELECT f.fact_text, f.created_at
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		JOIN users u ON u.id = f.user_id
		WHERE vf.embedding MATCH ?
		  AND k = ?
		  AND u.discord_id = ?
		  AND f.is_active = 1
		ORDER BY vf.distance
	`, serializeFloat32(embedding), retrievalLimit, discordID)
	if err != nil {
		log.Printf("memory: retrieval query failed: %v", err)
		return nil
	}
	defer rows.Close()

	var facts []RetrievedFact
	for rows.Next() {
		var f RetrievedFact
		if err := rows.Scan(&f.Text, &f.CreatedAt); err != nil {
			log.Printf("memory: retrieval scan failed: %v", err)
			continue
		}
		facts = append(facts, f)
	}
	if err := rows.Err(); err != nil {
		log.Printf("memory: retrieval rows error: %v", err)
	}

	return facts
}

// RetrieveGeneral fetches facts from any user that are semantically relevant
// to the query, excluding specified discord IDs to avoid duplicating per-user facts.
func RetrieveGeneral(query string, excludeDiscordIDs map[string]bool) []GeneralFact {
	if !enabled {
		return nil
	}

	ctx := context.Background()

	embedding, err := embed(ctx, query)
	if err != nil {
		log.Printf("memory: general retrieval embedding failed: %v", err)
		return nil
	}

	// Fetch more than we need since we filter out excluded users in Go
	rows, err := database.Query(`
		SELECT u.username, u.discord_id, f.fact_text, f.created_at
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		JOIN users u ON u.id = f.user_id
		WHERE vf.embedding MATCH ?
		  AND k = ?
		  AND f.is_active = 1
		ORDER BY vf.distance
	`, serializeFloat32(embedding), generalRetrievalLimit+len(excludeDiscordIDs))
	if err != nil {
		log.Printf("memory: general retrieval query failed: %v", err)
		return nil
	}
	defer rows.Close()

	var facts []GeneralFact
	for rows.Next() {
		var gf GeneralFact
		var discordID string
		if err := rows.Scan(&gf.Username, &discordID, &gf.Text, &gf.CreatedAt); err != nil {
			log.Printf("memory: general retrieval scan failed: %v", err)
			continue
		}
		if !excludeDiscordIDs[discordID] {
			facts = append(facts, gf)
			if len(facts) >= generalRetrievalLimit {
				break
			}
		}
	}
	if err := rows.Err(); err != nil {
		log.Printf("memory: general retrieval rows error: %v", err)
	}

	return facts
}

// RetrieveMultiUser fetches facts for multiple users concurrently and formats
// them into an XML block for injection into the system prompt.
func RetrieveMultiUser(query string, users map[string]string) string {
	if !enabled || len(users) == 0 {
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
			facts := Retrieve(query, did)
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
		gf := RetrieveGeneral(query, excludeIDs)
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

// formatFactsXML formats user and general facts into the XML block for system prompt injection.
// Each fact includes a date prefix so the chat model has temporal context.
func formatFactsXML(userFacts []UserFacts, generalFacts []GeneralFact) string {
	var sb strings.Builder
	sb.WriteString("<background_facts>\n")
	for _, uf := range userFacts {
		sb.WriteString(fmt.Sprintf("<user name=%q>\n", uf.Username))
		for _, fact := range uf.Facts {
			date := fact.CreatedAt[:10]
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", date, fact.Text))
		}
		sb.WriteString("</user>\n")
	}
	if len(generalFacts) > 0 {
		sb.WriteString("<general>\n")
		for _, gf := range generalFacts {
			date := gf.CreatedAt[:10]
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", date, gf.Text))
		}
		sb.WriteString("</general>\n")
	}
	sb.WriteString("</background_facts>")
	return sb.String()
}
