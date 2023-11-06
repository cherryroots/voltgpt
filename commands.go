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
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ask": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)

			var temperature float32 = 0
			var model string = ""
			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "question" {
					message := option.Value.(string)
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

			switch model {
			case openai.GPT3Davinci002, openai.GPT3Dot5TurboInstruct:
				sendInteractionCompletionResponse(s, i, temperature, model)
			default:
				sendInteractionChatResponse(s, i, temperature, model)
			}
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
			cache := getMessageCache(s, m.ChannelID, m.ID)
			log.Println("reply:", m.Content)
			chatMessages := createMessage(openai.ChatMessageRoleUser, m.Author.Username, m.Content)
			checkForReplies(s, m.Message, cache, &chatMessages)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			m.Message = cleanMessage(s, m.Message)
			log.Println("mention:", m.Content)
			chatMessages := createMessage(openai.ChatMessageRoleUser, m.Author.Username, m.Content)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}
}
