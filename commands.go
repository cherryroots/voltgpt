package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic/v2"

	ant "voltgpt/internal/anthropic"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/gamble"
	"voltgpt/internal/hasher"
	oai "voltgpt/internal/openai"
	"voltgpt/internal/utility"
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
					Name:        "negative-prompt",
					Description: "negative prompt to use",
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "ratio",
					Description: "ratio to use",
					Choices:     config.RatioChoices,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "style",
					Description: "style to use",
					Choices:     config.StyleChoices,
				},
			},
		},
		{
			Name:                     "hash_channel",
			Description:              "Hash all the images and videos in the channel",
			DefaultMemberPermissions: &writePermission,
			DMPermission:             &dmPermission,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionChannel,
					Name:        "channel",
					Description: "Which channel to retrieve messages from",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "threads",
					Description: "Whether to include threads",
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
		{
			Name: "Continue",
			Type: discordgo.MessageApplicationCommand,
		},
	}

	commandHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"draw": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferResponse(s, i)

			var prompt, negativePrompt, ratio, style string
			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "prompt" {
					prompt = option.StringValue()
				}
				if option.Name == "negative_prompt" {
					negativePrompt = option.StringValue()
				}
				if option.Name == "ratio" {
					ratio = option.StringValue()
				}
				if option.Name == "style" {
					style = option.StringValue()
				}
			}
			if negativePrompt == "" {
				negativePrompt = "none"
			}
			if ratio == "" {
				ratio = "1:1"
			}
			if style == "" {
				style = "none"
			}

			image, err := ant.DrawSAIImage(prompt, negativePrompt, ratio, style)
			if err != nil {
				log.Println(err)
				_, err = discord.SendFollowup(s, i, err.Error())
				if err != nil {
					log.Println(err)
				}
				return
			}
			message := fmt.Sprintf("Prompt: %s\nNegative prompt: %s\nRatio: %s\nStyle: %s", prompt, negativePrompt, ratio, style)
			log.Printf("Drawing: %s", message)
			_, err = discord.SendFollowupFile(s, i, message, image)
			if err != nil {
				log.Println(err)
			}
		},
		"hash_channel": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferResponse(s, i)

			var channel *discordgo.Channel
			var threads bool
			var outputMessage string
			msgCount, hashCount := 0, 0

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "channel" {
					channel = option.ChannelValue(s)
				}
				if option.Name == "threads" {
					threads = option.BoolValue()
				}
			}

			if !utility.IsAdmin(i.Interaction.Member.User.ID) {
				_, err := discord.SendFollowup(s, i, "Only admins can add players to the wheel!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			if threads {
				outputMessage = fmt.Sprintf("Retrieving thread messages for channel: <#%s>", channel.ID)
			} else {
				outputMessage = fmt.Sprintf("Retrieving messages for channel: <#%s>", channel.ID)
			}

			hashedMessages, _ := discord.SendFollowup(s, i, "Hashing messages...")
			fetchedMessages, _ := discord.SendMessage(s, hashedMessages, outputMessage)

			messsageStream := make(chan []*discordgo.Message)
			if threads {
				go utility.GetAllChannelThreadMessages(s, fetchedMessages, channel.ID, messsageStream)
			} else {
				go utility.GetAllChannelMessages(s, fetchedMessages, channel.ID, messsageStream)
			}

			var wg sync.WaitGroup
			for messages := range messsageStream {
				wg.Add(1)
				go func(messages []*discordgo.Message) {
					defer wg.Done()
					msgCount += len(messages)
					for _, message := range messages {
						if utility.HasImageURL(message) || utility.HasVideoURL(message) {
							var count int
							_, count = hasher.HashAttachments(message, true)
							hashCount += count
						}
					}
					_, err := discord.EditMessage(s, hashedMessages, fmt.Sprintf("Status: ongoing\nMessages processed: %d\nHashes: %d", msgCount, hashCount))
					if err != nil {
						log.Println(err)
					}
				}(messages)
			}

			wg.Wait()

			outputMessage = fmt.Sprintf("Status: done\nMessages processed: %d\nHashes: %d", msgCount, hashCount)

			_, err := discord.EditMessage(s, hashedMessages, outputMessage)
			if err != nil {
				log.Println(err)
			}
		},
		"TTS": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			files := oai.SplitTTS(message.Content, true)

			_, err := discord.SendFollowupFile(s, i, utility.LinkFromIMessage(i, message), files)
			if err != nil {
				log.Println(err)
			}
		},
		"CheckSnail": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferEphemeralResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			messageContent := hasher.FindSnails(i, message)

			if messageContent == "" {
				messageContent = "Fresh Content!"
			}

			_, err := discord.SendFollowup(s, i, messageContent)
			if err != nil {
				log.Println(err)
			}
		},
		"Hash": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferEphemeralResponse(s, i)

			message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]
			var count int

			if utility.HasImageURL(message) || utility.HasVideoURL(message) {
				_, count = hasher.HashAttachments(message, true)
			}
			_, err := discord.SendFollowup(s, i, fmt.Sprintf("Hashed: %d", count))
			if err != nil {
				log.Println(err)
			}
		},
		"Continue": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferEphemeralResponse(s, i)

			m := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

			if m.Author.ID != s.State.User.ID {
				_, err := discord.SendFollowup(s, i, fmt.Sprint("Not a voltbot message"))
				if err != nil {
					log.Println(err)
				}
				return
			} else if m.Type != discordgo.MessageTypeReply {
				_, err := discord.SendFollowup(s, i, fmt.Sprint("Not a reply message"))
				if err != nil {
					log.Println(err)
				}
				return
			}

			log.Printf("%s continue: %s", i.Interaction.Member.User.Username, utility.LinkFromIMessage(i, m))
			_, err := discord.SendFollowup(s, i, fmt.Sprint("Continuing..."))
			if err != nil {
				log.Println(err)
			}
			err = discord.SleepDeleteInteraction(s, i, 3)
			if err != nil {
				log.Println(err)
			}

			var chatMessages []anthropic.Message
			var cache []*discordgo.Message

			m = utility.CleanMessage(s, m)
			images, _ := utility.GetMessageMediaURL(m)
			content := config.RequestContent{
				Text: fmt.Sprintf("%s", m.Content),
				URL:  images,
			}

			ant.AppendMessage(anthropic.RoleAssistant, content, &chatMessages)

			cache = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
			ant.PrependReplyMessages(s, m, cache, &chatMessages)

			ant.StreamMessageResponse(s, m, chatMessages, m)
		},
		"wheel_status": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferResponse(s, i)
			if len(gamble.Wheel.Rounds) == 0 {
				gamble.Wheel.AddRound()
			}

			var round int

			for _, option := range i.ApplicationCommandData().Options {
				if option.Name == "round" {
					round = int(option.IntValue())
				}
			}

			if round == 0 {
				round = gamble.Wheel.CurrentRound().ID + 1
			}

			embed := gamble.Wheel.StatusEmbed(gamble.Wheel.Round(round))
			_, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
				Embeds:     []*discordgo.MessageEmbed{&embed},
				Components: gamble.RoundMessageComponents,
				Flags:      1 << 12,
			})
			if err != nil {
				log.Println(err)
			}
		},
		"wheel_add": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferEphemeralResponse(s, i)
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

			if !utility.IsAdmin(i.Interaction.Member.User.ID) {
				_, err := discord.SendFollowup(s, i, "Only admins can add players to the wheel!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			var message string
			player := gamble.Player{
				User: user,
			}
			if remove {
				gamble.Wheel.RemoveWheelOption(player)
				message = fmt.Sprintf("Removed %s from the wheel!", player.User.Username)
			} else {
				gamble.Wheel.AddWheelOption(player)
				message = fmt.Sprintf("Added %s to the wheel!", player.User.Username)
			}

			_, err := discord.SendFollowup(s, i, message)
			if err != nil {
				log.Println(err)
			}
		},
		"insert_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Recieved interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
			discord.DeferEphemeralResponse(s, i)

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

			if !utility.IsAdmin(i.Interaction.Member.User.ID) {
				_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			onPlayer := gamble.Player{
				User: on,
			}
			byPlayer := gamble.Player{
				User: by,
			}

			bet := gamble.Bet{
				By:     byPlayer,
				On:     onPlayer,
				Amount: amount,
			}

			var message string

			if bet.Amount == 0 {
				gamble.Wheel.Rounds[round-1].RemoveBet(byPlayer, onPlayer)
				message = fmt.Sprintf("Removed bet on %s, by %s on round %d", onPlayer.User.Username, byPlayer.User.Username, round)

			} else {
				gamble.Wheel.Rounds[round-1].AddBet(bet)
				message = fmt.Sprintf("Added bet on %s, by %s for %d on round %d", onPlayer.User.Username, byPlayer.User.Username, amount, round)

			}

			_, err := discord.SendFollowup(s, i, message)
			if err != nil {
				log.Println(err)
			}
		},
	}

	componentHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"button_refresh": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			discord.DeferEphemeralResponse(s, i)

			if len(gamble.Wheel.Rounds) == 0 {
				gamble.Wheel.AddRound()
			}

			embed := gamble.Wheel.StatusEmbed(gamble.Wheel.CurrentRound())
			message := fmt.Sprintf("Refreshed wheel to round %d", gamble.Wheel.CurrentRound().ID+1)

			_, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
				Channel:    i.ChannelID,
				ID:         i.Message.ID,
				Embeds:     &[]*discordgo.MessageEmbed{&embed},
				Components: &gamble.RoundMessageComponents,
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

			err = discord.SleepDeleteInteraction(s, i, 3)
			if err != nil {
				log.Println(err)
			}
		},
		"button_claim": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			discord.DeferEphemeralResponse(s, i)

			player := gamble.Player{
				User: i.Interaction.Member.User,
			}

			gamble.Wheel.AddPlayer(player)

			message := ""
			hasClaimed := false

			for _, claim := range gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].Claims {
				if claim.ID() == player.ID() {
					message = "You've already claimed!"
					hasClaimed = true
					break
				}
			}

			if !hasClaimed {
				gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].AddClaim(player)
				message = "Claimed 100!"
			}

			_, err := discord.SendFollowup(s, i, message)
			if err != nil {
				log.Println(err)
			}
		},
		"button_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)

			gamble.Wheel.AddPlayer(gamble.Player{User: i.Interaction.Member.User})

			var remove bool
			if strings.Contains(i.MessageComponentData().CustomID, "remove") {
				remove = true
			}
			gamble.Wheel.SendMenu(s, i, remove, false)
		},
		"button_winner": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			if !utility.IsAdmin(i.Interaction.Member.User.ID) {
				discord.DeferEphemeralResponse(s, i)
				_, err := discord.SendFollowup(s, i, "Only admins can pick winners!")
				if err != nil {
					log.Println(err)
				}
				return
			}
			gamble.Wheel.SendMenu(s, i, false, true)
		},
		"menu_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.MessageComponentData().CustomID, i.Interaction.Member.User.Username)
			selectedUser := i.MessageComponentData().Values
			round := gamble.Wheel.CurrentRound().ID

			if strings.Contains(i.MessageComponentData().CustomID, "remove") {
				member, _ := s.GuildMember(i.GuildID, selectedUser[0])
				by := gamble.Player{
					User: i.Interaction.Member.User,
				}
				on := gamble.Player{
					User: member.User,
				}
				gamble.Wheel.Rounds[round].RemoveBet(by, on)
				err := discord.UpdateResponse(s, i, "Removed bet!")
				if err != nil {
					log.Println(err)
				}
				return
			}
			if strings.Contains(i.MessageComponentData().CustomID, "winner") {
				member, _ := s.GuildMember(i.GuildID, selectedUser[0])
				player := gamble.Player{
					User: member.User,
				}

				if len(gamble.Wheel.Rounds[round].Bets) == 0 {
					err := discord.UpdateResponse(s, i, "No bets!")
					if err != nil {
						log.Println(err)
					}
					return
				}

				if !gamble.Wheel.Rounds[round].HasWinner() {
					gamble.Wheel.AddRound()
				}

				gamble.Wheel.Rounds[round].SetWinner(player)
				err := discord.UpdateResponse(s, i, "Set winner!")
				if err != nil {
					log.Println(err)
				}
				return
			}

			gamble.Wheel.SendModal(s, i, selectedUser[0])
		},
	}

	modalHandlers = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
		"modal_bet": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
			log.Printf("Received interaction: %s by %s", i.ModalSubmitData().CustomID, i.Interaction.Member.User.Username)

			userID := strings.Split(i.ModalSubmitData().CustomID, "-")[1]
			user, _ := s.GuildMember(i.GuildID, userID)

			byPlayer := gamble.Player{
				User: i.Interaction.Member.User,
			}
			onPlayer := gamble.Player{
				User: user.User,
			}

			modalInput := i.ModalSubmitData().Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
			amount, err := strconv.Atoi(modalInput)
			if err != nil {
				err := discord.UpdateResponse(s, i, "Invalid amount")
				if err != nil {
					log.Println(err)
				}
				return
			}

			bet := gamble.Bet{
				By:     byPlayer,
				On:     onPlayer,
				Amount: amount,
			}

			existingBet, err := gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].HasBet(bet)
			existingAmount := 0
			if err == nil {
				existingAmount = existingBet.Amount
			}

			options := len(gamble.Wheel.CurrentWheelOptions())
			PlayerBets, _ := gamble.Wheel.PlayerBets(byPlayer, gamble.Wheel.CurrentRound())
			if options%2 == 1 {
				options++
			}
			if PlayerBets >= (options / 2) {
				err := discord.UpdateResponse(s, i, "You can only bet on half of the players")
				if err != nil {
					log.Println(err)
				}
				return

			}

			if amount > gamble.Wheel.PlayerUsableMoney(byPlayer)+existingAmount {
				err := discord.UpdateResponse(s, i, "You don't have that much money")
				if err != nil {
					log.Println(err)
				}
				return
			}

			if amount <= 0 {
				err := discord.UpdateResponse(s, i, "You can't place a bet of 0 or lower")
				if err != nil {
					log.Println(err)
				}
				return
			}

			gamble.Wheel.Rounds[gamble.Wheel.CurrentRound().ID].AddBet(bet)
			message := fmt.Sprintf("Bet on %s for %d", onPlayer.User.Username, amount)

			err = discord.UpdateResponse(s, i, message)
			if err != nil {
				log.Println(err)
			}
		},
	}
)

// handleMessage is the main message handler function.
// It checks for and processes messages containing media, mentions, and replies.
func handleMessage(s *discordgo.Session, m *discordgo.MessageCreate) {
	// Delay 3 seconds to allow embeds to load
	go func() {
		time.Sleep(3 * time.Second)
		fetchedMessage, _ := s.ChannelMessage(m.Message.ChannelID, m.Message.ID)
		// Check if message contains media and hash it if so
		if utility.HasImageURL(fetchedMessage) || utility.HasVideoURL(fetchedMessage) {
			hasher.HashAttachments(fetchedMessage, true)
		}
	}()

	// Ignore messages from the bot itself
	if m.Author.ID == s.State.User.ID {
		return
	}

	// Ignore messages from other bots
	if m.Author.Bot {
		return
	}

	var chatMessages []anthropic.Message // Chat messages for building the message history
	var cache []*discordgo.Message       // Cache of messages for replies
	botMentioned, isReply := false, false

	// Check if bot was mentioned in the message
	for _, mention := range m.Mentions {
		if mention.ID == s.State.User.ID {
			botMentioned = true
			break
		}
	}

	// Check if message is a reply
	if m.Type == discordgo.MessageTypeReply {
		// Check if bot is mentioned or replied to in the referenced message
		if (m.ReferencedMessage.Author.ID == s.State.User.ID || botMentioned) && m.ReferencedMessage != nil {
			cache = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
			isReply = true
		}
	}

	// Process messages containing media, mentions, and replies
	if botMentioned || isReply {
		// Log message details
		if isReply {
			log.Printf("%s reply: %s", m.Author.Username, m.Content)
		} else {
			log.Printf("%s mention: %s", m.Author.Username, m.Content)
		}

		transcript, err := oai.GetTranscriptFromMessage(s, m.Message)
		if err != nil {
			log.Println(err)
		}

		// Clean and prepare message content
		m.Message = utility.CleanMessage(s, m.Message)
		images, _ := utility.GetMessageMediaURL(m.Message)
		content := config.RequestContent{
			Text: fmt.Sprintf("%s: %s %s %s %s",
				m.Author.Username,
				transcript,
				utility.AttachmentText(m.Message),
				utility.EmbedText(m.Message),
				m.Content,
			),
			URL: images,
		}

		// Append message to chat messages
		ant.AppendMessage(anthropic.RoleUser, content, &chatMessages)

		// Prepend reply messages to chat messages if applicable
		if isReply {
			ant.PrependReplyMessages(s, m.Message, cache, &chatMessages)
			ant.PrependUserMessagePlaceholder(&chatMessages)
		}

		// Stream message response
		ant.StreamMessageResponse(s, m.Message, chatMessages, nil)
	}
}
