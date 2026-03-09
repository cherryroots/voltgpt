package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

type channelBuffer struct {
	ChannelID string
	GuildID   string
	Messages  []bufMsg
	StartedAt time.Time
	UpdatedAt time.Time
	timer     *time.Timer
}

var (
	buffers   = make(map[string]*channelBuffer)
	buffersMu sync.Mutex
)

func stopAllBufferTimers() {
	buffersMu.Lock()
	defer buffersMu.Unlock()

	for channelID, buf := range buffers {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		delete(buffers, channelID)
	}
}

func purgeBufferedUserMessages(guildID, discordID string) {
	buffersMu.Lock()
	defer buffersMu.Unlock()

	for channelID, buf := range buffers {
		if guildID != "" && buf.GuildID != guildID {
			continue
		}

		filtered := buf.Messages[:0]
		for _, msg := range buf.Messages {
			if msg.DiscordID == discordID {
				continue
			}
			filtered = append(filtered, msg)
		}
		buf.Messages = filtered

		if len(buf.Messages) == 0 {
			if buf.timer != nil {
				buf.timer.Stop()
			}
			delete(buffers, channelID)
			continue
		}

		resetBufferTimerLocked(buf)
	}
}

func purgeGuildBuffers(guildID string) {
	buffersMu.Lock()
	defer buffersMu.Unlock()

	for channelID, buf := range buffers {
		if buf.GuildID != guildID {
			continue
		}
		if buf.timer != nil {
			buf.timer.Stop()
		}
		delete(buffers, channelID)
	}
}

var conversationNoteResponseSchema = shared.ResponseFormatJSONSchemaJSONSchemaParam{
	Name:        "conversation_note",
	Description: oa.String("A concise title and summary for a buffered Discord conversation"),
	Strict:      oa.Bool(true),
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"title":   map[string]any{"type": "string"},
			"summary": map[string]any{"type": "string"},
		},
		"required": []string{"title", "summary"},
	},
}

const conversationNoteSystemPrompt = `You summarize a Discord channel or thread buffer into one conversation note.

Rules:
- Summarize what happened across the whole buffer, not one message.
- Keep the title short and specific.
- Keep the summary concrete and grounded in what people actually said.
- Mention participants only when needed for clarity.
- Do not include bot messages.
- Do not invent facts outside the buffer.`

func BufferMessage(channelID, guildID, discordID, username, displayName, text, messageID string) {
	if !enabled || database == nil {
		return
	}
	if strings.TrimSpace(channelID) == "" || strings.TrimSpace(guildID) == "" {
		return
	}

	msg := bufMsg{
		DiscordID:   discordID,
		Username:    username,
		DisplayName: displayName,
		Text:        strings.TrimSpace(text),
		MessageID:   messageID,
	}
	if msg.Text == "" {
		return
	}

	for {
		now := time.Now().UTC()
		var stale *channelBuffer
		var full *channelBuffer

		buffersMu.Lock()
		existing := buffers[channelID]
		if existing != nil && now.Sub(existing.StartedAt) >= bufferMaxAge {
			if existing.timer != nil {
				existing.timer.Stop()
			}
			stale = cloneBuffer(existing)
			delete(buffers, channelID)
			buffersMu.Unlock()
			if err := flushBufferData(stale); err != nil {
				log.Printf("memory: max-age flush failed for %s: %v", channelID, err)
			}
			continue
		}

		if existing == nil {
			existing = &channelBuffer{
				ChannelID: channelID,
				GuildID:   guildID,
				StartedAt: now,
			}
			buffers[channelID] = existing
		}

		existing.GuildID = guildID
		existing.UpdatedAt = now
		existing.Messages = append(existing.Messages, msg)
		if len(existing.Messages) >= bufferMaxMessages {
			if existing.timer != nil {
				existing.timer.Stop()
			}
			full = cloneBuffer(existing)
			delete(buffers, channelID)
			buffersMu.Unlock()
			if err := flushBufferData(full); err != nil {
				log.Printf("memory: max-message flush failed for %s: %v", channelID, err)
			}
			return
		}
		resetBufferTimerLocked(existing)
		snapshot := cloneBuffer(existing)
		buffersMu.Unlock()

		if err := saveChannelBuffer(snapshot); err != nil {
			log.Printf("memory: failed to persist channel buffer %s: %v", channelID, err)
		}
		return
	}
}

func saveChannelBuffer(buf *channelBuffer) error {
	if buf == nil {
		return nil
	}
	payload, err := json.Marshal(buf.Messages)
	if err != nil {
		return err
	}

	_, err = database.Exec(`
		INSERT INTO channel_buffers (channel_id, guild_id, messages, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(channel_id) DO UPDATE SET
			guild_id = excluded.guild_id,
			messages = excluded.messages,
			started_at = excluded.started_at,
			updated_at = excluded.updated_at
	`, buf.ChannelID, buf.GuildID, string(payload), buf.StartedAt.UTC().Format(time.RFC3339Nano), buf.UpdatedAt.UTC().Format(time.RFC3339Nano))
	return err
}

func loadChannelBuffers() ([]*channelBuffer, error) {
	rows, err := database.Query(`
		SELECT channel_id, guild_id, messages, started_at, updated_at
		FROM channel_buffers
		ORDER BY updated_at ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var loaded []*channelBuffer
	for rows.Next() {
		var (
			buf        channelBuffer
			rawMsgs    string
			startedRaw string
			updatedRaw string
		)
		if err := rows.Scan(&buf.ChannelID, &buf.GuildID, &rawMsgs, &startedRaw, &updatedRaw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(rawMsgs), &buf.Messages); err != nil {
			return nil, err
		}
		if buf.StartedAt, err = time.Parse(time.RFC3339Nano, startedRaw); err != nil {
			return nil, err
		}
		if buf.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedRaw); err != nil {
			return nil, err
		}
		loaded = append(loaded, &buf)
	}
	return loaded, rows.Err()
}

func deleteChannelBuffer(channelID string) error {
	_, err := database.Exec("DELETE FROM channel_buffers WHERE channel_id = ?", channelID)
	return err
}

func loadAndRestartBuffers() error {
	if !enabled {
		return nil
	}

	loaded, err := loadChannelBuffers()
	if err != nil {
		return err
	}

	now := time.Now().UTC()
	for _, buf := range loaded {
		idleFor := now.Sub(buf.UpdatedAt)
		totalAge := now.Sub(buf.StartedAt)
		if idleFor >= bufferInactivityWindow || totalAge >= bufferMaxAge {
			if err := flushBufferData(buf); err != nil {
				log.Printf("memory: failed to flush reloaded buffer %s: %v", buf.ChannelID, err)
			}
			continue
		}

		buffersMu.Lock()
		resetBufferTimerWithDurationLocked(buf, bufferInactivityWindow-idleFor)
		buffers[buf.ChannelID] = buf
		buffersMu.Unlock()
	}
	return nil
}

func flushChannelBuffer(channelID string) error {
	buffersMu.Lock()
	buf := buffers[channelID]
	if buf != nil {
		if buf.timer != nil {
			buf.timer.Stop()
		}
		delete(buffers, channelID)
	}
	buffersMu.Unlock()

	if buf == nil {
		return nil
	}
	return flushBufferData(cloneBuffer(buf))
}

func flushBufferData(buf *channelBuffer) error {
	if buf == nil {
		return nil
	}
	if err := processBuffer(buf); err != nil {
		restoreBuffer(buf)
		return err
	}
	return deleteChannelBuffer(buf.ChannelID)
}

func processBuffer(buf *channelBuffer) error {
	if visibleContentLen(buf.Messages) < minBufferedContentLength {
		return nil
	}

	ctx := context.Background()
	generated, err := generateConversationNote(ctx, buf.GuildID, buf.ChannelID, buf.Messages)
	if err != nil {
		return err
	}

	participantIDs := make([]int64, 0)
	participants := make([]userIdentity, 0)
	seen := make(map[string]struct{})
	for _, msg := range buf.Messages {
		if _, ok := seen[msg.DiscordID]; ok {
			continue
		}
		seen[msg.DiscordID] = struct{}{}

		userID, _, err := upsertUser(msg.DiscordID, msg.Username, msg.DisplayName)
		if err != nil {
			return fmt.Errorf("upsert participant %s: %w", msg.DiscordID, err)
		}
		participantIDs = append(participantIDs, userID)

		user, err := getUserIdentityByID(userID)
		if err != nil {
			return err
		}
		if user != nil {
			participants = append(participants, *user)
		}
	}

	embedding, err := embedText(ctx, generated.Title+"\n"+generated.Summary)
	if err != nil {
		return err
	}

	note := InteractionNote{
		GuildID:            buf.GuildID,
		ChannelID:          buf.ChannelID,
		NoteType:           noteTypeConversation,
		Title:              strings.TrimSpace(generated.Title),
		Summary:            strings.TrimSpace(generated.Summary),
		NoteDate:           buf.UpdatedAt.UTC().Format(time.DateOnly),
		ParticipantUserIDs: dedupeInt64s(participantIDs),
	}
	note.ID, err = insertNote(note, note.ParticipantUserIDs, embedding)
	if err != nil {
		return err
	}

	for _, participant := range participants {
		current, err := getGuildUserProfileByUserID(note.GuildID, participant.UserID)
		if err != nil {
			log.Printf("memory: failed to load profile for user %d: %v", participant.UserID, err)
			_ = markProfileDirty(note.GuildID, participant.UserID)
			continue
		}
		if current == nil {
			profile := emptyProfile(note.GuildID, participant.UserID)
			current = &profile
		}

		result, err := incrementalProfileUpdate(ctx, *current, note, participant)
		if err != nil {
			log.Printf("memory: incremental profile update failed for user %d: %v", participant.UserID, err)
			_ = markProfileDirty(note.GuildID, participant.UserID)
			continue
		}
		if result.MarkDirty {
			log.Printf(
				"memory: profile_dirty reason=llm_mark_dirty guild=%s user=%d note=%d",
				note.GuildID,
				participant.UserID,
				note.ID,
			)
			_ = markProfileDirty(note.GuildID, participant.UserID)
			continue
		}

		result.Profile.GuildID = note.GuildID
		result.Profile.UserID = participant.UserID
		result.Profile.IsDirty = false
		if profileExceedsHysteresisBudget(result.Profile) {
			counts := countProfileFacts(result.Profile)
			log.Printf(
				"memory: profile_dirty reason=hysteresis_overflow guild=%s user=%d note=%d %s",
				note.GuildID,
				participant.UserID,
				note.ID,
				profileCountLogFields("", counts),
			)
			_ = markProfileDirty(note.GuildID, participant.UserID)
			continue
		}
		if err := writeGuildUserProfile(result.Profile); err != nil {
			log.Printf("memory: failed to write profile for user %d: %v", participant.UserID, err)
			_ = markProfileDirty(note.GuildID, participant.UserID)
			continue
		}
	}

	return nil
}

func restoreBuffer(buf *channelBuffer) {
	buffersMu.Lock()
	defer buffersMu.Unlock()

	copy := cloneBuffer(buf)
	resetBufferTimerLocked(copy)
	buffers[copy.ChannelID] = copy
	if err := saveChannelBuffer(copy); err != nil {
		log.Printf("memory: failed to restore channel buffer %s: %v", copy.ChannelID, err)
	}
}

func resetBufferTimerLocked(buf *channelBuffer) {
	resetBufferTimerWithDurationLocked(buf, bufferInactivityWindow)
}

func resetBufferTimerWithDurationLocked(buf *channelBuffer, d time.Duration) {
	if buf.timer != nil {
		buf.timer.Stop()
	}
	buf.timer = time.AfterFunc(d, func() {
		if err := flushChannelBuffer(buf.ChannelID); err != nil {
			log.Printf("memory: timed buffer flush failed for %s: %v", buf.ChannelID, err)
		}
	})
}

func cloneBuffer(buf *channelBuffer) *channelBuffer {
	if buf == nil {
		return nil
	}
	copy := *buf
	copy.Messages = append([]bufMsg(nil), buf.Messages...)
	copy.timer = nil
	return &copy
}

func visibleContentLen(messages []bufMsg) int {
	total := 0
	for _, msg := range messages {
		total += len(strings.TrimSpace(msg.Text))
	}
	return total
}

func generateConversationNoteOpenAI(ctx context.Context, guildID, channelID string, messages []bufMsg) (generatedConversationNote, error) {
	var transcript strings.Builder
	for _, msg := range messages {
		name := effectiveName("", msg.DisplayName, msg.Username)
		transcript.WriteString(name)
		transcript.WriteString(": ")
		transcript.WriteString(msg.Text)
		transcript.WriteByte('\n')
	}

	prompt := fmt.Sprintf("Guild: %s\nChannel: %s\n\nTranscript:\n%s", guildID, channelID, transcript.String())
	responseText, err := generateJSON(ctx, noteGenerationModel, conversationNoteSystemPrompt, prompt, conversationNoteResponseSchema)
	if err != nil {
		return generatedConversationNote{}, err
	}

	var note generatedConversationNote
	if err := json.Unmarshal([]byte(responseText), &note); err != nil {
		return generatedConversationNote{}, err
	}
	if strings.TrimSpace(note.Title) == "" || strings.TrimSpace(note.Summary) == "" {
		return generatedConversationNote{}, fmt.Errorf("conversation note response missing title or summary")
	}
	return note, nil
}
