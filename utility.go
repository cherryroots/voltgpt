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
	"github.com/pkoukk/tiktoken-go"
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

func logSendErrorFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	log.Println(content)
	_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})
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

func createMessageLink(s *discordgo.Session, i *discordgo.InteractionCreate, m *discordgo.Message) string {
	return fmt.Sprintf("https://discord.com/channels/%s/%s/%s", i.GuildID, m.ChannelID, m.ID)
}

func sendErrorMessage(s *discordgo.Session, m *discordgo.Message, content string) {
	_, _ = s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})
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
		message[0].Name = name
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
			url:  getAttachments(s, message),
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
		if message.ReferencedMessage == nil {
			if message.MessageReference != nil {
				cachedMessage := checkCache(cache, message.MessageReference.MessageID)
				if cachedMessage != nil {
					message.ReferencedMessage = cachedMessage
				}
				message.ReferencedMessage, _ = s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
			}
		}
		replyMessage := cleanMessage(s, message.ReferencedMessage)
		content := requestContent{
			text: replyMessage.Content,
			url:  getAttachments(s, replyMessage),
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

func getMessagesAround(s *discordgo.Session, channelID string, count int, messageID string) []*discordgo.Message {
	if messageID == "" {
		messageID = ""
	}
	messages, _ := s.ChannelMessages(channelID, count, "", "", messageID)
	return messages
}

func getMessages(s *discordgo.Session, channelID string, count int) []*discordgo.Message {
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

func getAttachments(s *discordgo.Session, m *discordgo.Message) []string {
	var attachments []string
	for _, attachment := range m.Attachments {
		if attachment.Width > 0 && attachment.Height > 0 {
			if isImageURL(attachment.URL) {
				attachments = append(attachments, attachment.URL)
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

func getMaxModelTokens(model string) (maxTokens int) {
	switch model {
	case openai.GPT4:
		maxTokens = 8192
	case openai.GPT4TurboPreview, openai.GPT4VisionPreview:
		maxTokens = 120000
	case openai.GPT3Dot5Turbo1106:
		maxTokens = 16385
	default:
		maxTokens = 4096
	}
	return maxTokens
}

func isOutputLimited(model string) bool {
	switch model {
	case openai.GPT4TurboPreview, openai.GPT4VisionPreview, openai.GPT3Dot5Turbo1106:
		return true
	default:
		return false
	}
}

func getRequestMaxTokensString(message string, model string) (maxTokens int, err error) {
	maxTokens = getMaxModelTokens(model)
	usedTokens := numTokensFromString(message)

	availableTokens := maxTokens - usedTokens

	if availableTokens < 0 {
		availableTokens = 0
		err = fmt.Errorf("not enough tokens")
		return availableTokens, err
	}

	if isOutputLimited(model) {
		availableTokens = 4096
	}

	return availableTokens, nil
}

func getRequestMaxTokens(message []openai.ChatCompletionMessage, model string) (maxTokens int, err error) {
	maxTokens = getMaxModelTokens(model)
	usedTokens := numTokensFromMessages(message, model)

	availableTokens := maxTokens - usedTokens

	if availableTokens < 0 {
		availableTokens = 0
		err = fmt.Errorf("not enough tokens")
		return availableTokens, err
	}

	if isOutputLimited(model) {
		availableTokens = 4096
	}

	return availableTokens, nil
}

func numTokensFromString(s string) (numTokens int) {
	encoding := "p50k_base"
	tkm, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		err = fmt.Errorf("encoding for model: %v", err)
		log.Println(err)
		return
	}
	numTokens = len(tkm.Encode(s, nil, nil))

	return numTokens
}

func numTokensFromMessages(messages []openai.ChatCompletionMessage, model string) (numTokens int) {
	tkm, err := tiktoken.EncodingForModel(model)
	if err != nil {
		err = fmt.Errorf("encoding for model: %v", err)
		log.Println(err)
		return
	}

	var tokensPerMessage, tokensPerName int
	switch model {
	case
		"gpt-3.5-turbo-0613",
		"gpt-3.5-turbo-16k-0613",
		"gpt-3.5-turbo-1106",
		"gpt-4-0314",
		"gpt-4-32k-0314",
		"gpt-4-0613",
		"gpt-4-32k-0613",
		"gpt-4-vision-preview",
		"gpt-4-1106-preview":
		tokensPerMessage = 3
		tokensPerName = 1
	case "gpt-3.5-turbo-0301":
		tokensPerMessage = 4 // every message follows <|start|>{role/name}\n{content}<|end|>\n
		tokensPerName = -1   // if there's a name, the role is omitted
	default:
		if strings.Contains(model, "gpt-3.5-turbo") {
			log.Println("warning: gpt-3.5-turbo may update over time. Returning num tokens assuming gpt-3.5-turbo-0613")
			return numTokensFromMessages(messages, "gpt-3.5-turbo-0613")
		} else if strings.Contains(model, "gpt-4") {
			log.Println("warning: gpt-4 may update over time. Returning num tokens assuming gpt-4-0613")
			return numTokensFromMessages(messages, "gpt-4-0613")
		} else {
			err = fmt.Errorf("num_tokens_from_messages() is not implemented for model %s. See https://github.com/openai/openai-python/blob/main/chatml.md for information on how messages are converted to tokens", model)
			log.Println(err)
			return
		}
	}

	for _, message := range messages {
		numTokens += tokensPerMessage
		numTokens += len(tkm.Encode(message.Content, nil, nil))
		numTokens += len(tkm.Encode(message.Role, nil, nil))
		numTokens += len(tkm.Encode(message.Name, nil, nil))
		if message.Name != "" {
			numTokens += tokensPerName
		}
	}
	numTokens += 3 // every reply is primed with <|start|>assistant<|message|>
	return numTokens
}
