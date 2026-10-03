package handler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"voltgpt/internal/discord"
	"voltgpt/internal/hasher"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

func hashServerCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferResponse(s, i)

	var channels []*discordgo.Channel
	var threads bool
	var endDate time.Time
	msgCount, hashCount := 0, 0

	for _, option := range i.ApplicationCommandData().Options {
		switch option.Name {
		case "channel":
			channels = append(channels, option.ChannelValue(s))
		case "threads":
			threads = option.BoolValue()
		case "date":
			// date is a string in yyyy/mm/dd format
			endDate, _ = time.Parse("2006/01/02", option.StringValue())
		}
	}

	if !requireAdmin(s, i) {
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
	var countMu sync.Mutex
	for messages := range messageChannel {
		wg.Add(1)
		go func(messages []*discordgo.Message) {
			defer wg.Done()
			localMsg := len(messages)
			localHash := 0
			for _, message := range messages {
				if utility.HasImageURL(message) || utility.HasVideoURL(message) {
					options := hasher.HashOptions{Store: true}
					_, count := hasher.HashAttachments(message, options)
					localHash += count
				}
			}
			countMu.Lock()
			msgCount += localMsg
			hashCount += localHash
			currentMsg := msgCount
			currentHash := hashCount
			countMu.Unlock()
			// format time to yyyy/mm/dd
			_, err := discord.EditMessage(s, hashedStatus, fmt.Sprintf("Status: ongoing\nThreads included: %t\nHashing until: %s\nMessages processed: %d\nHashes: %d", threads, endDate.Format("2006/01/02"), currentMsg, currentHash))
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
}

func checkSnailCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
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

	respond(s, i, messageContent)
}

func hashCommand(ctx context.Context, s *discordgo.Session, i *discordgo.InteractionCreate) {
	logCommand(i)
	discord.DeferEphemeralResponse(s, i)

	message := i.ApplicationCommandData().Resolved.Messages[i.ApplicationCommandData().TargetID]
	var count int

	if utility.HasImageURL(message) || utility.HasVideoURL(message) {
		options := hasher.HashOptions{Store: true}
		_, count = hasher.HashAttachments(message, options)
	}
	respond(s, i, fmt.Sprintf("Hashed: %d", count))
}
