package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/pkoukk/tiktoken-go"
	openai "github.com/sashabaranov/go-openai"
)

func sendFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
		Content: content,
	})

	return msg, err
}

func editFollowup(s *discordgo.Session, i *discordgo.InteractionCreate, followupID string, content string) (*discordgo.Message, error) {
	msg, err := s.FollowupMessageEdit(i.Interaction, followupID, &discordgo.WebhookEdit{
		Content: &content,
	})

	return msg, err
}

func deferResponse(s *discordgo.Session, i *discordgo.InteractionCreate) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})
	if err != nil {
		log.Println(err)
	}
}

func sendMessage(s *discordgo.Session, m *discordgo.Message, content string) (*discordgo.Message, error) {
	msg, err := s.ChannelMessageSendComplex(m.ChannelID, &discordgo.MessageSend{
		Content:   content,
		Reference: m.Reference(),
	})

	return msg, err
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

func checkForReplies(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]openai.ChatCompletionMessage) {
	if message.Type == discordgo.MessageTypeReply {
		if message.ReferencedMessage == nil {
			if message.MessageReference != nil {
				cachedMessage := checkCache(cache, message.MessageReference.MessageID)
				if cachedMessage != nil {
					message.ReferencedMessage = cachedMessage
				} else {
					message.ReferencedMessage, _ = s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
				}
			}
		}
		replyMessage := cleanMessage(s, message.ReferencedMessage)
		if replyMessage.Author.ID == s.State.User.ID {
			prependMessage(openai.ChatMessageRoleAssistant, replyMessage.Content, chatMessages)
		} else {
			prependMessage(openai.ChatMessageRoleUser, replyMessage.Content, chatMessages)
		}
		checkForReplies(s, message.ReferencedMessage, cache, chatMessages)
	}
}

func getMessageCache(s *discordgo.Session, channelID string, messageID string) []*discordgo.Message {
	messages, _ := s.ChannelMessages(channelID, 100, "", "", messageID)
	return messages
}

func checkCache(cache []*discordgo.Message, messageID string) *discordgo.Message {
	for _, message := range cache {
		if message.ID == messageID {
			return message
		}
	}
	return nil
}

func cleanMessage(s *discordgo.Session, message *discordgo.Message) *discordgo.Message {
	botid := fmt.Sprintf("<@%s>", s.State.User.ID)
	mentionRegex := regexp.MustCompile(botid)
	message.Content = mentionRegex.ReplaceAllString(message.Content, "")
	message.Content = strings.TrimSpace(message.Content)
	return message
}

func getMaxModelTokens(model string) (maxTokens int) {
	switch model {
	case openai.GPT4, openai.GPT40613, openai.GPT40314:
		maxTokens = 8192
	case openai.GPT432K, openai.GPT432K0613, openai.GPT432K0314:
		maxTokens = 32768
	case openai.GPT3Dot5Turbo16K, openai.GPT3Dot5Turbo16K0613:
		maxTokens = 16385
	case openai.GPT3Dot5Turbo, openai.GPT3Dot5Turbo0613, openai.GPT3Dot5Turbo0301, openai.GPT3Dot5TurboInstruct:
		maxTokens = 4097
	case openai.GPT3Davinci002:
		maxTokens = 16384
	}

	return maxTokens
}

func getRequestMaxTokensString(message string, model string) (maxTokens int) {
	maxTokens = getMaxModelTokens(model)
	usedTokens := numTokensFromString(message)

	availableTokens := maxTokens - usedTokens

	return availableTokens
}

func getRequestMaxTokens(message []openai.ChatCompletionMessage, model string) (maxTokens int) {
	maxTokens = getMaxModelTokens(model)
	usedTokens := numTokensFromMessages(message, model)

	availableTokens := maxTokens - usedTokens

	return availableTokens
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
