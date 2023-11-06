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
					MaxValue:    100,
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
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ask": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var message string
			var temperature float32 = 0
			var model string = ""
			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "question" {
					message = option.Value.(string)
					log.Println("ask:", message)
				}
				if option.Name == "temperature" {
					temperature = float32(option.Value.(float64))
				}
				if option.Name == "model" {
					model = option.Value.(string)
				}
			}
			if temperature == 0 {
				temperature = 0.7
			}
			if model == "" {
				model = openai.GPT40314
			}

			var reqMessage []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, i.Member.User.Username, message)
			sendInteractionChatResponse(s, i, reqMessage, temperature, model)
		},
		"summarize": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var message string
			var count int = 0
			var temperature float32 = 0
			var model string = ""
			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "question" {
					message = option.Value.(string)
					log.Println("summarize:", message)
				}
				if option.Name == "count" {
					count = int(option.Value.(float64))
				}
				if option.Name == "temperature" {
					temperature = float32(option.Value.(float64))
				}
				if option.Name == "model" {
					model = option.Value.(string)
				}
			}
			if count == 0 {
				count = 20
			}
			if temperature == 0 {
				temperature = 0.7
			}
			if model == "" {
				model = openai.GPT40314
			}

			var messages []*discordgo.Message = getMessages(s, i.ChannelID, count)
			messages = cleanMessages(s, messages)

			var chatMessages []openai.ChatCompletionMessage = createBatchMessages(s, messages)
			appendMessage(openai.ChatMessageRoleUser, i.Member.User.Username, message, &chatMessages)
			sendInteractionChatResponse(s, i, chatMessages, temperature, model)
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
			cache := getMessageBefore(s, m.ChannelID, 100, m.ID)
			log.Println("reply:", m.Content)
			var chatMessages []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, m.Author.Username, m.Content)
			checkForReplies(s, m.Message, cache, &chatMessages)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			m.Message = cleanMessage(s, m.Message)
			log.Println("mention:", m.Content)
			var chatMessages []openai.ChatCompletionMessage = createMessage(openai.ChatMessageRoleUser, m.Author.Username, m.Content)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}
}
