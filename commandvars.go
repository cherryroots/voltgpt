package main

import (
	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var (
	defaultTemp  float32 = 0.7
	defaultModel         = openai.GPT4TurboPreview

	modelChoices = []*discordgo.ApplicationCommandOptionChoice{
		{
			Name:  "gpt-4",
			Value: openai.GPT4,
		},
		{
			Name:  "gpt-4-1106-preview",
			Value: openai.GPT4TurboPreview,
		},
		{
			Name:  "gpt-4-vision-preview",
			Value: openai.GPT4VisionPreview,
		},
		{
			Name:  "gpt-3.5-turbo-1106",
			Value: openai.GPT3Dot5Turbo1106,
		},
	}
)

type responseOptions struct {
	message     string
	imageUrl    string
	temperature float32
	model       string
}

func newResponseOptions() *responseOptions {
	return &responseOptions{
		message:     "",
		imageUrl:    "",
		temperature: defaultTemp,
		model:       defaultModel,
	}
}
