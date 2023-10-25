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

func sendMessageResponse(s *discordgo.Session, m *discordgo.MessageCreate) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	req := openai.ChatCompletionRequest{
		Model: openai.GPT40613,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: m.Content,
			},
		},
		Stream: true,
	}
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Printf("ChatCompletionStream error: %v\n", err)
		return
	}
	defer stream.Close()
	msg := sendMessage(s, m, "Responding...")
	var i int = 1

	var message string
	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			editMessage(s, msg, message)
			return
		}
		if err != nil {
			log.Printf("\nStream error: %v\n", err)
			return
		}

		message = message + response.Choices[0].Delta.Content
		if i%15 == 0 {
			editMessage(s, msg, message)
		}
		i++
	}
}

func sendInteractionResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	req := openai.ChatCompletionRequest{
		Model: openai.GPT40613,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleUser,
				Content: i.ApplicationCommandData().Options[0].Value.(string),
			},
		},
		Stream: true,
	}
	stream, err := c.CreateChatCompletionStream(ctx, req)
	if err != nil {
		log.Printf("ChatCompletionStream error: %v\n", err)
		return
	}
	defer stream.Close()
	msg := sendFollowup(s, i, "Responding...")
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
			editFollowup(s, i, msg.ID, message)
		}
		count++
	}
}
