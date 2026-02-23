package utility

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/discord"
)

func SplitParagraph(message string) (firstPart string, lastPart string) {
	primarySeparator := "\n\n"
	secondarySeparator := "\n"

	lastPrimaryIndex := strings.LastIndex(message[:min(1990, len(message))], primarySeparator)
	lastSecondaryIndex := strings.LastIndex(message[:min(1990, len(message))], secondarySeparator)
	if lastPrimaryIndex != -1 {
		firstPart = message[:lastPrimaryIndex]
		lastPart = message[lastPrimaryIndex+len(primarySeparator):]
	} else if lastSecondaryIndex != -1 {
		firstPart = message[:lastSecondaryIndex]
		lastPart = message[lastSecondaryIndex+len(secondarySeparator):]
	} else {
		log.Printf("Splitting forcibly: %d", len(message))
		firstPart = message[:min(1990, len(message))]
		lastPart = message[min(1990, len(message)):]
	}

	if strings.Count(firstPart, "```")%2 != 0 {
		lastCodeBlockIndex := strings.LastIndex(firstPart, "```")
		lastCodeBlock := firstPart[lastCodeBlockIndex:]
		newlineIdx := strings.Index(lastCodeBlock, "\n")
		if newlineIdx < 0 {
			newlineIdx = len(lastCodeBlock)
		}
		languageCode := lastCodeBlock[:newlineIdx]

		firstPart = firstPart + "```"
		lastPart = languageCode + "\n" + lastPart
	}

	return firstPart, lastPart
}

// SplitMessageSlices splits a message into slices based on Discord's character limits
// Returns a slice of message parts that can be sent sequentially
func SplitMessageSlices(message string) []string {
	if len(message) <= 1800 {
		return []string{message}
	}

	var parts []string
	remaining := message

	for len(remaining) > 1800 {
		primarySeparator := "\n\n"
		secondarySeparator := "\n"
		maxLength := min(1900, len(remaining)) // Safety buffer

		// Find the best split point
		lastPrimaryIndex := strings.LastIndex(remaining[:maxLength], primarySeparator)
		lastSecondaryIndex := strings.LastIndex(remaining[:maxLength], secondarySeparator)

		var splitIndex int
		var separatorLen int

		if lastPrimaryIndex != -1 && lastPrimaryIndex > len(remaining)/4 {
			// Use primary separator if it's not too close to the beginning
			splitIndex = lastPrimaryIndex
			separatorLen = len(primarySeparator)
		} else if lastSecondaryIndex != -1 && lastSecondaryIndex > len(remaining)/4 {
			// Use secondary separator if it's not too close to the beginning
			splitIndex = lastSecondaryIndex
			separatorLen = len(secondarySeparator)
		} else {
			// Force split at safe length
			splitIndex = 1900
			separatorLen = 0
			log.Printf("Force splitting message at %d characters", splitIndex)
		}

		part := remaining[:splitIndex]

		// Handle code blocks to prevent breaking them
		if strings.Count(part, "```")%2 != 0 {
			lastCodeBlockIndex := strings.LastIndex(part, "```")
			if lastCodeBlockIndex != -1 {
				lastCodeBlock := part[lastCodeBlockIndex:]
				languageCode := ""
				if newlineIndex := strings.Index(lastCodeBlock, "\n"); newlineIndex != -1 {
					languageCode = lastCodeBlock[:newlineIndex]
				}
				part = part + "```"
				remaining = languageCode + "\n" + remaining[splitIndex+separatorLen:]
			} else {
				remaining = remaining[splitIndex+separatorLen:]
			}
		} else {
			remaining = remaining[splitIndex+separatorLen:]
		}

		parts = append(parts, strings.TrimSpace(part))
	}

	if len(remaining) > 0 {
		parts = append(parts, strings.TrimSpace(remaining))
	}

	return parts
}

func SplitSend(s *discordgo.Session, msg *discordgo.Message, currentMessage string) (string, *discordgo.Message, error) {
	if currentMessage == "" {
		return "", msg, fmt.Errorf("empty message")
	}

	if len(currentMessage) > 1800 {
		firstPart, lastPart := SplitParagraph(currentMessage)
		if lastPart == "" {
			lastPart = "..."
		}
		_, err := discord.EditMessage(s, msg, firstPart)
		if err != nil {
			return "", msg, err
		}
		msg, err = discord.SendMessage(s, msg, lastPart)
		if err != nil {
			return "", msg, err
		}
		currentMessage = lastPart
	} else {
		var err error
		msg, err = discord.EditMessage(s, msg, currentMessage)
		if err != nil {
			return "", msg, err
		}
	}
	return currentMessage, msg, nil
}

// SliceSend sends message slices sequentially, editing the first message and sending new ones for subsequent parts
func SliceSend(s *discordgo.Session, msg *discordgo.Message, messageSlices []string, currentSliceIndex int) (*discordgo.Message, error) {
	if len(messageSlices) == 0 {
		return msg, nil
	}

	// Edit the first message with the current slice
	if currentSliceIndex < len(messageSlices) {
		var err error
		msg, err = discord.EditMessage(s, msg, messageSlices[currentSliceIndex])
		if err != nil {
			return msg, err
		}
	}

	// Send additional messages for remaining slices
	for i := currentSliceIndex + 1; i < len(messageSlices); i++ {
		newMsg, err := discord.SendMessage(s, msg, messageSlices[i])
		if err != nil {
			return msg, err
		}
		msg = newMsg // Update to the latest message for potential further sends
	}

	return msg, nil
}

func GetMessagesBefore(s *discordgo.Session, channelID string, count int, messageID string) ([]*discordgo.Message, error) {
	messages, err := s.ChannelMessages(channelID, count, messageID, "", "")
	if err != nil {
		return nil, err
	}
	return messages, nil
}

func GetChannelMessages(s *discordgo.Session, channelID string, count int) []*discordgo.Message {
	var messages []*discordgo.Message
	lastMessage := &discordgo.Message{
		ID: "",
	}

	iterations := count / 100
	remainder := count % 100

	for range iterations {
		batch, err := GetMessagesBefore(s, channelID, 100, lastMessage.ID)
		if err != nil {
			log.Printf("Error getting messages: %s\nClosest message: %s", err, lastMessage.Timestamp.String())
		}
		if len(batch) == 0 {
			break
		}
		lastMessage = batch[len(batch)-1]
		messages = append(messages, batch...)
	}

	if remainder > 0 {
		batch, err := GetMessagesBefore(s, channelID, remainder, lastMessage.ID)
		if err != nil {
			log.Printf("Error getting messages: %s\nClosest message: %s", err, lastMessage.Timestamp.String())
		}
		messages = append(messages, batch...)
	}
	return messages
}

func GetAllServerMessages(s *discordgo.Session, statusMessage *discordgo.Message, channels []*discordgo.Channel, threads bool, endDate time.Time, c chan []*discordgo.Message) {
	defer close(c)

	var channelCollection []*discordgo.Channel

	if threads {
		discord.EditMessage(s, statusMessage, fmt.Sprintf("Fetching threads for %d channels...", len(channels)))
		for _, channel := range channels {
			activeThreads, err := s.ThreadsActive(channel.ID)
			if err != nil {
				log.Printf("Error getting active threads for channel %s: %s\n", channel.Name, err)
			}
			archivedThreads, err := s.ThreadsArchived(channel.ID, nil, 0)
			if err != nil {
				log.Printf("Error getting archived threads for channel %s: %s\n", channel.Name, err)
			}
			channelCollection = append(channelCollection, channel)
			channelCollection = append(channelCollection, activeThreads.Threads...)
			channelCollection = append(channelCollection, archivedThreads.Threads...)
		}
	} else {
		channelCollection = channels
	}

	for _, channel := range channelCollection {
		var outputMessage string
		var err error
		if channel.IsThread() {
			outputMessage = fmt.Sprintf("Fetching messages for thread: <#%s>", channel.ID)
		} else {
			outputMessage = fmt.Sprintf("Fetching messages for channel: <#%s>", channel.ID)
		}
		if len(statusMessage.Content) > 1800 {
			statusMessage, err = discord.SendMessage(s, statusMessage, outputMessage)
		} else {
			statusMessage, err = discord.EditMessage(s, statusMessage, fmt.Sprintf("%s\n%s", statusMessage.Content, outputMessage))
		}
		if err != nil {
			log.Println(err)
			return
		}

		messagesRetrieved := 100
		count := 0
		lastMessage := &discordgo.Message{
			ID: "",
		}
		for messagesRetrieved == 100 {
			batch, err := GetMessagesBefore(s, channel.ID, 100, lastMessage.ID)
			if err != nil {
				log.Printf("Error getting messages: %s\nClosest message: %s", err, lastMessage.Timestamp.String())
			}
			if len(batch) == 0 || batch == nil {
				log.Println("getAllChannelMessages: no messages retrieved")
				break
			}
			if batch[0].Timestamp.Before(endDate) {
				break
			}
			lastMessage = batch[len(batch)-1]
			messagesRetrieved = len(batch)
			count += messagesRetrieved
			_, err = discord.EditMessage(s, statusMessage, fmt.Sprintf("%s\n- Retrieved %d messages", statusMessage.Content, count))
			if err != nil {
				log.Println(err)
			}
			c <- batch
		}
		statusMessage, err = discord.EditMessage(s, statusMessage, fmt.Sprintf("%s\n- Retrieved %d messages", statusMessage.Content, count))
		if err != nil {
			log.Println(err)
		}
	}

	log.Println("getAllChannelThreadMessages: done")
}

func GetReferencedMessage(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message) *discordgo.Message {
	if message.ReferencedMessage != nil {
		return message.ReferencedMessage
	}

	if message.MessageReference != nil {
		cachedMessage := checkCache(cache, message.MessageReference.MessageID)
		if cachedMessage != nil {
			return cachedMessage
		}

		referencedMessage, _ := s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
		return referencedMessage
	}

	return nil
}

func checkCache(cache []*discordgo.Message, messageID string) *discordgo.Message {
	for _, message := range cache {
		if message.ID == messageID {
			return message
		}
	}
	return nil
}

// ReplyChainUsers walks the reply chain and returns a map of non-bot user IDs to usernames.
func ReplyChainUsers(s *discordgo.Session, m *discordgo.Message, cache []*discordgo.Message) map[string]string {
	users := map[string]string{}
	ref := GetReferencedMessage(s, m, cache)
	for ref != nil {
		if ref.Author != nil && !ref.Author.Bot && ref.Author.ID != s.State.User.ID {
			users[ref.Author.ID] = ref.Author.Username
		}
		if ref.Type == discordgo.MessageTypeReply {
			ref = GetReferencedMessage(s, ref, cache)
		} else {
			break
		}
	}
	return users
}
