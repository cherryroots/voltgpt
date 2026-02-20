package memory

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/genai"
)

const bufferWindow = 30 * time.Second

type messageBuffer struct {
	discordID string
	username  string
	messageID string // latest message ID for DB reference
	messages  []string
	timer     *time.Timer
}

var (
	buffers   = make(map[string]*messageBuffer)
	buffersMu sync.Mutex
)

// Extract buffers a message for fact extraction. Messages from the same user
// are batched together over a 30-second sliding window for better context.
func Extract(discordID, username, messageID, text string) {
	if !enabled {
		return
	}

	buffersMu.Lock()
	defer buffersMu.Unlock()

	buf, exists := buffers[discordID]
	if !exists {
		buf = &messageBuffer{
			discordID: discordID,
			username:  username,
		}
		buffers[discordID] = buf
	}

	buf.messages = append(buf.messages, text)
	buf.messageID = messageID
	buf.username = username

	// Reset or start the sliding window timer
	if buf.timer != nil {
		buf.timer.Stop()
	}
	buf.timer = time.AfterFunc(bufferWindow, func() {
		flushBuffer(discordID)
	})
}

// flushBuffer processes all buffered messages for a user.
func flushBuffer(discordID string) {
	buffersMu.Lock()
	buf, exists := buffers[discordID]
	if !exists {
		buffersMu.Unlock()
		return
	}
	messages := buf.messages
	username := buf.username
	messageID := buf.messageID
	delete(buffers, discordID)
	buffersMu.Unlock()

	combined := strings.Join(messages, "\n")
	if len(combined) < minMessageLength {
		return
	}

	ctx := context.Background()

	userID, err := upsertUser(discordID, username)
	if err != nil {
		log.Printf("memory: failed to upsert user %s: %v", discordID, err)
		return
	}

	facts, err := extractFacts(ctx, username, combined)
	if err != nil {
		log.Printf("memory: extraction failed for buffered messages from %s: %v", username, err)
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
		"Only extract facts the user explicitly states about THEMSELVES — never attribute actions, properties, or " +
		"possessions mentioned about other people to the message author. " +
		"Do not infer or assume facts that are not directly stated. " +
		"Ignore temporary states like current mood or what they're doing right now. " +
		"Each fact must be independent and non-overlapping — never return a fact that is a less specific version of another. " +
		"If multiple facts cover the same topic, return only the single most detailed one. " +
		"Prefer one high-quality fact over multiple shallow ones. " +
		"If no long-term facts can be extracted, return an empty array.\n" +
		"The user's name is " + username + ".\n" +
		"Messages: " + text

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
