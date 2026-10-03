package handler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	oa "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/responses"

	openaiapi "voltgpt/internal/apis/openai"
	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/utility"
)

// streamer buffers streamed text and periodically flushes it to Discord,
// splitting into follow-up messages when the content grows too long.
type streamer struct {
	Session    *discordgo.Session
	Message    *discordgo.Message
	Buffer     string
	hasOutput  bool
	mu         sync.Mutex
	flushMu    sync.Mutex
	done       chan struct{}
	stopOnce   sync.Once
	ticker     *time.Ticker
	tickerWG   sync.WaitGroup
	messageIDs []string
	flushErr   error
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
	s.tickerWG.Add(1)
	go func() {
		defer s.tickerWG.Done()
		for {
			select {
			case <-s.ticker.C:
				_ = s.Flush()
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

func (s *streamer) Stop() error {
	stopped := false
	s.stopOnce.Do(func() {
		stopped = true
		close(s.done)
		s.tickerWG.Wait()
		_ = s.Flush()
	})
	if !stopped {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	return s.flushErr
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

func (s *streamer) Flush() error {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()

	s.mu.Lock()
	buffer := s.Buffer
	message := s.Message
	if buffer == "" || strings.TrimSpace(buffer) == "" {
		err := s.flushErr
		s.mu.Unlock()
		return err
	}
	s.mu.Unlock()

	newBuffer, newMsg, err := utility.SplitSend(s.Session, message, buffer)
	if err == nil && newMsg != nil && newMsg.ID != message.ID {
		setResponsePending(s.Session, newMsg, true)
		setResponsePending(s.Session, message, false)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err != nil {
		log.Printf("chat: error sending message update: %v", err)
		if s.flushErr == nil {
			s.flushErr = fmt.Errorf("send Discord message update: %w", err)
		}
		return s.flushErr
	}

	if newMsg != nil {
		s.Message = newMsg
		s.rememberMessageID(newMsg.ID)
	}
	if strings.HasPrefix(s.Buffer, buffer) {
		s.Buffer = newBuffer + strings.TrimPrefix(s.Buffer, buffer)
	}
	s.flushErr = nil
	return nil
}

func attachGeneratedArtifacts(ctx context.Context, s *discordgo.Session, c *oa.Client, m *discordgo.Message, artifacts []openaiapi.Artifact) ([]*discordgo.MessageAttachment, error) {
	var files []*discordgo.File
	var errs []error

	for _, artifact := range artifacts {
		if len(files)+len(m.Attachments) >= 10 {
			errs = append(errs, fmt.Errorf("generated files exceed the message attachment limit"))
			break
		}
		body, contentType, err := openaiapi.DownloadArtifact(ctx, c, artifact)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		defer body.Close()
		files = append(files, &discordgo.File{
			Name:        artifact.Name(),
			ContentType: contentType,
			Reader:      body,
		})
	}

	if len(files) == 0 {
		return nil, errors.Join(errs...)
	}
	// Omitting Content preserves the final streamed text exactly.
	updated, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID: m.ID, Channel: m.ChannelID, Files: files,
	}, discordgo.WithContext(ctx))
	if err != nil {
		errs = append(errs, fmt.Errorf("attach generated files: %w", err))
		return nil, errors.Join(errs...)
	}
	return updated.Attachments, errors.Join(errs...)
}

var sandboxDestination = regexp.MustCompile(`sandbox:/[^\s<>\)]+`)

func linkGeneratedArtifacts(content string, attachments []*discordgo.MessageAttachment) string {
	return sandboxDestination.ReplaceAllStringFunc(content, func(destination string) string {
		decoded, err := url.PathUnescape(strings.TrimPrefix(destination, "sandbox:"))
		if err != nil {
			return destination
		}
		var matched string
		for _, attachment := range attachments {
			if attachment.Filename == path.Base(decoded) && attachment.URL != "" {
				if matched != "" { // Ambiguous filenames must not link to the wrong file.
					return destination
				}
				matched = attachment.URL
			}
		}
		if matched != "" {
			return matched
		}
		return destination
	})
}

// Reaction failures should never prevent delivery of the response. Cleanup uses
// its own bounded context so it can run even after generation is canceled.
func setResponsePending(s *discordgo.Session, m *discordgo.Message, pending bool) {
	setResponseReaction(s, m, "⏳", pending)
}

func setResponseReaction(s *discordgo.Session, m *discordgo.Message, emoji string, add bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var err error
	if add {
		err = s.MessageReactionAdd(m.ChannelID, m.ID, emoji, discordgo.WithContext(ctx))
	} else {
		err = s.MessageReactionRemove(m.ChannelID, m.ID, emoji, "@me", discordgo.WithContext(ctx))
	}
	if err != nil {
		log.Printf("chat: update %s reaction for %s: %v", emoji, m.ID, err)
	}
}

func chatInstructions(channelName, backgroundFacts string) string {
	return config.SystemMessage + fmt.Sprintf(
		"\n\n# [Ephemeral context for this turn only]\nCurrent time: %s\nChannel: %s\nRelevant memory/context:\n```xml\n%s\n```",
		time.Now().Format("2006-01-02 15:04:05"),
		channelName,
		backgroundFacts,
	)
}

// streamChatResponse streams an OpenAI response into Discord as a reply to m,
// attaches any generated files, and records the response ID for each sent
// message so replies can continue the conversation.
func streamChatResponse(ctx context.Context, s *discordgo.Session, c *oa.Client, m *discordgo.Message, input []responses.ResponseInputItemUnionParam, previousResponseID, backgroundFacts string) (retErr error) {
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
	deliveryFailed := false
	setResponsePending(s, msg, true)
	streamer.Start()
	defer func() {
		if err := streamer.Stop(); err != nil {
			retErr = errors.Join(retErr, err)
		}
		if retErr != nil && !errors.Is(retErr, context.Canceled) {
			_, err := discord.EditMessage(s, streamer.Message, "⚠️ Something went wrong while generating this response.")
			if err != nil {
				retErr = errors.Join(retErr, fmt.Errorf("set Discord error state: %w", err))
			}
		}
		for _, id := range streamer.MessageIDs() {
			setResponsePending(s, &discordgo.Message{ID: id, ChannelID: m.ChannelID}, false)
		}
		if retErr == nil && ctx.Err() == nil && !deliveryFailed {
			setResponseReaction(s, streamer.Message, "✅", true)
		}
	}()

	channel, err := s.Channel(m.ChannelID)
	if err != nil {
		channel = &discordgo.Channel{Name: "Unknown"}
	}

	completed, err := openaiapi.StreamChat(ctx, c, openaiapi.ChatRequest{
		Input:              input,
		Instructions:       chatInstructions(channel.Name, backgroundFacts),
		PreviousResponseID: previousResponseID,
		PromptCacheKey:     "discord:" + m.ChannelID,
	}, streamer.Update)
	if err != nil {
		return err
	}

	if err := streamer.Stop(); err != nil {
		return err
	}
	attachments, artifactErr := attachGeneratedArtifacts(ctx, s, c, streamer.Message, openaiapi.GeneratedArtifacts(completed))
	if artifactErr != nil {
		deliveryFailed = true
		log.Printf("chat: generated artifact delivery failed: %v", artifactErr)
		_, _ = discord.SendMessage(s, m, "⚠️ One or more generated files couldn't be attached.")
	}
	if len(attachments) > 0 {
		for _, id := range streamer.MessageIDs() {
			message, err := s.ChannelMessage(m.ChannelID, id, discordgo.WithContext(ctx))
			if err != nil {
				log.Printf("chat: fetch message for artifact links: %v", err)
				deliveryFailed = true
				continue
			}
			content := linkGeneratedArtifacts(message.Content, attachments)
			if content != message.Content {
				// Send the Markdown unchanged; generic link suppression can alter destinations.
				if _, err := s.ChannelMessageEditComplex(&discordgo.MessageEdit{ID: id, Channel: m.ChannelID, Content: &content}, discordgo.WithContext(ctx)); err != nil {
					deliveryFailed = true
					log.Printf("chat: update artifact links: %v", err)
				}
			}
		}
	}

	if !streamer.HasVisibleOutput() {
		emptyContent := utility.EmptyResponseEmoji
		if len(attachments) > 0 {
			emptyContent = "Generated files attached."
		}
		emptyMsg, err := discord.EditMessage(s, streamer.Message, emptyContent)
		if err != nil {
			return fmt.Errorf("set empty response emoji: %w", err)
		}
		streamer.Message = emptyMsg
	}

	for _, messageID := range streamer.MessageIDs() {
		if err := storeResponseID(messageID, completed.ID); err != nil {
			return fmt.Errorf("store response ID for %s: %w", messageID, err)
		}
	}

	return nil
}
