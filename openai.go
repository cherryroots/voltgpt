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
	openai "github.com/sashabaranov/go-openai"
)

func getTTSFile(message string, index string) []*discordgo.File {
	files := make([]*discordgo.File, 0)
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	res, err := c.CreateSpeech(ctx, openai.CreateSpeechRequest{
		Model: openai.TTSModel1,
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

	prompt := "Summarize this text as a filename: " + message

	req := openai.ChatCompletionRequest{
		Model:       openai.GPT3Dot5Turbo1106,
		Messages:    createMessage(openai.ChatMessageRoleUser, "", prompt),
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
			files := splitTTS(fullMessage)
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

func sendInteractionChatResponse(s *discordgo.Session, i *discordgo.InteractionCreate, reqMessage []openai.ChatCompletionMessage, options *responseOptions) {
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
			files := splitTTS(fullMessage)
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
