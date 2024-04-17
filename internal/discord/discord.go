package discord

import (
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

func LogSendErrorMessage(s *discordgo.Session, m *discordgo.Message, content string) {
	log.Println(content)
	_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})
}

func UpdateResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) error {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content: content,
		},
	})
	return err
}

func SendFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})

	return msg, err
}

func SendFollowupFile(s *discordgo.Session, i *discordgo.InteractionCreate, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Files:   files,
	})

	return msg, err
}

func EditFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
	})

	return msg, err
}

func EditFollowupFile(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
		Files:   files,
	})

	return msg, err
}

func SendMessage(s *discordgo.Session, m *discordgo.Message, content string) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})

	return msg, err
}

func SendMessageFile(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
		Files:     files,
	})

	return msg, err
}

func EditMessage(s *discordgo.Session, m *discordgo.Message, content string) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
	})

	return msg, err
}

// command to edit a given message the bot has sent
func EditMessageFile(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
		Files:   files,
	})

	return msg, err
}

func SleepDeleteInteraction(s *discordgo.Session, i *discordgo.InteractionCreate, seconds int) error {
	time.Sleep(time.Duration(seconds) * time.Second)

	err := s.InteractionResponseDelete(i.Interaction)
	if err != nil {
		return err
	}

	return nil
}

func DeferResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Println(err)
	}
}

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
