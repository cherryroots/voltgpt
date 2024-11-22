// Package openai is a package for interacting with the OpenAI API.
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/pkoukk/tiktoken-go"
	"github.com/sashabaranov/go-openai"

	"voltgpt/internal/config"
	"voltgpt/internal/utility"
)

func getTTSFile(message string, index int, hd bool) *discordgo.File {
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
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

	file := &discordgo.File{
		Name:   fmt.Sprintf("%d-%s.mp3", index+1, filename),
		Reader: strings.NewReader(string(buf)),
	}

	return file
}

func getFilenameSummary(message string) string {
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
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
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
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

	for _, u := range content.Images {
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
		images, _, pdfs := utility.GetMessageMediaURL(message)
		content := config.RequestContent{
			Text:   message.Content,
			Images: images,
			PDFs:   pdfs,
		}
		if message.Author.ID == s.State.User.ID {
			PrependMessage(openai.ChatMessageRoleAssistant, message.Author.Username, content, &batchMessages)
		}
		PrependMessage(openai.ChatMessageRoleUser, message.Author.Username, content, &batchMessages)
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

func GetMaxModelTokens(model string) (maxTokens int) {
	switch model {
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
	case openai.GPT4Turbo, openai.GPT3Dot5Turbo0125:
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
	encoding := "cl100k_base"
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
	separator := "\n\n"
	maxLength := 4000
	type fileIndex struct {
		file  *discordgo.File
		index int
	}
	var fileIndexes []fileIndex
	var messageChunks []string

	for len(message) > 0 {
		var chunk string
		if len(message) > maxLength {
			// Find the last separator before the maxLength character limit
			end := strings.LastIndex(message[:maxLength], separator)
			if end == -1 {
				end = maxLength
			}
			chunk = message[:end]
			message = message[end:]
		} else {
			chunk = message
			message = ""
		}
		messageChunks = append(messageChunks, chunk)
	}

	var wg sync.WaitGroup

	for count, chunk := range messageChunks {
		wg.Add(1)
		go func(count int, chunk string) {
			defer wg.Done()
			file := getTTSFile(chunk, count+1, hd)
			fileIndexes = append(fileIndexes, fileIndex{file: file, index: count + 1})
		}(count, chunk)
	}

	wg.Wait()

	sort.Slice(fileIndexes, func(i, j int) bool { return fileIndexes[i].index < fileIndexes[j].index })

	var filePointers []*discordgo.File
	for _, file := range fileIndexes {
		filePointers = append(filePointers, file.file)
	}

	return filePointers
}
