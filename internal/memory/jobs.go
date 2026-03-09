package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

var clusterResponseSchema = shared.ResponseFormatJSONSchemaJSONSchemaParam{
	Name:        "topic_clusters",
	Description: oa.String("Guild-day topic clusters derived from conversation notes"),
	Strict:      oa.Bool(true),
	Schema: map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"clusters": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"title":   map[string]any{"type": "string"},
						"summary": map[string]any{"type": "string"},
						"source_note_ids": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "integer"},
						},
					},
					"required": []string{"title", "summary", "source_note_ids"},
				},
			},
		},
		"required": []string{"clusters"},
	},
}

const clusterSystemPrompt = `You group one guild-day of conversation notes into broad topic clusters.

Rules:
- Each cluster should summarize multiple related notes when possible.
- Use only the provided conversation notes.
- Keep titles short and summaries concrete.
- Return an empty array if the input is too sparse for useful clusters.
- Preserve source_note_ids for every cluster.`

type guildDay struct {
	GuildID string
	Date    string
}

func runClusterPhase(guildID, date string) (err error) {
	if database == nil {
		return fmt.Errorf("memory system not initialized")
	}
	if err := startJobRun(guildID, date, jobPhaseCluster); err != nil {
		return err
	}
	defer finishJobRun(guildID, date, jobPhaseCluster, &err)

	notes, err := getConversationNotesForGuildDate(guildID, date)
	if err != nil {
		return err
	}
	if len(notes) < minClusterInputNotes {
		return nil
	}

	if err := deleteStaleClusterNotes(guildID, date); err != nil {
		return err
	}

	clusters, err := clusterGuildDay(context.Background(), guildID, date, notes)
	if err != nil {
		return err
	}
	for _, cluster := range clusters {
		if len(cluster.SourceNoteIDs) == 0 {
			continue
		}
		participantIDs, err := unionParticipantsForNotes(cluster.SourceNoteIDs)
		if err != nil {
			return err
		}
		embedding, err := embedText(context.Background(), cluster.Title+"\n"+cluster.Summary)
		if err != nil {
			return err
		}
		if _, err := insertNote(InteractionNote{
			GuildID:            guildID,
			NoteType:           noteTypeTopicCluster,
			Title:              cluster.Title,
			Summary:            cluster.Summary,
			SourceNoteIDs:      dedupeInt64s(cluster.SourceNoteIDs),
			NoteDate:           date,
			ParticipantUserIDs: participantIDs,
		}, participantIDs, embedding); err != nil {
			return err
		}
	}

	return nil
}

func startJobRun(guildID, date, phase string) error {
	_, err := database.Exec(`
		INSERT INTO memory_job_runs (guild_id, job_date, phase, status, started_at, finished_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP, NULL)
		ON CONFLICT(guild_id, job_date, phase) DO UPDATE SET
			status = excluded.status,
			started_at = CURRENT_TIMESTAMP,
			finished_at = NULL
	`, guildID, date, phase, jobStatusRunning)
	return err
}

func finishJobRun(guildID, date, phase string, errp *error) {
	status := jobStatusCompleted
	if errp != nil && *errp != nil {
		status = jobStatusFailed
	}
	if _, err := database.Exec(`
		UPDATE memory_job_runs
		SET status = ?, finished_at = CURRENT_TIMESTAMP
		WHERE guild_id = ? AND job_date = ? AND phase = ?
	`, status, guildID, date, phase); err != nil {
		log.Printf("memory: failed to finalize job run %s/%s/%s: %v", guildID, date, phase, err)
	}
}

func getJobStatus(guildID, date, phase string) (string, error) {
	var status string
	err := database.QueryRow(`
		SELECT status
		FROM memory_job_runs
		WHERE guild_id = ? AND job_date = ? AND phase = ?
	`, guildID, date, phase).Scan(&status)
	if err != nil {
		return "", err
	}
	return status, nil
}

func deleteStaleClusterNotes(guildID, date string) error {
	rows, err := database.Query(`
		SELECT id
		FROM interaction_notes
		WHERE guild_id = ? AND note_type = ? AND note_date = ?
	`, guildID, noteTypeTopicCluster, date)
	if err != nil {
		return err
	}
	defer rows.Close()

	var noteIDs []int64
	for rows.Next() {
		var noteID int64
		if err := rows.Scan(&noteID); err != nil {
			return err
		}
		noteIDs = append(noteIDs, noteID)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for _, noteID := range noteIDs {
		if err := deleteNoteAndVector(noteID); err != nil {
			return err
		}
	}
	return nil
}

func unionParticipantsForNotes(noteIDs []int64) ([]int64, error) {
	noteRefs, err := getNotesByIDs(noteIDs)
	if err != nil {
		return nil, err
	}
	var participantIDs []int64
	for _, noteID := range dedupeInt64s(noteIDs) {
		note, ok := noteRefs[noteID]
		if !ok {
			continue
		}
		participantIDs = append(participantIDs, note.ParticipantUserIDs...)
	}
	return dedupeInt64s(participantIDs), nil
}

type clusterPayload struct {
	Clusters []clusterResult `json:"clusters"`
}

func clusterGuildDayOpenAI(ctx context.Context, guildID, date string, notes []InteractionNote) ([]clusterResult, error) {
	if len(notes) < minClusterInputNotes {
		return nil, nil
	}

	prompt := fmt.Sprintf("Guild: %s\nDate: %s\nConversation notes JSON:\n%s", guildID, date, jsonString(notes))
	responseText, err := generateJSON(ctx, clusteringModel, clusterSystemPrompt, prompt, clusterResponseSchema)
	if err != nil {
		return nil, err
	}

	var payload clusterPayload
	if err := json.Unmarshal([]byte(responseText), &payload); err != nil {
		return nil, err
	}

	out := make([]clusterResult, 0, len(payload.Clusters))
	for _, cluster := range payload.Clusters {
		cluster.Title = stringsTrim(cluster.Title)
		cluster.Summary = stringsTrim(cluster.Summary)
		cluster.SourceNoteIDs = dedupeInt64s(cluster.SourceNoteIDs)
		if cluster.Title == "" || cluster.Summary == "" || len(cluster.SourceNoteIDs) == 0 {
			continue
		}
		out = append(out, cluster)
	}
	return out, nil
}

func runProfileMaintenancePhase(guildID, date string) (err error) {
	if database == nil {
		return fmt.Errorf("memory system not initialized")
	}
	if err := startJobRun(guildID, date, jobPhaseProfileMaintenance); err != nil {
		return err
	}
	defer finishJobRun(guildID, date, jobPhaseProfileMaintenance, &err)
	return rebuildDirtyProfiles(guildID)
}

func rebuildDirtyProfiles(guildID string) error {
	userIDs, err := listDirtyProfileUserIDs(guildID)
	if err != nil {
		return err
	}
	for _, userID := range userIDs {
		if err := rebuildOneGuildProfile(guildID, userID); err != nil {
			return err
		}
	}
	return nil
}

func rebuildOneGuildProfile(guildID string, userID int64) error {
	user, err := getUserIdentityByID(userID)
	if err != nil {
		return err
	}
	if user == nil {
		return nil
	}

	notes, err := getRecentConversationNotesForUser(guildID, userID, 1000)
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		_, err := database.Exec("DELETE FROM guild_user_profiles WHERE guild_id = ? AND user_id = ?", guildID, userID)
		return err
	}

	profile, err := rebuildGuildProfile(context.Background(), guildID, *user, notes)
	if err != nil {
		return err
	}
	profile.GuildID = guildID
	profile.UserID = userID
	profile.IsDirty = false
	profile.LastFullRebuildAt = time.Now().UTC().Format(time.RFC3339Nano)
	return writeGuildUserProfile(profile)
}

func runStartupCatchUp() error {
	if !enabled || database == nil {
		return nil
	}

	today := timeNow().Format(time.DateOnly)
	days, err := listConversationGuildDaysBefore(today)
	if err != nil {
		return err
	}

	sort.Slice(days, func(i, j int) bool {
		if days[i].Date == days[j].Date {
			return days[i].GuildID < days[j].GuildID
		}
		return days[i].Date < days[j].Date
	})

	for _, gd := range days {
		for _, phase := range []string{jobPhaseCluster, jobPhaseProfileMaintenance} {
			status, err := getJobStatus(gd.GuildID, gd.Date, phase)
			if err == nil && status == jobStatusCompleted {
				continue
			}
			if phase == jobPhaseCluster {
				if err := runClusterPhase(gd.GuildID, gd.Date); err != nil {
					log.Printf("memory: startup cluster catch-up failed for %s/%s: %v", gd.GuildID, gd.Date, err)
				}
				continue
			}
			if err := runProfileMaintenancePhase(gd.GuildID, gd.Date); err != nil {
				log.Printf("memory: startup profile catch-up failed for %s/%s: %v", gd.GuildID, gd.Date, err)
			}
		}
	}
	return nil
}

func stringsTrim(s string) string {
	return strings.TrimSpace(s)
}

func startMaintenanceScheduler() {
	lifecycleMu.Lock()
	stopCh := make(chan struct{})
	maintenanceStopCh = stopCh
	lifecycleMu.Unlock()

	go func() {
		ticker := time.NewTicker(maintenanceSchedulerInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				if err := runScheduledMaintenanceSweep(); err != nil {
					log.Printf("memory: scheduled maintenance sweep failed: %v", err)
				}
			case <-stopCh:
				return
			}
		}
	}()
}

func stopMaintenanceScheduler() {
	lifecycleMu.Lock()
	defer lifecycleMu.Unlock()

	if maintenanceStopCh == nil {
		return
	}
	close(maintenanceStopCh)
	maintenanceStopCh = nil
}

func runScheduledMaintenanceSweep() error {
	lifecycleMu.Lock()
	if maintenanceSweepRunning {
		lifecycleMu.Unlock()
		return nil
	}
	maintenanceSweepRunning = true
	lifecycleMu.Unlock()
	defer func() {
		lifecycleMu.Lock()
		maintenanceSweepRunning = false
		lifecycleMu.Unlock()
	}()

	if !enabled || database == nil {
		return nil
	}

	today := timeNow().Format(time.DateOnly)
	days, err := listConversationGuildDaysBefore(today)
	if err != nil {
		return err
	}
	for _, gd := range days {
		status, err := getJobStatus(gd.GuildID, gd.Date, jobPhaseCluster)
		if err == nil && status == jobStatusCompleted {
			continue
		}
		if err := runClusterPhase(gd.GuildID, gd.Date); err != nil {
			log.Printf("memory: scheduled cluster maintenance failed for %s/%s: %v", gd.GuildID, gd.Date, err)
		}
	}

	guildIDs, err := listGuildsWithDirtyProfiles()
	if err != nil {
		return err
	}

	for _, guildID := range guildIDs {
		if err := runProfileMaintenancePhase(guildID, today); err != nil {
			log.Printf("memory: scheduled profile maintenance failed for guild %s: %v", guildID, err)
		}
	}
	return nil
}

func listConversationGuildDaysBefore(today string) ([]guildDay, error) {
	rows, err := database.Query(`
		SELECT DISTINCT guild_id, note_date
		FROM interaction_notes
		WHERE note_type = ?
		ORDER BY note_date ASC, guild_id ASC
	`, noteTypeConversation)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var days []guildDay
	for rows.Next() {
		var gd guildDay
		if err := rows.Scan(&gd.GuildID, &gd.Date); err != nil {
			return nil, err
		}
		gd.Date = safeDate(gd.Date)
		if gd.Date >= today {
			continue
		}
		days = append(days, gd)
	}
	return days, rows.Err()
}

func listGuildsWithDirtyProfiles() ([]string, error) {
	rows, err := database.Query(`
		SELECT DISTINCT guild_id
		FROM guild_user_profiles
		WHERE is_dirty = 1
		ORDER BY guild_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var guildIDs []string
	for rows.Next() {
		var guildID string
		if err := rows.Scan(&guildID); err != nil {
			return nil, err
		}
		guildIDs = append(guildIDs, guildID)
	}
	return guildIDs, rows.Err()
}
