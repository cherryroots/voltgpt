package handler

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"

	"voltgpt/internal/reminder"

	"github.com/bwmarrin/discordgo"
)

// respondEphemeral answers the interaction directly with an ephemeral message.
func respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	data.Flags = discordgo.MessageFlagsEphemeral
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	})
	if err != nil {
		log.Println(err)
	}
}

func remindersCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)

	reminders, err := reminder.GetUserReminders(i.Interaction.Member.User.ID)
	if err != nil {
		log.Println(err)
		respondEphemeral(s, i, &discordgo.InteractionResponseData{
			Content: "Failed to retrieve your reminders. Please try again later.",
		})
		return
	}
	if len(reminders) == 0 {
		respondEphemeral(s, i, &discordgo.InteractionResponseData{
			Content: "You have no pending reminders!",
		})
		return
	}

	const maxOptions = 25
	var sb strings.Builder
	sb.WriteString("**Your reminders:**\n")
	displayed := reminders
	if len(reminders) > maxOptions {
		displayed = reminders[:maxOptions]
	}
	options := make([]discordgo.SelectMenuOption, 0, len(displayed))
	for idx, r := range displayed {
		imageNote := ""
		if len(r.Images) > 0 {
			imageNote = fmt.Sprintf(" [%d image(s)]", len(r.Images))
		}
		sb.WriteString(fmt.Sprintf("%d. <t:%d:R> — %s%s\n", idx+1, r.FireAt, r.Message, imageNote))

		label := fmt.Sprintf("%d. %s", idx+1, r.Message)
		if len(label) > 100 {
			label = label[:97] + "..."
		}
		options = append(options, discordgo.SelectMenuOption{
			Label: label,
			Value: strconv.FormatInt(r.ID, 10),
		})
	}
	if len(reminders) > maxOptions {
		sb.WriteString(fmt.Sprintf("\n*(showing first 25 of %d reminders)*", len(reminders)))
	}

	respondEphemeral(s, i, &discordgo.InteractionResponseData{
		Content: sb.String(),
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{
				Components: []discordgo.MessageComponent{
					discordgo.SelectMenu{
						CustomID:    "reminder",
						Placeholder: "Delete a reminder…",
						Options:     options,
					},
				},
			},
		},
	})
}
