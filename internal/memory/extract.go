package memory

import (
	"context"
	"encoding/json"
	"log"

	"google.golang.org/genai"
)

// Extract asynchronously extracts long-term facts from a message.
// Call this as a goroutine: go memory.Extract(...)
func Extract(discordID, username, messageID, text string) {
	if !enabled || len(text) < minMessageLength {
		return
	}

	ctx := context.Background()

	userID, err := upsertUser(discordID, username)
	if err != nil {
		log.Printf("memory: failed to upsert user %s: %v", discordID, err)
		return
	}

	facts, err := extractFacts(ctx, username, text)
	if err != nil {
		log.Printf("memory: extraction failed for message %s: %v", messageID, err)
		return
	}

	for _, fact := range facts {
		if err := consolidateAndStore(ctx, userID, messageID, fact); err != nil {
			log.Printf("memory: consolidation failed for fact %q: %v", fact, err)
		}
	}
}

// extractFacts calls Gemini to extract long-term facts from a message.
func extractFacts(ctx context.Context, username, text string) ([]string, error) {
	prompt := "Extract long-term, third-person facts about the user from this message. " +
		"Ignore temporary states like current mood or what they're doing right now. " +
		"If no long-term facts can be extracted, return an empty array.\n" +
		"The user's name is " + username + ".\n" +
		"Message: " + text

	t := float32(0.1)
	resp, err := client.Models.GenerateContent(ctx, generationModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			Temperature:      &t,
			ResponseMIMEType: "application/json",
			ResponseSchema: &genai.Schema{
				Type: genai.TypeArray,
				Items: &genai.Schema{
					Type: genai.TypeString,
				},
			},
		},
	)
	if err != nil {
		return nil, err
	}

	if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
		return nil, nil
	}

	var responseText string
	for _, part := range resp.Candidates[0].Content.Parts {
		if part.Text != "" {
			responseText += part.Text
		}
	}

	var facts []string
	if err := json.Unmarshal([]byte(responseText), &facts); err != nil {
		return nil, err
	}

	return facts, nil
}
