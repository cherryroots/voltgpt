// Package openai is a package for interacting with the OpenAI API.
package openai

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"

	"voltgpt/internal/apis/simplicity"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/transcription"
	"voltgpt/internal/utility"
)

func StreamMessageResponse(s *discordgo.Session, m *discordgo.Message, messages []openai.ChatCompletionMessage, refMsg *discordgo.Message) {
	token := os.Getenv("OPENAI_TOKEN")
	if token == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	c := openai.NewClient(token)
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
		currentMessage = messageToString(messages[len(messages)-1])
		msg = refMsg
	}

	instructionMessage := instructionSwitch(messages)
	currentTime := fmt.Sprintf("Current date and time in CET right now: %s", time.Now().Format("2006-01-02 15:04:05"))
	replacementStrings := []string{"<message>", "</message>", "<reply>", "</reply>"}

	stream, err := c.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oLatest,
		Messages: append([]openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("System message: %s %s\n\nInstruction message: %s", config.SystemMessageDefault.Text, currentTime, instructionMessage.Text),
			},
		}, removeInstructonMessages(messages)...),
		MaxTokens: 4096,
		Stream:    true,
	})
	if err != nil {
		discord.LogSendErrorMessage(s, m, fmt.Sprintf("Stream error: %v", err))
		return
	}
	defer stream.Close()

	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			discord.LogSendErrorMessage(s, m, fmt.Sprintf("Stream error: %v", err))
			return
		}

		currentMessage += response.Choices[0].Delta.Content
		fullMessage += response.Choices[0].Delta.Content
		i++
		if i%50 == 0 || i == 5 {
			currentMessage = utility.ReplaceMultiple(currentMessage, replacementStrings, "")
			currentMessage, msg, err = utility.SplitSend(s, m, msg, currentMessage)
			if err != nil {
				discord.LogSendErrorMessage(s, m, err.Error())
				return
			}
		}
	}

	currentMessage = utility.ReplaceMultiple(currentMessage, replacementStrings, "")
	currentMessage, msg, err = utility.SplitSend(s, m, msg, currentMessage)
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
		request := messageToString(messages[len(messages)-1])
		request = request[strings.Index(request, ":")+1:]
		request = strings.TrimSpace(request)
		prompt := utility.ExtractPairText(fullMessage, "§")
		if prompt == "" {
			return
		}
		ratio := getIntents(request, "ratio")
		files, err := simplicity.DrawImage(prompt, "", strings.ToLower(ratio))
		if err != nil {
			discord.LogSendErrorMessage(s, m, err.Error())
		}
		msg, err = discord.EditMessageFile(s, msg, currentMessage, files)
		if err != nil {
			discord.LogSendErrorMessage(s, m, err.Error())
			return
		}
	}()
}

func AppendMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

func PrependMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(createMessage(role, name, content), *messages...)
	*messages = newMessages
}

func createMessage(role string, name string, content config.RequestContent) []openai.ChatCompletionMessage {
	message := []openai.ChatCompletionMessage{
		{
			Role:         role,
			MultiContent: []openai.ChatMessagePart{},
		},
	}

	if name != "" {
		message[0].Name = utility.CleanName(name)
	}

	if content.Text != "" {
		message[0].MultiContent = append(message[0].MultiContent, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeText,
			Text: content.Text,
		})
	}

	for _, u := range content.Images {
		message[0].MultiContent = append(message[0].MultiContent, openai.ChatMessagePart{
			Type: openai.ChatMessagePartTypeImageURL,
			ImageURL: &openai.ChatMessageImageURL{
				URL:    u,
				Detail: openai.ImageURLDetailAuto,
			},
		})
	}

	return message
}

func CreateBatchMessages(s *discordgo.Session, messages []*discordgo.Message) []openai.ChatCompletionMessage {
	var batchMessages []openai.ChatCompletionMessage

	for _, message := range messages {
		images, _, _ := utility.GetMessageMediaURL(message)
		content := config.RequestContent{
			Text:   message.Content,
			Images: images,
		}
		if message.Author.ID == s.State.User.ID {
			PrependMessage(openai.ChatMessageRoleAssistant, message.Author.Username, content, &batchMessages)
		}
		PrependMessage(openai.ChatMessageRoleUser, message.Author.Username, content, &batchMessages)
	}

	return batchMessages
}

func PrependReplyMessages(s *discordgo.Session, originMember *discordgo.Member, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]openai.ChatCompletionMessage) {
	reference := utility.GetReferencedMessage(s, message, cache)
	if reference == nil {
		return
	}

	reply := utility.CleanMessage(s, reference)
	images, _, _ := utility.GetMessageMediaURL(reply)
	replyContent := config.RequestContent{
		Text: strings.TrimSpace(fmt.Sprintf("%s %s %s %s",
			transcription.GetTranscript(s, reply),
			utility.AttachmentText(reply),
			utility.EmbedText(reply),
			fmt.Sprintf("<message>%s</message>", reply.Content),
		)),
		Images: images,
	}

	role := determineRole(s, reply)
	PrependMessage(role, reply.Author.Username, replyContent, chatMessages)

	if reply.Type == discordgo.MessageTypeReply {
		PrependReplyMessages(s, originMember, reference, cache, chatMessages)
	}
}

func determineRole(s *discordgo.Session, message *discordgo.Message) string {
	if message.Author.ID == s.State.User.ID {
		return openai.ChatMessageRoleAssistant
	}
	return openai.ChatMessageRoleUser
}

func messagesToString(messages []openai.ChatCompletionMessage) string {
	var sb strings.Builder
	for _, message := range messages {
		sb.WriteString(messageToString(message) + "\n")
	}
	return sb.String()
}

func messageToString(message openai.ChatCompletionMessage) string {
	var sb strings.Builder
	if len(message.MultiContent) == 1 {
		if message.MultiContent[0].Type == openai.ChatMessagePartTypeText {
			sb.WriteString(message.MultiContent[0].Text)
		}
	} else {
		for _, content := range message.MultiContent {
			if content.Type == openai.ChatMessagePartTypeText {
				sb.WriteString(content.Text + "\n")
			}
		}
	}
	return sb.String()
}

func removeInstructonMessages(messages []openai.ChatCompletionMessage) []openai.ChatCompletionMessage {
	for i, message := range messages {
		text := messageToString(message)
		tempMessage := createMessage(message.Role, "", config.RequestContent{Text: text})
		instruction := instructionSwitch(tempMessage)
		if instruction.Text == "" {
			continue
		}
		if !strings.Contains(text, instruction.Text) {
			continue
		}
		for j, content := range message.MultiContent {
			if content.Type == openai.ChatMessagePartTypeText {
				replacedText := strings.ReplaceAll(content.Text, instruction.Text, "")
				messages[i].MultiContent[j].Text = replacedText
			}
		}
	}
	return messages
}

func instructionSwitch(m []openai.ChatCompletionMessage) config.RequestContent {
	var text string

	firstMessageText := messageToString(m[0])
	lastMessageText := messageToString(m[len(m)-1])

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
	token := os.Getenv("OPENAI_TOKEN")
	if token == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	c := openai.NewClient(token)
	ctx := context.Background()
	var messages []openai.ChatCompletionMessage

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
		messages = createMessage(openai.ChatMessageRoleUser, "", intentPrompt)
	case "ratio":
		messages = createMessage(openai.ChatMessageRoleUser, "", ratioPrompt)
	default:
		return "none"
	}

	resp, err := c.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model:     openai.GPT4oMini,
		Messages:  messages,
		MaxTokens: 8,
	})
	if err != nil {
		var e *openai.APIError
		if errors.As(err, &e) {
			fmt.Printf("ChatCompletion error, type: %s, message: %s", e.Type, e.Message)
		} else {
			fmt.Printf("ChatCompletion error: %v\n", err)
		}
		return "none"
	}
	return resp.Choices[0].Message.Content
}
