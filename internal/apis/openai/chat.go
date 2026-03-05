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

	oa "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/responses"

	"github.com/bwmarrin/discordgo"

	"voltgpt/internal/config"
	"voltgpt/internal/db"
	"voltgpt/internal/discord"
	"voltgpt/internal/utility"
)

const chatModel = "gpt-5.4"

var (
	sharedClient     *oa.Client
	sharedClientErr  error
	sharedClientOnce sync.Once
)

func builtInTools() []responses.ToolUnionParam {
	return []responses.ToolUnionParam{
		responses.ToolParamOfWebSearchPreview("web_search"),
		responses.ToolParamOfCodeInterpreter(responses.ToolCodeInterpreterContainerCodeInterpreterContainerAutoParam{}),
	}
}

func GetClient() (*oa.Client, error) {
	sharedClientOnce.Do(func() {
		token := strings.TrimSpace(os.Getenv("OPENAI_TOKEN"))
		if token == "" {
			sharedClientErr = fmt.Errorf("OPENAI_TOKEN is not set")
			return
		}

		client := oa.NewClient(option.WithAPIKey(token))
		sharedClient = &client
	})
	return sharedClient, sharedClientErr
}

type Streamer struct {
	Session        *discordgo.Session
	Message        *discordgo.Message
	Buffer         string
	mu             sync.Mutex
	done           chan struct{}
	stopOnce       sync.Once
	ticker         *time.Ticker
	replacementMap []string
	messageIDs     []string
}

func NewStreamer(s *discordgo.Session, m *discordgo.Message) *Streamer {
	return &Streamer{
		Session:        s,
		Message:        m,
		done:           make(chan struct{}),
		replacementMap: []string{"<username>", "</username>", "<attachments>", "</attachments>", "..."},
		messageIDs:     []string{m.ID},
	}
}

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

func (s *Streamer) Update(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Buffer += content
}

func (s *Streamer) Stop() {
	s.stopOnce.Do(func() {
		close(s.done)
		s.Flush()
	})
}

func (s *Streamer) MessageIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	ids := make([]string, len(s.messageIDs))
	copy(ids, s.messageIDs)
	return ids
}

func (s *Streamer) rememberMessageID(id string) {
	for _, existing := range s.messageIDs {
		if existing == id {
			return
		}
	}
	s.messageIDs = append(s.messageIDs, id)
}

func (s *Streamer) Flush() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Buffer == "" {
		return
	}

	cleanedMessage := utility.ReplaceMultiple(s.Buffer, s.replacementMap, "")
	if strings.TrimSpace(cleanedMessage) == "" {
		return
	}

	newBuffer, newMsg, err := utility.SplitSend(s.Session, s.Message, cleanedMessage)
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

func StreamMessageResponse(s *discordgo.Session, c *oa.Client, m *discordgo.Message, input []responses.ResponseInputItemUnionParam, previousResponseID, backgroundFacts string) error {
	if len(input) == 0 {
		return fmt.Errorf("no messages to send")
	}

	msg, err := discord.SendMessage(s, m, "Thinking...")
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}

	streamer := NewStreamer(s, msg)
	streamer.Start()
	defer streamer.Stop()

	systemMessageText := config.SystemMessage
	systemMessageText = strings.ReplaceAll(systemMessageText, "{TIME}", time.Now().Format("2006-01-02 15:04:05"))
	channel, err := s.Channel(m.ChannelID)
	if err != nil {
		channel = &discordgo.Channel{Name: "Unknown"}
	}
	systemMessageText = strings.ReplaceAll(systemMessageText, "{CHANNEL}", channel.Name)
	systemMessageText = strings.ReplaceAll(systemMessageText, "{BACKGROUND_FACTS}", backgroundFacts)

	params := responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam(input),
		},
		Instructions:      oa.String(systemMessageText),
		Model:             responses.ChatModel(chatModel),
		Store:             oa.Bool(true),
		Temperature:       oa.Float(1),
		Truncation:        responses.ResponseNewParamsTruncationAuto,
		ParallelToolCalls: oa.Bool(true),
		ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		},
		Tools: builtInTools(),
	}
	if previousResponseID != "" {
		params.PreviousResponseID = oa.String(previousResponseID)
	}

	ctx := context.Background()
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
		return fmt.Errorf("stream error: %w", err)
	}

	streamer.Stop()

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
