package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	gemini "voltgpt/internal/apis/gemini"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/hasher"
	"voltgpt/internal/memory"
	"voltgpt/internal/reminder"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"google.golang.org/genai"
)

func HandleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Delay 3 seconds to allow embeds to load
	if m.Message.GuildID == config.MainServer {
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
	if !config.MemoryBlacklist[m.ChannelID] && m.Message.GuildID == config.MainServer {
		extractContent := utility.ResolveMentions(m.Content, m.Mentions)
		go memory.Extract(m.Author.ID, m.Author.Username, m.Author.GlobalName, m.ID, extractContent)
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
	m.Message.Content = utility.ResolveMentions(m.Message.Content, m.Mentions)

	if triggerLen, ok := reminder.Trigger(m.Message.Content); ok {
		handleReminder(s, m.Message, triggerLen)
		return
	}

	images, videos, pdfs, ytURLs := utility.GetMessageMediaURL(m.Message)

	content := config.RequestContent{
		Text: strings.TrimSpace(fmt.Sprintf("<user name=\"%s\"> %s %s %s </user>",
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

	// Retrieve memory facts for users in the reply chain (not the whole channel)
	users := map[string]string{m.Author.ID: m.Author.Username}
	if isReply {
		ref := utility.GetReferencedMessage(s, m.Message, cache)
		for ref != nil {
			if ref.Author != nil && !ref.Author.Bot && ref.Author.ID != s.State.User.ID {
				users[ref.Author.ID] = ref.Author.Username
			}
			if ref.Type == discordgo.MessageTypeReply {
				ref = utility.GetReferencedMessage(s, ref, cache)
			} else {
				break
			}
		}
	}
	var backgroundFacts string
	if !strings.Contains(m.Content, "🚫") {
		backgroundFacts = memory.RetrieveMultiUser(m.Content, users)
	}

	err = gemini.StreamMessageResponse(s, c, m.Message, chatMessages, backgroundFacts)
	if err != nil {
		discord.LogSendErrorMessage(s, m.Message, err.Error())
	}
}

// handleReminder parses and stores a reminder from a Discord message.
func handleReminder(s *discordgo.Session, m *discordgo.Message, triggerLen int) {
	after := strings.TrimSpace(m.Content[triggerLen:])
	fireAt, msg, err := reminder.ParseTime(after, time.Now().UTC())
	if err != nil {
		discord.SendMessage(s, m, "Couldn't parse reminder time - try:\n- remind me in 2h30m do the thing\n- remind me at 16:30 CET do the thing")
		return
	}

	var images []reminder.Image
	for _, att := range m.Attachments {
		if att.Width > 0 && att.Height > 0 {
			data, err := utility.DownloadBytes(att.URL)
			if err != nil {
				log.Printf("reminder: download attachment %s: %v", att.URL, err)
				continue
			}
			images = append(images, reminder.Image{
				Filename: att.Filename,
				Data:     base64.StdEncoding.EncodeToString(data),
			})
		}
	}

	if err := reminder.Add(m.Author.ID, m.ChannelID, m.GuildID, msg, images, fireAt); err != nil {
		discord.SendMessage(s, m, fmt.Sprintf("Couldn't save reminder: %v", err))
		return
	}

	reply := fmt.Sprintf("<@%s> I'll remind you <t:%d:R>: %s", m.Author.ID, fireAt.Unix(), msg)
	if _, err := discord.SendMessage(s, m, reply); err != nil {
		log.Println(err)
	}
}
