package handler

import (
	"fmt"
	"log"
	"time"

	ant "voltgpt/internal/anthropic"
	"voltgpt/internal/config"
	"voltgpt/internal/hasher"
	"voltgpt/internal/openai"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic/v2"
)

// HandleMessage is the main message handler function.
// It checks for and processes messages containing media, mentions, and replies.
func HandleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Delay 3 seconds to allow embeds to load
	go func() {
		time.Sleep(3 * time.Second)
		fetchedMessage, _ := s.ChannelMessage(m.Message.ChannelID, m.Message.ID)
		if fetchedMessage == nil {
			return
		}

		options := hasher.HashOptions{Threshold: 1, IgnoreExtensions: []string{".gif"}}
		snails := hasher.FindSnails(m.GuildID, fetchedMessage, options)
		if snails != "" {
			s.MessageReactionAdd(m.ChannelID, m.Message.ID, "pensivesnail:908355170667212810")
		}

		if utility.HasImageURL(fetchedMessage) || utility.HasVideoURL(fetchedMessage) {
			options := hasher.HashOptions{Store: true}
			hasher.HashAttachments(fetchedMessage, options)
		}
	}()

	// Ignore messages from the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Ignore messages from other bots
	if m.Author.Bot {
		return
	}

	var chatMessages []anthropic.Message // Chat messages for building the message history
	var cache []*discordgo.Message       // Cache of messages for replies
	botMentioned, isReply := false, false

	// Check if bot was mentioned in the message
	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			botMentioned = true
			break
		}
	}

	// Check if message is a reply
	if m.Type == discordgo.MessageTypeReply {
		// Check if bot is mentioned or replied to in the referenced message
		if (m.ReferencedMessage.Author.ID == s.State.User.ID || botMentioned) && m.ReferencedMessage != nil {
			cache = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
			isReply = true
		}
	}

	// Process messages containing media, mentions, and replies
	if botMentioned || isReply {
		// Log message details
		if isReply {
			log.Printf("%s reply: %s", m.Author.Username, m.Content)
		} else {
			log.Printf("%s mention: %s", m.Author.Username, m.Content)
		}

		// Clean and prepare message content
		m.Message = utility.CleanMessage(s, m.Message)
		images, _ := utility.GetMessageMediaURL(m.Message)

		var transcript string
		if utility.HasAccessRole(m.Message.Member) {
			transcript = openai.GetTranscript(s, m.Message)
		}

		content := config.RequestContent{
			Text: fmt.Sprintf("%s: %s\n %s\n %s\n %s",
				m.Author.Username,
				transcript,
				utility.AttachmentText(m.Message),
				utility.EmbedText(m.Message),
				m.Content,
			),
			URL: images,
		}

		// Append message to chat messages
		ant.AppendMessage(anthropic.RoleUser, content, &chatMessages)

		// Prepend reply messages to chat messages if applicable
		if isReply {
			ant.PrependReplyMessages(s, m.Message.Member, m.Message, cache, &chatMessages)
			ant.PrependUserMessagePlaceholder(&chatMessages)
		}

		// Stream message response
		ant.StreamMessageResponse(s, m.Message, chatMessages, nil)
	}
}
