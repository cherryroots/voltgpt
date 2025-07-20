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
	sliceIndex   int
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

	var finishReason openai.FinishReason
	var accumulatedMessage string // Accumulate messages across iterations

	msg, err := discord.SendMessage(s, m, "Thinking...")
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	for finishReason == "" || finishReason == openai.FinishReasonToolCalls {
		// Use the response struct to manage state
		resp := &response{
			message:      "",
			sliceIndex:   0,
			toolCalls:    []openai.ToolCall{},
			finishReason: "",
		}
		var messageSlices []string

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
					resp.Lock()
					resp.finishReason = openai.FinishReasonStop
					resp.Unlock()
					streamDone <- nil
					return
				}
				if err != nil {
					streamDone <- fmt.Errorf("stream error during response: %v", err)
					return
				}

				resp.Lock()
				resp.message += streamResp.Choices[0].Delta.Content
				resp.finishReason = streamResp.Choices[0].FinishReason
				resp.Unlock()

				if streamResp.Choices[0].Delta.ToolCalls != nil {
					resp.Lock()
					resp.toolCalls = append(resp.toolCalls, streamResp.Choices[0].Delta.ToolCalls...)
					resp.Unlock()
				}

				if streamResp.Choices[0].FinishReason == openai.FinishReasonStop || streamResp.Choices[0].FinishReason == openai.FinishReasonToolCalls {
					streamDone <- nil
					return
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
				// Stream is done, get final state
				resp.Lock()
				finishReason = resp.finishReason
				finalMessage := resp.message
				finalToolCalls := resp.toolCalls
				resp.Unlock()

				// Accumulate the message from this iteration
				if strings.TrimSpace(finalMessage) != "" {
					if accumulatedMessage != "" {
						accumulatedMessage += "\n\n" + finalMessage
					} else {
						accumulatedMessage = finalMessage
					}
				}

				// Send the accumulated message
				if strings.TrimSpace(accumulatedMessage) != "" {
					cleanedMessage := utility.ReplaceMultiple(accumulatedMessage, replacementStrings, "")
					messageSlices = utility.SplitMessageSlices(cleanedMessage)
					if len(messageSlices) > 0 {
						newMsg, err := utility.SliceSend(s, msg, messageSlices, 0)
						if err != nil {
							log.Printf("Error sending final message slices: %v", err)
						} else {
							msg = newMsg
						}
					}
				}

				// Handle tool calls if needed
				if finishReason == openai.FinishReasonToolCalls {
					AppendMessage(openai.ChatMessageRoleAssistant, "", config.RequestContent{Text: finalMessage}, &messages)
					for _, toolCall := range finalToolCalls {
						functionReturn, ok := functionMap[toolCall.Function.Name]
						if ok {
							args := []any{}
							json.Unmarshal([]byte(toolCall.Function.Arguments), &args)
							AppendToolMessage(toolCall.ID, toolCall.Function.Name, functionReturn(args), &messages)
							log.Printf("Tool call: %s\nArguments: %v\nReturn: %s", toolCall.Function.Name, args, functionReturn(args))
						}
					}
				}
				goto streamComplete

			case <-updateTicker.C:
				// Periodic update
				resp.Lock()
				if strings.TrimSpace(resp.message) == "" {
					resp.Unlock()
					continue
				}

				// Build current display message (accumulated + current iteration)
				currentDisplayMessage := accumulatedMessage
				if currentDisplayMessage != "" && strings.TrimSpace(resp.message) != "" {
					currentDisplayMessage += "\n\n" + resp.message
				} else if strings.TrimSpace(resp.message) != "" {
					currentDisplayMessage = resp.message
				}

				// Clean the message and split into slices
				cleanedMessage := utility.ReplaceMultiple(currentDisplayMessage, replacementStrings, "")
				messageSlices = utility.SplitMessageSlices(cleanedMessage)

				// Send slices starting from current slice index
				if len(messageSlices) > 0 {
					newMsg, err := utility.SliceSend(s, msg, messageSlices, resp.sliceIndex)
					if err != nil {
						log.Printf("Error sending message slices: %v", err)
						resp.Unlock()
						continue
					}
					msg = newMsg
					// Update slice index to the last slice we've sent
					resp.sliceIndex = len(messageSlices) - 1
				}
				resp.Unlock()
			}
		}
	streamComplete:
	}

	return nil
}

func AppendMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

func AppendToolMessage(toolCallID string, toolName string, content string, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, CreateToolMessage(toolCallID, toolName, content))
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

func CreateToolMessage(toolCallID string, toolName string, content string) openai.ChatCompletionMessage {
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
