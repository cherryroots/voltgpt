package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic"
	"github.com/sashabaranov/go-openai"
)

var (
	writePermission int64 = discordgo.PermissionSendMessages
	// adminPermission int64   = discordgo.PermissionAdministrator
	dmPermission         = false
	tempMin              = 0.01
	integerMin   float64 = 1

	commands = []*discordgo.ApplicationCommand{
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
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "size",
					Description: "image size",
					Choices: []*discordgo.ApplicationCommandOptionChoice{
						{
							Name:  "1024x1024",
							Value: openai.CreateImageSize1024x1024,
						},
						{
							Name:  "1792x1024",
							Value: openai.CreateImageSize1792x1024,
						},
						{
							Name:  "1024x1792",
							Value: openai.CreateImageSize1024x1792,
						},
					},
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
					MaxValue:    500,
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
		"draw": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)

			var prompt, size string
			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "prompt" {
					prompt = option.StringValue()
					log.Println("draw:", prompt)
				}
				if option.Name == "size" {
					size = option.StringValue()
				}
			}
			if size == "" {
				size = openai.CreateImageSize1024x1024
			}

			image, err := drawImage(prompt, size)
			if err != nil {
				log.Println(err)
				_, err = sendFollowup(s, i, err.Error())
				if err != nil {
					log.Println(err)
				}
				return
			}
			_, err = sendFollowupFile(s, i, prompt, image)
			if err != nil {
				log.Println(err)
			}
		},
		"summarize": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)

			options := newOAIGenerationOptions()
			count := 0
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

			messages := getChannelMessages(s, i.ChannelID, count)
			messages = cleanMessages(s, messages)

			chatMessages := createBatchOAIMessages(s, messages)
			content := requestContent{
				text: options.message,
			}
			appendOAIMessage(openai.ChatMessageRoleUser, i.Member.User.Username, content, &chatMessages)
			streamInteractionResponse(s, i, chatMessages, options)
		},
		"hash_channel": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferResponse(s, i)

			var channelID string
			var outputMessage string
			msgCount, hashCount := 0, 0

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "channel" {
					channelID = option.Value.(string)
				}
			}

			if !isAdmin(i.Interaction.Member.User.ID) {
				_, err := sendFollowup(s, i, "Only admins can add players to the wheel!")
				if err != nil {
					log.Println(err)
				}
				return
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

			files := splitTTS(message.Content, true)

			_, err := sendFollowupFile(s, i, linkFromIMessage(i, message), files)
			if err != nil {
				log.Println(err)
			}
		},
		"CheckSnail": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			isSnail, results := checkInHashes(message)
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
					round = int(option.IntValue())
				}
			}

			if round == 0 {
				round = wheel.currentRound().ID + 1
			}

			embed := wheel.statusEmbed(wheel.round(round))
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Embeds:     []*discordgo.MessageEmbed{&embed},
				Components: roundMessageComponents,
				Flags:      1 << 12,
			})
			if err != nil {
				log.Println(err)
			}
		},
		"wheel_add": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)
			var user *discordgo.User
			var remove bool

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "user" {
					user = option.UserValue(s)
				}
				if option.Name == "remove" {
					remove = option.BoolValue()
				}
			}

			if !isAdmin(i.Interaction.Member.User.ID) {
				_, err := sendFollowup(s, i, "Only admins can add players to the wheel!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			var message string
			player := player{
				User: user,
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
		"insert_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Recieved interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			var on, by *discordgo.User
			var amount, round int

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "on" {
					on = option.UserValue(s)
				}
				if option.Name == "by" {
					by = option.UserValue(s)
				}
				if option.Name == "amount" {
					amount = int(option.IntValue())
				}
				if option.Name == "round" {
					round = int(option.IntValue())
				}
			}

			if !isAdmin(i.Interaction.Member.User.ID) {
				_, err := sendFollowup(s, i, "Only admins can add players to the wheel!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			onPlayer := player{
				User: on,
			}
			byPlayer := player{
				User: by,
			}

			bet := bet{
				By:     byPlayer,
				On:     onPlayer,
				Amount: amount,
			}

			wheel.Rounds[round-1].addBet(bet)

			message := fmt.Sprintf("Added bet on %s, by %s for %d on round %d", onPlayer.User.Username, byPlayer.User.Username, amount, round)

			_, err := sendFollowup(s, i, message)
			if err != nil {
				log.Println(err)
			}
		},
	}

	componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"button_refresh": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			if len(wheel.Rounds) == 0 {
				wheel.addRound()
			}

			embed := wheel.statusEmbed(wheel.currentRound())
			message := fmt.Sprintf("Refreshed wheel to round %d", wheel.currentRound().ID+1)

			_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
				Channel:    i.ChannelID,
				ID:         i.Message.ID,
				Embeds:     &[]*discordgo.MessageEmbed{&embed},
				Components: &roundMessageComponents,
			})
			if err != nil {
				log.Println(err)
				message += "\n\n" + err.Error()
			}

			_, err = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: &message,
			})
			if err != nil {
				log.Println(err)
			}

			err = sleepDeleteInteraction(s, i, 3)
			if err != nil {
				log.Println(err)
			}
		},
		"button_claim": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			deferEphemeralResponse(s, i)

			player := player{
				User: i.Interaction.Member.User,
			}

			wheel.addPlayer(player)

			message := ""
			hasClaimed := false

			for _, claim := range wheel.Rounds[wheel.currentRound().ID].Claims {
				if claim.id() == player.id() {
					message = "You've already claimed!"
					hasClaimed = true
					break
				}
			}

			if !hasClaimed {
				wheel.Rounds[wheel.currentRound().ID].addClaim(player)
				message = "Claimed 100!"
			}

			_, err := sendFollowup(s, i, message)
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
			if !isAdmin(i.Interaction.Member.User.ID) {
				deferEphemeralResponse(s, i)
				_, err := sendFollowup(s, i, "Only admins can pick winners!")
				if err != nil {
					log.Println(err)
				}
				return
			}
			wheel.sendMenu(s, i, false, true)
		},
		"menu_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			selectedUser := i.MessageComponentData().Values
			round := wheel.currentRound().ID

			if strings.Contains(i.MessageComponentData().CustomID, "remove") {
				member, _ := s.GuildMember(i.GuildID, selectedUser[0])
				by := player{
					User: i.Interaction.Member.User,
				}
				on := player{
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
				player := player{
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

			byPlayer := player{
				User: i.Interaction.Member.User,
			}
			onPlayer := player{
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

			bet := bet{
				By:     byPlayer,
				On:     onPlayer,
				Amount: amount,
			}

			existingBet, err := wheel.Rounds[wheel.currentRound().ID].hasBet(bet)
			existingAmount := 0
			if err == nil {
				existingAmount = existingBet.Amount
			}

			options := len(wheel.currentWheelOptions())
			playerBets, _ := wheel.playerBets(byPlayer, wheel.currentRound())
			if options%2 == 1 {
				options++
			}
			if playerBets >= (options / 2) {
				err := updateResponse(s, i, "You can only bet on half of the players")
				if err != nil {
					log.Println(err)
				}
				return

			}

			if amount > wheel.playerUsableMoney(byPlayer)+existingAmount {
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

	// var chatMessages []openai.ChatCompletionMessage
	var chatMessages []anthropic.Message
	var cache []*discordgo.Message
	botMentioned, isReply := false, false

	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			botMentioned = true
			break
		}
	}

	if m.Type == discordgo.MessageTypeReply {
		if (m.ReferencedMessage.Author.ID == s.State.User.ID || botMentioned) && m.ReferencedMessage != nil {
			cache = getMessagesBefore(s, m.ChannelID, 100, m.ID)
			isReply = true
		}
	}

	if botMentioned || isReply {
		if isReply {
			log.Printf("reply: %s", m.Content)
		} else {
			log.Printf("mention: %s", m.Content)
		}

		m.Message = cleanMessage(s, m.Message)

		content := requestContent{
			text: fmt.Sprintf("%s: %s", m.Author.Username, m.Message.Content),
			url:  getMessageImages(m.Message),
		}

		// appendOAIMessage(openai.ChatMessageRoleUser, m.Author.Username, content, &chatMessages)
		appendANTMessage(anthropic.RoleUser, content, &chatMessages)
		if isReply { // insert replies before the message sent to the bot
			// prependRepliesOAIMessages(s, m.Message, cache, &chatMessages)
			prependRepliesANTMessages(s, m.Message, cache, &chatMessages)
		}
		/*
			instructionMessage := instructionSwitchOAI(chatMessages)
			if instructionMessage.text != "" { // get the instruction and if it exists prepend it
				prependOAIMessage(openai.ChatMessageRoleSystem, m.Author.Username, instructionMessage, &chatMessages)
			}
		*/
		// prependOAIMessage(openai.ChatMessageRoleSystem, "", systemMessageDefault, &chatMessages)
		// streamMessageOAIResponse(s, m, chatMessages)
		streamMessageANTResponse(s, m, chatMessages)
		return
	}
}
