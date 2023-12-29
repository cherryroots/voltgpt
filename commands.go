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
	writePermission int64 = discordgo.PermissionSendMessages
	//adminPermission int64   = discordgo.PermissionAdministrator
	dmPermission         = false
	tempMin              = 0.01
	integerMin   float64 = 1

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
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "remove",
					Description: "Remove the user from the wheel",
					Required:    false,
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

			var options = newGenerationOptions()

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
			var reqMessage = createMessage(openai.ChatMessageRoleUser, "", content)
			sendInteractionChatResponse(s, i, reqMessage, options)
		},
		"summarize": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)

			var options = newGenerationOptions()
			var count = 0
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

			var messages = getChannelMessages(s, i.ChannelID, count)
			messages = cleanMessages(s, messages)

			var chatMessages = createBatchMessages(s, messages)
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
			var msgCount, hashCount = 0, 0

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "channel" {
					channelID = option.Value.(string)
				}
			}
			outputMessage = fmt.Sprintf("Retrieving messages for channel: <#%s>", channelID)
			iMsg, _ := sendFollowup(s, i, outputMessage)
			fMsg, _ := sendMessage(s, iMsg, "Retrieving messages...")
			c := make(chan []*discordgo.Message)

			go getAllChannelMessages(s, fMsg, channelID, c)

			var wg sync.WaitGroup
			for messageStream := range c {
				wg.Add(1)
				go func(messages []*discordgo.Message) {
					defer wg.Done()
					msgCount += len(messages)
					for _, message := range messages {
						if hasImageURL(message) {
							var count int
							_, count = hashAttachments(message, true)
							hashCount += count
						}
					}
				}(messageStream)
			}

			wg.Wait()

			outputMessage = fmt.Sprintf("Messages: %d\nHashes: %d", msgCount, hashCount)

			_, err := editMessage(s, fMsg, outputMessage)
			if err != nil {
				log.Println(err)
			}

		},
		"TTS": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			var files = splitTTS(message.Content, true)

			_, err := sendFollowupFile(s, i, linkFromIMessage(i, message), files)
			if err != nil {
				log.Println(err)
			}
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
					timestamp := result.message.Timestamp.UTC().Format("2006-01-02")
					if result.message.Author.ID == i.Interaction.Member.User.ID {
						messageContent += fmt.Sprintf("%dd: %s: Snail of yourself! %s\n", result.distance, timestamp, linkFromIMessage(i, result.message))
						continue
					}
					messageContent += fmt.Sprintf("%dd: %s: Snail of %s! %s\n", result.distance, timestamp, result.message.Author.Username, linkFromIMessage(i, result.message))
				}
			}

			if messageContent == "" {
				messageContent = "Fresh Content!"
			}

			_, err := sendFollowup(s, i, messageContent)
			if err != nil {
				log.Println(err)
			}
		},
		"Hash": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]
			var count int

			if hasImageURL(message) {
				_, count = hashAttachments(message, true)
			}
			_, err := sendFollowup(s, i, fmt.Sprintf("Hashed: %d", count))
			if err != nil {
				log.Println(err)
			}
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
				round = wheel.currentRound().ID + 1
			}

			embed := wheel.statusEmbed(wheel.round(round))
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Embeds:     []*discordgo.MessageEmbed{&embed},
				Components: roundMessageComponents,
			})
			if err != nil {
				log.Println(err)
			}
		},
		"wheel_add": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)
			var userID string
			var remove bool

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "user" {
					userID = option.Value.(string)
				}
				if option.Name == "remove" {
					remove = option.Value.(bool)
				}
			}

			if userID == "" {
				_, err := sendFollowup(s, i, "Please specify a user to add to the wheel!")
				if err != nil {
					log.Println(err)
				}
			}

			member, _ := s.GuildMember(i.GuildID, userID)

			var message string
			var player = player{
				User: member.User,
			}
			if remove {
				wheel.removeWheelOption(player)
				message = fmt.Sprintf("Removed %s from the wheel!", player.User.Username)
			} else {
				wheel.addWheelOption(player)
				message = fmt.Sprintf("Added %s to the wheel!", player.User.Username)
			}

			_, err := sendFollowup(s, i, message)
			if err != nil {
				log.Println(err)
			}
		},
	}

	componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"button_refresh": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

			embed := wheel.statusEmbed(wheel.currentRound())
			err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
				Type: discordgo.InteractionResponseUpdateMessage,
				Data: &discordgo.InteractionResponseData{
					Embeds:     []*discordgo.MessageEmbed{&embed},
					Components: roundMessageComponents,
				},
			})
			if err != nil {
				log.Println(err)
			}
		},
		"button_claim": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			var player = player{
				User: i.Interaction.Member.User,
			}

			wheel.addPlayer(player)

			wheel.Rounds[wheel.currentRound().ID].addClaim(player)

			_, err := sendFollowup(s, i, fmt.Sprintf("Claimed 100!"))
			if err != nil {
				log.Println(err)
			}
		},
		"button_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

			wheel.addPlayer(player{User: i.Interaction.Member.User})

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
			selectedUser := i.MessageComponentData().Values
			round := wheel.currentRound().ID

			if strings.Contains(i.MessageComponentData().CustomID, "remove") {
				member, _ := s.GuildMember(i.GuildID, selectedUser[0])
				var by = player{
					User: i.Interaction.Member.User,
				}
				var on = player{
					User: member.User,
				}
				wheel.Rounds[round].removeBet(by, on)
				err := updateResponse(s, i, "Removed bet!")
				if err != nil {
					log.Println(err)
				}
				return
			}
			if strings.Contains(i.MessageComponentData().CustomID, "winner") {
				member, _ := s.GuildMember(i.GuildID, selectedUser[0])
				var player = player{
					User: member.User,
				}

				if len(wheel.Rounds[round].Bets) == 0 {
					err := updateResponse(s, i, "No bets!")
					if err != nil {
						log.Println(err)
					}
					return
				}

				if !wheel.Rounds[round].hasWinner() {
					wheel.addRound()
				}

				wheel.Rounds[round].setWinner(player)
				err := updateResponse(s, i, "Set winner!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			wheel.sendModal(s, i, selectedUser[0])
		},
	}

	modalHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"modal_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ModalSubmitData().CustomID, i.Interaction.Member.User.Username)

			userID := strings.Split(i.ModalSubmitData().CustomID, "-")[1]
			user, _ := s.GuildMember(i.GuildID, userID)

			var byPlayer = player{
				User: i.Interaction.Member.User,
			}
			var onPlayer = player{
				User: user.User,
			}

			modalInput := i.ModalSubmitData().Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
			amount, err := strconv.Atoi(modalInput)
			if err != nil {
				err := updateResponse(s, i, "Invalid amount")
				if err != nil {
					log.Println(err)
				}
				return
			}

			options := len(wheel.currentWheelOptions())
			playerBets := wheel.playerBets(byPlayer)
			if options%2 == 1 {
				options += 1
			}
			if playerBets >= (options / 2) {
				err := updateResponse(s, i, "You can only bet on half of the players")
				if err != nil {
					log.Println(err)
				}
				return

			}

			if amount > wheel.playerUsableMoney(byPlayer) {
				err := updateResponse(s, i, "You don't have that much money")
				if err != nil {
					log.Println(err)
				}
				return
			}

			if amount <= 0 {
				err := updateResponse(s, i, "You can't place a bet of 0 or lower")
				if err != nil {
					log.Println(err)
				}
				return
			}

			bet := bet{
				By:     byPlayer,
				On:     onPlayer,
				Amount: amount,
			}

			wheel.Rounds[wheel.currentRound().ID].addBet(bet)
			message := fmt.Sprintf("Bet on %s for %d", onPlayer.User.Username, amount)

			err = updateResponse(s, i, message)
			if err != nil {
				log.Println(err)
			}

		},
	}
)

func handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return
	}

	go func(m *discordgo.MessageCreate) {
		fetchedMessage, _ := s.ChannelMessage(m.Message.ChannelID, m.Message.ID)
		if hasImageURL(fetchedMessage) {
			hashAttachments(fetchedMessage, true)
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
				url:  getMessageImages(m.Message),
			}
			var chatMessages = createMessage(openai.ChatMessageRoleUser, m.Author.Username, content)
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
				url:  getMessageImages(m.Message),
			}
			var chatMessages = createMessage(openai.ChatMessageRoleUser, "", content)
			sendMessageChatResponse(s, m, chatMessages)
			return
		}
	}
}
