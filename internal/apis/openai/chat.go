// Package openai is a package for interacting with the OpenAI API.
package openai

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"strings"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"

	"voltgpt/internal/config"
	"voltgpt/internal/transcription"
	"voltgpt/internal/utility"
)

func SummarizeCleanText(text string) string {
	token := os.Getenv("OPENROUTER_TOKEN")
	if token == "" {
		log.Fatal("OPENROUTER_TOKEN is not set")
	}
	cfg := openai.DefaultConfig(token)
	cfg.BaseURL = config.OpenRouterBaseURL
	c := openai.NewClientWithConfig(cfg)
	ctx := context.Background()

	instructions := `
	You are a helpful assistant. 
	You are given text from websites in a markdown format.
	Cut down on the amount of text but keep it filling.
	Keep links in the text for further browsing and reference.`

	messages := []openai.ChatCompletionMessage{
		{
			Role:    openai.ChatMessageRoleSystem,
			Content: instructions,
		},
		{
			Role:    openai.ChatMessageRoleUser,
			Content: text,
		},
	}

	req := openai.ChatCompletionRequest{
		Model:       "google/gemini-2.5-flash-lite",
		Messages:    messages,
		MaxTokens:   16384,
		Temperature: math.SmallestNonzeroFloat32,
	}

	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Printf("CreateCompletion error: %v\n", err)
		return ""
	}

	return resp.Choices[0].Message.Content
}

func AppendMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

func AppendToolRequestMessage(name string, content string, toolCalls []openai.ToolCall, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, CreateToolRequestMessage(name, content, toolCalls))
	*messages = newMessages
}

func AppendToolResultMessage(toolCallID string, toolName string, content string, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, CreateToolResultMessage(toolCallID, toolName, content))
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
		base64, err := utility.Base64ImageDownload(u)
		if err != nil {
			log.Printf("Error downloading image: %v", err)
			continue
		}
		for _, b := range base64 {
			message[0].MultiContent = append(message[0].MultiContent, openai.ChatMessagePart{
				Type: openai.ChatMessagePartTypeImageURL,
				ImageURL: &openai.ChatMessageImageURL{
					URL: b,
				},
			})
		}
	}

	return message
}

func CreateToolRequestMessage(name string, content string, toolCalls []openai.ToolCall) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:      openai.ChatMessageRoleAssistant,
		Content:   content,
		Name:      name,
		ToolCalls: toolCalls,
	}
}

func CreateToolResultMessage(toolCallID string, toolName string, content string) openai.ChatCompletionMessage {
	return openai.ChatCompletionMessage{
		Role:       openai.ChatMessageRoleTool,
		Content:    content,
		ToolCallID: toolCallID,
		Name:       toolName,
	}
}

func CreateBatchMessages(s *discordgo.Session, messages []*discordgo.Message) []openai.ChatCompletionMessage {
	var batchMessages []openai.ChatCompletionMessage

	for _, message := range messages {
		images, _, _, _ := utility.GetMessageMediaURL(message)
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
	images, videos, _, _ := utility.GetMessageMediaURL(reply)
	replyContent := config.RequestContent{
		Text: strings.TrimSpace(fmt.Sprintf("%s%s%s%s",
			transcription.GetTranscript(s, reply),
			utility.AttachmentText(reply),
			utility.EmbedText(reply),
			fmt.Sprintf("<message>%s</message>", reply.Content),
		)),
		Images: images,
		Videos: videos,
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
