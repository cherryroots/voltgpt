package main

import (
	"log"

	"github.com/bwmarrin/discordgo"
)

var (
	adminCommandPermission int64 = discordgo.PermissionAdministrator
	writePermission        int64 = discordgo.PermissionSendMessages

	commands = []*discordgo.ApplicationCommand{
		{
			Name:                     "ask",
			Description:              "Ask a question to gpt",
			DefaultMemberPermissions: &writePermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "question",
					Description: "question to ask",
					Required:    true,
				},
			},
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"ask": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			deferResponse(s, i)
			message := i.ApplicationCommandData().Options[0].Value.(string)
			log.Println("ask:", message)
			sendInteractionResponse(s, i)
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
			sendMessageResponse(s, m)
		}
	}
}
