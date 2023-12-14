package main

import (
	"fmt"
	"log"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var (
	writePermission int64   = discordgo.PermissionSendMessages
	adminPermission int64   = discordgo.PermissionAdministrator
	dmPermission    bool    = false
	tempMin         float64 = 0.01
	integerMin      float64 = 1

	commands = []*discordgo.ApplicationCommand{
		{
			Name:                     "ask",
			Description:              "Ask a question (default gpt-4-0314 and 0.7 temperature)",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "question",
					Description: "question to ask",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "image",
					Description: "image to use as context",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "temperature",
					Description: "Choose a number between 0 and 2. Higher values are more random, lower values are more factual.",
					MinValue:    &tempMin,
					MaxValue:    2,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "model",
					Description: "Pick a model to use",
					Choices:     modelChoices,
				},
			},
		},
		{
			Name:                     "summarize",
			Description:              "Summarize the message history of the channel (default 20 messages)",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "question",
					Description: "question to ask",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "count",
					Description: "Number of messages to include in the summary. ",
					Required:    false,
					MinValue:    &integerMin,
					MaxValue:    200,
				},
				{
					Type:        discordgo.ApplicationCommandOptionNumber,
					Name:        "temperature",
					Description: "Choose a number between 0 and 2. Higher values are more random, lower values are more factual.",
					MinValue:    &tempMin,
					MaxValue:    2,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "model",
					Description: "Pick a model to use",
					Choices:     modelChoices,
				},
			},
		},
		{
			Name:                     "hash_channel",
			Description:              "Get the message history of the channel (default 20 messages)",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "Which channel to retrieve messages from",
					Required:    true,
				},
			},
		},
		{
			Name: "TTS",
			Type: discordgo.MessageApplicationCommand,
		},
		{
			Name: "CheckSnail",
			Type: discordgo.MessageApplicationCommand,
		},
		{
			Name: "Hash",
			Type: discordgo.MessageApplicationCommand,
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ask": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var options *generationOptions = newGenerationOptions()

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "question" {
					options.message = option.Value.(string)
					log.Println("ask:", options.message)
				}
				if option.Name == "image" {
					options.imageUrl = option.Value.(string)
				}
				if option.Name == "temperature" {
					options.temperature = float32(option.Value.(float64))
				}
				if option.Name == "model" {
					options.model = option.Value.(string)
				}
			}

			content := requestContent{
				text: options.message,
			}
			if options.imageUrl != "" {
				content.url = append(content.url, options.imageUrl)
			}
			var reqMessage []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, "", content)
			sendInteractionChatResponse(s, i, reqMessage, options)
		},
		"summarize": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var options *generationOptions = newGenerationOptions()
			var count int = 0
			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "question" {
					options.message = option.Value.(string)
					log.Println("summarize:", options.message)
				}
				if option.Name == "count" {
					count = int(option.Value.(float64))
				}
				if option.Name == "temperature" {
					options.temperature = float32(option.Value.(float64))
				}
				if option.Name == "model" {
					options.model = option.Value.(string)
				}
			}
			if count == 0 {
				count = 20
			}

			var messages []*discordgo.Message = getChannelMessages(s, i.ChannelID, count)
			messages = cleanMessages(s, messages)

			var chatMessages []openai.ChatCompletionMessage = createBatchMessages(s, messages)
			content := requestContent{
				text: options.message,
			}
			appendMessage(openai.ChatMessageRoleUser, i.Member.User.Username, content, &chatMessages)
			sendInteractionChatResponse(s, i, chatMessages, options)
		},
		"hash_channel": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var channelID string
			var msgCount, hashes int = 0, 0

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "channel" {
					channelID = option.Value.(string)
				}
			}

			var outputMessage string
			c := make(chan []*discordgo.Message)

			fMsg, _ := sendFollowup(s, i, "Retrieving messages...", []*discordgo.File{})

			go getAllChannelMessages(s, i, channelID, fMsg.ID, c)

			var wg sync.WaitGroup
			for msgs := range c {
				wg.Add(1)
				go func(msgs []*discordgo.Message) {
					defer wg.Done()
					msgCount += len(msgs)
					for _, msg := range msgs {
						if hasImageURL(msg) {
							var count int
							_, count = hashAttachments(s, msg, true)
							hashes += count
						}
					}
				}(msgs)
			}

			wg.Wait()

			outputMessage += fmt.Sprintf("\nHashes: %d", hashes)

			editFollowup(s, i, fMsg.ID, outputMessage, []*discordgo.File{})

		},
		"TTS": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			var files []*discordgo.File = splitTTS(message.Content, true)

			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: linkFromIMessage(s, i, message),
				Files:   files,
			})
		},
		"CheckSnail": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferEphemeralResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			isSnail, messages := checkInHashes(s, message)
			var messageContent string
			if isSnail {
				for _, msg := range messages {
					if msg.ID == message.ID {
						continue
					}
					if msg.Author.ID == message.Author.ID {
						messageContent += fmt.Sprintf("Snail of yourself! %s\n", linkFromIMessage(s, i, msg))
						continue
					}
					messageContent += fmt.Sprintf("Snail of %s! %s\n", msg.Author.Username, linkFromIMessage(s, i, msg))
				}
			}

			if messageContent == "" {
				messageContent = "Fresh Content!"
			}

			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: messageContent,
			})
		},
		"Hash": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferEphemeralResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]
			var count int

			if hasImageURL(message) {
				_, count = hashAttachments(s, message, true)
			}

			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("Hashed: %d", count),
			})
		},
	}
)

func handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	go func(m *discordgo.MessageCreate) {
		refetchedMessage, _ := s.ChannelMessage(m.Message.ChannelID, m.Message.ID)
		if hasImageURL(refetchedMessage) {
			hashAttachments(s, refetchedMessage, true)
		}
	}(m)

	if m.Author.Bot {
		return
	}

	m.Message = cleanMessage(s, m.Message)

	if m.Type == discordgo.MessageTypeReply {
		if m.ReferencedMessage == nil {
			return
		}
		botMentioned := false
		for _, mention := range m.Mentions {
			if mention.ID == s.State.User.ID {
				botMentioned = true
				break
			}
		}
		if m.ReferencedMessage.Author.ID == s.State.User.ID || botMentioned {
			cache := getMessagesBefore(s, m.ChannelID, 100, m.ID)
			log.Println("reply:", m.Content)
			content := requestContent{
				text: m.Content,
				url:  getMessageImages(s, m.Message),
			}
			var chatMessages []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, m.Author.Username, content)
			checkForReplies(s, m.Message, cache, &chatMessages)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			m.Message = cleanMessage(s, m.Message)
			log.Println("mention:", m.Content)
			content := requestContent{
				text: m.Content,
				url:  getMessageImages(s, m.Message),
			}
			var chatMessages []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, "", content)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}
}
