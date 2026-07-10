package openai

import (
	"context"
	"database/sql"
	"encoding/base64"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	oa "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/config"
	"voltgpt/internal/db"
	"voltgpt/internal/discord"
	"voltgpt/internal/utility"
)

const chatModel = "gpt-5.6-sol"

var (
	sharedClient           *oa.Client
	sharedClientErr        error
	sharedClientOnce       sync.Once
	sharedMemoryClient     *oa.Client
	sharedMemoryClientErr  error
	sharedMemoryClientOnce sync.Once
)

func GetClient() (*oa.Client, error) {
	sharedClientOnce.Do(func() {
		token := strings.TrimSpace(os.Getenv("OPENAI_TOKEN"))
		if token == "" {
			sharedClientErr = fmt.Errorf("OPENAI_TOKEN is not set")
			return
		}

		opts := []option.RequestOption{option.WithAPIKey(token)}
		if baseURL := chatBaseURL(); baseURL != "" {
			opts = append(opts, option.WithBaseURL(baseURL))
			log.Printf("openai: chat client using base URL %s", baseURL)
		}

		client := oa.NewClient(opts...)
		sharedClient = &client
	})
	return sharedClient, sharedClientErr
}

func chatBaseURL() string {
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE"))
	if baseURL == "" {
		return ""
	}

	baseURL = strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(baseURL, "/v1") {
		baseURL += "/v1"
	}
	return baseURL + "/"
}

func GetMemoryClient() (*oa.Client, error) {
	sharedMemoryClientOnce.Do(func() {
		token := strings.TrimSpace(os.Getenv("MEMORY_OPENAI_TOKEN"))
		if token == "" {
			sharedMemoryClientErr = fmt.Errorf("MEMORY_OPENAI_TOKEN is not set")
			return
		}

		client := oa.NewClient(option.WithAPIKey(token))
		sharedMemoryClient = &client
	})
	return sharedMemoryClient, sharedMemoryClientErr
}

type streamer struct {
	Session    *discordgo.Session
	Message    *discordgo.Message
	Buffer     string
	hasOutput  bool
	mu         sync.Mutex
	done       chan struct{}
	stopOnce   sync.Once
	ticker     *time.Ticker
	messageIDs []string
}

func newStreamer(s *discordgo.Session, m *discordgo.Message) *streamer {
	messageIDs := make([]string, 0, 1)
	if m != nil && m.ID != "" {
		messageIDs = append(messageIDs, m.ID)
	}

	return &streamer{
		Session:    s,
		Message:    m,
		done:       make(chan struct{}),
		messageIDs: messageIDs,
	}
}

func (s *streamer) Start() {
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

func (s *streamer) Update(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if utility.HasVisibleContent(content) {
		s.hasOutput = true
	}
	s.Buffer += content
}

func (s *streamer) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.Flush()
	})
}

func (s *streamer) MessageIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, len(s.messageIDs))
	copy(ids, s.messageIDs)
	return ids
}

func (s *streamer) rememberMessageID(id string) {
	if id == "" {
		return
	}
	for _, existing := range s.messageIDs {
		if existing == id {
			return
		}
	}
	s.messageIDs = append(s.messageIDs, id)
}

func (s *streamer) HasVisibleOutput() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hasOutput
}

func (s *streamer) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Buffer == "" {
		return
	}

	if strings.TrimSpace(s.Buffer) == "" {
		return
	}

	newBuffer, newMsg, err := utility.SplitSend(s.Session, s.Message, s.Buffer)
	if err != nil {
		log.Printf("openai: error sending message update: %v", err)
		return
	}

	if newMsg != nil {
		s.Message = newMsg
		s.rememberMessageID(newMsg.ID)
	}
	s.Buffer = newBuffer
}

func builtInTools() []responses.ToolUnionParam {
	return []responses.ToolUnionParam{
		responses.ToolParamOfWebSearch("web_search"),
		responses.ToolParamOfCodeInterpreter(responses.ToolCodeInterpreterContainerCodeInterpreterContainerAutoParam{}),
	}
}

func StreamMessageResponse(ctx context.Context, s *discordgo.Session, c *oa.Client, m *discordgo.Message, input []responses.ResponseInputItemUnionParam, previousResponseID, backgroundFacts string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(input) == 0 {
		return fmt.Errorf("no messages to send")
	}

	msg, err := discord.SendMessage(s, m, "Thinking...")
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	streamer := newStreamer(s, msg)
	streamer.Start()
	defer streamer.Stop()

	channel, err := s.Channel(m.ChannelID)
	if err != nil {
		channel = &discordgo.Channel{Name: "Unknown"}
	}

	contextText := fmt.Sprintf(
		"\n\n# [Ephemeral context for this turn only]\nCurrent time: %s\nChannel: %s\nRelevant memory/context:\n```xml\n%s\n```",
		time.Now().Format("2006-01-02 15:04:05"),
		channel.Name,
		backgroundFacts,
	)

	params := responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam(input),
		},
		Instructions:      oa.String(config.SystemMessage + contextText),
		Metadata:          ResponseMetadata("chat"),
		Model:             responses.ChatModel(chatModel),
		Store:             oa.Bool(true),
		Reasoning:         shared.ReasoningParam{Effort: "medium"},
		Text:              responses.ResponseTextConfigParam{Verbosity: responses.ResponseTextConfigVerbosityMedium},
		Truncation:        responses.ResponseNewParamsTruncationAuto,
		ParallelToolCalls: oa.Bool(true),
		ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		},
		Tools:          builtInTools(),
		PromptCacheKey: oa.String("discord:" + m.ChannelID),
	}
	if previousResponseID != "" {
		params.PreviousResponseID = oa.String(previousResponseID)
	}

	stream := c.Responses.NewStreaming(ctx, params)

	var responseID string
	for stream.Next() {
		event := stream.Current()

		switch e := event.AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			streamer.Update(e.Delta)
		case responses.ResponseRefusalDeltaEvent:
			streamer.Update(e.Delta)
		case responses.ResponseCompletedEvent:
			responseID = e.Response.ID
		case responses.ResponseErrorEvent:
			return fmt.Errorf("openai response error: %s", e.Message)
		case responses.ResponseFailedEvent:
			return fmt.Errorf("openai response failed: status=%s", e.Response.Status)
		}
	}
	if err := stream.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return fmt.Errorf("stream error: %w", err)
	}

	streamer.Stop()
	if !streamer.HasVisibleOutput() {
		emptyMsg, err := discord.EditMessage(s, streamer.Message, utility.EmptyResponseEmoji)
		if err != nil {
			return fmt.Errorf("set empty response emoji: %w", err)
		}
		streamer.Message = emptyMsg
	}

	if responseID == "" {
		return fmt.Errorf("missing response ID from OpenAI stream")
	}

	for _, messageID := range streamer.MessageIDs() {
		if err := StoreResponseID(messageID, responseID); err != nil {
			return fmt.Errorf("store response ID for %s: %w", messageID, err)
		}
	}

	return nil
}

func LookupResponseID(discordMsgID string) (string, error) {
	if db.DB == nil {
		return "", fmt.Errorf("database is not initialized")
	}

	var responseID string
	err := db.DB.QueryRow(
		"SELECT openai_response_id FROM response_ids WHERE discord_message_id = ?",
		discordMsgID,
	).Scan(&responseID)
	if err == sql.ErrNoRows {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return responseID, nil
}

func StoreResponseID(discordMsgID, openaiResponseID string) error {
	if db.DB == nil {
		return fmt.Errorf("database is not initialized")
	}

	_, err := db.DB.Exec(
		`INSERT INTO response_ids (discord_message_id, openai_response_id)
		 VALUES (?, ?)
		 ON CONFLICT(discord_message_id) DO UPDATE SET openai_response_id = excluded.openai_response_id`,
		discordMsgID,
		openaiResponseID,
	)
	return err
}

func PrependReplyMessages(s *discordgo.Session, _ *discordgo.Member, message *discordgo.Message, cache []*discordgo.Message, chatMessages *[]responses.ResponseInputItemUnionParam) {
	reference := utility.GetReferencedMessage(s, message, cache)
	if reference == nil {
		return
	}

	reply := utility.CleanMessage(s, reference)
	reply.Content = utility.ResolveMentions(reply.Content, reply.Mentions)
	images, videos, pdfs, _ := utility.GetMessageMediaURL(reply)

	replyContent := config.RequestContent{
		Text: strings.TrimSpace(fmt.Sprintf("%s%s%s",
			utility.AttachmentText(reply),
			utility.EmbedText(reply),
			reply.Content,
		)),
		Images: images,
		Videos: videos,
		PDFs:   pdfs,
	}

	role := "user"
	if reply.Author != nil && reply.Author.ID == s.State.User.ID {
		role = "assistant"
	} else {
		replyContent.Text = fmt.Sprintf("<user name=\"%s\"> %s </user>", reply.Author.Username, replyContent.Text)
	}

	newMsg := CreateContent(role, replyContent)
	*chatMessages = append([]responses.ResponseInputItemUnionParam{newMsg}, *chatMessages...)

	if reply.Type == discordgo.MessageTypeReply {
		PrependReplyMessages(s, nil, reference, cache, chatMessages)
	}
}

func CreateContent(role string, content config.RequestContent) responses.ResponseInputItemUnionParam {
	var parts responses.ResponseInputMessageContentListParam

	if strings.TrimSpace(content.Text) != "" {
		parts = append(parts, responses.ResponseInputContentParamOfInputText(content.Text))
	}

	if role != "assistant" {
		for _, imageURL := range content.Images {
			part, err := imageURLToInputPart(imageURL)
			if err != nil {
				log.Printf("openai: skip image %s: %v", imageURL, err)
				continue
			}
			parts = append(parts, part)
		}

		for _, videoURL := range content.Videos {
			frames, err := utility.VideoToBase64Images(videoURL)
			if err != nil {
				log.Printf("openai: skip video %s: %v", videoURL, err)
				continue
			}
			for _, frame := range frames {
				parts = append(parts, dataURLImagePart("image/png", frame))
			}
		}
	}

	if len(parts) == 0 {
		parts = append(parts, responses.ResponseInputContentParamOfInputText("(unsupported content omitted)"))
	}

	return responses.ResponseInputItemParamOfMessage(parts, toRole(role))
}

func toRole(role string) responses.EasyInputMessageRole {
	switch role {
	case "assistant", "model":
		return responses.EasyInputMessageRoleAssistant
	default:
		return responses.EasyInputMessageRoleUser
	}
}

func imageURLToInputPart(imageURL string) (responses.ResponseInputContentUnionParam, error) {
	mime := utility.MediaType(imageURL)
	if mime == "" {
		return responses.ResponseInputContentUnionParam{}, fmt.Errorf("unknown media type")
	}

	data, err := utility.DownloadBytes(imageURL)
	if err != nil {
		return responses.ResponseInputContentUnionParam{}, err
	}

	return dataURLImagePart(mime, base64.StdEncoding.EncodeToString(data)), nil
}

func dataURLImagePart(mime, data string) responses.ResponseInputContentUnionParam {
	part := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
	part.OfInputImage.ImageURL = param.NewOpt(fmt.Sprintf("data:%s;base64,%s", mime, data))
	return part
}
