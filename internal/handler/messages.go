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

	if m.Author.ID == s.State.User.ID || m.Author.Bot {
		return
	}

	var chatMessages []anthropic.Message
	var cache []*discordgo.Message
	var isMentioned, isReply bool

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			isMentioned = true
			break
		}
	}

	if m.Type == discordgo.MessageTypeReply {
		if m.ReferencedMessage.Author.ID == s.State.User.ID || isMentioned {
			cache, _ = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
			isReply = true
		}
	}

	if !isMentioned && !isReply {
		return
	}

	m.Message = utility.CleanMessage(s, m.Message)
	images, _, pdfs := utility.GetMessageMediaURL(m.Message)

	content := config.RequestContent{
		Text: fmt.Sprintf("%s: %s%s%s%s",
			fmt.Sprintf("<username>%s</username>", m.Author.Username),
			openai.GetTranscript(s, m.Message),
			utility.AttachmentText(m.Message),
			utility.EmbedText(m.Message),
			fmt.Sprintf("<message>%s</message>", m.Content),
		),
		Images: images,
		PDFs:   pdfs,
	}

	ant.AppendMessage(anthropic.RoleUser, content, &chatMessages)

	if isReply {
		ant.PrependReplyMessages(s, m.Message.Member, m.Message, cache, &chatMessages)
		ant.PrependUserMessagePlaceholder(&chatMessages)
	}

	ant.StreamMessageResponse(s, m.Message, chatMessages, nil)
}
