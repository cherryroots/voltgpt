package main

import (
	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

var (
	modelChoices = []*discordgo.ApplicationCommandOptionChoice{
		{
			Name:  "gpt-4-0613",
			Value: openai.GPT40613,
		},
		{
			Name:  "gpt-4-0314",
			Value: openai.GPT40314,
		},
		{
			Name:  "gpt-4-32k-0613",
			Value: openai.GPT432K0613,
		},
		{
			Name:  "gpt-4-32k-0314",
			Value: openai.GPT432K0314,
		},
		{
			Name:  "gpt-3.5-turbo-16k-0613",
			Value: openai.GPT3Dot5Turbo16K0613,
		},
		{
			Name:  "gpt-3.5-turbo-0613",
			Value: openai.GPT3Dot5Turbo0613,
		},
		{
			Name:  "gpt-3.5-turbo-0301",
			Value: openai.GPT3Dot5Turbo0301,
		},
	}
)
