// Package discord is a utility package for interacting with Discord.
package discord

import (
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

// LogSendErrorMessage logs an error message and sends it to a Discord channel.
func LogSendErrorMessage(s *discordgo.Session, m *discordgo.Message, content string) {
	log.Println(content)
	_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})
}

// UpdateResponse updates a Discord interaction response with the provided content.
func UpdateResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: content,
		},
	})
	return err
}

// SendFollowup sends a follow-up message to a Discord interaction.
func SendFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})

	return msg, err
}

// SendFollowupEmbeds sends a follow-up message with embeds to a Discord interaction.
func SendFollowupEmbeds(s *discordgo.Session, i *discordgo.InteractionCreate, embeds []*discordgo.MessageEmbed) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Embeds: embeds,
	})

	return msg, err
}

// SendFollowupFile sends a follow-up message with files to a Discord interaction.
func SendFollowupFile(s *discordgo.Session, i *discordgo.InteractionCreate, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Files:   files,
	})

	return msg, err
}

// EditFollowup edits a follow-up message for a Discord interaction.
func EditFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
	})

	return msg, err
}

// EditFollowupFile sends a follow-up message with files to a Discord interaction.
func EditFollowupFile(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
		Files:   files,
	})

	return msg, err
}

// SendMessage sends a message to a Discord channel.
func SendMessage(s *discordgo.Session, m *discordgo.Message, content string) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})

	return msg, err
}

// SendMessageFile sends a message with files to a Discord channel.
func SendMessageFile(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
		Files:     files,
	})

	return msg, err
}

// EditMessage edits a message in a Discord channel.
func EditMessage(s *discordgo.Session, m *discordgo.Message, content string) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
	})

	return msg, err
}

// EditMessageFile edits a message in a Discord channel with files.
func EditMessageFile(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
		Files:   files,
	})

	return msg, err
}

// SleepDeleteInteraction sleeps for a specified duration and then deletes an interaction response.
func SleepDeleteInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, seconds int) error {
	time.Sleep(time.Duration(seconds) * time.Second)

	err := s.InteractionResponseDelete(i.Interaction)
	if err != nil {
		return err
	}

	return nil
}

// DeferResponse defers a response to a Discord interaction with the provided session and interaction.
func DeferResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Println(err)
	}
}

// DeferEphemeralResponse defers an ephemeral response to a Discord interaction.
func DeferEphemeralResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Println(err)
	}
}
