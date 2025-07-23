// Package openai is a package for interacting with the OpenAI API.
package openai

import (
	"context"
	"encoding/json"
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

type response struct {
	sync.RWMutex
	message      string
	buffer       string
	toolCalls    []openai.ToolCall
	finishReason openai.FinishReason
}

func StreamMessageResponse(s *discordgo.Session, m *discordgo.Message, messages []openai.ChatCompletionMessage) error {
	token := os.Getenv("OPENROUTER_TOKEN")
	if token == "" {
		log.Fatal("OPENROUTER_TOKEN is not set")
	}
	cfg := openai.DefaultConfig(token)
	cfg.BaseURL = config.OpenRouterBaseURL
	c := openai.NewClientWithConfig(cfg)
	ctx := context.Background()

	replacementStrings := []string{"<message>", "</message>", "<reply>", "</reply>", "<username>", "</username>", "<attachments>", "</attachments>", "..."}

	instructionMessage := instructionSwitch(messages)
	systemMessage := fmt.Sprintf("System message: %s\n\nInstruction message: %s", config.SystemMessageMinimal, instructionMessage)
	PrependMessage(openai.ChatMessageRoleSystem, "", config.RequestContent{Text: systemMessage}, &messages)

	response := response{
		message:      "",
		buffer:       "",
		toolCalls:    []openai.ToolCall{},
		finishReason: "",
	}

	msg, err := discord.SendMessage(s, m, "Thinking...")
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	maxIterations := 10
	iteration := 0

	for response.finishReason == "" || response.finishReason == openai.FinishReasonToolCalls {
		iteration++
		if iteration > maxIterations {
			return fmt.Errorf("max iterations reached")
		}

		stream, err := c.CreateChatCompletionStream(ctx, openai.ChatCompletionRequest{
			Model:               "google/gemini-2.5-pro",
			Messages:            messages,
			MaxCompletionTokens: 16384,
			// ReasoningEffort:     "high",
			Stream: true,
			// Temperature: 0.3,
			Tools: GetTools(),
		})
		if err != nil {
			return fmt.Errorf("stream error on start: %v", err)
		}

		// Channels for communication between goroutines
		streamDone := make(chan error, 1)
		updateTicker := time.NewTicker(time.Second)
		defer updateTicker.Stop()

		// Run the stream processing in the background
		go func() {
			defer stream.Close()
			for {
				streamResp, err := stream.Recv()
				if errors.Is(err, io.EOF) {
					streamDone <- nil
					return
				}
				if err != nil {
					streamDone <- fmt.Errorf("stream error during response: %v", err)
					return
				}

				response.Lock()
				response.message += streamResp.Choices[0].Delta.Content
				response.buffer += streamResp.Choices[0].Delta.Content
				if streamResp.Choices[0].FinishReason != "" {
					response.finishReason = streamResp.Choices[0].FinishReason
				}
				response.Unlock()

				if streamResp.Choices[0].Delta.ToolCalls != nil {
					response.Lock()
					response.toolCalls = append(response.toolCalls, streamResp.Choices[0].Delta.ToolCalls...)
					response.Unlock()
				}
			}
		}()

		// Main loop handles updates and waits for stream completion
		for {
			select {
			case err := <-streamDone:
				if err != nil {
					return err
				}
				response.Lock()
				if strings.TrimSpace(response.buffer) != "" {
					cleanedMessage := utility.ReplaceMultiple(response.buffer, replacementStrings, "")
					newBuffer, newMsg, err := utility.SplitSend(s, msg, cleanedMessage)
					if err != nil {
						log.Printf("Error sending final message: %v", err)
					} else {
						msg = newMsg
						response.buffer = newBuffer + "\n"
					}
				}

				if response.finishReason == openai.FinishReasonToolCalls || len(response.toolCalls) > 0 {
					AppendToolRequestMessage(s.State.User.Username, response.message, response.toolCalls, &messages)
					for i, toolCall := range response.toolCalls {
						functionReturn, ok := functionMap[toolCall.Function.Name]
						if ok {
							args := map[string]any{}
							json.Unmarshal([]byte(toolCall.Function.Arguments), &args)
							log.Printf("Tool call: %s\nArguments: %v", toolCall.Function.Name, args)
							result := functionReturn(args)
							AppendToolResultMessage(toolCall.ID, toolCall.Function.Name, result, &messages)
							log.Println("Result len: ", len(result))
						}
						response.toolCalls = append(response.toolCalls[:i], response.toolCalls[i+1:]...)
					}
				}
				response.Unlock()
				goto streamComplete

			case <-updateTicker.C:
				// Periodic update
				response.Lock()
				if strings.TrimSpace(response.buffer) == "" {
					response.Unlock()
					continue
				}

				cleanedMessage := utility.ReplaceMultiple(response.buffer, replacementStrings, "")
				newBuffer, newMsg, err := utility.SplitSend(s, msg, cleanedMessage)
				if err != nil {
					log.Printf("Error sending message: %v", err)
					response.Unlock()
					continue
				}
				msg = newMsg
				response.buffer = newBuffer
				response.Unlock()
			}
		}
	streamComplete:
	}

	log.Println("Finished stream")

	return nil
}

func AppendMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

func AppendToolRequestMessage(name string, content string, toolCalls []openai.ToolCall, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, CreateToolRequestmessage(name, content, toolCalls))
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

	for _, u := range content.Media {
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

func CreateToolRequestmessage(name string, content string, toolCalls []openai.ToolCall) openai.ChatCompletionMessage {
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
		images, _, _ := utility.GetMessageMediaURL(message)
		content := config.RequestContent{
			Text:  message.Content,
			Media: images,
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
	images, videos, _ := utility.GetMessageMediaURL(reply)
	replyContent := config.RequestContent{
		Text: strings.TrimSpace(fmt.Sprintf("%s%s%s%s",
			transcription.GetTranscript(s, reply),
			utility.AttachmentText(reply),
			utility.EmbedText(reply),
			fmt.Sprintf("<message>%s</message>", reply.Content),
		)),
		Media: append(images, videos...),
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
