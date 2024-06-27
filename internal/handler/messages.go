package handler

import (
	"fmt"
	"time"

	ant "voltgpt/internal/anthropic"
	"voltgpt/internal/config"
	"voltgpt/internal/hasher"
	"voltgpt/internal/openai"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic/v2"
)

func HandleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Delay 3 seconds to allow embeds to load
	go func() {
		time.Sleep(3 * time.Second)
		fetchedMessage, _ := s.ChannelMessage(m.Message.ChannelID, m.Message.ID)
		if fetchedMessage == nil {
			return
		}

		if utility.HasImageURL(fetchedMessage) || utility.HasVideoURL(fetchedMessage) {
			options := hasher.HashOptions{Store: true}
			hasher.HashAttachments(fetchedMessage, options)
		}
	}()

	if m.Author.ID == s.State.User.ID {
		return
	}

	if m.Author.Bot {
		return
	}

	var chatMessages []anthropic.Message
	var cache []*discordgo.Message
	botMentioned, isReply := false, false

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			botMentioned = true
			break
		}
	}

	if m.Type == discordgo.MessageTypeReply {
		if (m.ReferencedMessage.Author.ID == s.State.User.ID || botMentioned) && m.ReferencedMessage != nil {
			cache = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
			isReply = true
		}
	}

	if botMentioned || isReply {
		m.Message = utility.CleanMessage(s, m.Message)
		images, _ := utility.GetMessageMediaURL(m.Message)

		var transcript string
		if utility.HasAccessRole(m.Message.Member) {
			transcript = openai.GetTranscript(s, m.Message)
		}

		content := config.RequestContent{
			Text: fmt.Sprintf("%s: %s%s%s%s",
				fmt.Sprintf("<username>%s</username>", m.Author.Username),
				transcript,
				utility.AttachmentText(m.Message),
				utility.EmbedText(m.Message),
				fmt.Sprintf("<message>%s</message>", m.Content),
			),
			URL: images,
		}

		ant.AppendMessage(anthropic.RoleUser, content, &chatMessages)

		if isReply {
			ant.PrependReplyMessages(s, m.Message.Member, m.Message, cache, &chatMessages)
			ant.PrependUserMessagePlaceholder(&chatMessages)
		}

		ant.StreamMessageResponse(s, m.Message, chatMessages, nil)
	}
}
