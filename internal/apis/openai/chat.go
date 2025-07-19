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

	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/transcription"
	"voltgpt/internal/utility"
)

func StreamMessageResponse(s *discordgo.Session, m *discordgo.Message, messages []openai.ChatCompletionMessage, refMsg *discordgo.Message) error {
	token := os.Getenv("OPENROUTER_TOKEN")
	if token == "" {
		log.Fatal("OPENROUTER_TOKEN is not set")
	}
	cfg := openai.DefaultConfig(token)
	cfg.BaseURL = config.OpenRouterBaseURL
	c := openai.NewClientWithConfig(cfg)
	ctx := context.Background()

	var currentBuffer, fullBuffer string
	var bufferMutex sync.Mutex
	var msg *discordgo.Message
	var err error

	if refMsg == nil {
		msg, err = discord.SendMessageFile(s, m, "Thinking...", nil)
		if err != nil {
			return fmt.Errorf("failed to send message: %v", err)
		}
	} else {
		currentBuffer = messageToString(messages[len(messages)-1])
		msg = refMsg
	}

	instructionMessage := instructionSwitch(messages)
	currentTime := fmt.Sprintf("Current date and time in CET right now: %s", time.Now().Format("2006-01-02 15:04:05"))
	replacementStrings := []string{"<message>", "</message>", "<reply>", "</reply>", "<username>", "</username>", "<attachments>", "</attachments>"}
	newMessages := append([]openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: fmt.Sprintf("System message: %s %s\n\nInstruction message: %s", config.SystemMessageMinimal, currentTime, instructionMessage),
		},
	}, removeInstructonMessages(messages)...)

	stream, err := c.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
		Model:               "moonshotai/kimi-k2",
		Messages:            newMessages,
		MaxCompletionTokens: 16384,
		// ReasoningEffort:     "high",
		Stream:      true,
		Temperature: 0.6,
	})
	if err != nil {
		return fmt.Errorf("stream error on start: %v", err)
	}
	defer stream.Close()

	go func() {
		for range time.Tick(time.Second) {
			bufferMutex.Lock()
			if strings.TrimSpace(currentBuffer) != "" {
				currentBuffer = utility.ReplaceMultiple(currentBuffer, replacementStrings, "")
				var err error
				currentBuffer, msg, err = utility.SplitSend(s, m, msg, currentBuffer)
				if err != nil {
					bufferMutex.Unlock()
					return
				}
			}
			bufferMutex.Unlock()
		}
	}()

	for {
		response, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("stream error during response: %v", err)
		}

		bufferMutex.Lock()
		currentBuffer += response.Choices[0].Delta.Content
		fullBuffer += response.Choices[0].Delta.Content
		bufferMutex.Unlock()

		if response.Choices[0].FinishReason != "" {
			bufferMutex.Lock()
			switch response.Choices[0].FinishReason {
			case openai.FinishReasonLength:
				currentBuffer += "**Length limit reached.**"
			case openai.FinishReasonContentFilter:
				currentBuffer += "**Content filter triggered.**"
			}
			bufferMutex.Unlock()
		}
	}

	// Final update
	bufferMutex.Lock()
	currentBuffer = utility.ReplaceMultiple(currentBuffer, replacementStrings, "")
	currentBuffer, msg, err = utility.SplitSend(s, m, msg, currentBuffer)
	bufferMutex.Unlock()
	if err != nil {
		return fmt.Errorf("stream error on final update: %v", err)
	}

	if after, ok := strings.CutPrefix(currentBuffer, "..."); ok {
		currentBuffer = after
		_, err = discord.EditMessage(s, msg, currentBuffer)
		if err != nil {
			return fmt.Errorf("stream error on final update: %v", err)
		}
	}

	return nil
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
				// URL: u,
				URL: fmt.Sprintf("data:%s;base64,%s", utility.MediaType(u), utility.Base64Image(u)),
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
	if message.Content != "" {
		sb.WriteString(message.Content)
	}
	if len(message.MultiContent) == 1 {
		if message.MultiContent[0].Type == openai.ChatMessagePartTypeText {
			sb.WriteString(message.MultiContent[0].Text)
		}
	} else if len(message.MultiContent) > 1 {
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
		if instruction == "" {
			continue
		}
		if !strings.Contains(text, instruction) {
			continue
		}
		for j, content := range message.MultiContent {
			if content.Type == openai.ChatMessagePartTypeText {
				replacedText := strings.ReplaceAll(content.Text, instruction, "")
				messages[i].MultiContent[j].Text = replacedText
			}
		}
	}
	return messages
}

func instructionSwitch(m []openai.ChatCompletionMessage) string {
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
		return strings.TrimSpace(sysMsg)
	} else if sysMsg := utility.ExtractPairText(text, "⚙"); sysMsg != "" {
		return strings.TrimSpace(sysMsg)
	}

	return config.InstructionMessageDefault
}
