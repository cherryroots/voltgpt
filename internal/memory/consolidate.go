package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

type consolidationAction struct {
	Action     string `json:"action"`
	MergedText string `json:"merged_text"`
}

type similarFact struct {
	ID       int64
	FactText string
	Distance float64
}

const consolidationSystemPrompt = `You compare a NEW fact with an OLD fact about the same user and decide how to update the memory database.

Actions:

1. REINFORCE — The new fact restates what we already know. Same meaning, possibly different wording.
   Examples:
   - OLD: "Uses AutoCAD" / NEW: "Works with AutoCAD" → REINFORCE (same tool, rephrased)
   - OLD: "Likes pizza" / NEW: "Enjoys eating pizza" → REINFORCE (same preference)
   - OLD: "Plays guitar" / NEW: "Plays guitar" → REINFORCE (exact duplicate)

2. INVALIDATE — The new fact contradicts or replaces the old fact. The old fact is no longer true.
   Examples:
   - OLD: "Lives in New York" / NEW: "Moved to Los Angeles" → INVALIDATE
   - OLD: "Works at Google" / NEW: "Left Google and joined Apple" → INVALIDATE
   - OLD: "Is single" / NEW: "Got married" → INVALIDATE
   Do NOT invalidate for temporary states: OLD "Lives in Tokyo" / NEW "Visiting Paris" → KEEP (visiting is temporary)

3. MERGE — The facts are about the same topic and can be combined into a single richer fact.
   Examples:
   - OLD: "Owns an Xbox" / NEW: "Bought a PS5" → MERGE: "Owns both an Xbox and a PS5"
   - OLD: "Studies computer science" / NEW: "Studies at MIT" → MERGE: "Studies computer science at MIT"
   - OLD: "Has a dog" / NEW: "Dog's name is Bento" → MERGE: "Has a dog named Bento"
   - OLD: "Uses Python" / NEW: "Knows JavaScript" → MERGE: "Knows Python and JavaScript" (same domain: programming languages)

4. KEEP — The facts are about different topics and should both exist independently.
   Examples:
   - OLD: "Likes cooking" / NEW: "Works at Google" → KEEP
   - OLD: "Plays guitar" / NEW: "Owns a cat" → KEEP

If you choose MERGE, provide the combined fact in merged_text. For all other actions, leave merged_text blank.`

var consolidationResponseSchema = shared.ResponseFormatJSONSchemaJSONSchemaParam{
	Name:        "memory_consolidation",
	Description: oa.String("How a new fact should update an existing memory fact"),
	Strict:      oa.Bool(true),
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{"REINFORCE", "INVALIDATE", "MERGE", "KEEP"},
			},
			"merged_text": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"action", "merged_text"},
	},
}

var (
	embedFunc        = embed
	decideActionFunc = decideAction
)

// consolidateAndStore embeds a new fact, checks for similar existing facts,
// and either inserts, merges, or invalidates as appropriate.
func consolidateAndStore(ctx context.Context, userID int64, messageID, factText string) error {
	embedding, err := embedFunc(ctx, factText)
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	similar, err := findSimilarFacts(userID, embedding)
	if err != nil {
		return fmt.Errorf("similarity search failed: %w", err)
	}
	if len(similar) == 0 {
		// OpenAI embeddings are less tightly clustered than the previous Gemini ones for
		// some update-style facts ("lives in X" -> "moved to Y"). When the strict
		// threshold misses, allow a bounded relaxed search instead of falling back to
		// arbitrary nearest neighbors.
		similar, err = findFallbackSimilarFacts(userID, embedding)
		if err != nil {
			return fmt.Errorf("fallback similarity search failed: %w", err)
		}
	}

	// No similar facts — insert directly
	if len(similar) == 0 {
		return insertFact(userID, messageID, factText, embedding)
	}

	// Check each similar fact for consolidation
	reinforced := false
	for _, sf := range similar {
		action, err := decideActionFunc(ctx, sf.FactText, factText)
		if err != nil {
			log.Printf("memory: consolidation decision failed for fact %d: %v", sf.ID, err)
			continue
		}

		switch action.Action {
		case "REINFORCE":
			// Same info restated — bump confidence, don't insert.
			// Continue loop to check remaining facts for contradictions.
			if err := reinforceFact(sf.ID); err != nil {
				log.Printf("memory: failed to reinforce fact %d: %v", sf.ID, err)
			}
			reinforced = true

		case "INVALIDATE":
			return replaceFact(sf.ID, userID, messageID, factText, embedding)

		case "MERGE":
			if action.MergedText == "" {
				log.Printf("memory: MERGE action returned empty merged_text, inserting as new fact")
				return insertFact(userID, messageID, factText, embedding)
			}
			mergedEmbedding, err := embedFunc(ctx, action.MergedText)
			if err != nil {
				return fmt.Errorf("merge embedding failed: %w", err)
			}
			return replaceFact(sf.ID, userID, messageID, action.MergedText, mergedEmbedding)

		case "KEEP":
			// Different topics — continue checking other similar facts
			continue
		}
	}

	if reinforced {
		return nil
	}
	// No similar fact claimed this knowledge — insert as new
	return insertFact(userID, messageID, factText, embedding)
}

// findSimilarFacts queries vec_facts for active facts belonging to the same user
// that are within the distance threshold.
func findSimilarFacts(userID int64, embedding []float32) ([]similarFact, error) {
	return findFactsWithinDistance(userID, embedding, distanceThreshold, similarityLimit)
}

func findFallbackSimilarFacts(userID int64, embedding []float32) ([]similarFact, error) {
	return findFactsWithinDistance(userID, embedding, consolidationFallbackThreshold, similarityLimit)
}

func findFactsWithinDistance(userID int64, embedding []float32, maxDistance float64, limit int) ([]similarFact, error) {
	rows, err := database.Query(`
		SELECT f.id, f.fact_text, vec_distance_cosine(vf.embedding, ?) AS distance
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		WHERE f.user_id = ?
		  AND f.is_active = 1
		ORDER BY distance
		LIMIT ?
	`, serializeFloat32(embedding), userID, limit*distanceQueryMultiplier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var results []similarFact
	for rows.Next() {
		var sf similarFact
		if err := rows.Scan(&sf.ID, &sf.FactText, &sf.Distance); err != nil {
			return nil, err
		}
		if sf.Distance > maxDistance {
			break
		}
		results = append(results, sf)
		if len(results) >= limit {
			break
		}
	}
	return results, rows.Err()
}

// decideAction calls OpenAI to decide whether to REINFORCE, INVALIDATE, MERGE, or KEEP two facts.
func decideAction(ctx context.Context, oldFact, newFact string) (*consolidationAction, error) {
	prompt := fmt.Sprintf("OLD: %q\nNEW: %q", oldFact, newFact)

	responseText, err := generateJSON(ctx, consolidationSystemPrompt, prompt, consolidationResponseSchema)
	if err != nil {
		return nil, err
	}

	var action consolidationAction
	if err := json.Unmarshal([]byte(responseText), &action); err != nil {
		return nil, err
	}

	return &action, nil
}
