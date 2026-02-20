package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"google.golang.org/genai"
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

const consolidationSystemPrompt = `You are a memory consolidation AI. Your job is to compare a NEW fact with an OLD fact and decide how to update the database.

Rules:
1. REINFORCE: Use this if the new fact is essentially the same information as the old fact, just reconfirming what we already know (e.g., 'Uses AutoCAD' vs 'Uses AutoCAD'). The existing fact's confidence will be increased.
2. INVALIDATE: Use this if the new fact completely replaces or contradicts the old fact (e.g., 'Lives in NY' vs 'Moved to LA').
3. MERGE: Use this if the facts are about the exact same topic/entity and can be combined into a single, richer sentence (e.g., 'Owns an Xbox' + 'Bought a PS5' -> 'Owns both an Xbox and a PS5').
4. KEEP: Use this if the facts are about completely different topics and should both exist independently (e.g., 'Likes cooking' vs 'Works at Google').

If you choose MERGE, you must provide the newly combined fact. For REINFORCE, KEEP, or INVALIDATE, leave the merged text blank.`

// consolidateAndStore embeds a new fact, checks for similar existing facts,
// and either inserts, merges, or invalidates as appropriate.
func consolidateAndStore(ctx context.Context, userID int64, messageID, factText string) error {
	embedding, err := embed(ctx, factText)
	if err != nil {
		return fmt.Errorf("embedding failed: %w", err)
	}

	similar, err := findSimilarFacts(userID, embedding)
	if err != nil {
		return fmt.Errorf("similarity search failed: %w", err)
	}

	// No similar facts — insert directly
	if len(similar) == 0 {
		return insertFact(userID, messageID, factText, embedding)
	}

	// Check each similar fact for consolidation
	for _, sf := range similar {
		action, err := decideAction(ctx, sf.FactText, factText)
		if err != nil {
			log.Printf("memory: consolidation decision failed for fact %d: %v", sf.ID, err)
			continue
		}

		switch action.Action {
		case "REINFORCE":
			// Same info restated — bump confidence, don't insert
			if err := reinforceFact(sf.ID); err != nil {
				log.Printf("memory: failed to reinforce fact %d: %v", sf.ID, err)
			}
			return nil

		case "INVALIDATE":
			return replaceFact(sf.ID, userID, messageID, factText, embedding)

		case "MERGE":
			if action.MergedText == "" {
				log.Printf("memory: MERGE action returned empty merged_text, falling back to REINFORCE")
				return nil
			}
			mergedEmbedding, err := embed(ctx, action.MergedText)
			if err != nil {
				return fmt.Errorf("merge embedding failed: %w", err)
			}
			return replaceFact(sf.ID, userID, messageID, action.MergedText, mergedEmbedding)

		case "KEEP":
			// Different topics — continue checking other similar facts
			continue
		}
	}

	// No similar fact claimed this knowledge — insert as new
	return insertFact(userID, messageID, factText, embedding)
}

// findSimilarFacts queries vec_facts for active facts belonging to the same user
// that are within the distance threshold.
func findSimilarFacts(userID int64, embedding []float32) ([]similarFact, error) {
	rows, err := database.Query(`
		SELECT f.id, f.fact_text, vf.distance
		FROM vec_facts vf
		JOIN facts f ON f.id = vf.fact_id
		WHERE vf.embedding MATCH ?
		  AND k = ?
		  AND f.user_id = ?
		  AND f.is_active = 1
		ORDER BY vf.distance
	`, serializeFloat32(embedding), similarityLimit, userID)
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
		if sf.Distance < distanceThreshold {
			results = append(results, sf)
		}
	}
	return results, rows.Err()
}

// decideAction calls Gemini to decide whether to REINFORCE, INVALIDATE, MERGE, or KEEP two facts.
func decideAction(ctx context.Context, oldFact, newFact string) (*consolidationAction, error) {
	prompt := fmt.Sprintf("OLD: %q\nNEW: %q", oldFact, newFact)

	t := float32(0.1)
	resp, err := client.Models.GenerateContent(ctx, generationModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(consolidationSystemPrompt, genai.RoleModel),
			Temperature:       &t,
			ResponseMIMEType:  "application/json",
			ResponseSchema: &genai.Schema{
				Type: genai.TypeObject,
				Properties: map[string]*genai.Schema{
					"action": {
						Type: genai.TypeString,
						Enum: []string{"REINFORCE", "INVALIDATE", "MERGE", "KEEP"},
					},
					"merged_text": {
						Type: genai.TypeString,
					},
				},
				Required: []string{"action", "merged_text"},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return &consolidationAction{Action: "REINFORCE"}, nil
	}

	var responseText string
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			responseText += part.Text
		}
	}

	var action consolidationAction
	if err := json.Unmarshal([]byte(responseText), &action); err != nil {
		return nil, err
	}

	return &action, nil
}
