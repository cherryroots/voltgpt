package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic/v2"
	"github.com/pkoukk/tiktoken-go"
	"github.com/sashabaranov/go-openai"

	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/utility"
)

func instructionSwitch(m []openai.ChatCompletionMessage) config.RequestContent {
	firstMessageText := m[0].MultiContent[0].Text
	lastMessageText := m[len(m)-1].MultiContent[0].Text
	text := lastMessageText
	if firstMessageText != lastMessageText { // if there are multiple messages
		text = fmt.Sprintf("%s\n%s", firstMessageText, lastMessageText)
	}
	if strings.Contains(text, "❤️") || strings.Contains(text, "❤") || strings.Contains(text, ":heart:") {
		return config.InstructionMessageDefault
	}
	return config.InstructionMessageMean
}

func getTTSFile(message string, index string, hd bool) []*discordgo.File {
	files := make([]*discordgo.File, 0)
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()
	model := openai.TTSModel1

	if hd {
		model = openai.TTSModel1HD
	}

	res, err := c.CreateSpeech(ctx, openai.CreateSpeechRequest{
		Model: model,
		Input: message,
		Voice: openai.VoiceNova,
	})
	if err != nil {
		log.Printf("CreateSpeech error: %v\n", err)
	}
	defer res.Close()

	buf, err := io.ReadAll(res)
	if err != nil {
		log.Printf("io.ReadAll error: %v\n", err)
	}

	filename := getFilenameSummary(message)

	files = append(files, &discordgo.File{
		Name:   index + "-" + filename + ".mp3",
		Reader: strings.NewReader(string(buf)),
	})

	return files
}

func getIntents(message string) string {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	prompt := "What's the intent in this message? Intents can be 'draw' or 'none'. " +
		"The draw intent is for when the message asks to draw or generate some kind of image. " +
		"none intent is for when nothing image generation related is asked. Focus more on the last part of the message. " +
		"Don't include anything except the intent in the generated text: \n\n" +
		message

	maxTokens, err := GetRequestMaxTokensString(prompt, openai.GPT3Dot5Turbo0125)
	if err != nil {
		log.Printf("getRequestMaxTokens error: %v\n", err)
		return ""
	}

	req := openai.ChatCompletionRequest{
		Model:       openai.GPT3Dot5Turbo0125,
		Messages:    createMessage(openai.ChatMessageRoleUser, "", config.RequestContent{Text: prompt}),
		Temperature: config.DefaultTemp,
		MaxTokens:   maxTokens,
	}

	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Printf("CreateCompletion error: %v\n", err)
		return ""
	}

	return resp.Choices[0].Message.Content
}

func getFilenameSummary(message string) string {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	prompt := "Summarize this text as a filename but without a file extension: " + message

	maxTokens, err := GetRequestMaxTokensString(prompt, openai.GPT3Dot5Turbo0125)
	if err != nil {
		log.Printf("getRequestMaxTokens error: %v\n", err)
		return "file"
	}

	req := openai.ChatCompletionRequest{
		Model:       openai.GPT3Dot5Turbo0125,
		Messages:    createMessage(openai.ChatMessageRoleUser, "", config.RequestContent{Text: prompt}),
		Temperature: config.DefaultTemp,
		MaxTokens:   maxTokens,
	}

	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Printf("CreateCompletion error: %v\n", err)
		return "file"
	}

	return resp.Choices[0].Message.Content
}

func drawImage(message string, size string) ([]*discordgo.File, error) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	reqURL := openai.ImageRequest{
		Prompt:         message,
		Model:          openai.CreateImageModelDallE3,
		Quality:        openai.CreateImageQualityStandard,
		Size:           size,
		ResponseFormat: openai.CreateImageResponseFormatB64JSON,
		N:              1,
	}

	respBase64, err := c.CreateImage(ctx, reqURL)
	if err != nil {
		return nil, err
	}

	resp, err := base64.StdEncoding.DecodeString(respBase64.Data[0].B64JSON)
	if err != nil {
		return nil, err
	}

	files := []*discordgo.File{
		{
			Name:   "image.png",
			Reader: bytes.NewReader(resp),
		},
	}

	return files, nil
}

func streamMessageOAIResponse(s *discordgo.Session, m *discordgo.MessageCreate, messages []openai.ChatCompletionMessage) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokens(messages, config.DefaultOAIModel)
	if err != nil {
		discord.LogSendErrorMessage(s, m.Message, err.Error())
		return
	}

	// Create a new request
	req := openai.ChatCompletionRequest{
		Model:       config.DefaultOAIModel,
		Messages:    messages,
		Temperature: config.DefaultTemp,
		MaxTokens:   maxTokens,
		Stream:      true,
	}
	// Send the request
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		errmsg := fmt.Sprintf("ChatCompletionStream error: %v\n", err)
		discord.LogSendErrorMessage(s, m.Message, errmsg)
		return
	}
	defer stream.Close()

	msg, err := discord.SendMessageFile(s, m.Message, "Responding...", nil)
	if err != nil {
		discord.LogSendErrorMessage(s, m.Message, err.Error())
		return
	}

	var i int
	var message string
	var fullMessage string
	// Read the stream and send the response
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// At the end of the stream
			// Send the last message state
			message = strings.TrimPrefix(message, "...")
			_, err = discord.EditMessageFile(s, msg, message, nil)
			if err != nil {
				discord.LogSendErrorMessage(s, m.Message, err.Error())
				return
			}
			go func() {
				AppendMessage(openai.ChatMessageRoleAssistant, s.State.User.Username, config.RequestContent{Text: fullMessage}, &messages)
				request := messagesToString(messages)
				if len(request) > 4000 {
					request = request[len(request)-4000:]
				}
				intent := getIntents(request)
				log.Printf("Intent: %s\n", intent)
				if intent == "draw" {
					files, err := drawImage(request, openai.CreateImageSize1024x1024)
					if err != nil {
						discord.LogSendErrorMessage(s, m.Message, err.Error())
					}
					_, err = discord.EditMessageFile(s, msg, message, files)
					if err != nil {
						discord.LogSendErrorMessage(s, m.Message, err.Error())
						return
					}
				}
			}()
			/*
				go func() {
					files := splitTTS(fullMessage, true)
					_, err = discord.EditMessageFile(s, msg, message, files)
					if err != nil {
						discord.LogSendErrorMessage(s, m.Message, err.Error())
						return
					}
				}()
			*/
			return
		}
		if err != nil {
			errmsg := fmt.Sprintf("\nStream error: %v\n", err)
			discord.LogSendErrorMessage(s, m.Message, errmsg)
			return
		}

		message = message + response.Choices[0].Delta.Content
		fullMessage = fullMessage + response.Choices[0].Delta.Content
		i++
		// Every 50 delta send the message
		if i%50 == 0 {
			// If the message is too long, split it into a new message
			if len(message) > 1800 {
				firstPart, lastPart := utility.SplitParagraph(message)
				if lastPart == "" {
					lastPart = "..."
				}

				_, err = discord.EditMessageFile(s, msg, firstPart, nil)
				if err != nil {
					discord.LogSendErrorMessage(s, m.Message, err.Error())
					return
				}
				msg, err = discord.SendMessageFile(s, msg, lastPart, nil)
				if err != nil {
					discord.LogSendErrorMessage(s, m.Message, err.Error())
					return
				}
				message = lastPart
			} else {
				_, err = discord.EditMessageFile(s, msg, message, nil)
				if err != nil {
					discord.LogSendErrorMessage(s, m.Message, err.Error())
					return
				}
			}
		}
	}
}

func StreamInteractionResponse(s *discordgo.Session, i *discordgo.InteractionCreate, reqMessage []openai.ChatCompletionMessage, options *config.GenerationOptions) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokens(reqMessage, options.Model)
	if err != nil {
		log.Println(err)
		return
	}

	req := openai.ChatCompletionRequest{
		Model:       options.Model,
		Messages:    reqMessage,
		Temperature: options.Temperature,
		MaxTokens:   maxTokens,
		Stream:      true,
	}
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Printf("ChatCompletionStream error: %v\n", err)
		return
	}
	defer stream.Close()
	msg, err := discord.SendFollowupFile(s, i, "Responding...", nil)
	if err != nil {
		log.Printf("discord.SendFollowup error: %v\n", err)
		return
	}

	var count int
	var message string
	var fullMessage string
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			_, err = discord.EditFollowupFile(s, i, msg.ID, message, nil)
			if err != nil {
				log.Printf("editFollowup error: %v\n", err)
				return
			}
			/*
				go func() {
					appendMessage(openai.ChatMessageRoleAssistant, s.State.User.Username, config.RequestContent{text: fullMessage}, &reqMessage)
					request := messagesToString(reqMessage)
					if len(request) > 4000 {
						request = request[len(request)-4000:]
					}
					intent := getIntents(request)
					log.Printf("Intent: %s\n", intent)
					if intent == "draw" {
						files, err := drawImage(request, openai.CreateImageSize1024x1024)
						if err != nil {
							log.Printf("draw error: %v\n", err)
						}
						_, err = discord.EditFollowupFile(s, i, msg.ID, message, files)
						if err != nil {
							log.Printf("editFollowup error: %v\n", err)
							return
						}
					}
				}()

				go func() {
					files := splitTTS(fullMessage, true)
					_, err = discord.EditFollowupFile(s, i, msg.ID, message, files)
					if err != nil {
						log.Printf("editFollowup error: %v\n", err)
						return
					}
				}()
			*/
			return
		}
		if err != nil {
			log.Printf("\nStream error: %v\n", err)
			return
		}

		message = message + response.Choices[0].Delta.Content
		fullMessage = fullMessage + response.Choices[0].Delta.Content
		count++
		if count%50 == 0 {
			if len(message) > 1800 {
				firstPart, lastPart := utility.SplitParagraph(message)
				if lastPart == "" {
					lastPart = "..."
				}
				_, err = discord.EditFollowupFile(s, i, msg.ID, firstPart, nil)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
				msg, err = discord.SendFollowupFile(s, i, lastPart, nil)
				if err != nil {
					log.Printf("discord.SendFollowup error: %v\n", err)
					return
				}
				message = lastPart
			} else {
				_, err = discord.EditFollowupFile(s, i, msg.ID, message, nil)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
			}
		}
	}
}

func AppendMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

func PrependMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(createMessage(role, name, content), *messages...)
	*messages = newMessages
}

func createMessage(role string, name string, content config.RequestContent) []openai.ChatCompletionMessage {
	message := []openai.ChatCompletionMessage{
		{
			Role:         role,
			MultiContent: []openai.ChatMessagePart{},
		},
	}

	if name != "" {
		message[0].Name = utility.CleanName(name)
	}

	if content.Text != "" {
		message[0].MultiContent = append(message[0].MultiContent, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: content.Text,
		})
	}

	for _, u := range content.Url {
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

func CreateBatchMessages(s *discordgo.Session, messages []*discordgo.Message) []openai.ChatCompletionMessage {
	var batchMessages []openai.ChatCompletionMessage

	for _, message := range messages {
		images, _ := utility.GetMessageMediaURL(message)
		content := config.RequestContent{
			Text: message.Content,
			Url:  images,
		}
		if message.Author.ID == s.State.User.ID {
			PrependMessage(openai.ChatMessageRoleAssistant, message.Author.Username, content, &batchMessages)
		}
		PrependMessage(openai.ChatMessageRoleUser, message.Author.Username, content, &batchMessages)
	}

	return batchMessages
}

func prependRepliesMessages(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]openai.ChatCompletionMessage) {
	// check if the message has a reference, if not get it
	if message.ReferencedMessage == nil {
		if message.MessageReference != nil {
			cachedMessage := utility.CheckCache(cache, message.MessageReference.MessageID)
			if cachedMessage != nil {
				message.ReferencedMessage = cachedMessage
			} else {
				message.ReferencedMessage, _ = s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
			}
		} else {
			return
		}
	}
	replyMessage := utility.CleanMessage(s, message.ReferencedMessage)
	images, _ := utility.GetMessageMediaURL(replyMessage)
	replyContent := config.RequestContent{
		Text: replyMessage.Content,
		Url:  images,
	}
	if replyMessage.Author.ID == s.State.User.ID {
		PrependMessage(openai.ChatMessageRoleAssistant, replyMessage.Author.Username, replyContent, chatMessages)
	} else {
		PrependMessage(openai.ChatMessageRoleUser, replyMessage.Author.Username, replyContent, chatMessages)
	}

	if replyMessage.Type == discordgo.MessageTypeReply {
		prependRepliesMessages(s, message.ReferencedMessage, cache, chatMessages)
	}
}

func messagesToString(messages []openai.ChatCompletionMessage) string {
	var sb strings.Builder
	for _, message := range messages {
		sb.WriteString(fmt.Sprintf("From: %s, Role: %s: %s\n", message.Name, message.Role, message.MultiContent[0].Text))
	}
	return sb.String()
}

func GetMaxModelTokens(model string) (maxTokens int) {
	switch model {
	case anthropic.ModelClaude3Haiku20240307, anthropic.ModelClaude3Sonnet20240229, anthropic.ModelClaude3Opus20240229:
		maxTokens = 200000
	case openai.GPT4:
		maxTokens = 8192
	case openai.GPT4Turbo:
		maxTokens = 128000
	case openai.GPT3Dot5Turbo0125:
		maxTokens = 16385
	default:
		maxTokens = 4096
	}
	return maxTokens
}

func IsOutputLimited(model string) bool {
	switch model {
	case openai.GPT4Turbo, openai.GPT3Dot5Turbo0125, anthropic.ModelClaude3Haiku20240307, anthropic.ModelClaude3Sonnet20240229, anthropic.ModelClaude3Opus20240229:
		return true
	default:
		return false
	}
}

func GetRequestMaxTokensString(message string, model string) (maxTokens int, err error) {
	maxTokens = GetMaxModelTokens(model)
	usedTokens := NumTokensFromString(message)

	availableTokens := maxTokens - usedTokens

	if availableTokens < 0 {
		availableTokens = 0
		err = fmt.Errorf("not enough tokens")
		return availableTokens, err
	}

	if IsOutputLimited(model) {
		availableTokens = 4096
	}

	return availableTokens, nil
}

func getRequestMaxTokens(message []openai.ChatCompletionMessage, model string) (maxTokens int, err error) {
	maxTokens = GetMaxModelTokens(model)
	usedTokens := NumTokensFromMessages(message, model)

	availableTokens := maxTokens - usedTokens

	if availableTokens < 0 {
		availableTokens = 0
		err = fmt.Errorf("not enough tokens")
		return availableTokens, err
	}

	if IsOutputLimited(model) {
		availableTokens = 4096
	}

	return availableTokens, nil
}

func NumTokensFromString(s string) (numTokens int) {
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

func NumTokensFromMessages(messages []openai.ChatCompletionMessage, model string) (numTokens int) {
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
		"gpt-4-turbo",
		"gpt-4-turbo-2024-04-09":
		tokensPerMessage = 3
		tokensPerName = 1
	case "gpt-3.5-turbo-0301":
		tokensPerMessage = 4 // every message follows <|start|>{role/name}\n{content}<|end|>\n
		tokensPerName = -1   // if there's a name, the role is omitted
	default:
		if strings.Contains(model, "gpt-3.5-turbo") {
			log.Println("warning: gpt-3.5-turbo may update over time. Returning num tokens assuming gpt-3.5-turbo-0613")
			return NumTokensFromMessages(messages, "gpt-3.5-turbo-0613")
		} else if strings.Contains(model, "gpt-4") {
			log.Println("warning: gpt-4 may update over time. Returning num tokens assuming gpt-4-0613")
			return NumTokensFromMessages(messages, "gpt-4-0613")
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

func SplitTTS(message string, hd bool) []*discordgo.File {
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
		go func(count int, chunk string) {
			defer wg.Done()
			files := getTTSFile(chunk, fmt.Sprintf("%d", count+1), hd)
			for _, file := range files {
				filesChan <- file
			}
		}(count, chunk)
	}

	wg.Wait()
	close(filesChan)

	for file := range filesChan {
		files = append(files, file)
	}

	return files
}
