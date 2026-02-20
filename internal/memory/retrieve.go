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

// RetrieveMultiUser fetches facts for multiple users concurrently and formats
// them into an XML block for injection into the system prompt.
func RetrieveMultiUser(query string, users map[string]string) string {
	if !enabled || len(users) == 0 {
		return ""
	}

	var mu sync.Mutex
	var wg sync.WaitGroup
	var allFacts []UserFacts

	for discordID, username := range users {
		wg.Add(1)
		go func(did, uname string) {
			defer wg.Done()
			facts := Retrieve(query, did)
			if len(facts) > 0 {
				mu.Lock()
				allFacts = append(allFacts, UserFacts{Username: uname, Facts: facts})
				mu.Unlock()
			}
		}(discordID, username)
	}

	wg.Wait()

	if len(allFacts) == 0 {
		return ""
	}

	return formatFactsXML(allFacts)
}

// formatFactsXML formats user facts into the XML block for system prompt injection.
// Each fact includes a date prefix so the chat model has temporal context.
func formatFactsXML(allFacts []UserFacts) string {
	var sb strings.Builder
	sb.WriteString("<background_facts>\n")
	for _, uf := range allFacts {
		sb.WriteString(fmt.Sprintf("<user name=%q>\n", uf.Username))
		for _, fact := range uf.Facts {
			date := strings.SplitN(fact.CreatedAt, " ", 2)[0] // "2026-02-20 15:49:47" -> "2026-02-20"
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", date, fact.Text))
		}
		sb.WriteString("</user>\n")
	}
	sb.WriteString("</background_facts>")
	return sb.String()
}
