// Package anthropic is a package for interacting with the Anthropic API.
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
	"time"

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

func removeInstructonMessages(messages []anthropic.Message) []anthropic.Message {
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

	if strings.Contains(text, "💢") || strings.Contains(text, "�") {
		return config.InstructionMessageMean
	}

	if sysMsg := utility.ExtractPairText(text, "⚙️"); sysMsg != "" {
		return config.RequestContent{Text: strings.TrimSpace(sysMsg)}
	} else if sysMsg := utility.ExtractPairText(text, "⚙"); sysMsg != "" {
		return config.RequestContent{Text: strings.TrimSpace(sysMsg)}
	}

	return config.InstructionMessageDefault
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
		"Don't include anything except the intent in the generated text under any cirmustances, and without quote marks or <message></message>: " + message}

	ratioPrompt := config.RequestContent{Text: "What's the ratio requested in this message? Rations can be '16:9', '1:1', '21:9', '2:3', '3:2', '4:5', '5:4', '9:16', '9:21'.\n " +
		"The '1:1' or 'none' aspect ratio is the default one, if the message doesn't ask for any other aspect ratio.\n " +
		"The '16:9', '21:9', '2:3', '3:2', '4:5', '5:4', '9:16', '9:21' ratios are for when the message asks for a specific aspect ratio.\n " +
		"If they ask for something like 'portrait' or 'landscape' or 'square' use the closest aspect ratio to that. \n " +
		"Don't include anything except the aspect ratio in the generated text under any cirmustances, and without quote marks or <message></message>: " + message}

	switch questionType {
	case "intent":
		messages = createMessage(anthropic.RoleUser, intentPrompt)
	case "ratio":
		messages = createMessage(anthropic.RoleUser, ratioPrompt)
	default:
		return "none"
	}

	resp, err := c.CreateMessages(ctx, anthropic.MessagesRequest{
		Model:     anthropic.ModelClaude3Dot5Sonnet20241022,
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

func DrawSAIImage(prompt string, negativePrompt string, ratio string) ([]*discordgo.File, error) {
	stabilityToken := os.Getenv("STABILITY_TOKEN")
	if stabilityToken == "" {
		log.Fatal("STABILITY_TOKEN is not set")
	}

	url := "https://api.stability.ai/v2beta/stable-image/generate/ultra"
	ratios := []string{"16:9", "1:1", "21:9", "2:3", "3:2", "4:5", "5:4", "9:16", "9:21"}

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
	c := anthropic.NewClient(token, anthropic.WithBetaVersion(anthropic.BetaMaxTokens35Sonnet20240715))
	ctx := context.Background()

	var i int
	var currentMessage, fullMessage string
	var msg *discordgo.Message
	var err error

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
	instructionMessage := instructionSwitch(messages)
	currentTime := fmt.Sprintf("Current date and time in CET right now: %s", time.Now().Format("2006-01-02 15:04:05"))

	_, err = c.CreateMessagesStream(ctx, anthropic.MessagesStreamRequest{
		MessagesRequest: anthropic.MessagesRequest{
			Model: anthropic.ModelClaude3Dot5Sonnet20240620,
			System: fmt.Sprintf("System message: %s %s\n\nInstruction message: %s",
				config.SystemMessageDefault.Text, currentTime, instructionMessage.Text),
			Messages:  removeInstructonMessages(messages),
			MaxTokens: 4096,
		},
		OnContentBlockDelta: func(data anthropic.MessagesEventContentBlockDeltaData) {
			replacementStrings := []string{"<message>", "</message>", "<reply>", "</reply>"}
			currentMessage = currentMessage + *data.Delta.Text
			fullMessage = fullMessage + *data.Delta.Text
			i++
			if i%25 == 0 || i == 5 {
				currentMessage = utility.ReplaceMultiple(currentMessage, replacementStrings, "")
				currentMessage, msg, err = splitSend(s, m, msg, currentMessage)
				if err != nil {
					discord.LogSendErrorMessage(s, m, err.Error())
					return
				}
			}
		},
		OnMessageStop: func(_ anthropic.MessagesEventMessageStopData) {
			replacementStrings := []string{"<message>", "</message>", "<reply>", "</reply>"}
			currentMessage = utility.ReplaceMultiple(currentMessage, replacementStrings, "")
			currentMessage, msg, err = splitSend(s, m, msg, currentMessage)
			if err != nil {
				discord.LogSendErrorMessage(s, m, err.Error())
				return
			}
			if strings.HasPrefix(currentMessage, "...") {
				currentMessage = strings.TrimPrefix(currentMessage, "...")
				_, err = discord.EditMessage(s, msg, currentMessage)
				if err != nil {
					discord.LogSendErrorMessage(s, m, err.Error())
					return
				}
			}
			go func() {
				request := getMessageText(messages[len(messages)-1])
				request = request[strings.Index(request, ":")+1:]
				request = strings.TrimSpace(request)
				prompt := utility.ExtractPairText(fullMessage, "§")
				if prompt == "" {
					return
				}
				ratio := getIntents(request, "ratio")
				files, err := DrawSAIImage(prompt, "", strings.ToLower(ratio))
				if err != nil {
					discord.LogSendErrorMessage(s, m, err.Error())
				}
				msg, err = discord.EditMessageFile(s, msg, currentMessage, files)
				if err != nil {
					discord.LogSendErrorMessage(s, m, err.Error())
					return
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

func splitSend(s *discordgo.Session, m *discordgo.Message, msg *discordgo.Message, currentMessage string) (string, *discordgo.Message, error) {
	if len(currentMessage) > 1750 {
		firstPart, lastPart := utility.SplitParagraph(currentMessage)
		if lastPart == "" {
			lastPart = "..."
		}
		_, err := discord.EditMessage(s, msg, firstPart)
		if err != nil {
			return "", msg, err
		}
		msg, err = discord.SendMessageFile(s, msg, lastPart, nil)
		if err != nil {
			return "", msg, err
		}
		currentMessage = lastPart
	} else {
		_, err := discord.EditMessage(s, msg, currentMessage)
		if err != nil {
			discord.LogSendErrorMessage(s, m, err.Error())
			return "", msg, err
		}
	}
	return currentMessage, msg, nil
}

func AppendMessage(role anthropic.ChatRole, content config.RequestContent, messages *[]anthropic.Message) {
	*messages = append(*messages, createMessage(role, content)...)
}

func PrependMessage(role anthropic.ChatRole, content config.RequestContent, messages *[]anthropic.Message) {
	*messages = append(createMessage(role, content), *messages...)
}

func combineMessages(newMessage []anthropic.Message, messages *[]anthropic.Message) {
	(*messages)[0].Content = append(newMessage[0].Content, (*messages)[0].Content...)
}

func createMessage(role anthropic.ChatRole, content config.RequestContent) []anthropic.Message {
	message := []anthropic.Message{
		{
			Role:    role,
			Content: []anthropic.MessageContent{},
		},
	}

	for _, url := range content.URL {
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

func PrependReplyMessages(s *discordgo.Session, originMember *discordgo.Member, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]anthropic.Message) {
	reference := getReferencedMessage(s, message, cache)
	if reference == nil {
		return
	}

	reply := utility.CleanMessage(s, reference)
	images, _ := utility.GetMessageMediaURL(reply)
	replyContent := config.RequestContent{
		Text: fmt.Sprintf("%s %s %s %s",
			openai.GetTranscript(s, reply),
			utility.AttachmentText(reply),
			utility.EmbedText(reply),
			fmt.Sprintf("<message>%s</message>", reply.Content),
		),
		URL: images,
	}

	role := determineRole(s, reply)
	if role == anthropic.RoleUser {
		replyContent.Text = fmt.Sprintf("<username>%s</username>: %s", reply.Author.Username, replyContent.Text)
	}

	prependMessageByRole(role, replyContent, chatMessages)
	if reply.Type == discordgo.MessageTypeReply {
		PrependReplyMessages(s, originMember, reference, cache, chatMessages)
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

func determineRole(s *discordgo.Session, message *discordgo.Message) anthropic.ChatRole {
	if message.Author.ID == s.State.User.ID {
		return anthropic.RoleAssistant
	}
	return anthropic.RoleUser
}

func prependMessageByRole(role anthropic.ChatRole, content config.RequestContent, chatMessages *[]anthropic.Message) {
	if len(*chatMessages) == 0 || (*chatMessages)[0].Role != role {
		if role == anthropic.RoleAssistant && len(content.URL) > 0 {
			newMessage := createMessage(anthropic.RoleUser, config.RequestContent{URL: content.URL})
			combineMessages(newMessage, chatMessages)
			PrependMessage(anthropic.RoleAssistant, config.RequestContent{Text: content.Text}, chatMessages)
			return
		}

		PrependMessage(role, content, chatMessages)
		return
	}

	newMessage := createMessage(role, content)
	combineMessages(newMessage, chatMessages)
}

func PrependUserMessagePlaceholder(messages *[]anthropic.Message) {
	if len(*messages) > 0 && (*messages)[0].Role == anthropic.RoleAssistant {
		PrependMessage(anthropic.RoleUser, config.RequestContent{Text: "PLACEHOLDER MESSAGE - IGNORE"}, messages)
	}
}
