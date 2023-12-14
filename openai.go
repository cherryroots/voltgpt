package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/pkoukk/tiktoken-go"
	openai "github.com/sashabaranov/go-openai"
)

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
		Voice: openai.VoiceAlloy,
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

func getFilenameSummary(message string) string {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokensString(message, defaultModel)
	if err != nil {
		log.Printf("getRequestMaxTokens error: %v\n", err)
		return "file"
	}

	prompt := "Summarize this text as a filename but without a file extension: " + message

	req := openai.ChatCompletionRequest{
		Model:       openai.GPT3Dot5Turbo1106,
		Messages:    createMessage(openai.ChatMessageRoleUser, "", requestContent{text: prompt}),
		Temperature: defaultTemp,
		MaxTokens:   maxTokens,
	}

	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Printf("CreateCompletion error: %v\n", err)
		return "file"
	}

	return resp.Choices[0].Message.Content
}

func sendMessageChatResponse(s *discordgo.Session, m *discordgo.MessageCreate, messages []openai.ChatCompletionMessage) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokens(messages, defaultModel)
	if err != nil {
		logSendErrorMessage(s, m.Message, err.Error())
		return
	}

	// Create a new request
	req := openai.ChatCompletionRequest{
		Model:       defaultModel,
		Messages:    messages,
		Temperature: defaultTemp,
		MaxTokens:   maxTokens,
		Stream:      true,
	}
	// Send the request
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		errmsg := fmt.Sprintf("ChatCompletionStream error: %v\n", err)
		logSendErrorMessage(s, m.Message, errmsg)
		return
	}
	defer stream.Close()

	msg, err := sendMessage(s, m.Message, "Responding...", nil)
	if err != nil {
		logSendErrorMessage(s, m.Message, err.Error())
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
			editMessage(s, msg, message, nil)
			files := splitTTS(fullMessage, false)
			editMessage(s, msg, message, files)
			return
		}
		if err != nil {
			errmsg := fmt.Sprintf("\nStream error: %v\n", err)
			logSendErrorMessage(s, m.Message, errmsg)
			return
		}

		message = message + response.Choices[0].Delta.Content
		fullMessage = fullMessage + response.Choices[0].Delta.Content
		i++
		// Every 50 delta send the message
		if i%50 == 0 {
			// If the message is too long, split it into a new message
			if len(message) > 1800 {
				firstPart, lastPart := splitParagraph(message)
				if lastPart == "" {
					lastPart = "..."
				}

				editMessage(s, msg, firstPart, nil)
				msg, err = sendMessage(s, msg, lastPart, nil)
				if err != nil {
					logSendErrorMessage(s, m.Message, err.Error())
					return
				}
				message = lastPart
			} else {
				editMessage(s, msg, message, nil)
			}
		}
	}
}

func sendInteractionChatResponse(s *discordgo.Session, i *discordgo.InteractionCreate, reqMessage []openai.ChatCompletionMessage, options *generationOptions) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokens(reqMessage, options.model)
	if err != nil {
		log.Println(err)
		return
	}

	req := openai.ChatCompletionRequest{
		Model:       options.model,
		Messages:    reqMessage,
		Temperature: options.temperature,
		MaxTokens:   maxTokens,
		Stream:      true,
	}
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Printf("ChatCompletionStream error: %v\n", err)
		return
	}
	defer stream.Close()
	msg, err := sendFollowup(s, i, "Responding...", nil)
	if err != nil {
		log.Printf("sendFollowup error: %v\n", err)
		return
	}

	var count int
	var message string
	var fullMessage string
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			editFollowup(s, i, msg.ID, message, nil)
			files := splitTTS(fullMessage, false)
			editFollowup(s, i, msg.ID, message, files)
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
				firstPart, lastPart := splitParagraph(message)
				if lastPart == "" {
					lastPart = "..."
				}
				_, err = editFollowup(s, i, msg.ID, firstPart, nil)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
				msg, err = sendFollowup(s, i, lastPart, nil)
				if err != nil {
					log.Printf("sendFollowup error: %v\n", err)
					return
				}
				message = lastPart
			} else {
				_, err = editFollowup(s, i, msg.ID, message, nil)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
			}
		}
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
