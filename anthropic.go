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
	for i, content := range msg.Content {
		if i == len(msg.Content)-1 {
			sb.WriteString(content.GetText())
		} else {
			sb.WriteString(content.GetText() + "\n")
		}
	}
	return sb.String()
}

func cleanInstructionsANTMessages(messages []anthropic.Message) []anthropic.Message {
	for i, message := range messages {
		text := getANTMessageText(message)
		tempMessage := createANTMessage(message.Role, requestContent{text: text})
		instruction := instructionSwitchANT(tempMessage)
		if !strings.Contains(text, instruction.text) {
			continue
		}
		for j, content := range message.Content {
			if content.IsTextContent() {
				replacedText := strings.ReplaceAll(content.GetText(), instruction.text, "")
				messages[i].Content[j].Text = &replacedText
			}
		}
	}
	return messages
}

func instructionSwitchANT(m []anthropic.Message) requestContent {
	var text string

	firstMessageText := getANTMessageText(m[0])
	lastMessageText := getANTMessageText(m[len(m)-1])

	if firstMessageText == lastMessageText {
		text = lastMessageText
	} else {
		text = fmt.Sprintf("%s\n%s", firstMessageText, lastMessageText)
	}

	if strings.Contains(text, "❤️") || strings.Contains(text, "❤") {
		return instructionMessageDefault
	}

	if sysMsg := extractPairText(text, "⚙️"); sysMsg != "" {
		return requestContent{text: strings.TrimSpace(sysMsg)}
	} else if sysMsg := extractPairText(text, "⚙"); sysMsg != "" {
		return requestContent{text: strings.TrimSpace(sysMsg)}
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
		"The draw intent is for when the message asks to draw, generate, or change some kind of image" +
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

func streamMessageANTResponse(s *discordgo.Session, m *discordgo.Message, messages []anthropic.Message, refMsg *discordgo.Message) {
	token := os.Getenv("ANTHROPIC_TOKEN")
	if token == "" {
		log.Fatal("ANTHROPIC_TOKEN is not set")
	}
	c := anthropic.NewClient(token)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokensANT(messages, defaultOAIModel)
	if err != nil {
		logSendErrorMessage(s, m, err.Error())
		return
	}

	var i int
	var currentMessage, fullMessage string
	var msg *discordgo.Message

	if refMsg == nil {
		msg, err = sendMessageFile(s, m, "Responding...", nil)
		if err != nil {
			logSendErrorMessage(s, m, err.Error())
			return
		}
	} else {
		currentMessage = getANTMessageText(messages[len(messages)-1])
		msg = refMsg
	}

	replacementStrings := []string{"⚙️", "⚙"}
	instructionSwitchMessage := instructionSwitchANT(messages)
	instructionSwitchMessage.text = strings.TrimSpace(replaceMultiple(instructionSwitchMessage.text, replacementStrings, ""))

	_, err = c.CreateMessagesStream(ctx, anthropic.MessagesStreamRequest{
		MessagesRequest: anthropic.MessagesRequest{
			Model:     defaultANTModel,
			System:    fmt.Sprintf("%s\n\n%s", systemMessageDefault.text, instructionSwitchMessage.text),
			Messages:  cleanInstructionsANTMessages(messages),
			MaxTokens: maxTokens,
		},
		OnContentBlockDelta: func(data anthropic.MessagesEventContentBlockDeltaData) {
			currentMessage = currentMessage + data.Delta.Text
			fullMessage = fullMessage + data.Delta.Text
			i++
			if i%20 == 0 || i == 5 {
				// If the message is too long, split it into a new message
				if len(currentMessage) > 1800 {
					firstPart, lastPart := splitParagraph(currentMessage)
					if lastPart == "" {
						lastPart = "..."
					}

					_, err = editMessageFile(s, msg, firstPart, nil)
					if err != nil {
						logSendErrorMessage(s, m, err.Error())
						return
					}
					msg, err = sendMessageFile(s, msg, lastPart, nil)
					if err != nil {
						logSendErrorMessage(s, m, err.Error())
						return
					}
					currentMessage = lastPart
				} else {
					_, err = editMessageFile(s, msg, currentMessage, nil)
					if err != nil {
						logSendErrorMessage(s, m, err.Error())
						return
					}
				}
			}
		},
		OnMessageStop: func(_ anthropic.MessagesEventMessageStopData) {
			currentMessage = strings.TrimPrefix(currentMessage, "...")
			_, err = editMessageFile(s, msg, currentMessage, nil)
			if err != nil {
				logSendErrorMessage(s, m, err.Error())
				return
			}
			go func() {
				request := antMessagesToString(messages)
				intent := getOAIIntents(request)
				log.Printf("Intent: %s\n", intent)
				if intent == "draw" {
					if len(request) > 4000 {
						request = request[len(request)-4000:]
					}
					files, err := drawImage(request, openai.CreateImageSize1024x1024)
					if err != nil {
						logSendErrorMessage(s, m, err.Error())
					}
					_, err = editMessageFile(s, msg, currentMessage, files)
					if err != nil {
						logSendErrorMessage(s, m, err.Error())
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
			logSendErrorMessage(s, m, errmsg)
		} else {
			errmsg := fmt.Sprintf("\nStream error: %v\n", err)
			logSendErrorMessage(s, m, errmsg)
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
	// Get the referenced message
	referencedMessage := getReferencedMessage(s, message, cache)
	if referencedMessage == nil {
		return
	}

	// Clean and prepare the reply message content
	replyMessage := cleanMessage(s, referencedMessage)
	replyContent := requestContent{
		text: replyMessage.Content,
		url:  getMessageImages(replyMessage),
	}

	// Determine the role and format the reply content accordingly
	role := determineRole(s, replyMessage)
	if role == anthropic.RoleUser {
		replyContent.text = fmt.Sprintf("%s: %s", replyMessage.Author.Username, replyContent.text)
	}

	// Create and prepend the ANT message based on the role and content
	prependANTMessageByRole(role, replyContent, chatMessages)

	// Recursively process the referenced message if it's a reply
	if replyMessage.Type == discordgo.MessageTypeReply {
		prependRepliesANTMessages(s, referencedMessage, cache, chatMessages)
	}
}

func getReferencedMessage(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message) *discordgo.Message {
	if message.ReferencedMessage != nil {
		return message.ReferencedMessage
	}

	if message.MessageReference != nil {
		cachedMessage := checkCache(cache, message.MessageReference.MessageID)
		if cachedMessage != nil {
			return cachedMessage
		}

		referencedMessage, _ := s.ChannelMessage(message.MessageReference.ChannelID, message.MessageReference.MessageID)
		return referencedMessage
	}

	return nil
}

func determineRole(s *discordgo.Session, message *discordgo.Message) string {
	if message.Author.ID == s.State.User.ID {
		return anthropic.RoleAssistant
	}
	return anthropic.RoleUser
}

func prependANTMessageByRole(role string, content requestContent, chatMessages *[]anthropic.Message) {
	if len(*chatMessages) == 0 || (*chatMessages)[0].Role != role {
		prependANTMessage(role, content, chatMessages)
		return
	}

	if role == anthropic.RoleAssistant && len(content.url) > 0 {
		// Attach the image to the user message after the assistant message (the newer message)
		newMessage := createANTMessage(anthropic.RoleUser, requestContent{url: content.url})
		combineANTMessages(newMessage, chatMessages)
		// Add only the text to the assistant message
		prependANTMessage(anthropic.RoleAssistant, requestContent{text: content.text}, chatMessages)
	} else {
		newMessage := createANTMessage(role, content)
		combineANTMessages(newMessage, chatMessages)
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
