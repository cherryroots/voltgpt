// Package gemini provides a client for interacting with the Gemini API.
package gemini

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"google.golang.org/genai"

	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/transcription"
	"voltgpt/internal/utility"
)

// Streamer handles streaming responses to Discord.
type Streamer struct {
	Session          *discordgo.Session
	Message          *discordgo.Message
	Buffer           string
	ThoughtSignature []byte
	mu               sync.Mutex
	done             chan bool
	ticker           *time.Ticker
	replacementMap   []string
}

// NewStreamer creates a new Streamer instance.
func NewStreamer(s *discordgo.Session, m *discordgo.Message) *Streamer {
	return &Streamer{
		Session:        s,
		Message:        m,
		replacementMap: []string{"<message>", "</message>", "<reply>", "</reply>", "<username>", "</username>", "<attachments>", "</attachments>", "..."},
		done:           make(chan bool),
	}
}

// Start begins the streaming ticker.
func (s *Streamer) Start() {
	s.ticker = time.NewTicker(1 * time.Second)
	go func() {
		for {
			select {
			case <-s.ticker.C:
				s.Flush()
			case <-s.done:
				s.ticker.Stop()
				return
			}
		}
	}()
}

// Update appends new content to the buffer.
func (s *Streamer) Update(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Buffer += content
}

func (s *Streamer) UpdateThoughtSignature(signature []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ThoughtSignature = signature
}

// Stop stops the streamer and performs a final flush.
func (s *Streamer) Stop() {
	s.done <- true
	s.Flush()
}

// Flush sends the current buffer content to Discord.
func (s *Streamer) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ThoughtSignature != nil {
		buf, err := utility.BytesToPNG(s.ThoughtSignature)
		if err != nil {
			log.Printf("Error converting thought signature to PNG: %v", err)
			return
		}

		discord.EditMessageFile(s.Session, s.Message, s.Message.Content, []*discordgo.File{
			{
				Name:   "thought_signature.png",
				Reader: bytes.NewReader(buf.Bytes()),
			},
		})
	}

	if s.Buffer == "" {
		return
	}

	cleanedMessage := utility.ReplaceMultiple(s.Buffer, s.replacementMap, "")
	if strings.TrimSpace(cleanedMessage) == "" {
		return
	}

	newBuffer, newMsg, err := utility.SplitSend(s.Session, s.Message, cleanedMessage)
	if err != nil {
		log.Printf("Error sending message update: %v", err)
		return
	}

	s.Message = newMsg
	s.Buffer = newBuffer
}

// StreamMessageResponse streams the response from Gemini to Discord.
func StreamMessageResponse(s *discordgo.Session, m *discordgo.Message, history []*genai.Content) error {
	apiKey := os.Getenv("GEMINI_TOKEN")
	if apiKey == "" {
		return fmt.Errorf("GEMINI_TOKEN is not set")
	}

	ctx := context.Background()
	// Initialize the Gemini client
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey:  apiKey,
		Backend: genai.BackendGeminiAPI,
	})
	if err != nil {
		return fmt.Errorf("failed to create gemini client: %v", err)
	}

	// Configure the model
	modelName := "gemini-3-pro-preview"

	// Handle "Thinking..." message
	msg, err := discord.SendMessage(s, m, "Thinking...")
	if err != nil {
		return fmt.Errorf("failed to send message: %v", err)
	}

	// Setup streaming
	streamer := NewStreamer(s, msg)
	streamer.Start()
	defer streamer.Stop()

	// Inject System Message
	instructionMessage := instructionSwitch(history)
	systemMessageText := fmt.Sprintf("System message: %s\n\nInstruction message: %s", config.SystemMessageMinimal, instructionMessage)

	// Create system content
	systemInstruction := genai.NewContentFromText(systemMessageText, genai.RoleModel)

	// Prepare the request
	var chatHistory []*genai.Content
	var lastMessage *genai.Content

	if len(history) > 0 && history[0].Role == "model" {
		chatHistory = history[1:]
	} else {
		chatHistory = history
	}

	if len(chatHistory) > 0 {
		lastMessage = chatHistory[len(chatHistory)-1]
		chatHistory = chatHistory[:len(chatHistory)-1]
	}

	if lastMessage == nil {
		return fmt.Errorf("no messages to send")
	}

	// Re-assembling contents
	allContents := append(chatHistory, lastMessage)

	t := float32(1)
	config := &genai.GenerateContentConfig{
		SystemInstruction: systemInstruction,
		Temperature:       &t,
		Tools: []*genai.Tool{
			{
				GoogleSearch:  &genai.GoogleSearch{},
				URLContext:    &genai.URLContext{},
				CodeExecution: &genai.ToolCodeExecution{},
			},
		},
	}

	// Call the API
	stream := client.Models.GenerateContentStream(ctx, modelName, allContents, config)

	// Consume stream
	for resp, err := range stream {
		if err != nil {
			return fmt.Errorf("stream error: %v", err)
		}

		// Accumulate text
		for _, cand := range resp.Candidates {
			if cand.Content != nil {
				for _, part := range cand.Content.Parts {
					if part.Text != "" {
						streamer.Update(part.Text)
					}
					if part.ThoughtSignature != nil {
						streamer.UpdateThoughtSignature(part.ThoughtSignature)
					}
				}
			}
		}
	}

	return nil
}

func instructionSwitch(history []*genai.Content) string {
	if len(history) == 0 {
		return config.InstructionMessageDefault
	}

	var text string
	firstMessageText := contentToString(history[0])
	lastMessageText := contentToString(history[len(history)-1])

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

func contentToString(c *genai.Content) string {
	var sb strings.Builder
	for _, part := range c.Parts {
		if part.Text != "" {
			sb.WriteString(part.Text + "\n")
		}
	}
	return sb.String()
}

func SummarizeCleanText(text string) string {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Println("GEMINI_API_KEY is not set")
		return ""
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		APIKey: apiKey,
	})
	if err != nil {
		log.Printf("failed to create gemini client: %v", err)
		return ""
	}

	instructions := `
	You are a helpful assistant. 
	You are given text from websites in a markdown format.
	Cut down on the amount of text but keep it filling.
	Keep links in the text for further browsing and reference.`

	modelName := "gemini-2.0-flash-exp"

	resp, err := client.Models.GenerateContent(ctx, modelName,
		genai.Text(text),
		&genai.GenerateContentConfig{
			SystemInstruction: genai.NewContentFromText(instructions, genai.RoleModel),
			Temperature:       genai.Ptr(float32(0.1)), // Low temp for summarization
		},
	)
	if err != nil {
		log.Printf("GenerateContent error: %v\n", err)
		return ""
	}

	if len(resp.Candidates) > 0 && resp.Candidates[0].Content != nil {
		return contentToString(resp.Candidates[0].Content)
	}

	return ""
}

func AppendMessage(role string, name string, content config.RequestContent, messages *[]*genai.Content) {
	finalText := content.Text

	parts := []*genai.Part{}
	if finalText != "" {
		parts = append(parts, &genai.Part{Text: finalText})
	}

	for _, mediaURL := range content.Media {
		mimeType := utility.MediaType(mediaURL)
		data, err := utility.DownloadURL(mediaURL)
		if err != nil {
			log.Printf("Error downloading media %s: %v", mediaURL, err)
			continue
		}
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{
				MIMEType: mimeType,
				Data:     data,
			},
		})
	}

	*messages = append(*messages, &genai.Content{
		Role:  role,
		Parts: parts,
	})
}

func PrependReplyMessages(s *discordgo.Session, originMember *discordgo.Member, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]*genai.Content) {
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

	role := "user"
	if reply.Author.ID == s.State.User.ID {
		role = "model"
	}

	if role == "user" {
		replyContent.Text = fmt.Sprintf("<username>%s</username>: %s", reply.Author.Username, replyContent.Text)
	}

	newMsg := createContentStruct(role, replyContent)
	*chatMessages = append([]*genai.Content{newMsg}, *chatMessages...)

	if reply.Type == discordgo.MessageTypeReply {
		PrependReplyMessages(s, originMember, reference, cache, chatMessages)
	}
}

func createContentStruct(role string, content config.RequestContent) *genai.Content {
	parts := []*genai.Part{}
	if content.Text != "" {
		parts = append(parts, &genai.Part{Text: content.Text})
	}

	for _, mediaURL := range content.Media {
		if strings.Contains(mediaURL, "thought_signature.png") {
			data, err := utility.DownloadURL(mediaURL)
			if err != nil {
				continue
			}
			data, err = utility.PNGToBytes(data)
			if err != nil {
				continue
			}
			parts = append(parts, &genai.Part{
				ThoughtSignature: data,
			})
			continue
		}
		mimeType := utility.MediaType(mediaURL)
		if mimeType == "" {
			continue
		}
		data, err := utility.DownloadURL(mediaURL)
		if err != nil {
			continue
		}
		parts = append(parts, &genai.Part{
			InlineData: &genai.Blob{
				MIMEType: mimeType,
				Data:     data,
			},
		})
	}

	return &genai.Content{
		Role:  role,
		Parts: parts,
	}
}
