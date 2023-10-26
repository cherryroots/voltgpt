package main

import (
	"log"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var (
	writePermission int64 = discordgo.PermissionSendMessages

	commands = []*discordgo.ApplicationCommand{
		{
			Name:                     "ask",
			Description:              "Ask a question (default gpt-4-0314)",
			DefaultMemberPermissions: &writePermission,
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
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "gpt-4-0613",
							Value: openai.GPT40613,
						},
						{
							Name:  "gpt-4-0314",
							Value: openai.GPT40314,
						},
						{
							Name:  "gpt-4-32k-0613",
							Value: openai.GPT432K0613,
						},
						{
							Name:  "gpt-4-32k-0314",
							Value: openai.GPT432K0314,
						},
						{
							Name:  "gpt-3.5-turbo-16k-0613",
							Value: openai.GPT3Dot5Turbo16K0613,
						},
						{
							Name:  "gpt-3.5-turbo-0613",
							Value: openai.GPT3Dot5Turbo0613,
						},
						{
							Name:  "gpt-3.5-turbo-0301",
							Value: openai.GPT3Dot5Turbo0301,
						},
						{
							Name:  "davinci-002",
							Value: openai.GPT3Davinci002,
						},
						{
							Name:  "gpt-3.5-turbo-instruct",
							Value: openai.GPT3Dot5TurboInstruct,
						},
					},
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

			if model == openai.GPT3Davinci002 || model == openai.GPT3Dot5TurboInstruct {
				sendInteractionCompletionResponse(s, i, model)
			} else {
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
