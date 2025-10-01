package config

import (
	"github.com/bwmarrin/discordgo"
)

var (
	writePermission int64 = discordgo.PermissionSendMessages
	// adminPermission int64   = discordgo.PermissionAdministrator
	dmPermission         = false
	integerMin   float64 = 1
	guidanceMin  float64

	// Commands are the commands that the bot will respond to.
	Commands = []*discordgo.ApplicationCommand{
		{
			Name:                     "draw",
			Description:              "Draw an image",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "prompt",
					Description: "prompt to use",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "amount",
					Description: "Amount of images to generate",
					Required:    false,
					MinValue:    &integerMin,
					MaxValue:    4,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "resolution",
					Description: "resolution to use",
					Required:    false,
					Choices:     ResolutionChoices,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "urls",
					Description: "jpg/jpeg/png urls separated by spaces",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "image1",
					Description: "jpg/jpeg/png",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "image2",
					Description: "jpg/jpeg/png",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "image3",
					Description: "jpg/jpeg/png",
					Required:    false,
				},
			},
		},
		{
			Name:                     "video",
			Description:              "Create a video",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "prompt",
					Description: "prompt to use",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionAttachment,
					Name:        "image",
					Description: "jpg/jpeg/png",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "duration",
					Description: "Duration of the video (default: 5)",
					Required:    false,
					Choices:     DurationChoices,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "audio",
					Description: "Add audio to the video",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "audioprompt",
					Description: "Audio prompt to use",
					Required:    false,
				},
			},
		},
		{
			Name:                     "hash_server",
			Description:              "Hash all the images and videos in the server",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "Which channel to retrieve messages from",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "threads",
					Description: "Whether to include threads",
					Required:    false,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "date",
					Description: "yyyy/mm/dd, inclusive",
					Required:    false,
				},
			},
		},
		{
			Name:                     "wheel_status",
			Description:              "Movie wheel",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "round",
					Description: "Which round to retrieve messages from",
					Required:    false,
					MinValue:    &integerMin,
				},
			},
		},
		{
			Name:                     "wheel_add",
			Description:              "Add a user to the wheel",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "User to add to the wheel",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "remove",
					Description: "Remove the user from the wheel",
					Required:    false,
				},
			},
		},
		{
			Name:                     "insert_bet",
			Description:              "Add a bet to a round",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "by",
					Description: "Who made the bet",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "on",
					Description: "Who to bet on",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "amount",
					Description: "How much was bet",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "round",
					Description: "Which round to place it on",
					Required:    true,
				},
			},
		},
		{
			Name:                     "reset_wheel",
			Description:              "Reset the wheel",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
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
)
