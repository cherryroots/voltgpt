package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

type profileLLMResponse struct {
	MarkDirty     bool          `json:"mark_dirty"`
	Bio           []ProfileFact `json:"bio"`
	Interests     []ProfileFact `json:"interests"`
	Skills        []ProfileFact `json:"skills"`
	Opinions      []ProfileFact `json:"opinions"`
	Relationships []ProfileFact `json:"relationships"`
	Other         []ProfileFact `json:"other"`
}

var profileResponseSchema = shared.ResponseFormatJSONSchemaJSONSchemaParam{
	Name:        "guild_user_profile",
	Description: oa.String("A guild-scoped user profile with source note citations"),
	Strict:      oa.Bool(true),
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"mark_dirty":    map[string]any{"type": "boolean"},
			"bio":           profileFactArraySchema(),
			"interests":     profileFactArraySchema(),
			"skills":        profileFactArraySchema(),
			"opinions":      profileFactArraySchema(),
			"relationships": profileFactArraySchema(),
			"other":         profileFactArraySchema(),
		},
		"required": []string{"mark_dirty", "bio", "interests", "skills", "opinions", "relationships", "other"},
	},
}

func profileFactArraySchema() map[string]any {
	return map[string]any{
		"type": "array",
		"items": map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
				"source_note_ids": map[string]any{
					"type":  "array",
					"items": map[string]any{"type": "integer"},
				},
			},
			"required": []string{"text", "source_note_ids"},
		},
	}
}

func GetGuildUserProfile(guildID, discordID string) (*GuildUserProfile, error) {
	user, err := getUserIdentityByDiscordID(discordID)
	if err != nil || user == nil {
		return nil, err
	}
	return getGuildUserProfileByUserID(guildID, user.UserID)
}

func getGuildUserProfileByUserID(guildID string, userID int64) (*GuildUserProfile, error) {
	var (
		profile       = emptyProfile(guildID, userID)
		bio           string
		interests     string
		skills        string
		opinions      string
		relationships string
		other         string
		isDirty       int
		lastRebuild   sql.NullString
	)

	err := database.QueryRow(`
		SELECT bio, interests, skills, opinions, relationships, other, is_dirty, updated_at, last_full_rebuild_at
		FROM guild_user_profiles
		WHERE guild_id = ? AND user_id = ?
	`, guildID, userID).Scan(
		&bio, &interests, &skills, &opinions, &relationships, &other,
		&isDirty, &profile.UpdatedAt, &lastRebuild,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if profile.Bio, err = unmarshalProfileFacts(bio); err != nil {
		return nil, err
	}
	if profile.Interests, err = unmarshalProfileFacts(interests); err != nil {
		return nil, err
	}
	if profile.Skills, err = unmarshalProfileFacts(skills); err != nil {
		return nil, err
	}
	if profile.Opinions, err = unmarshalProfileFacts(opinions); err != nil {
		return nil, err
	}
	if profile.Relationships, err = unmarshalProfileFacts(relationships); err != nil {
		return nil, err
	}
	if profile.Other, err = unmarshalProfileFacts(other); err != nil {
		return nil, err
	}
	profile.IsDirty = isDirty == 1
	if lastRebuild.Valid {
		profile.LastFullRebuildAt = lastRebuild.String
	}
	return &profile, nil
}

func writeGuildUserProfile(profile GuildUserProfile) error {
	profile, compaction := compactProfileWithStats(profile)
	if compaction.changed() {
		log.Printf(
			"memory: profile_compaction guild=%s user=%d %s %s facts_dropped=%d text_facts_trimmed=%d source_refs_trimmed=%d",
			profile.GuildID,
			profile.UserID,
			profileCountLogFields("before_", compaction.Before),
			profileCountLogFields("after_", compaction.After),
			compaction.factsDropped(),
			compaction.TextFactsTrimmed,
			compaction.SourceRefsTrimmed,
		)
	}

	var lastFullRebuild any
	if strings.TrimSpace(profile.LastFullRebuildAt) != "" {
		lastFullRebuild = profile.LastFullRebuildAt
	}

	_, err := database.Exec(`
		INSERT INTO guild_user_profiles (
			guild_id, user_id, bio, interests, skills, opinions, relationships, other, is_dirty, updated_at, last_full_rebuild_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP, ?)
		ON CONFLICT(guild_id, user_id) DO UPDATE SET
			bio = excluded.bio,
			interests = excluded.interests,
			skills = excluded.skills,
			opinions = excluded.opinions,
			relationships = excluded.relationships,
			other = excluded.other,
			is_dirty = excluded.is_dirty,
			updated_at = CURRENT_TIMESTAMP,
			last_full_rebuild_at = excluded.last_full_rebuild_at
	`, profile.GuildID, profile.UserID,
		marshalProfileFacts(profile.Bio),
		marshalProfileFacts(profile.Interests),
		marshalProfileFacts(profile.Skills),
		marshalProfileFacts(profile.Opinions),
		marshalProfileFacts(profile.Relationships),
		marshalProfileFacts(profile.Other),
		boolToInt(profile.IsDirty),
		lastFullRebuild,
	)
	return err
}

func markProfileDirty(guildID string, userID int64) error {
	_, err := database.Exec(`
		INSERT INTO guild_user_profiles (guild_id, user_id, is_dirty)
		VALUES (?, ?, 1)
		ON CONFLICT(guild_id, user_id) DO UPDATE SET
			is_dirty = 1,
			updated_at = CURRENT_TIMESTAMP
	`, guildID, userID)
	return err
}

func MarkGuildUserProfileDirty(guildID, discordID, username, displayName string) error {
	if database == nil {
		return fmt.Errorf("memory system not initialized")
	}

	user, err := getUserIdentityByDiscordID(discordID)
	if err != nil {
		return err
	}

	var userID int64
	if user == nil {
		userID, _, err = upsertUser(discordID, username, displayName)
		if err != nil {
			return err
		}
	} else {
		userID = user.UserID
	}

	if err := markProfileDirty(guildID, userID); err != nil {
		return err
	}

	log.Printf("memory: profile_dirty reason=admin_user guild=%s user=%d", guildID, userID)
	return nil
}

func MarkAllGuildProfilesDirty(guildID string) (int64, error) {
	if database == nil {
		return 0, fmt.Errorf("memory system not initialized")
	}

	res, err := database.Exec(`
		UPDATE guild_user_profiles
		SET is_dirty = 1,
		    updated_at = CURRENT_TIMESTAMP
		WHERE guild_id = ?
	`, guildID)
	if err != nil {
		return 0, err
	}

	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}

	log.Printf("memory: profile_dirty reason=admin_guild guild=%s count=%d", guildID, count)
	return count, nil
}

func clearProfileDirty(guildID string, userID int64) error {
	_, err := database.Exec(`
		UPDATE guild_user_profiles
		SET is_dirty = 0, updated_at = CURRENT_TIMESTAMP
		WHERE guild_id = ? AND user_id = ?
	`, guildID, userID)
	return err
}

func DeleteGuildUserProfile(guildID, discordID string) error {
	_, err := database.Exec(`
		DELETE FROM guild_user_profiles
		WHERE guild_id = ?
		  AND user_id = (SELECT id FROM users WHERE discord_id = ?)
	`, guildID, discordID)
	return err
}

func DeleteAllGuildProfiles(guildID string) (int64, error) {
	res, err := database.Exec("DELETE FROM guild_user_profiles WHERE guild_id = ?", guildID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func listDirtyProfileUserIDs(guildID string) ([]int64, error) {
	rows, err := database.Query(`
		SELECT user_id
		FROM guild_user_profiles
		WHERE guild_id = ? AND is_dirty = 1
		ORDER BY updated_at ASC
	`, guildID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var userIDs []int64
	for rows.Next() {
		var userID int64
		if err := rows.Scan(&userID); err != nil {
			return nil, err
		}
		userIDs = append(userIDs, userID)
	}
	return userIDs, rows.Err()
}

func RenderProfileMarkdown(profile *GuildUserProfile, fallbackName string) string {
	if profile == nil {
		return ""
	}

	sourceNoteIDs := make([]int64, 0)
	for _, section := range [][]ProfileFact{
		profile.Bio,
		profile.Interests,
		profile.Skills,
		profile.Opinions,
		profile.Relationships,
		profile.Other,
	} {
		for _, fact := range section {
			sourceNoteIDs = append(sourceNoteIDs, fact.SourceNoteIDs...)
		}
	}
	noteRefs, err := getNotesByIDs(sourceNoteIDs)
	if err != nil {
		log.Printf("memory: failed to resolve profile note refs: %v", err)
		noteRefs = map[int64]InteractionNote{}
	}

	var sb strings.Builder
	if fallbackName != "" {
		sb.WriteString(fmt.Sprintf("**Memory for %s**\n", fallbackName))
	}
	if profile.IsDirty {
		sb.WriteString("_Profile is marked dirty; recent notes may be fresher until maintenance rebuilds it._\n\n")
	}

	renderProfileSectionMarkdown(&sb, "Bio", profile.Bio, noteRefs)
	renderProfileSectionMarkdown(&sb, "Interests", profile.Interests, noteRefs)
	renderProfileSectionMarkdown(&sb, "Skills", profile.Skills, noteRefs)
	renderProfileSectionMarkdown(&sb, "Opinions", profile.Opinions, noteRefs)
	renderProfileSectionMarkdown(&sb, "Relationships", profile.Relationships, noteRefs)
	renderProfileSectionMarkdown(&sb, "Other", profile.Other, noteRefs)

	return strings.TrimSpace(sb.String())
}

func renderProfileSectionMarkdown(sb *strings.Builder, title string, facts []ProfileFact, noteRefs map[int64]InteractionNote) {
	if len(facts) == 0 {
		return
	}
	sb.WriteString("**" + title + "**\n")
	for _, fact := range facts {
		sb.WriteString("- " + fact.Text)
		if citation := renderCitation(noteRefs, fact.SourceNoteIDs); citation != "" {
			sb.WriteString(" [" + citation + "]")
		}
		sb.WriteByte('\n')
	}
	sb.WriteByte('\n')
}

const profileSectionGuidance = `

Section guidance:
- bio: stable identity or background facts such as role, location, long-lived life context, or enduring setup.
- interests: recurring hobbies, fandoms, media tastes, topics, and activities the user repeatedly enjoys.
- skills: demonstrated abilities, expertise, or tools the user can actually use well; do not treat aspirations as skills.
- opinions: durable preferences, stances, likes, dislikes, or repeated comparisons the user seems to hold.
- relationships: stable connections to other people or groups, including family, friends, coworkers, teammates, or recurring guild dynamics.
- other: durable constraints, habits, routines, recurring projects, or important facts that do not fit the sections above.
- Leave a section empty rather than forcing weak evidence into it.
- Never duplicate the same fact across multiple sections.
- Do not store one-off plans, temporary status, current-session chatter, jokes, or throwaway remarks unless they clearly recur or define the user.`

func profileCompactnessRule(prefix string) string {
	return fmt.Sprintf(
		"- %s compact: bio<=%d, interests<=%d, skills<=%d, opinions<=%d, relationships<=%d, other<=%d.",
		prefix,
		profileMaxBioFacts,
		profileMaxInterestFacts,
		profileMaxSkillFacts,
		profileMaxOpinionFacts,
		profileMaxRelationshipFacts,
		profileMaxOtherFacts,
	)
}

func profileFactLengthRule() string {
	return fmt.Sprintf(
		"- Keep each fact to %d words or fewer and each source_note_ids array to %d note IDs or fewer.",
		profileMaxFactWords,
		profileMaxSourceNoteIDs,
	)
}

func incrementalProfileSystemPrompt() string {
	return `You update one guild-scoped user profile using a single new conversation note.` + profileSectionGuidance + `

Rules:
- Only keep facts about the target user.
- Never attribute another participant's facts to the target user.
- Return the full updated profile sections, not a patch.
- Keep and merge source_note_ids when facts overlap.
- Use concise third-person facts.
- Prefer durable, high-signal facts over stale or transient trivia.
- Order facts within each section from most durable/high-signal to least.
` + profileCompactnessRule("Keep the profile") + `
` + profileFactLengthRule() + `
- If a new note is weak, one-off, or ambiguous, prefer not to promote it into the profile.
- If the note is too ambiguous or the updated profile would exceed those limits, set mark_dirty=true and leave the sections unchanged.
- Do not invent facts.`
}

func rebuildProfileSystemPrompt() string {
	return `You rebuild one guild-scoped user profile from conversation notes.` + profileSectionGuidance + `

Rules:
- Only keep facts about the target user.
- Never use topic clusters as source truth.
- Preserve source_note_ids on every fact.
- Prefer concise, merged facts over duplicates.
- Drop stale or transient trivia in favor of durable preferences, recurring patterns, stable relationships, and capabilities.
- Order facts within each section from most durable/high-signal to least.
- Keep only durable, high-signal facts when space is tight.
` + profileCompactnessRule("Keep the rebuilt profile") + `
` + profileFactLengthRule() + `
- Prefer recurring patterns and stable traits over isolated anecdotes.
- If the notes are empty, return empty arrays.
- Do not infer facts that are not supported by the notes.`
}

func incrementalProfileUpdateOpenAI(ctx context.Context, current GuildUserProfile, note InteractionNote, target userIdentity) (profileUpdateResult, error) {
	prompt := fmt.Sprintf(
		"Target user: %s (%s)\nCurrent profile JSON:\n%s\n\nConversation note JSON:\n%s",
		target.EffectiveName(),
		target.DiscordID,
		jsonString(current),
		jsonString(note),
	)

	responseText, err := generateJSON(ctx, incrementalUpdateModel, incrementalProfileSystemPrompt(), prompt, profileResponseSchema)
	if err != nil {
		return profileUpdateResult{}, err
	}

	var payload profileLLMResponse
	if err := json.Unmarshal([]byte(responseText), &payload); err != nil {
		return profileUpdateResult{}, err
	}
	if payload.MarkDirty {
		return profileUpdateResult{Profile: current, MarkDirty: true}, nil
	}

	next := cloneProfile(current)
	next.GuildID = current.GuildID
	next.UserID = current.UserID
	next.Bio = normalizeProfileFacts(payload.Bio)
	next.Interests = normalizeProfileFacts(payload.Interests)
	next.Skills = normalizeProfileFacts(payload.Skills)
	next.Opinions = normalizeProfileFacts(payload.Opinions)
	next.Relationships = normalizeProfileFacts(payload.Relationships)
	next.Other = normalizeProfileFacts(payload.Other)
	next.IsDirty = false

	return profileUpdateResult{Profile: next}, nil
}

func rebuildGuildProfileOpenAI(ctx context.Context, guildID string, target userIdentity, notes []InteractionNote) (GuildUserProfile, error) {
	if len(notes) == 0 {
		return emptyProfile(guildID, target.UserID), nil
	}

	prompt := fmt.Sprintf(
		"Target user: %s (%s)\nConversation notes JSON:\n%s",
		target.EffectiveName(),
		target.DiscordID,
		jsonString(notes),
	)

	responseText, err := generateJSON(ctx, fullRebuildModel, rebuildProfileSystemPrompt(), prompt, profileResponseSchema)
	if err != nil {
		return GuildUserProfile{}, err
	}

	var payload profileLLMResponse
	if err := json.Unmarshal([]byte(responseText), &payload); err != nil {
		return GuildUserProfile{}, err
	}

	profile := emptyProfile(guildID, target.UserID)
	profile.Bio = normalizeProfileFacts(payload.Bio)
	profile.Interests = normalizeProfileFacts(payload.Interests)
	profile.Skills = normalizeProfileFacts(payload.Skills)
	profile.Opinions = normalizeProfileFacts(payload.Opinions)
	profile.Relationships = normalizeProfileFacts(payload.Relationships)
	profile.Other = normalizeProfileFacts(payload.Other)
	profile.IsDirty = false
	profile.LastFullRebuildAt = time.Now().UTC().Format(time.RFC3339Nano)
	return profile, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
