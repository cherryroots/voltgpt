package main

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

func updateResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: content,
		},
	})
}

func sendFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})

	return msg, err
}

func sendFollowupFile(s *discordgo.Session, i *discordgo.InteractionCreate, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Files:   files,
	})

	return msg, err
}

func editFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
	})

	return msg, err
}

func editFollowupFile(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
		Files:   files,
	})

	return msg, err
}

func sendMessage(s *discordgo.Session, m *discordgo.Message, content string) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})

	return msg, err
}

func sendMessageFile(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
		Files:     files,
	})

	return msg, err
}

func editMessage(s *discordgo.Session, m *discordgo.Message, content string) *discordgo.Message {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
	})
	if err != nil {
		log.Println(err)
	}

	return msg
}

// command to edit a given message the bot has sent
func editMessageFile(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) *discordgo.Message {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
		Files:   files,
	})
	if err != nil {
		log.Println(err)
	}

	return msg
}

func deferResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Println(err)
	}
}

func deferEphemeralResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
