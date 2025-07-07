package handler

import (
	"fmt"
	"log"
	"sync"
	"time"

	oai "voltgpt/internal/apis/openai"
	wave "voltgpt/internal/apis/wavespeed"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/gamble"
	"voltgpt/internal/hasher"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

// Commands is a map of command names and their corresponding functions.
var Commands = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
	"draw": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		var prompt config.RequestContent
		var resolution string
		var guidance float64
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "prompt" {
				prompt.Text = option.StringValue()
			}
			if option.Name == "resolution" {
				resolution = option.StringValue()
			}
			if option.Name == "guidance" {
				guidance = option.FloatValue()
			}
		}
		if resolution == "" {
			resolution = "1024x1024"
		}
		if guidance == 0 {
			guidance = 2.5
		}
		syncMode := true

		req := wave.SeedDreamSubmissionRequest{
			Prompt:        prompt.Text,
			Size:          &resolution,
			GuidanceScale: &guidance,
			SyncMode:      &syncMode,
		}

		resp, err := wave.SendSeedDreamRequest(req)
		if err != nil {
			log.Println(err)
			_, err = discord.SendFollowup(s, i, err.Error())
			if err != nil {
				log.Println(err)
			}
			return
		}
		image, err := wave.DownloadResult(resp)
		if err != nil {
			log.Println(err)
			_, err = discord.SendFollowup(s, i, err.Error())
			if err != nil {
				log.Println(err)
			}
			return
		}
		message := fmt.Sprintf("Prompt: %s\nResolution: %s\nGuidance: %.1f", prompt.Text, resolution, guidance)
		_, err = discord.SendFollowupFile(s, i, message, image)
		if err != nil {
			log.Println(err)
		}
	},
	"video": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		var prompt config.RequestContent
		var img string
		var duration int
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "prompt" {
				prompt.Text = option.StringValue()
			}
			if option.Name == "image" {
				img = i.ApplicationCommandData().Resolved.Attachments[option.Value.(string)].URL
			}
			if option.Name == "duration" {
				duration = int(option.IntValue())
			}
		}

		if prompt.Text == "" && img == "" {
			_, err := discord.SendFollowup(s, i, "Please provide a prompt or an image")
			if err != nil {
				log.Println(err)
			}
			return
		}

		imgFilled := img != ""
		// if imgFilled check if it's an actual image
		if imgFilled {
			if !wave.IsImageURL(img) {
				_, err := discord.SendFollowup(s, i, "Please provide a valid image URL")
				if err != nil {
					log.Println(err)
				}
				return
			}
		}

		if duration == 0 {
			duration = 5
		}

		if duration != 5 && duration != 10 {
			_, err := discord.SendFollowup(s, i, "Duration must be 5 or 10")
			if err != nil {
				log.Println(err)
			}
			return
		}

		version := wave.SeedDancePro
		resolution := wave.SeedDance480p

		var resp *wave.WaveSpeedResponse
		var err error
		if imgFilled {
			req := wave.SeedDanceI2VSubmissionRequest{
				Prompt:   &prompt.Text,
				Image:    utility.Base64Image(img),
				Duration: &duration,
			}
			resp, err = wave.SendSeedDanceI2VRequest(req, version, wave.SeedDanceI2V, resolution)
		} else {
			req := wave.SeedDanceT2VSubmissionRequest{
				Prompt:   prompt.Text,
				Duration: &duration,
			}
			resp, err = wave.SendSeedDanceT2VRequest(req, version, wave.SeedDanceT2V, resolution)
		}
		if err != nil {
			log.Println(err)
			_, err = discord.SendFollowup(s, i, err.Error())
			if err != nil {
				log.Println(err)
			}
			return
		}

		_, err = discord.SendFollowup(s, i, "Processing...")
		if err != nil {
			log.Println(err)
			return
		}

		resp, err = wave.WaitForComplete(resp.Data.ID)
		if err != nil {
			log.Println(err)
			_, err = discord.SendFollowup(s, i, err.Error())
			if err != nil {
				log.Println(err)
			}
			return
		}

		image, err := wave.DownloadResult(resp)
		if err != nil {
			log.Println(err)
			_, err = discord.SendFollowup(s, i, err.Error())
			if err != nil {
				log.Println(err)
			}
			return
		}
		message := fmt.Sprintf("Prompt: %s", prompt.Text)
		_, err = discord.SendFollowupFile(s, i, message, image)
		if err != nil {
			log.Println(err)
		}
	},
	"hash_server": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		var channels []*discordgo.Channel
		var threads bool
		var endDate time.Time
		msgCount, hashCount := 0, 0

		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "channel" {
				channels = append(channels, option.ChannelValue(s))
			}
			if option.Name == "threads" {
				threads = option.BoolValue()
			}
			if option.Name == "date" {
				// date is a string in yyyy/mm/dd format
				endDate, _ = time.Parse("2006/01/02", option.StringValue())
			}
		}

		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		hashedStatus, _ := discord.SendFollowup(s, i, "Hashing messages...")
		fetchedStatus, _ := discord.SendMessage(s, hashedStatus, "Fetching channels...")

		messageChannel := make(chan []*discordgo.Message) // create a channel containing messages

		if channels == nil {
			allChannels, err := s.GuildChannels(i.GuildID)
			if err != nil {
				log.Println(err)
			}
			for _, channel := range allChannels {
				_, err := s.UserChannelPermissions(s.State.User.ID, channel.ID)
				if err != nil {
					continue
				}

				if channel.Type == discordgo.ChannelTypeGuildText || channel.Type == discordgo.ChannelTypeGuildPublicThread || channel.Type == discordgo.ChannelTypeGuildPrivateThread {
					channels = append(channels, channel)
				}
			}
		}

		go utility.GetAllServerMessages(s, fetchedStatus, channels, threads, endDate, messageChannel)

		var wg sync.WaitGroup
		for messages := range messageChannel {
			wg.Add(1)
			go func(messages []*discordgo.Message) {
				defer wg.Done()
				msgCount += len(messages)
				for _, message := range messages {
					if utility.HasImageURL(message) || utility.HasVideoURL(message) {
						options := hasher.HashOptions{Store: true}
						_, count := hasher.HashAttachments(message, options)
						hashCount += count
					}
				}
				// format time to yyyy/mm/dd
				_, err := discord.EditMessage(s, hashedStatus, fmt.Sprintf("Status: ongoing\nThreads included: %t\nHashing until: %s\nMessages processed: %d\nHashes: %d", threads, endDate.Format("2006/01/02"), msgCount, hashCount))
				if err != nil {
					log.Println(err)
				}
			}(messages)
		}

		wg.Wait()

		_, err := discord.EditMessage(s, hashedStatus, fmt.Sprintf("Status: done\n Threads included: %t\nHashing until: %s\nMessages processed: %d\nHashes: %d", threads, endDate.Format("2006/01/02"), msgCount, hashCount))
		if err != nil {
			log.Println(err)
		}
	},
	"TTS": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

		files := oai.SplitTTS(message.Content)

		_, err := discord.SendFollowupFile(s, i, utility.LinkFromIMessage(i.GuildID, message), files)
		if err != nil {
			log.Println(err)
		}
	},
	"CheckSnail": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]

		options := hasher.HashOptions{Threshold: 8}

		messageContent, embeds := hasher.FindSnails(i.GuildID, message, options)

		if len(embeds) > 0 && len(embeds) < 10 {
			_, err := discord.SendFollowupEmbeds(s, i, embeds)
			if err != nil {
				log.Println(err)
			}
			return
		}

		if messageContent == "" {
			messageContent = "No snails found in this message!"
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
			options := hasher.HashOptions{Store: true}
			_, count = hasher.HashAttachments(message, options)
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

		log.Printf("%s continue: %s", i.Interaction.Member.User.Username, utility.LinkFromIMessage(i.GuildID, m))
		_, err := discord.SendFollowup(s, i, fmt.Sprint("Continuing..."))
		if err != nil {
			log.Println(err)
		}
		err = discord.SleepDeleteInteraction(s, i, 3)
		if err != nil {
			log.Println(err)
		}

		var chatMessages []openai.ChatCompletionMessage
		var cache []*discordgo.Message

		m = utility.CleanMessage(s, m)
		images, _, pdfs := utility.GetMessageMediaURL(m)
		content := config.RequestContent{
			Text:   fmt.Sprintf("%s", m.Content),
			Images: images,
			PDFs:   pdfs,
		}

		oai.AppendMessage(openai.ChatMessageRoleAssistant, "", content, &chatMessages)

		cache, _ = utility.GetMessagesBefore(s, m.ChannelID, 100, m.ID)
		oai.PrependReplyMessages(s, i.Interaction.Member, m, cache, &chatMessages)

		oai.StreamMessageResponse(s, m, chatMessages, m)
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
			message = fmt.Sprintf("Removed %s from the wheel!", player.User.DisplayName())
		} else {
			gamble.Wheel.AddWheelOption(player)
			message = fmt.Sprintf("Added %s to the wheel!", player.User.DisplayName())
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
			message = fmt.Sprintf("Removed bet on %s, by %s on round %d", onPlayer.User.DisplayName(), byPlayer.User.DisplayName(), round)

		} else {
			gamble.Wheel.Rounds[round-1].AddBet(bet)
			message = fmt.Sprintf("Added bet on %s, by %s for %d on round %d", onPlayer.User.DisplayName(), byPlayer.User.DisplayName(), amount, round)

		}

		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
}
