package memory

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
)

// UserFacts holds retrieved facts for a single user.
type UserFacts struct {
	Username string
	Facts    []string
}

// Retrieve fetches the top relevant active facts for a user.
func Retrieve(query string, discordID string) []string {
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
		SELECT f.fact_text
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

	var facts []string
	for rows.Next() {
		var fact string
		if err := rows.Scan(&fact); err != nil {
			log.Printf("memory: retrieval scan failed: %v", err)
			continue
		}
		facts = append(facts, fact)
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
func formatFactsXML(allFacts []UserFacts) string {
	var sb strings.Builder
	sb.WriteString("<background_facts>\n")
	for _, uf := range allFacts {
		sb.WriteString(fmt.Sprintf("<user name=%q>\n", uf.Username))
		for _, fact := range uf.Facts {
			sb.WriteString(fmt.Sprintf("- %s\n", fact))
		}
		sb.WriteString("</user>\n")
	}
	sb.WriteString("</background_facts>")
	return sb.String()
}
