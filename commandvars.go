package main

import (
	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var (
	admins       []string = []string{"102087943627243520", "123116664207179777", "95681688914366464"}
	defaultTemp  float32  = 0.7
	defaultModel          = openai.GPT4VisionPreview

	modelChoices = []*discordgo.ApplicationCommandOptionChoice{
		{
			Name:  "gpt-4",
			Value: openai.GPT4,
		},
		{
			Name:  "gpt-4-0125-preview",
			Value: openai.GPT4Turbo0125,
		},
		{
			Name:  "gpt-4-vision-preview",
			Value: openai.GPT4VisionPreview,
		},
		{
			Name:  "gpt-3.5-turbo-0125",
			Value: openai.GPT3Dot5Turbo0125,
		},
	}
)

type generationOptions struct {
	message     string
	imageUrl    string
	temperature float32
	model       string
}

func newGenerationOptions() *generationOptions {
	return &generationOptions{
		message:     "",
		imageUrl:    "",
		temperature: defaultTemp,
		model:       defaultModel,
	}
}
