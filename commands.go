package main

import (
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var (
	writePermission int64 = discordgo.PermissionSendMessages
	dmPermission    bool  = false

	commands = []*discordgo.ApplicationCommand{
		{
			Name:                     "ask",
			Description:              "Ask a question (default gpt-4-0314)",
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

			var model string = ""
			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "question" {
					message := option.Value.(string)
					log.Println("ask:", message)
				}
				if option.Name == "model" {
					model = option.Value.(string)
				}
			}
			if model == "" {
				model = openai.GPT40314
			}

			switch model {
			case openai.GPT3Davinci002, openai.GPT3Dot5TurboInstruct:
				sendInteractionCompletionResponse(s, i, model)
			default:
				sendInteractionChatResponse(s, i, model)
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

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			log.Println("mention:", m.Content)
			sendMessageChatResponse(s, m)
		}
	}
}
