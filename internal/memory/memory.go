package memory

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	openaiapi "voltgpt/internal/apis/openai"

	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

const (
	embeddingModel                            = oa.EmbeddingModelTextEmbedding3Small
	embeddingDimensions                 int64 = 1536
	noteGenerationModel                       = "gpt-5.4-nano"
	noteGenerationReasoning                   = shared.ReasoningEffortHigh
	incrementalUpdateModel                    = "gpt-5.4-nano"
	incrementalUpdateReasoning                = shared.ReasoningEffortHigh
	clusteringModel                           = "gpt-5.4-mini"
	clusteringReasoning                       = shared.ReasoningEffortMedium
	fullRebuildModel                          = "gpt-5.4-mini"
	fullRebuildReasoning                      = shared.ReasoningEffortMedium
	strictRetrievalDistance                   = 0.45
	fallbackRetrievalDistance                 = 0.62
	retrievalCandidateMultiplier              = 12
	topicRetrievalLimit                       = 3
	conversationRetrievalLimit                = 5
	mentionedProfileLimit                     = 3
	extraProfileLimit                         = 2
	recentUserFallbackNoteLimit               = 3
	minBufferedContentLength                  = 100
	minClusterInputNotes                      = 3
	bufferInactivityWindow                    = 40 * time.Minute
	bufferMaxAge                              = 2 * time.Hour
	bufferMaxMessages                         = 100
	maintenanceSchedulerInterval              = 1 * time.Hour
	profileMaxBioFacts                        = 5
	profileMaxInterestFacts                   = 6
	profileMaxSkillFacts                      = 6
	profileMaxOpinionFacts                    = 5
	profileMaxRelationshipFacts               = 6
	profileMaxOtherFacts                      = 5
	profileMaxTotalFacts                      = 33
	profileMaxFactWords                       = 20
	profileMaxSourceNoteIDs                   = 4
	profileHysteresisExtraFacts               = 1
	profileHysteresisExtraTotalFacts          = 4
	profileHysteresisExtraFactWords           = 4
	profileHysteresisExtraSourceNoteIDs       = 1
	noteTypeConversation                      = "conversation"
	noteTypeTopicCluster                      = "topic_cluster"
	jobPhaseCluster                           = "cluster"
	jobPhaseProfileMaintenance                = "profile_maintenance"
	jobStatusRunning                          = "running"
	jobStatusCompleted                        = "completed"
	jobStatusFailed                           = "failed"
)

type ProfileFact struct {
	Text          string  `json:"text"`
	SourceNoteIDs []int64 `json:"source_note_ids"`
}

type GuildUserProfile struct {
	GuildID           string
	UserID            int64
	Bio               []ProfileFact
	Interests         []ProfileFact
	Skills            []ProfileFact
	Opinions          []ProfileFact
	Relationships     []ProfileFact
	Other             []ProfileFact
	IsDirty           bool
	UpdatedAt         string
	LastFullRebuildAt string
}

type InteractionNote struct {
	ID                 int64
	GuildID            string
	ChannelID          string
	NoteType           string
	Title              string
	Summary            string
	SourceNoteIDs      []int64
	NoteDate           string
	CreatedAt          string
	ParticipantUserIDs []int64
}

type bufMsg struct {
	DiscordID   string `json:"discord_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Text        string `json:"text"`
	MessageID   string `json:"message_id"`
}

type RetrieveRequest struct {
	GuildID           string
	ChannelID         string
	Query             string
	ConversationUsers map[string]string
	MentionedUsers    map[string]string
}

type generatedConversationNote struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

type profileUpdateResult struct {
	Profile   GuildUserProfile
	MarkDirty bool
}

type clusterResult struct {
	Title         string  `json:"title"`
	Summary       string  `json:"summary"`
	SourceNoteIDs []int64 `json:"source_note_ids"`
}

type userIdentity struct {
	UserID        int64
	DiscordID     string
	Username      string
	DisplayName   string
	PreferredName string
}

func (u userIdentity) EffectiveName() string {
	return effectiveName(u.PreferredName, u.DisplayName, u.Username)
}

var (
	database *sql.DB
	client   *oa.Client
	enabled  bool

	embedText                = embed
	generateConversationNote = generateConversationNoteOpenAI
	incrementalProfileUpdate = incrementalProfileUpdateOpenAI
	clusterGuildDay          = clusterGuildDayOpenAI
	rebuildGuildProfile      = rebuildGuildProfileOpenAI
	timeNow                  = func() time.Time { return time.Now().UTC() }
)

var (
	lifecycleMu             sync.Mutex
	maintenanceStopCh       chan struct{}
	maintenanceSweepRunning bool
)

func Init(db *sql.DB) {
	database = db
	client = nil
	enabled = false
	stopMaintenanceScheduler()

	c, err := openaiapi.GetMemoryClient()
	if err != nil {
		log.Printf("memory: model-backed features disabled: %v", err)
		return
	}

	client = c
	enabled = true
	log.Println("memory: v2 initialized")

	if err := loadAndRestartBuffers(); err != nil {
		log.Printf("memory: failed to reload channel buffers: %v", err)
	}
	go func() {
		if err := runStartupCatchUp(); err != nil {
			log.Printf("memory: startup catch-up failed: %v", err)
		}
	}()
	startMaintenanceScheduler()
}

func Shutdown() {
	stopMaintenanceScheduler()
	stopAllBufferTimers()
	enabled = false
	client = nil
	database = nil
}

func generateJSON(ctx context.Context, model, systemPrompt, userPrompt string, reasoning shared.ReasoningEffort, schema shared.ResponseFormatJSONSchemaJSONSchemaParam) (string, error) {
	if client == nil {
		return "", fmt.Errorf("chat completion: OpenAI client is not initialized")
	}

	resp, err := client.Chat.Completions.New(ctx, oa.ChatCompletionNewParams{
		Messages: []oa.ChatCompletionMessageParamUnion{
			oa.DeveloperMessage(systemPrompt),
			oa.UserMessage(userPrompt),
		},
		Model:           oa.ChatModel(model),
		ReasoningEffort: reasoning,
		ResponseFormat: oa.ChatCompletionNewParamsResponseFormatUnion{
			OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
				JSONSchema: schema,
			},
		},
	})
	if err != nil {
		return "", err
	}
	if len(resp.Choices) == 0 {
		return "", fmt.Errorf("chat completion returned no choices")
	}

	content := strings.TrimSpace(resp.Choices[0].Message.Content)
	if content == "" {
		return "", fmt.Errorf("chat completion returned empty content")
	}
	return content, nil
}

func embed(ctx context.Context, text string) ([]float32, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("embed: empty text")
	}
	if client == nil {
		return nil, fmt.Errorf("embed: OpenAI client is not initialized")
	}

	resp, err := client.Embeddings.New(ctx, oa.EmbeddingNewParams{
		Input: oa.EmbeddingNewParamsInputUnion{
			OfString: oa.String(text),
		},
		Model:          embeddingModel,
		Dimensions:     oa.Int(embeddingDimensions),
		EncodingFormat: oa.EmbeddingNewParamsEncodingFormatFloat,
	})
	if err != nil {
		return nil, err
	}
	if len(resp.Data) == 0 {
		return nil, fmt.Errorf("embedding API returned no embeddings")
	}

	values := make([]float32, len(resp.Data[0].Embedding))
	for i, value := range resp.Data[0].Embedding {
		values[i] = float32(value)
	}
	return values, nil
}

func serializeFloat32(v []float32) []byte {
	buf := make([]byte, len(v)*4)
	for i, f := range v {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(f))
	}
	return buf
}

func TotalNotes() int {
	if database == nil {
		return 0
	}
	var count int
	_ = database.QueryRow("SELECT COUNT(*) FROM interaction_notes").Scan(&count)
	return count
}

func SetPreferredName(discordID, username, preferredName string) error {
	if database == nil {
		return fmt.Errorf("memory system not initialized")
	}

	var id int64
	err := database.QueryRow("SELECT id FROM users WHERE discord_id = ?", discordID).Scan(&id)
	if err == sql.ErrNoRows {
		_, err = database.Exec(
			"INSERT INTO users (discord_id, username, preferred_name) VALUES (?, ?, ?)",
			discordID, username, preferredName,
		)
		return err
	}
	if err != nil {
		return err
	}

	_, err = database.Exec("UPDATE users SET preferred_name = ? WHERE id = ?", preferredName, id)
	return err
}

func GetPreferredName(discordID string) string {
	if database == nil {
		return ""
	}
	var name string
	_ = database.QueryRow("SELECT preferred_name FROM users WHERE discord_id = ?", discordID).Scan(&name)
	return name
}

func upsertUser(discordID, username, displayName string) (int64, string, error) {
	var (
		id            int64
		preferredName string
	)
	err := database.QueryRow("SELECT id, preferred_name FROM users WHERE discord_id = ?", discordID).Scan(&id, &preferredName)
	if err == nil {
		_, _ = database.Exec("UPDATE users SET username = ?, display_name = ? WHERE id = ?", username, displayName, id)
		return id, effectiveName(preferredName, displayName, username), nil
	}
	if err != sql.ErrNoRows {
		return 0, "", err
	}

	res, err := database.Exec(
		"INSERT INTO users (discord_id, username, display_name) VALUES (?, ?, ?)",
		discordID, username, displayName,
	)
	if err != nil {
		return 0, "", err
	}
	id, err = res.LastInsertId()
	return id, effectiveName("", displayName, username), err
}

func getUserIdentityByDiscordID(discordID string) (*userIdentity, error) {
	var user userIdentity
	err := database.QueryRow(`
		SELECT id, discord_id, username, display_name, preferred_name
		FROM users
		WHERE discord_id = ?
	`, discordID).Scan(&user.UserID, &user.DiscordID, &user.Username, &user.DisplayName, &user.PreferredName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func getUserIdentityByID(userID int64) (*userIdentity, error) {
	var user userIdentity
	err := database.QueryRow(`
		SELECT id, discord_id, username, display_name, preferred_name
		FROM users
		WHERE id = ?
	`, userID).Scan(&user.UserID, &user.DiscordID, &user.Username, &user.DisplayName, &user.PreferredName)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &user, nil
}

func effectiveName(preferredName, displayName, username string) string {
	if preferredName != "" {
		return preferredName
	}
	if displayName != "" {
		return displayName
	}
	return username
}

func emptyProfile(guildID string, userID int64) GuildUserProfile {
	return GuildUserProfile{
		GuildID:       guildID,
		UserID:        userID,
		Bio:           []ProfileFact{},
		Interests:     []ProfileFact{},
		Skills:        []ProfileFact{},
		Opinions:      []ProfileFact{},
		Relationships: []ProfileFact{},
		Other:         []ProfileFact{},
	}
}

func cloneProfile(profile GuildUserProfile) GuildUserProfile {
	return GuildUserProfile{
		GuildID:           profile.GuildID,
		UserID:            profile.UserID,
		Bio:               append([]ProfileFact(nil), profile.Bio...),
		Interests:         append([]ProfileFact(nil), profile.Interests...),
		Skills:            append([]ProfileFact(nil), profile.Skills...),
		Opinions:          append([]ProfileFact(nil), profile.Opinions...),
		Relationships:     append([]ProfileFact(nil), profile.Relationships...),
		Other:             append([]ProfileFact(nil), profile.Other...),
		IsDirty:           profile.IsDirty,
		UpdatedAt:         profile.UpdatedAt,
		LastFullRebuildAt: profile.LastFullRebuildAt,
	}
}

func profileHasContent(profile *GuildUserProfile) bool {
	if profile == nil {
		return false
	}
	return len(profile.Bio)+len(profile.Interests)+len(profile.Skills)+len(profile.Opinions)+len(profile.Relationships)+len(profile.Other) > 0
}

func profileExceedsBudget(profile GuildUserProfile) bool {
	return profileExceedsLimits(profile, 0, 0, 0, 0)
}

func profileExceedsHysteresisBudget(profile GuildUserProfile) bool {
	return profileExceedsLimits(
		profile,
		profileHysteresisExtraFacts,
		profileHysteresisExtraTotalFacts,
		profileHysteresisExtraFactWords,
		profileHysteresisExtraSourceNoteIDs,
	)
}

func profileExceedsLimits(profile GuildUserProfile, extraFacts, extraTotalFacts, extraFactWords, extraSourceNoteIDs int) bool {
	totalFacts := 0
	for _, section := range []struct {
		facts []ProfileFact
		max   int
	}{
		{facts: profile.Bio, max: profileMaxBioFacts},
		{facts: profile.Interests, max: profileMaxInterestFacts},
		{facts: profile.Skills, max: profileMaxSkillFacts},
		{facts: profile.Opinions, max: profileMaxOpinionFacts},
		{facts: profile.Relationships, max: profileMaxRelationshipFacts},
		{facts: profile.Other, max: profileMaxOtherFacts},
	} {
		normalized := normalizeProfileFacts(section.facts)
		if len(normalized) > section.max+extraFacts {
			return true
		}
		totalFacts += len(normalized)
		for _, fact := range normalized {
			if len(strings.Fields(fact.Text)) > profileMaxFactWords+extraFactWords {
				return true
			}
			if len(dedupeInt64s(fact.SourceNoteIDs)) > profileMaxSourceNoteIDs+extraSourceNoteIDs {
				return true
			}
		}
	}
	return totalFacts > profileMaxTotalFacts+extraTotalFacts
}

func compactProfile(profile GuildUserProfile) GuildUserProfile {
	compacted, _ := compactProfileWithStats(profile)
	return compacted
}

func compactProfileWithStats(profile GuildUserProfile) (GuildUserProfile, profileCompactionStats) {
	compacted := cloneProfile(profile)
	stats := profileCompactionStats{Before: countProfileFacts(profile)}
	remaining := profileMaxTotalFacts
	var textTrimmed, sourceTrimmed int
	compacted.Bio, textTrimmed, sourceTrimmed = compactProfileFacts(compacted.Bio, profileMaxBioFacts, &remaining)
	stats.TextFactsTrimmed += textTrimmed
	stats.SourceRefsTrimmed += sourceTrimmed
	compacted.Interests, textTrimmed, sourceTrimmed = compactProfileFacts(compacted.Interests, profileMaxInterestFacts, &remaining)
	stats.TextFactsTrimmed += textTrimmed
	stats.SourceRefsTrimmed += sourceTrimmed
	compacted.Skills, textTrimmed, sourceTrimmed = compactProfileFacts(compacted.Skills, profileMaxSkillFacts, &remaining)
	stats.TextFactsTrimmed += textTrimmed
	stats.SourceRefsTrimmed += sourceTrimmed
	compacted.Opinions, textTrimmed, sourceTrimmed = compactProfileFacts(compacted.Opinions, profileMaxOpinionFacts, &remaining)
	stats.TextFactsTrimmed += textTrimmed
	stats.SourceRefsTrimmed += sourceTrimmed
	compacted.Relationships, textTrimmed, sourceTrimmed = compactProfileFacts(compacted.Relationships, profileMaxRelationshipFacts, &remaining)
	stats.TextFactsTrimmed += textTrimmed
	stats.SourceRefsTrimmed += sourceTrimmed
	compacted.Other, textTrimmed, sourceTrimmed = compactProfileFacts(compacted.Other, profileMaxOtherFacts, &remaining)
	stats.TextFactsTrimmed += textTrimmed
	stats.SourceRefsTrimmed += sourceTrimmed
	stats.After = countProfileFacts(compacted)
	return compacted, stats
}

func compactProfileFacts(facts []ProfileFact, maxFacts int, remainingTotal *int) ([]ProfileFact, int, int) {
	limit := maxFacts
	if *remainingTotal < limit {
		limit = *remainingTotal
	}
	if limit <= 0 {
		return []ProfileFact{}, 0, 0
	}

	normalized := normalizeProfileFacts(facts)
	if len(normalized) > limit {
		normalized = rankProfileFacts(normalized)
	}
	out := make([]ProfileFact, 0, len(normalized))
	textTrimmed := 0
	sourceTrimmed := 0
	for _, fact := range normalized {
		text, trimmedText := compactFactText(fact.Text)
		if text == "" {
			continue
		}
		if trimmedText {
			textTrimmed++
		}

		sourceNoteIDs := dedupeInt64s(fact.SourceNoteIDs)
		if len(sourceNoteIDs) > profileMaxSourceNoteIDs {
			sourceNoteIDs = append([]int64(nil), sourceNoteIDs[:profileMaxSourceNoteIDs]...)
			sourceTrimmed++
		} else {
			sourceNoteIDs = append([]int64(nil), sourceNoteIDs...)
		}

		out = append(out, ProfileFact{
			Text:          text,
			SourceNoteIDs: sourceNoteIDs,
		})
		if len(out) >= limit {
			break
		}
	}

	*remainingTotal -= len(out)
	return out, textTrimmed, sourceTrimmed
}

func compactFactText(text string) (string, bool) {
	words := strings.Fields(strings.TrimSpace(text))
	if len(words) == 0 {
		return "", false
	}
	if len(words) <= profileMaxFactWords {
		return strings.Join(words, " "), false
	}
	return strings.Join(words[:profileMaxFactWords], " ") + "...", true
}

func rankProfileFacts(facts []ProfileFact) []ProfileFact {
	ranked := append([]ProfileFact(nil), facts...)
	sort.SliceStable(ranked, func(i, j int) bool {
		return len(dedupeInt64s(ranked[i].SourceNoteIDs)) > len(dedupeInt64s(ranked[j].SourceNoteIDs))
	})
	return ranked
}

func marshalProfileFacts(facts []ProfileFact) string {
	b, err := json.Marshal(normalizeProfileFacts(facts))
	if err != nil {
		return "[]"
	}
	return string(b)
}

func unmarshalProfileFacts(raw string) ([]ProfileFact, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var facts []ProfileFact
	if err := json.Unmarshal([]byte(raw), &facts); err != nil {
		return nil, err
	}
	return normalizeProfileFacts(facts), nil
}

func normalizeProfileFacts(facts []ProfileFact) []ProfileFact {
	out := make([]ProfileFact, 0, len(facts))
	for _, fact := range facts {
		text := strings.TrimSpace(fact.Text)
		if text == "" {
			continue
		}
		out = append(out, ProfileFact{
			Text:          text,
			SourceNoteIDs: dedupeInt64s(fact.SourceNoteIDs),
		})
	}
	return out
}

func marshalInt64Slice(ids []int64) string {
	b, err := json.Marshal(dedupeInt64s(ids))
	if err != nil {
		return "[]"
	}
	return string(b)
}

func parseInt64Slice(raw string) ([]int64, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var ids []int64
	if err := json.Unmarshal([]byte(raw), &ids); err != nil {
		return nil, err
	}
	return dedupeInt64s(ids), nil
}

func dedupeInt64s(ids []int64) []int64 {
	if len(ids) == 0 {
		return nil
	}
	seen := make(map[int64]struct{}, len(ids))
	out := make([]int64, 0, len(ids))
	for _, id := range ids {
		if id == 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func quoteList(items []string) string {
	if len(items) == 0 {
		return "(none)"
	}
	return strings.Join(items, ", ")
}

func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}
