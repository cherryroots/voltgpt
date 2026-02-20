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

	// ExtractionBlacklist contains channel IDs where fact extraction is disabled.
	ExtractionBlacklist = map[string]bool{
		"850179179281776670": true,
	}
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

const extractionSystemPrompt = `You extract long-term facts about a user from their Discord messages. Output third-person facts using their name.

What to extract:
- Preferences and opinions (likes, dislikes, favorites)
- Skills, tools, and expertise (languages, software, hobbies)
- Biographical info (location, job, education, age)
- Relationships and social context (friends, pets, family)
- Possessions and belongings (devices, vehicles, collections)
- Habits and routines (exercise, diet, sleep schedule)

What to ignore:
- Temporary states: mood, current activity, what they're doing right now
- Greetings, jokes, memes, filler ("lol", "brb", reactions)
- Facts about other people — if the user talks about someone else, do not attribute those traits to the message author
- Anything not explicitly stated — never infer or assume

Rules:
- Each fact must use the user's name (e.g., "Alex likes sushi")
- One fact per distinct topic — never return overlapping facts
- If multiple facts cover the same topic, return only the most detailed one
- Prefer one high-quality fact over multiple shallow ones
- Return an empty array if nothing qualifies

Examples:
- "I just got a mass 2 monitor" → ["Alex owns a Mass 2 monitor."]
- "me and jake went climbing yesterday, it was sick" → ["Alex goes rock climbing."]
- "he was using the onboard intel gpu instead of his gpu" → [] (talking about someone else)
- "lol yeah" → [] (no factual content)
- "I moved to Austin last year and started working at Dell" → ["Alex lives in Austin and works at Dell."]`

// extractFacts calls Gemini to extract long-term facts from buffered messages.
func extractFacts(ctx context.Context, username, text string) ([]string, error) {
	prompt := "The user's name is " + username + ".\n\nMessages:\n" + text

	t := float32(0.1)
	resp, err := client.Models.GenerateContent(ctx, generationModel,
		genai.Text(prompt),
		&genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(extractionSystemPrompt, genai.RoleModel),
			Temperature:       &t,
			ResponseMIMEType:  "application/json",
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
