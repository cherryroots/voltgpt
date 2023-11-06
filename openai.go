package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"

	"github.com/bwmarrin/discordgo"
	openai "github.com/sashabaranov/go-openai"
)

func appendMessage(role string, name string, content string, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

func prependMessage(role string, name string, content string, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(createMessage(role, name, content), *messages...)
	*messages = newMessages
}

func createMessage(role string, name string, content string) []openai.ChatCompletionMessage {
	return []openai.ChatCompletionMessage{
		{
			Role:    role,
			Name:    name,
			Content: content,
		},
	}
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

	// Create a new request
	req := openai.ChatCompletionRequest{
		Model:       openai.GPT40314,
		Messages:    messages,
		Temperature: 0.7,
		MaxTokens:   getRequestMaxTokens(messages, openai.GPT40314),
		Stream:      true,
	}
	// Send the request
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Printf("ChatCompletionStream error: %v\n", err)
		return
	}
	defer stream.Close()

	msg, err := sendMessage(s, m.Message, "Responding...")
	if err != nil {
		log.Println(err)
		return
	}

	var i int
	var message string
	// Read the stream and send the response
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			// At the end of the stream
			// Send the last message state
			editMessage(s, msg, message)
			return
		}
		if err != nil {
			log.Printf("\nStream error: %v\n", err)
			return
		}

		message = message + response.Choices[0].Delta.Content
		i++
		// Every 15 delta send the message
		if i%15 == 0 {
			// If the message is too long, split it into a new message
			if len(message) > 1900 {
				editMessage(s, msg, message)
				msg, err = sendMessage(s, msg, "...")
				if err != nil {
					log.Println(err)
					return
				}
				message = ""
			} else {
				editMessage(s, msg, message)
			}
		}
	}
}

func sendInteractionChatResponse(s *discordgo.Session, i *discordgo.InteractionCreate, temperature float32, model string) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	reqMessage := createMessage(openai.ChatMessageRoleUser, i.Member.User.Username, i.ApplicationCommandData().Options[0].Value.(string))

	req := openai.ChatCompletionRequest{
		Model:       model,
		Messages:    reqMessage,
		Temperature: temperature,
		MaxTokens:   getRequestMaxTokens(reqMessage, model),
		Stream:      true,
	}
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Printf("ChatCompletionStream error: %v\n", err)
		return
	}
	defer stream.Close()
	msg, err := sendFollowup(s, i, "Responding...")
	if err != nil {
		log.Printf("sendFollowup error: %v\n", err)
		return
	}
	var count int = 1

	var message string
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			editFollowup(s, i, msg.ID, message)
			return
		}
		if err != nil {
			log.Printf("\nStream error: %v\n", err)
			return
		}

		message = message + response.Choices[0].Delta.Content
		if count%15 == 0 {
			if len(message) > 1900 {
				_, err = editFollowup(s, i, msg.ID, message)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
				msg, err = sendFollowup(s, i, "...")
				if err != nil {
					log.Printf("sendFollowup error: %v\n", err)
					return
				}
				message = ""
			} else {
				_, err = editFollowup(s, i, msg.ID, message)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
			}
		}
		count++
	}
}

func sendInteractionCompletionResponse(s *discordgo.Session, i *discordgo.InteractionCreate, temperature float32, model string) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	prompt := i.ApplicationCommandData().Options[0].Value.(string)

	req := openai.CompletionRequest{
		Model:       model,
		Prompt:      prompt,
		Temperature: temperature,
		MaxTokens:   getRequestMaxTokensString(prompt, model),
		Stream:      true,
	}
	stream, err := c.CreateCompletionStream(ctx, req)
	if err != nil {
		log.Printf("ChatCompletionStream error: %v\n", err)
		return
	}
	defer stream.Close()
	msg, err := sendFollowup(s, i, "Responding...")
	if err != nil {
		log.Printf("sendFollowup error: %v\n", err)
		return
	}
	var count int = 1

	var message string = prompt
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			editFollowup(s, i, msg.ID, message)
			return
		}
		if err != nil {
			log.Printf("\nStream error: %v\n", err)
			return
		}

		message = message + response.Choices[0].Text
		if count%15 == 0 {
			if len(message) > 1900 {
				_, err = editFollowup(s, i, msg.ID, message)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
				msg, err = sendFollowup(s, i, "...")
				if err != nil {
					log.Printf("sendFollowup error: %v\n", err)
					return
				}
				message = ""
			} else {
				_, err = editFollowup(s, i, msg.ID, message)
				if err != nil {
					log.Printf("editFollowup error: %v\n", err)
					return
				}
			}
		}
		count++
	}
}
