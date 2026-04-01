package handler

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"voltgpt/internal/memory"

	"github.com/bwmarrin/discordgo"
)

const memoryDigestPageSize = 6

func sendMemoryDigestPage(s *discordgo.Session, i *discordgo.InteractionCreate, page int) error {
	totalNotes := memory.CountGuildNotes(i.GuildID)
	embed, components := buildMemoryDigestPage(i.GuildID, page, totalNotes)

	_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds:     []*discordgo.MessageEmbed{embed},
		Components: components,
	})
	return err
}

func updateMemoryDigestPage(s *discordgo.Session, i *discordgo.InteractionCreate, page int) error {
	totalNotes := memory.CountGuildNotes(i.GuildID)
	embed, components := buildMemoryDigestPage(i.GuildID, page, totalNotes)

	return s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

func buildMemoryDigestPage(guildID string, page, totalNotes int) (*discordgo.MessageEmbed, []discordgo.MessageComponent) {
	if totalNotes <= 0 {
		return &discordgo.MessageEmbed{
			Title:       "Memory Digest",
			Description: "No guild-scoped memory has been captured yet.",
		}, nil
	}

	totalPages := (totalNotes + memoryDigestPageSize - 1) / memoryDigestPageSize
	if totalPages <= 0 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	offset := (page - 1) * memoryDigestPageSize
	notes, err := memory.GetRecentGuildNotesPage(guildID, memoryDigestPageSize, offset)
	if err != nil {
		log.Printf("memory digest: failed to load page %d: %v", page, err)
		return &discordgo.MessageEmbed{
			Title:       "Memory Digest",
			Description: "Failed to load the requested digest page.",
		}, nil
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Memory Digest",
		Description: "Recent conversation notes and topic digests.",
		Fields:      buildMemoryDigestFields(notes),
		Footer: &discordgo.MessageEmbedFooter{
			Text: fmt.Sprintf("Page %d/%d • %d total notes", page, totalPages, totalNotes),
		},
	}
	if len(embed.Fields) == 0 {
		embed.Description = "No memory entries were found for this page."
	}

	return embed, buildMemoryDigestComponents(page, totalPages)
}

func buildMemoryDigestFields(notes []memory.InteractionNote) []*discordgo.MessageEmbedField {
	fields := make([]*discordgo.MessageEmbedField, 0, len(notes))
	for _, note := range notes {
		label := memoryDigestLabel(note.NoteType)

		title := truncateForEmbed(strings.TrimSpace(note.Title), 180)
		if title == "" {
			title = "Untitled"
		}

		fieldName := fmt.Sprintf("%s • %s • %s", label, formatMemoryDigestDate(note.NoteDate), title)
		summary := truncateForEmbed(strings.TrimSpace(note.Summary), 1024)
		if summary == "" {
			summary = "_No description._"
		}

		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   truncateForEmbed(fieldName, 256),
			Value:  summary,
			Inline: false,
		})
	}
	return fields
}

func buildMemoryDigestComponents(page, totalPages int) []discordgo.MessageComponent {
	if totalPages <= 1 {
		return nil
	}

	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				&discordgo.Button{
					CustomID: fmt.Sprintf("memorydigest-%d", page-1),
					Label:    "Previous",
					Style:    discordgo.SecondaryButton,
					Disabled: page <= 1,
				},
				&discordgo.Button{
					CustomID: fmt.Sprintf("memorydigest-%d", page+1),
					Label:    "Next",
					Style:    discordgo.PrimaryButton,
					Disabled: page >= totalPages,
				},
			},
		},
	}
}

func parseMemoryDigestPage(customID string) int {
	parts := strings.Split(customID, "-")
	if len(parts) < 2 {
		return 1
	}

	page, err := strconv.Atoi(parts[1])
	if err != nil {
		return 1
	}
	return page
}

func formatMemoryDigestDate(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "Unknown date"
	}

	parsed, err := time.Parse("2006-01-02", raw)
	if err != nil {
		return raw
	}
	return parsed.Format("Jan 2, 2006")
}

func truncateForEmbed(text string, limit int) string {
	text = strings.TrimSpace(text)
	runes := []rune(text)
	if limit <= 0 || len(runes) <= limit {
		return text
	}
	if limit <= 3 {
		return string(runes[:limit])
	}
	return string(runes[:limit-3]) + "..."
}

func memoryDigestLabel(noteType string) string {
	switch noteType {
	case "", "conversation":
		return "Conversation"
	case "topic_cluster":
		return "Topic"
	default:
		return strings.ReplaceAll(noteType, "_", " ")
	}
}
