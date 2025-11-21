package handler

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	gemini "voltgpt/internal/apis/gemini"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/hasher"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"google.golang.org/genai"
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

	apiKey := os.Getenv("GEMINI_TOKEN")
	if apiKey == "" {
		discord.LogSendErrorMessage(s, m.Message, "GEMINI_TOKEN is not set")
		return
	}

	ctx := context.Background()
	// Initialize the Gemini client
	c, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return
	}

	var chatMessages []*genai.Content
	var cache []*discordgo.Message
	var isMentioned, isReply bool

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			isMentioned = true
			break
		}
	}

	if m.Type == discordgo.MessageTypeReply {
		if (m.ReferencedMessage.Author.ID == s.State.User.ID && isMentioned) || isMentioned {
			cache, _ = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
			isReply = true
		}
	}

	if !isMentioned {
		return
	}

	m.Message = utility.CleanMessage(s, m.Message)
	images, videos, pdfs, ytURLs := utility.GetMessageMediaURL(m.Message)

	content := config.RequestContent{
		Text: strings.TrimSpace(fmt.Sprintf("<username>%s</username>: %s%s%s",
			m.Message.Author.Username,
			utility.AttachmentText(m.Message),
			utility.EmbedText(m.Message),
			fmt.Sprintf("<message>%s</message>", m.Content),
		)),
		Media:  append(append(images, videos...), pdfs...),
		YTURLs: ytURLs,
	}

	gemini.AppendMessage("user", m.Message.Author.Username, content, &chatMessages)

	if isReply {
		gemini.PrependReplyMessages(s, c, m.Message.Member, m.Message, cache, &chatMessages)
	}

	err = gemini.StreamMessageResponse(s, c, m.Message, chatMessages)
	if err != nil {
		discord.LogSendErrorMessage(s, m.Message, err.Error())
	}
}
