package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic/v2"
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
			if content.Type == anthropic.MessagesContentTypeText {
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

func getANTIntents(message string, questionType string) string {
	token := os.Getenv("ANTHROPIC_TOKEN")
	if token == "" {
		log.Fatal("ANTHROPIC_TOKEN is not set")
	}
	c := anthropic.NewClient(token)
	ctx := context.Background()
	var messages []anthropic.Message

	intentPrompt := requestContent{text: "What's the intent in this message? Intents can be 'draw' or 'none'.\n " +
		"The 'draw' intent is for when the message asks to draw, generate, or change some kind of image.\n" +
		"'none' intent is for when nothing image generation related is asked.\n " +
		"Don't include anything except the intent in the generated text and without quote marks: " + message}

	ratioPrompt := requestContent{text: "What's the intent in this message? Intents can be '16:9', '1:1', '21:9', '2:3', '3:2', '4:5', '5:4', '9:16', '9:21'.\n " +
		"The '1:1' or 'none' aspect ratio is the default one, if the message doesn't ask for any other aspect ratio.\n " +
		"The '16:9', '21:9', '2:3', '3:2', '4:5', '5:4', '9:16', '9:21' ratios are for when the message asks for a specific aspect ratio.\n " +
		"Don't include anything except the aspect ratio in the generated text and without quote marks: " + message}

	stylePrompt := requestContent{text: "What's the intent in this message? Intents can be " +
		"'3d-model', 'analog-film', 'anime', 'cinematic', 'comic-book', 'digital-art', 'enhance', 'fantasy-art', 'isometric', 'line-art', 'low-poly', " +
		"'modeling-compound', 'neon-punk', 'origami', 'photographic', 'pixel-art', 'tile-texture'.\n " +
		"If the message doesn't ask for any other style, 'none' is the default one, that means nothing at all.\n " +
		"The other styles are for when the message asks for a specific style.\n " +
		"Don't include anything except the style in the generated text and without quote marks: " + message}

	switch questionType {
	case "intent":
		messages = createANTMessage(anthropic.RoleUser, intentPrompt)
	case "ratio":
		messages = createANTMessage(anthropic.RoleUser, ratioPrompt)
	case "style":
		messages = createANTMessage(anthropic.RoleUser, stylePrompt)
	default:
		return "none"
	}

	resp, err := c.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:     anthropic.ModelClaude3Haiku20240307,
		Messages:  messages,
		MaxTokens: 10,
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
	return *resp.Content[0].Text
}

func drawSAIImage(prompt string, negativePrompt string, ratio string, style string) ([]*discordgo.File, error) {
	// OpenAI API key
	stabilityToken := os.Getenv("STABILITY_TOKEN")
	if stabilityToken == "" {
		log.Fatal("STABILITY_TOKEN is not set")
	}

	url := "https://api.stability.ai/v2beta/stable-image/generate/core"

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("prompt", prompt)
	_ = writer.WriteField("output_format", "png")
	if negativePrompt != "" && negativePrompt != "none" {
		_ = writer.WriteField("negative_prompt", negativePrompt)
	}
	if ratio != "" && ratio != "none" {
		_ = writer.WriteField("aspect_ratio", ratio)
	}
	if style != "" && style != "none" {
		_ = writer.WriteField("style_preset", style)
	}
	_ = writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())
	req.Header.Add("Accept", "image/*")
	req.Header.Add("Authorization", stabilityToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// erro is application/json
		errInterface := make(map[string]interface{})
		err = json.NewDecoder(resp.Body).Decode(&errInterface)
		return nil, fmt.Errorf("unexpected status code: %d\n%s", resp.StatusCode, errInterface)
	}

	imageBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	files := []*discordgo.File{
		{
			Name:   "image.png",
			Reader: bytes.NewReader(imageBytes),
		},
	}

	return files, nil
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
			currentMessage = currentMessage + *data.Delta.Text
			fullMessage = fullMessage + *data.Delta.Text
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
				request := getANTMessageText(messages[len(messages)-1])
				request = request[strings.Index(request, ":")+1:]
				request = strings.TrimSpace(request)
				intent := getANTIntents(request, "intent")
				log.Printf("Intent: %s\n", intent)
				if intent == "draw" {
					ratio := getANTIntents(request, "ratio")
					style := getANTIntents(request, "style")
					log.Printf("Ratio: %s, Style: %s\n", ratio, style)
					if len(request) > 4000 {
						request = request[len(request)-4000:]
					}
					files, err := drawSAIImage(fmt.Sprintf("%s\n%s", request, fullMessage), "", ratio, style)
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
	images, _ := getMessageMediaURL(replyMessage)
	replyContent := requestContent{
		text: replyMessage.Content,
		url:  images,
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
		if role == anthropic.RoleAssistant && len(content.url) > 0 {
			// Attach the image to the user message after the assistant message (the newer message)
			newMessage := createANTMessage(anthropic.RoleUser, requestContent{url: content.url})
			combineANTMessages(newMessage, chatMessages)
			// Add only the text to the assistant message
			prependANTMessage(anthropic.RoleAssistant, requestContent{text: content.text}, chatMessages)
			return
		}

		prependANTMessage(role, content, chatMessages)
		return
	}

	newMessage := createANTMessage(role, content)
	combineANTMessages(newMessage, chatMessages)
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
