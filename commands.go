package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
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
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
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
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
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
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)

			var channelID string
			var outputMessage string
			var msgCount, hashCount int = 0, 0

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "channel" {
					channelID = option.Value.(string)
				}
			}
			outputMessage = fmt.Sprintf("Retrieving messages for channel: <#%s>", channelID)
			iMsg, _ := sendFollowup(s, i, outputMessage)
			fMsg, _ := sendMessage(s, iMsg, "Retrieving messages...")
			c := make(chan []*discordgo.Message)

			go getAllChannelMessages(s, i, fMsg, channelID, c)

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
							hashCount += count
						}
					}
				}(msgs)
			}

			wg.Wait()

			outputMessage = fmt.Sprintf("Messages: %d\nHashes: %d", msgCount, hashCount)

			editMessage(s, fMsg, outputMessage)

		},
		"TTS": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			var files []*discordgo.File = splitTTS(message.Content, true)

			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: linkFromIMessage(s, i, message),
				Files:   files,
			})
		},
		"CheckSnail": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			isSnail, results := checkInHashes(s, message)
			var messageContent string
			if isSnail {
				for _, result := range results {
					if result.message.ID == message.ID {
						continue
					}
					if result.message.Timestamp.After(message.Timestamp) {
						continue
					}
					if result.message.Author.ID == i.Interaction.Member.User.ID {
						messageContent += fmt.Sprintf("%dd: Snail of yourself! %s\n", result.distance, linkFromIMessage(s, i, result.message))
						continue
					}
					messageContent += fmt.Sprintf("%dd: Snail of %s! %s\n", result.distance, result.message.Author.Username, linkFromIMessage(s, i, result.message))
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
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
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
		"wheel_status": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)
			if len(wheel.Rounds) == 0 {
				wheel.addRound()
			}

			var round int

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "round" {
					round = int(option.Value.(float64))
				}
			}

			if round == 0 {
				round = wheel.currentRound().ID
			}

			embed := wheel.statusEmbed(wheel.getRound(round))
			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Embeds:     []*discordgo.MessageEmbed{&embed},
				Components: roundMessageComponents,
			})
		},
		"wheel_add": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)
			var userid string

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "user" {
					userid = option.Value.(string)
				}
			}

			if userid == "" {
				sendFollowup(s, i, "Please specify a user to add to the wheel!")
			}

			member, _ := s.GuildMember(i.GuildID, userid)

			var player player = player{
				User: member.User,
			}
			wheel.addWheelOption(player)
			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("Added %s to the wheel!", player.User.Username),
			})
		},
	}

	componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"button_refresh": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

			embed := wheel.statusEmbed(wheel.currentRound())
			s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Embeds:     []*discordgo.MessageEmbed{&embed},
					Components: roundMessageComponents,
				},
			})
		},
		"button_claim": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			var player player = player{
				User: i.Interaction.Member.User,
			}

			wheel.addPlayer(player)

			wheel.Rounds[wheel.currentRound().ID].addClaim(player)

			s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Content: fmt.Sprintf("Claimed %s!", i.Interaction.Member.User.Username),
			})
		},
		"button_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			var remove bool
			if strings.Contains(i.MessageComponentData().CustomID, "remove") {
				remove = true
			}
			wheel.sendMenu(s, i, remove, false)
		},
		"button_winner": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			wheel.sendMenu(s, i, false, true)
		},
		"menu_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			selectedUseer := i.MessageComponentData().Values

			if strings.Contains(i.MessageComponentData().CustomID, "remove") {
				member, _ := s.GuildMember(i.GuildID, selectedUseer[0])
				var by player = player{
					User: i.Interaction.Member.User,
				}
				var on player = player{
					User: member.User,
				}
				wheel.Rounds[wheel.currentRound().ID].removeBetOnPlayer(by, on)
				updateResponse(s, i, "Removed bet!")
				return
			} else if strings.Contains(i.MessageComponentData().CustomID, "winner") {
				member, _ := s.GuildMember(i.GuildID, selectedUseer[0])
				var player player = player{
					User: member.User,
				}
				hasWinner := wheel.Rounds[wheel.currentRound().ID].hasWinner()
				wheel.Rounds[wheel.currentRound().ID].setWinner(player)
				if !hasWinner {
					wheel.addRound()
				}
				updateResponse(s, i, "Set winner!")
				return
			}

			wheel.sendModal(s, i, selectedUseer[0])
		},
	}

	modalHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"modal_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ModalSubmitData().CustomID, i.Interaction.Member.User.Username)

			userid := strings.Split(i.ModalSubmitData().CustomID, "-")[1]
			user, _ := s.GuildMember(i.GuildID, userid)

			var byPlayer player = player{
				User: i.Interaction.Member.User,
			}
			var onPlayer player = player{
				User: user.User,
			}

			amountString := i.ModalSubmitData().Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
			amount, err := strconv.Atoi(amountString)
			if err != nil {
				updateResponse(s, i, "Invalid amount")
				return
			}

			if amount > wheel.checkPlayerAvailableMoney(byPlayer) {
				updateResponse(s, i, "You don't have that much money")
				return
			}

			bet := bet{
				By:     byPlayer,
				On:     onPlayer,
				Amount: amount,
			}

			wheel.Rounds[wheel.currentRound().ID].addBet(bet)
			message := fmt.Sprintf("Bet on %s for %d", onPlayer.User.Username, amount)

			updateResponse(s, i, message)

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
