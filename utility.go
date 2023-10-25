package main

import (
	"fmt"
	"log"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/pkoukk/tiktoken-go"
	openai "github.com/sashabaranov/go-openai"
)

func sendFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) *discordgo.Message {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})
	if err != nil {
		log.Println(err)
	}

	return msg
}

func editFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string) *discordgo.Message {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
	})
	if err != nil {
		log.Println(err)
	}

	return msg
}

func deferResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Println(err)
	}
}

func sendMessage(s *discordgo.Session, m *discordgo.MessageCreate, content string) *discordgo.Message {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})
	if err != nil {
		log.Println(err)
	}

	return msg
}

// command to edit a given message the bot has sent
func editMessage(s *discordgo.Session, m *discordgo.Message, content string) *discordgo.Message {
	msg, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Content: &content,
		ID:      m.ID,
		Channel: m.ChannelID,
	})
	if err != nil {
		log.Println(err)
	}

	return msg
}

func NumTokensFromString(s string) (numTokens int) {
	message := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleUser,
			Content: s,
		},
	}
	tokens := NumTokensFromMessages(message, openai.GPT40613)

	return tokens
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
	case "gpt-3.5-turbo-0613",
		"gpt-3.5-turbo-16k-0613",
		"gpt-4-0314",
		"gpt-4-32k-0314",
		"gpt-4-0613",
		"gpt-4-32k-0613":
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
