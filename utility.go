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
	openai "github.com/sashabaranov/go-openai"
)

type requestContent struct {
	text string
	url  []string
}

func sendFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
		Files:   files,
	})

	return msg, err
}

func editFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
		Files:   files,
	})

	return msg, err
}

func deferResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Println(err)
	}
}

func deferEphemeralResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		log.Println(err)
	}
}

func sendMessage(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
		Files:     files,
	})

	return msg, err
}

// command to edit a given message the bot has sent
func editMessage(s *discordgo.Session, m *discordgo.Message, content string, files []*discordgo.File) *discordgo.Message {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
		Files:   files,
	})
	if err != nil {
		log.Println(err)
	}

	return msg
}

func linkFromIMessage(s *discordgo.Session, i *discordgo.InteractionCreate, m *discordgo.Message) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, m.ChannelID, m.ID)
}

func linkFromMcMessage(s *discordgo.Session, mc *discordgo.MessageCreate, m *discordgo.Message) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", mc.Message.GuildID, m.ChannelID, m.ID)
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

	for _, url := range content.url {
		message[0].MultiContent = append(message[0].MultiContent, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    url,
				Detail: openai.ImageURLDetailLow,
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
			url:  getMessageImages(s, message),
		}
		if message.Author.ID == s.State.User.ID {
			prependMessage(openai.ChatMessageRoleAssistant, message.Author.Username, content, &batchMessages)
		}
		prependMessage(openai.ChatMessageRoleUser, message.Author.Username, content, &batchMessages)
	}

	return batchMessages
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

func checkForReplies(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]openai.ChatCompletionMessage) {
	if message.Type == discordgo.MessageTypeReply {
		// check if the message has a refference, if not get it
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
		content := requestContent{
			text: replyMessage.Content,
			url:  getMessageImages(s, replyMessage),
		}
		if replyMessage.Author.ID == s.State.User.ID {
			prependMessage(openai.ChatMessageRoleAssistant, replyMessage.Author.Username, content, chatMessages)
		} else {
			prependMessage(openai.ChatMessageRoleUser, replyMessage.Author.Username, content, chatMessages)
		}
		checkForReplies(s, message.ReferencedMessage, cache, chatMessages)
	}
}

func getMessagesBefore(s *discordgo.Session, channelID string, count int, messageID string) []*discordgo.Message {
	if messageID == "" {
		messageID = ""
	}
	messages, _ := s.ChannelMessages(channelID, count, messageID, "", "")
	return messages
}

func getChannelMessages(s *discordgo.Session, channelID string, count int) []*discordgo.Message {
	// if the count is over 100 split into multiple runs with the last message id being the beforeid argument
	var messages []*discordgo.Message
	var lastMessageID string

	// Calculate the number of full iterations and the remainder, dividing ints floors the result
	iterations := count / 100
	remainder := count % 100

	// Fetch full iterations of 100 messages
	for i := 0; i < iterations; i++ {
		var batch []*discordgo.Message = getMessagesBefore(s, channelID, 100, lastMessageID)
		lastMessageID = batch[len(batch)-1].ID
		messages = append(messages, batch...)
	}

	// Fetch the remainder of messages if there are any
	if remainder > 0 {
		var batch []*discordgo.Message = getMessagesBefore(s, channelID, remainder, lastMessageID)
		messages = append(messages, batch...)
	}
	return messages
}

func getAllChannelMessages(s *discordgo.Session, i *discordgo.InteractionCreate, channelID string, followupID string) []*discordgo.Message {
	var messages []*discordgo.Message
	var lastMessageID string
	messagesRetrieved := 100
	count := 0

	for messagesRetrieved == 100 {
		var batch []*discordgo.Message = getMessagesBefore(s, channelID, 100, lastMessageID)
		lastMessageID = batch[len(batch)-1].ID
		messagesRetrieved = len(batch)
		count += messagesRetrieved
		editFollowup(s, i, followupID, fmt.Sprintf("Retrieved %d messages", count), []*discordgo.File{})
		messages = append(messages, batch...)
	}

	return messages
}

func getMessageImages(s *discordgo.Session, m *discordgo.Message) []string {
	var attachments []string
	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if isImageURL(attachment.URL) {
				attachments = append(attachments, attachment.URL)
			}
		}
	}
	for _, embed := range m.Embeds {
		if embed.Thumbnail != nil {
			if isImageURL(embed.Thumbnail.URL) {
				attachments = append(attachments, embed.Thumbnail.URL)
			}
		}
		if embed.Image != nil {
			if isImageURL(embed.Image.URL) {
				attachments = append(attachments, embed.Image.URL)
			}
		}
	}

	regex := regexp.MustCompile(`(?m)[<]?(https?:\/\/[^\s<>]+)[>]?\b`)
	result := regex.FindAllStringSubmatch(m.Content, -1)
	for _, match := range result {
		if isImageURL(match[1]) {
			attachments = append(attachments, match[1])
		}
	}
	return attachments
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
	botid := fmt.Sprintf("<@%s>", s.State.User.ID)
	mentionRegex := regexp.MustCompile(botid)
	message.Content = mentionRegex.ReplaceAllString(message.Content, "")
	message.Content = strings.TrimSpace(message.Content)
	return message
}

func cleanName(name string) string {
	// '^[a-zA-Z0-9_-]{1,64}$' are the only characters the name can contain

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

	regex := regexp.MustCompile(`(?m)[<]?(https?:\/\/[^\s<>]+)[>]?\b`)
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

func getFileExt(urlStr string) string {
	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ""
	}
	fileExt := filepath.Ext(parsedURL.Path)
	fileExt = strings.ToLower(fileExt)

	return fileExt
}
