package main

import (
	"fmt"
	"log"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

type requestContent struct {
	text string
	url  []string
}

func isAdmin(id string) bool {
	for _, admin := range admins {
		if admin == id {
			return true
		}
	}
	return false
}

func linkFromIMessage(i *discordgo.InteractionCreate, m *discordgo.Message) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, m.ChannelID, m.ID)
}

func logSendErrorMessage(s *discordgo.Session, m *discordgo.Message, content string) {
	log.Println(content)
	_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})
}

func appendMessage(role string, name string, content requestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

func prependMessage(role string, name string, content requestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(createMessage(role, name, content), *messages...)
	*messages = newMessages
}

func createMessage(role string, name string, content requestContent) []openai.ChatCompletionMessage {
	message := []openai.ChatCompletionMessage{
		{
			Role:         role,
			MultiContent: []openai.ChatMessagePart{},
		},
	}

	if name != "" {
		message[0].Name = cleanName(name)
	}

	if content.text != "" {
		message[0].MultiContent = append(message[0].MultiContent, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: content.text,
		})
	}

	for _, u := range content.url {
		message[0].MultiContent = append(message[0].MultiContent, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    u,
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}

	return message
}

func createBatchMessages(s *discordgo.Session, messages []*discordgo.Message) []openai.ChatCompletionMessage {
	var batchMessages []openai.ChatCompletionMessage

	for _, message := range messages {
		content := requestContent{
			text: message.Content,
			url:  getMessageImages(message),
		}
		if message.Author.ID == s.State.User.ID {
			prependMessage(openai.ChatMessageRoleAssistant, message.Author.Username, content, &batchMessages)
		}
		prependMessage(openai.ChatMessageRoleUser, message.Author.Username, content, &batchMessages)
	}

	return batchMessages
}

func messagesToString(messages []openai.ChatCompletionMessage) string {
	var sb strings.Builder
	for _, message := range messages {
		sb.WriteString(fmt.Sprintf("From: %s, Role: %s: %s\n", message.Name, message.Role, message.MultiContent[0].Text))
	}
	return sb.String()
}

func splitTTS(message string, hd bool) []*discordgo.File {
	// Chunk up message into maxLength character chunks separated by newlines
	separator := "\n\n"
	maxLength := 4000
	var files []*discordgo.File
	var messageChunks []string

	// Split message into chunks of up to maxLength characters
	for len(message) > 0 {
		var chunk string
		if len(message) > maxLength {
			// Find the last separator before the maxLength character limit
			end := strings.LastIndex(message[:maxLength], separator)
			if end == -1 {
				// No separator found, so just cut at maxLength characters
				end = maxLength
			}
			chunk = message[:end]
			message = message[end:]
		} else {
			chunk = message
			message = ""
		}
		// Add chunk to messageChunks
		messageChunks = append(messageChunks, chunk)
	}

	var wg sync.WaitGroup
	filesChan := make(chan *discordgo.File, len(messageChunks))

	for count, chunk := range messageChunks {
		wg.Add(1)
		go func(chunk string, index int) {
			defer wg.Done()
			files := getTTSFile(chunk, fmt.Sprintf("%d", index+1), hd)
			for _, file := range files {
				filesChan <- file
			}
		}(chunk, count)
	}

	wg.Wait()
	close(filesChan)

	for file := range filesChan {
		files = append(files, file)
	}

	return files
}

func splitParagraph(message string) (string, string) {
	primarySeparator := "\n\n"
	secondarySeparator := "\n"
	var firstPart string
	var lastPart string

	// split the message into two parts based on the separator
	lastPrimaryIndex := strings.LastIndex(message, primarySeparator)
	lastSecondaryIndex := strings.LastIndex(message, secondarySeparator)
	// if there's a separator in the message
	if lastPrimaryIndex != -1 {
		// split the message into two parts
		firstPart = message[:lastPrimaryIndex]
		lastPart = message[lastPrimaryIndex+len(primarySeparator):]
	} else if lastSecondaryIndex != -1 {
		// split the message into two parts
		firstPart = message[:lastSecondaryIndex]
		lastPart = message[lastSecondaryIndex+len(secondarySeparator):]

	} else {
		// if there's no separator, return the whole message, and start the next one
		firstPart = message
		lastPart = ""
	}

	// if there's a code block in the first part that's not closed
	if strings.Count(firstPart, "```")%2 != 0 {
		lastCodeBlockIndex := strings.LastIndex(firstPart, "```")
		lastCodeBlock := firstPart[lastCodeBlockIndex:]
		// returns ```lang of the last code block in the first part
		languageCode := lastCodeBlock[:strings.Index(lastCodeBlock, "\n")]

		// ends the code block in the first message and starts a new code block in the next one
		firstPart = firstPart + "```"
		lastPart = languageCode + "\n" + lastPart
	}

	return firstPart, lastPart
}

func prependReplies(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]openai.ChatCompletionMessage) {
	// check if the message has a reference, if not get it
	if message.ReferencedMessage == nil {
		if message.MessageReference != nil {
			cachedMessage := checkCache(cache, message.MessageReference.MessageID)
			if cachedMessage != nil {
				message.ReferencedMessage = cachedMessage
			} else {
				message.ReferencedMessage, _ = s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
			}
		}
	}
	replyMessage := cleanMessage(s, message.ReferencedMessage)
	replyContent := requestContent{
		text: replyMessage.Content,
		url:  getMessageImages(replyMessage),
	}
	if replyMessage.Author.ID == s.State.User.ID {
		prependMessage(openai.ChatMessageRoleAssistant, replyMessage.Author.Username, replyContent, chatMessages)
	} else {
		prependMessage(openai.ChatMessageRoleUser, replyMessage.Author.Username, replyContent, chatMessages)
	}

	if replyMessage.Type == discordgo.MessageTypeReply {
		prependReplies(s, message.ReferencedMessage, cache, chatMessages)
	}
}

func getMessagesBefore(s *discordgo.Session, channelID string, count int, messageID string) []*discordgo.Message {
	messages, err := s.ChannelMessages(channelID, count, messageID, "", "")
	if err != nil {
		return nil
	}
	return messages
}

func getChannelMessages(s *discordgo.Session, channelID string, count int) []*discordgo.Message {
	// if the count is over 100 split into multiple runs with the last message id being the before id argument
	var messages []*discordgo.Message
	var lastMessageID string

	// Calculate the number of full iterations and the remainder, dividing an int floors the result
	iterations := count / 100
	remainder := count % 100

	// Fetch full iterations of 100 messages
	for i := 0; i < iterations; i++ {
		var batch = getMessagesBefore(s, channelID, 100, lastMessageID)
		lastMessageID = batch[len(batch)-1].ID
		messages = append(messages, batch...)
	}

	// Fetch the remainder of messages if there are any
	if remainder > 0 {
		var batch = getMessagesBefore(s, channelID, remainder, lastMessageID)
		messages = append(messages, batch...)
	}
	return messages
}

func getAllChannelMessages(s *discordgo.Session, m *discordgo.Message, channelID string, c chan []*discordgo.Message) {
	defer close(c)
	var lastMessageID string
	messagesRetrieved := 100
	count := 0

	for messagesRetrieved == 100 {
		var batch = getMessagesBefore(s, channelID, 100, lastMessageID)
		if len(batch) == 0 || batch == nil {
			log.Println("getAllChannelMessages: no messages retrieved")
			break
		}
		lastMessageID = batch[len(batch)-1].ID
		messagesRetrieved = len(batch)
		count += messagesRetrieved
		_, err := editMessage(s, m, fmt.Sprintf("Retrieved %d messages", count))
		if err != nil {
			log.Println(err)
		}
		c <- batch
	}

	log.Println("getAllChannelMessages: done")
}

func getMessageImages(m *discordgo.Message) []string {
	seen := make(map[string]bool)
	var urls []string
	var uniqueURLs []string

	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if isImageURL(attachment.URL) {
				urls = append(urls, attachment.URL)
			}
		}
	}
	for _, embed := range m.Embeds {
		if embed.Thumbnail != nil {
			if isImageURL(embed.Thumbnail.URL) {
				urls = append(urls, embed.Thumbnail.URL)
			}
		}
		if embed.Image != nil {
			if isImageURL(embed.Image.URL) {
				urls = append(urls, embed.Image.URL)
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if isImageURL(match[1]) {
			urls = append(urls, match[1])
		}
	}

	for _, u := range urls {
		checkUrl := cleanUrl(u)
		if !seen[checkUrl] {
			seen[checkUrl] = true
			uniqueURLs = append(uniqueURLs, u)
		}
	}

	return uniqueURLs
}

func checkCache(cache []*discordgo.Message, messageID string) *discordgo.Message {
	for _, message := range cache {
		if message.ID == messageID {
			return message
		}
	}
	return nil
}

func cleanMessage(s *discordgo.Session, message *discordgo.Message) *discordgo.Message {
	botID := fmt.Sprintf("<@%s>", s.State.User.ID)
	mentionRegex := regexp.MustCompile(botID)
	message.Content = mentionRegex.ReplaceAllString(message.Content, "")
	message.Content = strings.TrimSpace(message.Content)
	return message
}

func cleanName(name string) string {
	if len(name) > 64 {
		name = name[:64]
	}
	name = regexp.MustCompile("[^a-zA-Z0-9_-]").ReplaceAllString(name, "")
	return name
}

func cleanMessages(s *discordgo.Session, messages []*discordgo.Message) []*discordgo.Message {
	for i, message := range messages {
		messages[i] = cleanMessage(s, message)
	}
	return messages
}

func hasImageURL(m *discordgo.Message) bool {
	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if isImageURL(attachment.URL) {
				return true
			}
		}
	}
	for _, embed := range m.Embeds {
		// check if embed has image
		if embed.Thumbnail != nil {
			if isImageURL(embed.Thumbnail.URL) {
				return true
			}
		}
		if embed.Image != nil {
			if isImageURL(embed.Image.URL) {
				return true
			}
		}
	}

	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if isImageURL(match[1]) {
			return true
		}
	}
	return false
}

func isImageURL(urlStr string) bool {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return false
	}
	fileExt := filepath.Ext(parsedURL.Path)
	fileExt = strings.ToLower(fileExt)

	switch fileExt {
	case ".jpg", ".jpeg", ".png", ".gif", ".webp":
		return true
	default:
		return false
	}
}

func cleanUrl(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return urlStr
	}
	return parsedURL.Path
}
