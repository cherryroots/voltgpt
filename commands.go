package main

import (
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var (
	writePermission int64   = discordgo.PermissionSendMessages
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
			Name: "TTS",
			Type: discordgo.MessageApplicationCommand,
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ask": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var options *responseOptions = newResponseOptions()

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
				url:  []string{options.imageUrl},
			}
			var reqMessage []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, "", content)
			sendInteractionChatResponse(s, i, reqMessage, options)
		},
		"summarize": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var options *responseOptions = newResponseOptions()
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

			var messages []*discordgo.Message = getMessages(s, i.ChannelID, count)
			messages = cleanMessages(s, messages)

			var chatMessages []openai.ChatCompletionMessage = createBatchMessages(s, messages)
			content := requestContent{
				text: options.message,
			}
			appendMessage(openai.ChatMessageRoleUser, i.Member.User.Username, content, &chatMessages)
			sendInteractionChatResponse(s, i, chatMessages, options)
		},
		"TTS": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			var files []*discordgo.File = splitTTS(message.Content, true)

			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: createMessageLink(s, i, message),
				Files:   files,
			})
		},
	}
)

func handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	if m.Author.Bot {
		return
	}

	m.Message = cleanMessage(s, m.Message)

	if m.Type == discordgo.MessageTypeReply {
		if m.ReferencedMessage == nil {
			return
		}
		if m.ReferencedMessage.Author.ID == s.State.User.ID {
			cache := getMessagesBefore(s, m.ChannelID, 100, m.ID)
			log.Println("reply:", m.Content)
			content := requestContent{
				text: m.Content,
				url:  getAttachments(s, m.Message),
			}
			var chatMessages []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, "", content)
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
				url:  getAttachments(s, m.Message),
			}
			var chatMessages []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, "", content)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}
}
