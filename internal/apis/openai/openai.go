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
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"

	"voltgpt/internal/apis/bfl"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/transcription"
	"voltgpt/internal/utility"
)

func StreamMessageResponse(s *discordgo.Session, m *discordgo.Message, messages []openai.ChatCompletionMessage, refMsg *discordgo.Message) {
	token := os.Getenv("MISTRAL_TOKEN")
	if token == "" {
		log.Fatal("MISTRAL_TOKEN is not set")
	}
	cfg := openai.DefaultConfig(token)
	cfg.BaseURL = "https://api.mistral.ai/v1"
	c := openai.NewClientWithConfig(cfg)
	ctx := context.Background()

	var currentBuffer, fullBuffer string
	var bufferMutex sync.Mutex
	var msg *discordgo.Message
	var err error

	if refMsg == nil {
		msg, err = discord.SendMessageFile(s, m, "Generating response...", nil)
		if err != nil {
			discord.LogSendErrorMessage(s, m, err.Error())
			return
		}
	} else {
		currentBuffer = messageToString(messages[len(messages)-1])
		msg = refMsg
	}

	instructionMessage := instructionSwitch(messages)
	currentTime := fmt.Sprintf("Current date and time in CET right now: %s", time.Now().Format("2006-01-02 15:04:05"))
	replacementStrings := []string{"<message>", "</message>", "<reply>", "</reply>", "<username>", "</username>"}

	stream, err := c.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model: config.PixtralModel,
		Messages: append([]openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: fmt.Sprintf("System message: %s %s\n\nInstruction message: %s", config.SystemMessageDefault.Text, currentTime, instructionMessage.Text),
			},
		}, removeInstructonMessages(messages)...),
		Temperature: config.DefaultTemp,
		Stream:      true,
	})
	if err != nil {
		discord.LogSendErrorMessage(s, m, fmt.Sprintf("Stream error: %v", err))
		return
	}
	defer stream.Close()

	ticker := time.NewTicker(time.Second)
	done := make(chan bool)

	go func() {
		for {
			select {
			case <-ticker.C:
				bufferMutex.Lock()
				if strings.TrimSpace(currentBuffer) != "" {
					currentBuffer = utility.ReplaceMultiple(currentBuffer, replacementStrings, "")
					var err error
					currentBuffer, msg, err = utility.SplitSend(s, m, msg, currentBuffer)
					if err != nil {
						discord.LogSendErrorMessage(s, m, fmt.Sprintf("Stream error: %v", err))
						bufferMutex.Unlock()
						return
					}
				}
				bufferMutex.Unlock()
			case <-done:
				return
			}
		}
	}()

	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			discord.LogSendErrorMessage(s, m, fmt.Sprintf("Stream error: %v", err))
			ticker.Stop()
			close(done)
			return
		}

		if response.Choices[0].FinishReason != "" {
			bufferMutex.Lock()
			switch response.Choices[0].FinishReason {
			case openai.FinishReasonLength:
				currentBuffer += "**Length limit reached.**"
			case openai.FinishReasonContentFilter:
				currentBuffer += "**Content filter triggered.**"
			}
			bufferMutex.Unlock()
			break
		}

		bufferMutex.Lock()
		currentBuffer += response.Choices[0].Delta.Content
		fullBuffer += response.Choices[0].Delta.Content
		bufferMutex.Unlock()
	}

	ticker.Stop()
	close(done)

	// Final update
	bufferMutex.Lock()
	currentBuffer = utility.ReplaceMultiple(currentBuffer, replacementStrings, "")
	currentBuffer, msg, err = utility.SplitSend(s, m, msg, currentBuffer)
	bufferMutex.Unlock()
	if err != nil {
		discord.LogSendErrorMessage(s, m, fmt.Sprintf("Stream error: %v", err))
		return
	}

	if strings.HasPrefix(currentBuffer, "...") {
		currentBuffer = strings.TrimPrefix(currentBuffer, "...")
		_, err = discord.EditMessage(s, msg, currentBuffer)
		if err != nil {
			discord.LogSendErrorMessage(s, m, err.Error())
			return
		}
	}

	go func() {
		request := messageToString(messages[len(messages)-1])
		request = request[strings.Index(request, ":")+1:]
		request = strings.TrimSpace(request)
		prompt := utility.ExtractPairText(fullBuffer, "§")
		if prompt == "" {
			return
		}
		type intentResult struct {
			value string
			name  string
		}
		intentChan := make(chan intentResult, 2)
		intents := []string{"ratio", "raw"}
		for _, intent := range intents {
			go func() {
				intentChan <- intentResult{getIntents(request, intent), intent}
			}()
		}

		var ratio, raw string
		for i := 0; i < 2; i++ {
			result := <-intentChan
			switch result.name {
			case "ratio":
				ratio = result.value
			case "raw":
				raw = result.value
			}
		}
		files, err := bfl.DrawImage(prompt, ratio, raw)
		if err != nil {
			discord.LogSendErrorMessage(s, m, err.Error())
		}
		msg, err = discord.EditMessageFile(s, msg, currentBuffer, files)
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

	/*
		if name != "" {
			message[0].Name = utility.CleanName(name)
		}
	*/

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
		Text: strings.TrimSpace(fmt.Sprintf("%s%s%s%s",
			transcription.GetTranscript(s, reply),
			utility.AttachmentText(reply),
			utility.EmbedText(reply),
			fmt.Sprintf("<message>%s</message>", reply.Content),
		)),
		Images: images,
	}

	role := determineRole(s, reply)
	if role == openai.ChatMessageRoleUser {
		replyContent.Text = fmt.Sprintf("<username>%s</username>: %s", reply.Author.Username, replyContent.Text)
	}
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
		"The 'none' aspect ratio is the default one, if the message doesn't ask for any other aspect ratio.\n " +
		"The '16:9', '21:9', '2:3', '3:2', '4:5', '5:4', '9:16', '9:21' ratios are for when the message asks for a specific aspect ratio.\n " +
		"If they ask for something like 'portrait' or 'landscape' or 'square' use the closest aspect ratio to that. \n " +
		"They can also ask for a phone, tablet or desktop aspect ratio, do your best to closely match that. \n " +
		"Don't include anything except the aspect ratio in the generated text under any cirmustances, and without quote marks or <message></message>: " + message}

	heightPrompt := config.RequestContent{Text: "What's the height requested in this message? Heights can be between '256' and '1440'. Default is '768'.\n " +
		"If they ask for something like 'tall' or 'short' use the closest height to that. \n " +
		"Don't include anything except the height in the generated text under any cirmustances, and without quote marks or <message></message>: " + message}

	widthPrompt := config.RequestContent{Text: "What's the width requested in this message? Widths can be between '256' and '1440'. Default is '1024'.\n " +
		"If they ask for something like 'wide' or 'narrow' use the closest width to that. \n " +
		"Don't include anything except the width in the generated text under any cirmustances, and without quote marks or <message></message>: " + message}

	rawPrompt := config.RequestContent{Text: "What's the realism requested in this message? Realism can be 'true' or 'false'.\n " +
		"The 'true' realism is for when the message asks to draw, generate, or change some kind of realistic image. " +
		"The request has to be very specific, don't just say 'true' if they mention the word realistic.\n " +
		"'false' realism is for when no specific realism level is requested.\n " +
		"Don't include anything except the realism level in the generated text under any circumstances, and without quote marks or <message></message>: " + message}

	switch questionType {
	case "intent":
		messages = createMessage(openai.ChatMessageRoleUser, "", intentPrompt)
	case "ratio":
		messages = createMessage(openai.ChatMessageRoleUser, "", ratioPrompt)
	case "height":
		messages = createMessage(openai.ChatMessageRoleUser, "", heightPrompt)
	case "width":
		messages = createMessage(openai.ChatMessageRoleUser, "", widthPrompt)
	case "raw":
		messages = createMessage(openai.ChatMessageRoleUser, "", rawPrompt)
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
