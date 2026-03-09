package memory

import (
	"context"
	"fmt"
	"html"
	"log"
	"sort"
	"strings"
)

type noteMatch struct {
	Note     InteractionNote
	Distance float64
}

func BuildPromptContext(req RetrieveRequest) string {
	if !enabled || database == nil {
		return ""
	}
	if strings.TrimSpace(req.GuildID) == "" || strings.TrimSpace(req.Query) == "" {
		return ""
	}

	ctx := context.Background()
	embedding, err := embedText(ctx, req.Query)
	if err != nil {
		log.Printf("memory: retrieval embedding failed: %v", err)
		return ""
	}

	topics, err := searchRelevantNotes(req.GuildID, req.ChannelID, noteTypeTopicCluster, embedding, topicRetrievalLimit)
	if err != nil {
		log.Printf("memory: topic retrieval failed: %v", err)
	}

	notes, err := searchRelevantNotes(req.GuildID, req.ChannelID, noteTypeConversation, embedding, conversationRetrievalLimit)
	if err != nil {
		log.Printf("memory: conversation retrieval failed: %v", err)
	}

	selectedNotes := make(map[int64]InteractionNote)
	for _, note := range notes {
		selectedNotes[note.ID] = note
	}

	selectedUsers := collectRequestedUsers(req.ConversationUsers, req.MentionedUsers)
	extraCandidates := collectExtraUserCandidates(notes, topics, selectedUsers)
	for _, userID := range extraCandidates {
		if len(selectedUsers) >= len(req.ConversationUsers)+mentionedProfileLimit+extraProfileLimit {
			break
		}
		if _, ok := selectedUsers[userID]; ok {
			continue
		}
		selectedUsers[userID] = struct{}{}
	}

	var renderedUsers []renderedUser
	for userID := range selectedUsers {
		user, err := getUserIdentityByID(userID)
		if err != nil || user == nil {
			continue
		}

		profile, err := getGuildUserProfileByUserID(req.GuildID, userID)
		if err != nil {
			log.Printf("memory: failed to load guild profile for user %d: %v", userID, err)
			continue
		}
		if profile != nil && !profile.IsDirty && profileHasContent(profile) {
			renderedUsers = append(renderedUsers, renderedUser{
				Name:    user.EffectiveName(),
				Profile: profile,
			})
			continue
		}

		fallbackNotes, err := getRecentConversationNotesForUser(req.GuildID, userID, recentUserFallbackNoteLimit)
		if err != nil {
			log.Printf("memory: failed to load fallback notes for user %d: %v", userID, err)
			continue
		}
		for _, note := range fallbackNotes {
			selectedNotes[note.ID] = note
		}
		if enabled {
			go func(guildID string, userID int64) {
				if err := rebuildOneGuildProfile(guildID, userID); err != nil {
					log.Printf("memory: background profile rebuild failed for user %d: %v", userID, err)
				}
			}(req.GuildID, userID)
		}
	}

	renderedNotes := mapValues(selectedNotes)
	sort.Slice(renderedNotes, func(i, j int) bool {
		if renderedNotes[i].NoteDate == renderedNotes[j].NoteDate {
			return renderedNotes[i].CreatedAt > renderedNotes[j].CreatedAt
		}
		return renderedNotes[i].NoteDate > renderedNotes[j].NoteDate
	})

	if len(renderedUsers) == 0 && len(topics) == 0 && len(renderedNotes) == 0 {
		return ""
	}

	return renderPromptContext(renderedUsers, topics, renderedNotes)
}

func searchRelevantNotes(guildID, channelID, noteType string, embedding []float32, limit int) ([]InteractionNote, error) {
	rows, err := database.Query(`
		SELECT n.id, n.guild_id, COALESCE(n.channel_id, ''), n.note_type, n.title, n.summary, n.source_note_ids, n.note_date, n.created_at,
		       vec_distance_cosine(v.embedding, ?) AS distance
		FROM vec_notes v
		JOIN interaction_notes n ON n.id = v.note_id
		WHERE n.guild_id = ?
		  AND n.note_type = ?
		ORDER BY
			CASE WHEN COALESCE(n.channel_id, '') = ? THEN 0 ELSE 1 END,
			distance ASC,
			n.note_date DESC,
			n.created_at DESC
		LIMIT ?
	`, serializeFloat32(embedding), guildID, noteType, channelID, limit*retrievalCandidateMultiplier)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var strictMatches []noteMatch
	var fallbackMatches []noteMatch
	for rows.Next() {
		note, distance, err := scanNoteMatch(rows)
		if err != nil {
			return nil, err
		}
		if distance <= strictRetrievalDistance && len(strictMatches) < limit {
			strictMatches = append(strictMatches, noteMatch{Note: note, Distance: distance})
			continue
		}
		if distance <= fallbackRetrievalDistance && len(fallbackMatches) < limit {
			fallbackMatches = append(fallbackMatches, noteMatch{Note: note, Distance: distance})
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	matches := strictMatches
	if len(matches) == 0 {
		matches = fallbackMatches
	}

	notes := make([]InteractionNote, 0, len(matches))
	for _, match := range matches {
		notes = append(notes, match.Note)
	}
	return attachNoteParticipants(notes)
}

func scanNoteMatch(scanner interface {
	Scan(dest ...any) error
}) (InteractionNote, float64, error) {
	var (
		note          InteractionNote
		sourceNoteIDs string
		distance      float64
	)
	err := scanner.Scan(
		&note.ID,
		&note.GuildID,
		&note.ChannelID,
		&note.NoteType,
		&note.Title,
		&note.Summary,
		&sourceNoteIDs,
		&note.NoteDate,
		&note.CreatedAt,
		&distance,
	)
	if err != nil {
		return InteractionNote{}, 0, err
	}
	note.SourceNoteIDs, err = parseInt64Slice(sourceNoteIDs)
	if err != nil {
		return InteractionNote{}, 0, err
	}
	return note, distance, nil
}

func collectRequestedUsers(conversationUsers, mentionedUsers map[string]string) map[int64]struct{} {
	selected := make(map[int64]struct{})

	for _, discordID := range sortedDiscordIDs(conversationUsers) {
		user, err := getUserIdentityByDiscordID(discordID)
		if err == nil && user != nil {
			selected[user.UserID] = struct{}{}
		}
	}

	mentionedCount := 0
	for _, discordID := range sortedDiscordIDs(mentionedUsers) {
		if mentionedCount >= mentionedProfileLimit {
			break
		}
		user, err := getUserIdentityByDiscordID(discordID)
		if err == nil && user != nil {
			if _, exists := selected[user.UserID]; !exists {
				mentionedCount++
			}
			selected[user.UserID] = struct{}{}
		}
	}

	return selected
}

func collectExtraUserCandidates(notes, topics []InteractionNote, selected map[int64]struct{}) []int64 {
	seen := make(map[int64]struct{})
	var candidates []int64
	for _, note := range append(append([]InteractionNote{}, topics...), notes...) {
		for _, userID := range note.ParticipantUserIDs {
			if _, exists := selected[userID]; exists {
				continue
			}
			if _, exists := seen[userID]; exists {
				continue
			}
			seen[userID] = struct{}{}
			candidates = append(candidates, userID)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	return candidates
}

func sortedDiscordIDs(users map[string]string) []string {
	ids := make([]string, 0, len(users))
	for discordID := range users {
		ids = append(ids, discordID)
	}
	sort.Strings(ids)
	return ids
}

type renderedUser struct {
	Name    string
	Profile *GuildUserProfile
}

func renderPromptContext(users []renderedUser, topics, notes []InteractionNote) string {
	sourceNoteIDs := make([]int64, 0)
	for _, user := range users {
		for _, section := range [][]ProfileFact{
			user.Profile.Bio,
			user.Profile.Interests,
			user.Profile.Skills,
			user.Profile.Opinions,
			user.Profile.Relationships,
			user.Profile.Other,
		} {
			for _, fact := range section {
				sourceNoteIDs = append(sourceNoteIDs, fact.SourceNoteIDs...)
			}
		}
	}
	noteRefs, err := getNotesByIDs(sourceNoteIDs)
	if err != nil {
		log.Printf("memory: failed to resolve prompt citations: %v", err)
		noteRefs = map[int64]InteractionNote{}
	}

	sort.Slice(users, func(i, j int) bool { return users[i].Name < users[j].Name })

	var sb strings.Builder
	sb.WriteString("<background_facts>\n")
	for _, user := range users {
		sb.WriteString(fmt.Sprintf("<user name=\"%s\">\n", html.EscapeString(user.Name)))
		renderProfileSectionXML(&sb, "Bio", user.Profile.Bio, noteRefs)
		renderProfileSectionXML(&sb, "Interests", user.Profile.Interests, noteRefs)
		renderProfileSectionXML(&sb, "Skills", user.Profile.Skills, noteRefs)
		renderProfileSectionXML(&sb, "Opinions", user.Profile.Opinions, noteRefs)
		renderProfileSectionXML(&sb, "Relationships", user.Profile.Relationships, noteRefs)
		renderProfileSectionXML(&sb, "Other", user.Profile.Other, noteRefs)
		sb.WriteString("</user>\n")
	}

	if len(topics) > 0 {
		sb.WriteString("<topics>\n")
		for _, topic := range topics {
			sb.WriteString(fmt.Sprintf("- [%s] %s - %s\n", safeDate(topic.NoteDate), xmlText(topic.Title), xmlText(topic.Summary)))
		}
		sb.WriteString("</topics>\n")
	}

	if len(notes) > 0 {
		sb.WriteString("<notes>\n")
		for _, note := range notes {
			sb.WriteString(fmt.Sprintf("- [%s] %s - %s\n", safeDate(note.NoteDate), xmlText(note.Title), xmlText(note.Summary)))
		}
		sb.WriteString("</notes>\n")
	}
	sb.WriteString("</background_facts>")
	return sb.String()
}

func renderProfileSectionXML(sb *strings.Builder, title string, facts []ProfileFact, noteRefs map[int64]InteractionNote) {
	if len(facts) == 0 {
		return
	}
	sb.WriteString(title + ":\n")
	for _, fact := range facts {
		sb.WriteString("- " + xmlText(fact.Text))
		if citation := renderCitation(noteRefs, fact.SourceNoteIDs); citation != "" {
			sb.WriteString(" [" + xmlText(citation) + "]")
		}
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
}

func renderCitation(noteRefs map[int64]InteractionNote, ids []int64) string {
	ids = dedupeInt64s(ids)
	if len(ids) == 0 {
		return ""
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		note, ok := noteRefs[id]
		if !ok {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s, %s", note.Title, safeDate(note.NoteDate)))
		if len(parts) >= 2 {
			break
		}
	}
	return strings.Join(parts, "; ")
}

func xmlText(s string) string {
	return html.EscapeString(s)
}

func safeDate(s string) string {
	if len(s) >= 10 {
		return s[:10]
	}
	return s
}

func mapValues(notes map[int64]InteractionNote) []InteractionNote {
	out := make([]InteractionNote, 0, len(notes))
	for _, note := range notes {
		out = append(out, note)
	}
	return out
}
