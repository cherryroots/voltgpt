package anthropic

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

	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/openai"
	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic/v2"
)

func getMessageText(msg anthropic.Message) string {
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

func cleanInstructionsMessages(messages []anthropic.Message) []anthropic.Message {
	for i, message := range messages {
		text := getMessageText(message)
		tempMessage := createMessage(message.Role, config.RequestContent{Text: text})
		instruction := instructionSwitch(tempMessage)
		if !strings.Contains(text, instruction.Text) {
			continue
		}
		for j, content := range message.Content {
			if content.Type == anthropic.MessagesContentTypeText {
				replacedText := strings.ReplaceAll(content.GetText(), instruction.Text, "")
				messages[i].Content[j].Text = &replacedText
			}
		}
	}
	return messages
}

func instructionSwitch(m []anthropic.Message) config.RequestContent {
	var text string

	firstMessageText := getMessageText(m[0])
	lastMessageText := getMessageText(m[len(m)-1])

	if firstMessageText == lastMessageText {
		text = lastMessageText
	} else {
		text = fmt.Sprintf("%s\n%s", firstMessageText, lastMessageText)
	}

	if strings.Contains(text, "❤️") || strings.Contains(text, "❤") {
		return config.InstructionMessageDefault
	}

	if sysMsg := utility.ExtractPairText(text, "⚙️"); sysMsg != "" {
		return config.RequestContent{Text: strings.TrimSpace(sysMsg)}
	} else if sysMsg := utility.ExtractPairText(text, "⚙"); sysMsg != "" {
		return config.RequestContent{Text: strings.TrimSpace(sysMsg)}
	}

	return config.InstructionMessageMean
}

func getIntents(message string, questionType string) string {
	token := os.Getenv("ANTHROPIC_TOKEN")
	if token == "" {
		log.Fatal("ANTHROPIC_TOKEN is not set")
	}
	c := anthropic.NewClient(token)
	ctx := context.Background()
	var messages []anthropic.Message

	intentPrompt := config.RequestContent{Text: "What's the intent in this message? Intents can be 'draw' or 'none'.\n " +
		"The 'draw' intent is for when the message asks to draw, generate, or change some kind of image. " +
		"The request has to be very specific, don't just say 'draw' if they mention the words draw, generate or change.\n " +
		"'none' intent is for when nothing image generation related is asked.\n " +
		"Don't include anything except the intent in the generated text under any cirmustances, and without quote marks: " + message}

	ratioPrompt := config.RequestContent{Text: "What's the ratio requested in this message? Rations can be '16:9', '1:1', '21:9', '2:3', '3:2', '4:5', '5:4', '9:16', '9:21'.\n " +
		"The '1:1' or 'none' aspect ratio is the default one, if the message doesn't ask for any other aspect ratio.\n " +
		"The '16:9', '21:9', '2:3', '3:2', '4:5', '5:4', '9:16', '9:21' ratios are for when the message asks for a specific aspect ratio.\n " +
		"If they ask for something like 'portrait' or 'landscape' or 'square' use the closest aspect ratio to that. \n " +
		"Don't include anything except the aspect ratio in the generated text under any cirmustances, and without quote marks: " + message}

	stylePrompt := config.RequestContent{Text: "What's the style requested in this message? Styles can be " +
		"'3d-model', 'analog-film', 'anime', 'cinematic', 'comic-book', 'digital-art', 'enhance', 'fantasy-art', 'isometric', 'line-art', 'low-poly', " +
		"'modeling-compound', 'neon-punk', 'origami', 'photographic', 'pixel-art', 'tile-texture'.\n " +
		"If the message doesn't ask for any other style, 'none' is the default one, that means nothing at all.\n " +
		"The other styles are for when the message asks for a specific style.\n " +
		"Don't include anything except the style in the generated text under any cirmustances, and without quote marks: " + message}

	switch questionType {
	case "intent":
		messages = createMessage(anthropic.RoleUser, intentPrompt)
	case "ratio":
		messages = createMessage(anthropic.RoleUser, ratioPrompt)
	case "style":
		messages = createMessage(anthropic.RoleUser, stylePrompt)
	default:
		return "none"
	}

	resp, err := c.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:     anthropic.ModelClaude3Haiku20240307,
		Messages:  messages,
		MaxTokens: 8,
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

func DrawSAIImage(prompt string, negativePrompt string, ratio string, style string) ([]*discordgo.File, error) {
	// OpenAI API key
	stabilityToken := os.Getenv("STABILITY_TOKEN")
	if stabilityToken == "" {
		log.Fatal("STABILITY_TOKEN is not set")
	}

	url := "https://api.stability.ai/v2beta/stable-image/generate/core"
	ratios := []string{"16:9", "1:1", "21:9", "2:3", "3:2", "4:5", "5:4", "9:16", "9:21"}
	styles := []string{"3d-model", "analog-film", "anime", "cinematic", "comic-book", "digital-art", "enhance", "fantasy-art", "isometric", "line-art", "low-poly", "modeling-compound", "neon-punk", "origami", "photographic", "pixel-art", "tile-texture"}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("prompt", prompt)
	_ = writer.WriteField("output_format", "png")
	if negativePrompt != "" && negativePrompt != "none" {
		_ = writer.WriteField("negative_prompt", negativePrompt)
	}
	if utility.MatchMultiple(ratio, ratios) {
		_ = writer.WriteField("aspect_ratio", ratio)
	}
	if utility.MatchMultiple(style, styles) {
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

func StreamMessageResponse(s *discordgo.Session, m *discordgo.Message, messages []anthropic.Message, refMsg *discordgo.Message) {
	token := os.Getenv("ANTHROPIC_TOKEN")
	if token == "" {
		log.Fatal("ANTHROPIC_TOKEN is not set")
	}
	c := anthropic.NewClient(token)
	ctx := context.Background()

	maxTokens, err := getRequestMaxTokens(messages, config.DefaultOAIModel)
	if err != nil {
		discord.LogSendErrorMessage(s, m, err.Error())
		return
	}

	var i int
	var currentMessage, fullMessage string
	var msg *discordgo.Message

	if refMsg == nil {
		msg, err = discord.SendMessageFile(s, m, "Responding...", nil)
		if err != nil {
			discord.LogSendErrorMessage(s, m, err.Error())
			return
		}
	} else {
		currentMessage = getMessageText(messages[len(messages)-1])
		msg = refMsg
	}

	replacementStrings := []string{"⚙️", "⚙"}
	instructionSwitchMessage := instructionSwitch(messages)
	instructionSwitchMessage.Text = strings.TrimSpace(utility.ReplaceMultiple(instructionSwitchMessage.Text, replacementStrings, ""))

	_, err = c.CreateMessagesStream(ctx, anthropic.MessagesStreamRequest{
		MessagesRequest: anthropic.MessagesRequest{
			Model:     config.DefaultANTModel,
			System:    fmt.Sprintf("%s\n\n%s", config.InstructionMessageDefault.Text, instructionSwitchMessage.Text),
			Messages:  cleanInstructionsMessages(messages),
			MaxTokens: maxTokens,
		},
		OnContentBlockDelta: func(data anthropic.MessagesEventContentBlockDeltaData) {
			currentMessage = currentMessage + *data.Delta.Text
			fullMessage = fullMessage + *data.Delta.Text
			i++
			if i%20 == 0 || i == 5 {
				// If the message is too long, split it into a new message
				if len(currentMessage) > 1800 {
					firstPart, lastPart := utility.SplitParagraph(currentMessage)
					if lastPart == "" {
						lastPart = "..."
					}

					_, err = discord.EditMessageFile(s, msg, firstPart, nil)
					if err != nil {
						discord.LogSendErrorMessage(s, m, err.Error())
						return
					}
					msg, err = discord.SendMessageFile(s, msg, lastPart, nil)
					if err != nil {
						discord.LogSendErrorMessage(s, m, err.Error())
						return
					}
					currentMessage = lastPart
				} else {
					_, err = discord.EditMessageFile(s, msg, currentMessage, nil)
					if err != nil {
						discord.LogSendErrorMessage(s, m, err.Error())
						return
					}
				}
			}
		},
		OnMessageStop: func(_ anthropic.MessagesEventMessageStopData) {
			currentMessage = strings.TrimPrefix(currentMessage, "...")
			_, err = discord.EditMessageFile(s, msg, currentMessage, nil)
			if err != nil {
				discord.LogSendErrorMessage(s, m, err.Error())
				return
			}
			go func() {
				request := getMessageText(messages[len(messages)-1])
				request = request[strings.Index(request, ":")+1:]
				request = strings.TrimSpace(request)
				intent := getIntents(request, "intent")
				log.Printf("Intent: %s\n", intent)
				if strings.ToLower(intent) == "draw" {
					ratio := getIntents(request, "ratio")
					style := getIntents(request, "style")
					log.Printf("Ratio: %s, Style: %s\n", ratio, style)
					if len(request) > 4000 {
						request = request[len(request)-4000:]
					}
					files, err := DrawSAIImage(fmt.Sprintf("%s\n%s", request, fullMessage), "", strings.ToLower(ratio), strings.ToLower(style))
					if err != nil {
						discord.LogSendErrorMessage(s, m, err.Error())
					}
					_, err = discord.EditMessageFile(s, msg, currentMessage, files)
					if err != nil {
						discord.LogSendErrorMessage(s, m, err.Error())
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
			discord.LogSendErrorMessage(s, m, errmsg)
		} else {
			errmsg := fmt.Sprintf("\nStream error: %v\n", err)
			discord.LogSendErrorMessage(s, m, errmsg)
		}
		return
	}
}

func AppendMessage(role string, content config.RequestContent, messages *[]anthropic.Message) {
	newMessages := append(*messages, createMessage(role, content)...)
	*messages = newMessages
}

func PrependMessage(role string, content config.RequestContent, messages *[]anthropic.Message) {
	newMessages := append(createMessage(role, content), *messages...)
	*messages = newMessages
}

func combineMessages(newMessage []anthropic.Message, messages *[]anthropic.Message) {
	// prepend newMessage.Content to the first message in messages
	(*messages)[0].Content = append(newMessage[0].Content, (*messages)[0].Content...)
}

func createMessage(role string, content config.RequestContent) []anthropic.Message {
	// Create a new message with the role and content
	message := []anthropic.Message{
		{
			Role:    role,
			Content: []anthropic.MessageContent{},
		},
	}

	for _, url := range content.Url {
		data, err := utility.DownloadURL(url)
		if err != nil {
			log.Printf("Error downloading image: %v", err)
			continue
		}
		message[0].Content = append(message[0].Content, anthropic.NewImageMessageContent(anthropic.MessageContentImageSource{
			Type:      "base64",
			MediaType: utility.MediaType(url),
			Data:      data,
		}))
	}

	if content.Text != "" {
		message[0].Content = append(message[0].Content, anthropic.NewTextMessageContent(content.Text))
	}

	return message
}

func PrependReplyMessages(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]anthropic.Message) {
	// Get the referenced message
	referencedMessage := getReferencedMessage(s, message, cache)
	if referencedMessage == nil {
		return
	}

	// Clean and prepare the reply message content
	replyMessage := utility.CleanMessage(s, referencedMessage)
	images, _ := utility.GetMessageMediaURL(replyMessage)
	replyContent := config.RequestContent{
		Text: fmt.Sprintf("%s %s", utility.AttachmentText(replyMessage), replyMessage.Content),
		Url:  images,
	}

	// Determine the role and format the reply content accordingly
	role := determineRole(s, replyMessage)
	if role == anthropic.RoleUser {
		replyContent.Text = fmt.Sprintf("%s: %s", replyMessage.Author.Username, replyContent.Text)
	}

	// Create and prepend the ANT message based on the role and content
	prependMessageByRole(role, replyContent, chatMessages)

	// Recursively process the referenced message if it's a reply
	if replyMessage.Type == discordgo.MessageTypeReply {
		PrependReplyMessages(s, referencedMessage, cache, chatMessages)
	}
}

func getReferencedMessage(s *discordgo.Session, message *discordgo.Message, cache []*discordgo.Message) *discordgo.Message {
	if message.ReferencedMessage != nil {
		return message.ReferencedMessage
	}

	if message.MessageReference != nil {
		cachedMessage := utility.CheckCache(cache, message.MessageReference.MessageID)
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

func prependMessageByRole(role string, content config.RequestContent, chatMessages *[]anthropic.Message) {
	if len(*chatMessages) == 0 || (*chatMessages)[0].Role != role {
		if role == anthropic.RoleAssistant && len(content.Url) > 0 {
			// Attach the image to the user message after the assistant message (the newer message)
			newMessage := createMessage(anthropic.RoleUser, config.RequestContent{Url: content.Url})
			combineMessages(newMessage, chatMessages)
			// Add only the text to the assistant message
			PrependMessage(anthropic.RoleAssistant, config.RequestContent{Text: content.Text}, chatMessages)
			return
		}

		PrependMessage(role, content, chatMessages)
		return
	}

	newMessage := createMessage(role, content)
	combineMessages(newMessage, chatMessages)
}

func antMessagesToString(messages []anthropic.Message) string {
	var sb strings.Builder
	for _, message := range messages {
		text := getMessageText(message)
		sb.WriteString(fmt.Sprintf("Role: %s: %s\n", message.Role, text))
	}
	return sb.String()
}

func getRequestMaxTokens(messages []anthropic.Message, model string) (maxTokens int, err error) {
	maxTokens = openai.GetMaxModelTokens(model)
	usedTokens := openai.NumTokensFromString(antMessagesToString(messages))

	availableTokens := maxTokens - usedTokens

	if availableTokens < 0 {
		availableTokens = 0
		err = fmt.Errorf("not enough tokens")
		return availableTokens, err
	}

	if openai.IsOutputLimited(model) {
		availableTokens = 4096
	}

	return availableTokens, nil
}
