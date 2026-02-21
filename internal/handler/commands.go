package handler

import (
	"fmt"
	"log"
	"math/rand"
	"strings"
	"sync"
	"time"

	wave "voltgpt/internal/apis/wavespeed"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/gamble"
	"voltgpt/internal/hasher"
	"voltgpt/internal/memory"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

// Commands is a map of command names and their corresponding functions.
var Commands = map[string]func(s *discordgo.Session, i *discordgo.InteractionCreate){
	"draw": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		var prompt config.RequestContent
		var resolution string
		var imgs []*string
		var amount int
		syncMode := true
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "prompt" {
				prompt.Text = option.StringValue()
			}
			if option.Name == "amount" {
				amount = int(option.IntValue())
			}
			if option.Name == "resolution" {
				resolution = option.StringValue()
			}
			if strings.HasPrefix(option.Name, "image") {
				if val, ok := option.Value.(string); ok {
					if att, exists := i.ApplicationCommandData().Resolved.Attachments[val]; exists {
						imgs = append(imgs, &att.URL)
					}
				}
			}
			if option.Name == "urls" {
				urls := option.StringValue()
				for _, url := range strings.Split(urls, " ") {
					if !wave.IsImageURL(url) {
						_, err := discord.SendFollowup(s, i, "Please provide a valid image URL [jpg, jpeg, png]")
						if err != nil {
							log.Println(err)
						}
						return
					}
					imgs = append(imgs, &url)
				}
			}
		}
		imgFilled := len(imgs) > 0

		if amount < 1 {
			amount = 2
		}

		if resolution == "" {
			resolution = "2048*2048"
		}
		var images []*discordgo.File
		var imgMu sync.Mutex

		var wg sync.WaitGroup
		for range amount {
			wg.Add(1)
			go func() {
				defer wg.Done()
				var resp *wave.WaveSpeedResponse
				var err error
				if imgFilled {
					for _, img := range imgs {
						if !wave.IsImageURL(*img) {
							_, err := discord.SendFollowup(s, i, "Please provide a valid image URL [jpg, jpeg, png]")
							if err != nil {
								log.Println(err)
							}
							return
						}
					}
					aspectRatio, err := utility.GetAspectRatio(*imgs[0])
					if err != nil {
						_, err := discord.SendFollowup(s, i, err.Error())
						if err != nil {
							log.Println(err)
						}
						return
					}
					var base64Images []*string
					for _, img := range imgs {
						base64Image, err := utility.Base64ImageDownload(*img)
						if err != nil {
							_, err := discord.SendFollowup(s, i, err.Error())
							if err != nil {
								log.Println(err)
							}
							return
						}
						base64Images = append(base64Images, &base64Image[0])
					}
					if aspectRatio >= 1 {
						H := 2048
						W := int(float64(H) * aspectRatio)
						if W > 4096 {
							W = 4096
							H = max(int(float64(W)/aspectRatio), 1024)
						}
						resolution = fmt.Sprintf("%d*%d", W, H)
					} else {
						W := 2048
						H := int(float64(W) / aspectRatio)
						if H > 4096 {
							H = 4096
							W = max(int(float64(H)*aspectRatio), 1024)
						}
						resolution = fmt.Sprintf("%d*%d", W, H)
					}
					req := wave.SeedDreamEditSubmissionRequest{
						Prompt:   prompt.Text,
						Size:     &resolution,
						Images:   base64Images,
						SyncMode: &syncMode,
					}
					resp, err = wave.SendSeedDreamEditRequest(req)
					if err != nil {
						log.Println(err)
						_, err = discord.SendFollowup(s, i, err.Error())
						if err != nil {
							log.Println(err)
						}
						return
					}
				} else {
					req := wave.SeedDreamSubmissionRequest{
						Prompt:   prompt.Text,
						Size:     &resolution,
						SyncMode: &syncMode,
					}

					resp, err = wave.SendSeedDreamRequest(req)
					if err != nil {
						log.Println(err)
						_, err = discord.SendFollowup(s, i, err.Error())
						if err != nil {
							log.Println(err)
						}
						return
					}
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
				imgMu.Lock()
				images = append(images, image...)
				imgMu.Unlock()
			}()
		}
		wg.Wait()
		mode := "drawing"
		if imgFilled {
			mode = "editing"
		}
		message := fmt.Sprintf("Prompt: %s\nResolution: %s\nMode: %s", prompt.Text, resolution, mode)
		_, err := discord.SendFollowupFile(s, i, message, images)
		if err != nil {
			log.Println(err)
		}
	},
	"video": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		var prompt string
		var negativePrompt string
		var img string
		var duration int
		var seed int
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "prompt" {
				prompt = option.StringValue()
			}
			if option.Name == "negative_prompt" {
				negativePrompt = option.StringValue()
			}
			if option.Name == "image" {
				if val, ok := option.Value.(string); ok {
					if att, exists := i.ApplicationCommandData().Resolved.Attachments[val]; exists {
						img = att.URL
					}
				}
			}
			if option.Name == "duration" {
				duration = int(option.IntValue())
			}
			if option.Name == "seed" {
				seed = int(option.IntValue())
			}
		}

		if prompt == "" && img == "" {
			_, err := discord.SendFollowup(s, i, "Please provide a prompt or an image")
			if err != nil {
				log.Println(err)
			}
			return
		}

		if duration == 0 {
			duration = 5
		}

		if seed == 0 {
			seed = rand.Intn(2147483647)
		}
		i2vSize := "480p"
		t2vSize := "832*480"

		if duration != 5 && duration != 10 {
			_, err := discord.SendFollowup(s, i, "Duration must be 5 or 10")
			if err != nil {
				log.Println(err)
			}
			return
		}

		imgFilled := img != ""

		var resp *wave.WaveSpeedResponse
		var err error
		if imgFilled {
			if !wave.IsImageURL(img) {
				_, err := discord.SendFollowup(s, i, "Please provide a valid image URL [jpg, jpeg, png]")
				if err != nil {
					log.Println(err)
				}
				return
			}
			base64Image, err := utility.Base64ImageDownload(img)
			if err != nil {
				_, err := discord.SendFollowup(s, i, err.Error())
				if err != nil {
					log.Println(err)
				}
				return
			}
			req := wave.WanI2VSubmissionRequest{
				Prompt:         &prompt,
				NegativePrompt: &negativePrompt,
				Image:          base64Image[0],
				Duration:       &duration,
				Seed:           &seed,
				Resolution:     &i2vSize,
			}
			resp, err = wave.SendWanI2VRequest(req)
			if err != nil {
				_, err := discord.SendFollowup(s, i, err.Error())
				if err != nil {
					log.Println(err)
				}
				return
			}
		} else {
			req := wave.WanT2VSubmissionRequest{
				Prompt:         prompt,
				NegativePrompt: &negativePrompt,
				Duration:       &duration,
				Seed:           &seed,
				Size:           &t2vSize,
			}
			resp, err = wave.SendWanT2VRequest(req)
			if err != nil {
				_, err := discord.SendFollowup(s, i, err.Error())
				if err != nil {
					log.Println(err)
				}
				return
			}
		}
		if err != nil {
			log.Println(err)
			_, err = discord.SendFollowup(s, i, err.Error())
			if err != nil {
				log.Println(err)
			}
			return
		}

		msg, err := discord.SendFollowup(s, i, "Processing...")
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
		video, err := wave.DownloadResult(resp)
		if err != nil {
			log.Println(err)
			_, err = discord.SendFollowup(s, i, err.Error())
			if err != nil {
				log.Println(err)
			}
			return
		}

		message := fmt.Sprintf("Gen time: %ds\nPrompt: %s\nNegative Prompt: %s", resp.Data.Timings.Inference/1000, prompt, negativePrompt)
		_, err = discord.EditFollowupFile(s, i, msg.ID, message, video)
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
	"wheel_status": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		gamble.Mu.Lock()
		defer gamble.Mu.Unlock()

		if len(gamble.GameState.Rounds) == 0 {
			gamble.GameState.AddRound()
		}

		var round int

		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "round" {
				round = int(option.IntValue())
			}
		}

		if round == 0 {
			round = gamble.GameState.CurrentRound().ID + 1
		}

		embed := gamble.GameState.StatusEmbed(gamble.GameState.Round(round))
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

		gamble.Mu.Lock()
		defer gamble.Mu.Unlock()

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
			gamble.GameState.RemoveWheelOption(player)
			message = fmt.Sprintf("Removed %s from the wheel!", player.User.DisplayName())
		} else {
			gamble.GameState.AddWheelOption(player)
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

		gamble.Mu.Lock()
		defer gamble.Mu.Unlock()

		if round <= 0 || round > len(gamble.GameState.Rounds) {
			_, err := discord.SendFollowup(s, i, fmt.Sprintf("Invalid round number! Must be between 1 and %d", len(gamble.GameState.Rounds)))
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
			gamble.GameState.Rounds[round-1].RemoveBet(byPlayer, onPlayer)
			message = fmt.Sprintf("Removed bet on %s, by %s on round %d", onPlayer.User.DisplayName(), byPlayer.User.DisplayName(), round)

		} else {
			gamble.GameState.Rounds[round-1].AddBet(bet)
			message = fmt.Sprintf("Added bet on %s, by %s for %d on round %d", onPlayer.User.DisplayName(), byPlayer.User.DisplayName(), amount, round)

		}

		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
	"reset_wheel": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Recieved interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		gamble.Mu.Lock()
		defer gamble.Mu.Unlock()

		gamble.GameState.ResetWheel()
		_, err := discord.SendFollowup(s, i, "Wheel reset!")
		if err != nil {
			log.Println(err)
		}
	},
	"memory_admin_view": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		var user *discordgo.User
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "user" {
				user = option.UserValue(s)
			}
		}

		if user == nil {
			_, err := discord.SendFollowup(s, i, "Please select a user.")
			if err != nil {
				log.Println(err)
			}
			return
		}

		facts := memory.GetUserFacts(user.ID)
		if len(facts) == 0 {
			_, err := discord.SendFollowup(s, i, fmt.Sprintf("No facts stored for %s.", user.Username))
			if err != nil {
				log.Println(err)
			}
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**Facts about %s** (%d total):\n", user.Username, len(facts)))
		for _, f := range facts {
			reinforcement := ""
			if f.ReinforcementCount > 0 {
				reinforcement = fmt.Sprintf(" [x%d]", f.ReinforcementCount)
			}
			sb.WriteString(fmt.Sprintf("- %s%s\n", f.FactText, reinforcement))
		}

		message := sb.String()
		if len(message) > 2000 {
			message = message[:1997] + "..."
		}

		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
	"memory_admin_delete": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		var user *discordgo.User
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "user" {
				user = option.UserValue(s)
			}
		}

		var count int64
		var err error
		var message string

		if user != nil {
			count, err = memory.DeleteUserFacts(user.ID)
			message = fmt.Sprintf("Deleted %d facts for %s.", count, user.Username)
		} else {
			count, err = memory.DeleteAllFacts()
			message = fmt.Sprintf("Deleted %d facts for all users.", count)
		}

		if err != nil {
			_, err := discord.SendFollowup(s, i, fmt.Sprintf("Error: %v", err))
			if err != nil {
				log.Println(err)
			}
			return
		}

		_, err = discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
	"memory_self": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		facts := memory.GetUserFacts(i.Interaction.Member.User.ID)
		if len(facts) == 0 {
			_, err := discord.SendFollowup(s, i, "I don't have any facts stored about you yet!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("**What I know about you** (%d facts):\n", len(facts)))
		for _, f := range facts {
			sb.WriteString(fmt.Sprintf("- %s\n", f.FactText))
		}

		message := sb.String()
		if len(message) > 2000 {
			message = message[:1997] + "..."
		}

		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
	"memory_admin_digest": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferResponse(s, i)

		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		recentFacts := memory.GetRecentFacts(20)
		if len(recentFacts) == 0 {
			_, err := discord.SendFollowup(s, i, "No facts learned recently!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		// Group facts by user
		userFacts := make(map[string][]string)
		for _, f := range recentFacts {
			userFacts[f.Username] = append(userFacts[f.Username], f.FactText)
		}

		var sb strings.Builder
		sb.WriteString("**Memory Digest** — Here's what I've learned recently:\n\n")
		for username, facts := range userFacts {
			sb.WriteString(fmt.Sprintf("**%s:**\n", username))
			for _, fact := range facts {
				sb.WriteString(fmt.Sprintf("- %s\n", fact))
			}
			sb.WriteString("\n")
		}

		message := sb.String()
		if len(message) > 2000 {
			message = message[:1997] + "..."
		}

		_, err := discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
	"memory_setname": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		var name string
		var targetUser *discordgo.User
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "name" {
				name = strings.TrimSpace(option.StringValue())
			}
			if option.Name == "user" {
				targetUser = option.UserValue(s)
			}
		}

		// Non-admins cannot target other users
		if targetUser != nil && targetUser.ID != i.Interaction.Member.User.ID && !utility.IsAdmin(i.Interaction.Member.User.ID) {
			_, err := discord.SendFollowup(s, i, "Only admins can set names for other users!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		// Default to the invoking user
		if targetUser == nil {
			targetUser = i.Interaction.Member.User
		}

		// Clear preferred name when no name is provided
		if name == "" {
			if err := memory.SetPreferredName(targetUser.ID, targetUser.Username, ""); err != nil {
				_, err := discord.SendFollowup(s, i, fmt.Sprintf("Error: %v", err))
				if err != nil {
					log.Println(err)
				}
				return
			}
			msg := fmt.Sprintf("Cleared preferred name for %s.", targetUser.Username)
			_, err := discord.SendFollowup(s, i, msg)
			if err != nil {
				log.Println(err)
			}
			return
		}

		if err := memory.SetPreferredName(targetUser.ID, targetUser.Username, name); err != nil {
			_, err := discord.SendFollowup(s, i, fmt.Sprintf("Error: %v", err))
			if err != nil {
				log.Println(err)
			}
			return
		}

		msg := fmt.Sprintf("Set preferred name for %s to **%s**.", targetUser.Username, name)
		_, err := discord.SendFollowup(s, i, msg)
		if err != nil {
			log.Println(err)
		}
	},
	"memory_admin_refreshnames": func(s *discordgo.Session, i *discordgo.InteractionCreate) {
		log.Printf("Received interaction: %s by %s", i.ApplicationCommandData().Name, i.Interaction.Member.User.Username)
		discord.DeferEphemeralResponse(s, i)

		if !utility.IsAdmin(i.Interaction.Member.User.ID) {
			_, err := discord.SendFollowup(s, i, "Only admins can use this command!")
			if err != nil {
				log.Println(err)
			}
			return
		}

		var user *discordgo.User
		for _, option := range i.ApplicationCommandData().Options {
			if option.Name == "user" {
				user = option.UserValue(s)
			}
		}

		var count int64
		var err error
		var message string

		if user != nil {
			count, err = memory.RefreshFactNames(user.ID)
			message = fmt.Sprintf("Updated %d facts for %s.", count, user.Username)
		} else {
			count, err = memory.RefreshAllFactNames()
			message = fmt.Sprintf("Updated %d facts across all users.", count)
		}

		if err != nil {
			_, err := discord.SendFollowup(s, i, fmt.Sprintf("Error: %v", err))
			if err != nil {
				log.Println(err)
			}
			return
		}

		_, err = discord.SendFollowup(s, i, message)
		if err != nil {
			log.Println(err)
		}
	},
}
