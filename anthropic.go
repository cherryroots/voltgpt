package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic"
	"github.com/sashabaranov/go-openai"
)

func getANTMessageText(msg anthropic.Message) string {
	var sb strings.Builder
	for _, content := range msg.Content {
		if content.Text != nil {
			sb.WriteString(*content.Text + "\n")
		}
	}
	return sb.String()
}

func instructionSwitchANT(m []anthropic.Message) requestContent {
	firstMessageText := getANTMessageText(m[0])
	lastMessageText := getANTMessageText(m[len(m)-1])
	text := lastMessageText
	if firstMessageText != lastMessageText { // if there are multiple messages
		text = fmt.Sprintf("%s\n%s", firstMessageText, lastMessageText)
	}
	if strings.Contains(text, "❤️") || strings.Contains(text, "❤") || strings.Contains(text, ":heart:") {
		return instructionMessageDefault
	}
	return instructionMessageMean
}

func getANTIntents(message string) string {
	token := os.Getenv("ANTHROPIC_TOKEN")
	if token == "" {
		log.Fatal("ANTHROPIC_TOKEN is not set")
	}
	c := anthropic.NewClient(token)
	ctx := context.Background()

	prompt := requestContent{text: "What's the intent in this message? Intents can be 'draw' or 'none'. " +
		"The draw intent is for when the message asks to draw or generate some kind of image. " +
		"none intent is for when nothing image generation related is asked. " +
		"Don't include anything except the intent in the generated text: " + message}

	resp, err := c.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:     anthropic.ModelClaude3Haiku20240307,
		Messages:  createANTMessage(anthropic.RoleUser, prompt),
		MaxTokens: 3,
	})
	if err != nil {
		var e *anthropic.APIError
		if errors.As(err, &e) {
			fmt.Printf("Messages error, type: %s, message: %s", e.Type, e.Message)
		} else {
			fmt.Printf("Messages error: %v\n", err)
		}
		return "none"
	}
	return resp.Content[0].Text
}

func streamMessageANTResponse(s *discordgo.Session, m *discordgo.MessageCreate, messages []anthropic.Message) {
	token := os.Getenv("ANTHROPIC_TOKEN")
	if token == "" {
		log.Fatal("ANTHROPIC_TOKEN is not set")
	}
	c := anthropic.NewClient(token)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokensANT(messages, defaultOAIModel)
	if err != nil {
		logSendErrorMessage(s, m.Message, err.Error())
		return
	}

	msg, err := sendMessageFile(s, m.Message, "Responding...", nil)
	if err != nil {
		logSendErrorMessage(s, m.Message, err.Error())
		return
	}

	var i int
	var message, fullMessage string

	_, err = c.CreateMessagesStream(ctx, anthropic.MessagesStreamRequest{
		MessagesRequest: anthropic.MessagesRequest{
			Model:     defaultANTModel,
			System:    fmt.Sprintf("%s\n\n%s", systemMessageDefault.text, instructionSwitchANT(messages).text),
			Messages:  messages,
			MaxTokens: maxTokens,
		},
		OnContentBlockDelta: func(data anthropic.MessagesEventContentBlockDeltaData) {
			message = message + data.Delta.Text
			fullMessage = fullMessage + data.Delta.Text
			i++
			if i%20 == 0 {
				// If the message is too long, split it into a new message
				if len(message) > 1800 {
					firstPart, lastPart := splitParagraph(message)
					if lastPart == "" {
						lastPart = "..."
					}

					_, err = editMessageFile(s, msg, firstPart, nil)
					if err != nil {
						logSendErrorMessage(s, m.Message, err.Error())
						return
					}
					msg, err = sendMessageFile(s, msg, lastPart, nil)
					if err != nil {
						logSendErrorMessage(s, m.Message, err.Error())
						return
					}
					message = lastPart
				} else {
					_, err = editMessageFile(s, msg, message, nil)
					if err != nil {
						logSendErrorMessage(s, m.Message, err.Error())
						return
					}
				}
			}
		},
		OnMessageStop: func(_ anthropic.MessagesEventMessageStopData) {
			message = strings.TrimPrefix(message, "...")
			_, err = editMessageFile(s, msg, message, nil)
			if err != nil {
				logSendErrorMessage(s, m.Message, err.Error())
				return
			}
			go func() {
				appendANTMessage(anthropic.RoleAssistant, requestContent{text: fullMessage}, &messages)
				request := antMessagesToString(messages)
				if len(request) > 4000 {
					request = request[len(request)-4000:]
				}
				intent := getOAIIntents(request)
				log.Printf("Intent: %s\n", intent)
				if intent == "draw" {
					files, err := drawImage(request, openai.CreateImageSize1024x1024)
					if err != nil {
						logSendErrorMessage(s, m.Message, err.Error())
					}
					_, err = editMessageFile(s, msg, message, files)
					if err != nil {
						logSendErrorMessage(s, m.Message, err.Error())
						return
					}
				}
			}()
			return
		},
	})
	if err != nil {
		var e *anthropic.APIError
		if errors.As(err, &e) {
			errmsg := fmt.Sprintf("\nMessages stream error, type: %s, message: %s", e.Type, e.Message)
			logSendErrorMessage(s, m.Message, errmsg)
		} else {
			errmsg := fmt.Sprintf("\nStream error: %v\n", err)
			logSendErrorMessage(s, m.Message, errmsg)
		}
		return
	}
}

func appendANTMessage(role string, content requestContent, messages *[]anthropic.Message) {
	newMessages := append(*messages, createANTMessage(role, content)...)
	*messages = newMessages
}

func prependANTMessage(role string, content requestContent, messages *[]anthropic.Message) {
	newMessages := append(createANTMessage(role, content), *messages...)
	*messages = newMessages
}

func combineANTMessages(newMessage []anthropic.Message, messages *[]anthropic.Message) {
	// prepend newMessage.Content to the first message in messages
	(*messages)[0].Content = append(newMessage[0].Content, (*messages)[0].Content...)
}

func createANTMessage(role string, content requestContent) []anthropic.Message {
	// Create a new message with the role and content
	message := []anthropic.Message{
		{
			Role:    role,
			Content: []anthropic.MessageContent{},
		},
	}

	for _, url := range content.url {
		data, err := downloadURL(url)
		if err != nil {
			log.Printf("Error downloading image: %v", err)
			continue
		}
		message[0].Content = append(message[0].Content, anthropic.NewImageMessageContent(anthropic.MessageContentImageSource{
			Type:      "base64",
			MediaType: mediaType(url),
			Data:      data,
		}))
	}

	if content.text != "" {
		message[0].Content = append(message[0].Content, anthropic.NewTextMessageContent(content.text))
	}

	return message
}

func prependRepliesANTMessages(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]anthropic.Message) {
	// check if the message has a reference, if not get it
	if message.ReferencedMessage == nil {
		if message.MessageReference != nil {
			cachedMessage := checkCache(cache, message.MessageReference.MessageID)
			if cachedMessage != nil {
				message.ReferencedMessage = cachedMessage
			} else {
				message.ReferencedMessage, _ = s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
			}
		} else {
			return
		}
	}
	replyMessage := cleanMessage(s, message.ReferencedMessage)
	replyContent := requestContent{
		text: replyMessage.Content,
		url:  getMessageImages(replyMessage),
	}
	if replyMessage.Author.ID == s.State.User.ID {
		if len(*chatMessages) == 0 || (*chatMessages)[0].Role == anthropic.RoleUser {
			prependANTMessage(anthropic.RoleAssistant, replyContent, chatMessages)
		} else {
			newMessage := createANTMessage(anthropic.RoleAssistant, replyContent)
			combineANTMessages(newMessage, chatMessages)
		}
	} else {
		replyContent.text = fmt.Sprintf("%s: %s", replyMessage.Author.Username, replyContent.text)
		if len(*chatMessages) == 0 || (*chatMessages)[0].Role == anthropic.RoleAssistant {
			prependANTMessage(anthropic.RoleUser, replyContent, chatMessages)
		} else {
			newMessage := createANTMessage(anthropic.RoleUser, replyContent)
			combineANTMessages(newMessage, chatMessages)
		}
	}

	if replyMessage.Type == discordgo.MessageTypeReply {
		prependRepliesANTMessages(s, message.ReferencedMessage, cache, chatMessages)
	}
}

func antMessagesToString(messages []anthropic.Message) string {
	var sb strings.Builder
	for _, message := range messages {
		text := getANTMessageText(message)
		sb.WriteString(fmt.Sprintf("Role: %s: %s\n", message.Role, text))
	}
	return sb.String()
}

func getRequestMaxTokensANT(messages []anthropic.Message, model string) (maxTokens int, err error) {
	maxTokens = getMaxModelTokens(model)
	usedTokens := numTokensFromString(antMessagesToString(messages))

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
