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
	"voltgpt/internal/memory"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"google.golang.org/genai"
)

func HandleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Delay 3 seconds to allow embeds to load
	if m.Message.GuildID == config.HashServer {
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
	}

	if m.Author.ID == s.State.User.ID || m.Author.Bot {
		return
	}

	// Background fact extraction for all non-bot messages
	go memory.Extract(m.Author.ID, m.Author.Username, m.ID, m.Content)

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
		discord.LogSendErrorMessage(s, m.Message, fmt.Sprintf("Failed to create Gemini client: %v", err))
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
			m.Content,
		)),
		Images: images,
		Videos: videos,
		PDFs:   pdfs,
		YTURLs: ytURLs,
	}

	chatMessages = append(chatMessages, gemini.CreateContent(c, "user", content))

	if isReply {
		gemini.PrependReplyMessages(s, c, m.Message.Member, m.Message, cache, &chatMessages)
	}

	// Retrieve memory facts for all users in the conversation
	users := map[string]string{m.Author.ID: m.Author.Username}
	if cache != nil {
		for _, cached := range cache {
			if cached.Author != nil && !cached.Author.Bot && cached.Author.ID != s.State.User.ID {
				users[cached.Author.ID] = cached.Author.Username
			}
		}
	}
	backgroundFacts := memory.RetrieveMultiUser(m.Content, users)

	err = gemini.StreamMessageResponse(s, c, m.Message, chatMessages, backgroundFacts)
	if err != nil {
		discord.LogSendErrorMessage(s, m.Message, err.Error())
	}
}
