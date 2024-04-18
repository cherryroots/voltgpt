// Package openai is a package for interacting with the OpenAI API.
package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/gob"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/bwmarrin/discordgo"
	"github.com/liushuangls/go-anthropic/v2"
	"github.com/pkoukk/tiktoken-go"
	"github.com/sashabaranov/go-openai"

	"voltgpt/internal/config"
	"voltgpt/internal/discord"
	"voltgpt/internal/utility"
)

// TranscriptCache is a map of transcriptions.
var TranscriptCache = struct {
	sync.RWMutex
	t map[string]transcript
}{
	t: make(map[string]transcript),
}

// game is a struct representing the game.
type transcript struct {
	ContentURL *url.URL
	Response   openai.AudioResponse
}

func (t transcript) String() string {
	return t.ContentURL.String()
}

// WriteToFile writes the global variable TranscriptCache to a file named "transcripts.gob".
func WriteToFile() {
	if _, err := os.Stat("transcripts.gob"); os.IsNotExist(err) {
		file, err := os.Create("transcripts.gob")
		if err != nil {
			log.Fatal(err)
		}
		defer file.Close()

		if err := gob.NewEncoder(file).Encode(TranscriptCache.t); err != nil {
			log.Fatal(err)
		}

		ReadFromFile()

		return
	}

	buf := new(bytes.Buffer)
	if err := gob.NewEncoder(buf).Encode(TranscriptCache.t); err != nil {
		log.Printf("Encode error: %v\n", err)
		return
	}

	file, err := os.OpenFile("transcripts.gob", os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.Printf("OpenFile error: %v\n", err)
		return
	}
	defer file.Close()

	if _, err := buf.WriteTo(file); err != nil {
		log.Printf("WriteTo error: %v\n", err)
		return
	}
}

// ReadFromFile reads data from a file named "transcripts.gob" and decodes it into the global variable TranscriptCache.
func ReadFromFile() {
	dataFile, err := os.Open("transcripts.gob")
	if err != nil {
		return
	}
	defer dataFile.Close()

	if err := gob.NewDecoder(dataFile).Decode(&TranscriptCache.t); err != nil {
		log.Fatal(err)
	}
}

func readTranscriptCache(contentURL string, locking bool) transcript {
	if locking {
		TranscriptCache.Lock()
		defer TranscriptCache.Unlock()
	}

	return TranscriptCache.t[contentURL]
}

func writeTranscriptCache(transcript transcript, locking bool) {
	if locking {
		TranscriptCache.Lock()
		defer TranscriptCache.Unlock()
	}

	TranscriptCache.t[transcript.String()] = transcript
}

func checkTranscriptCache(contentURL string, locking bool) bool {
	if locking {
		TranscriptCache.Lock()
		defer TranscriptCache.Unlock()
	}

	_, ok := TranscriptCache.t[contentURL]

	return ok
}

// TotalTranscripts returns the total number of transcripts
func TotalTranscripts() int {
	TranscriptCache.RLock()
	defer TranscriptCache.RUnlock()
	return len(TranscriptCache.t)
}

// GetTranscriptFromMessage returns the transcript of the message
func GetTranscriptFromMessage(s *discordgo.Session, message *discordgo.Message) (string, error) {
	regex := regexp.MustCompile(`(?m)<?(https?://[^\s<>]+)>?\b`)
	result := regex.FindAllStringSubmatch(message.Content, -1)
	var transcript string
	for _, match := range result {
		if utility.MatchVideo(match[1]) {
			msg, err := discord.SendMessage(s, message, fmt.Sprintf("Gettings transcript for <%s>...", match[1]))
			if err != nil {
				return "", err
			}
			transcript, err = GetTranscriptFromVideo(match[1])
			if err != nil {
				log.Println(err)
			}

			s.ChannelMessageDelete(message.ChannelID, msg.ID)
		}
	}

	return transcript, nil
}

// GetTranscriptFromVideo returns the transcript of the video
func GetTranscriptFromVideo(videoURL string) (string, error) {
	// Check if the video is already in the cache
	if checkTranscriptCache(videoURL, false) {
		transcript := readTranscriptCache(videoURL, false)
		return transcript.Response.Text, nil
	}

	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a temp dir to store the video
	dir, err := os.MkdirTemp("", "video-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)

	// Download the audio using ytdlp
	cmd := exec.Command("yt-dlp", "-f", "bestaudio[ext=m4a]", "-x", "-o", fmt.Sprintf("%s/audio.%%(ext)s", dir), videoURL)
	if err := cmd.Run(); err != nil {
		return "", err
	}

	// Get the video path
	videoPath := fmt.Sprintf("%s/audio.m4a", dir)

	// Create the whisper client
	client := openai.NewClient(openaiToken)

	// Upload the video to whisper
	resp, err := client.CreateTranscription(
		context.Background(),
		openai.AudioRequest{
			Model:    openai.Whisper1,
			FilePath: videoPath,
			Format:   openai.AudioResponseFormatSRT,
		},
	)
	if err != nil {
		return "", err
	}

	parsedURL, err := url.Parse(videoURL)
	if err != nil {
		log.Printf("Parse error, not writing to cache: %v\n", err)
	} else {
		writeTranscriptCache(transcript{ContentURL: parsedURL, Response: resp}, true)
	}

	text := fmt.Sprintf("Transcript for %s: %s", videoURL, resp.Text)

	return text, nil
}

func getTTSFile(message string, index string, hd bool) []*discordgo.File {
	files := make([]*discordgo.File, 0)
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()
	model := openai.TTSModel1

	if hd {
		model = openai.TTSModel1HD
	}

	res, err := c.CreateSpeech(ctx, openai.CreateSpeechRequest{
		Model: model,
		Input: message,
		Voice: openai.VoiceNova,
	})
	if err != nil {
		log.Printf("CreateSpeech error: %v\n", err)
	}
	defer res.Close()

	buf, err := io.ReadAll(res)
	if err != nil {
		log.Printf("io.ReadAll error: %v\n", err)
	}

	filename := getFilenameSummary(message)

	files = append(files, &discordgo.File{
		Name:   index + "-" + filename + ".mp3",
		Reader: strings.NewReader(string(buf)),
	})

	return files
}

func getFilenameSummary(message string) string {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	prompt := "Summarize this text as a filename but without a file extension: " + message

	maxTokens, err := GetRequestMaxTokensString(prompt, openai.GPT3Dot5Turbo0125)
	if err != nil {
		log.Printf("getRequestMaxTokens error: %v\n", err)
		return "file"
	}

	req := openai.ChatCompletionRequest{
		Model:       openai.GPT3Dot5Turbo0125,
		Messages:    createMessage(openai.ChatMessageRoleUser, "", config.RequestContent{Text: prompt}),
		Temperature: config.DefaultTemp,
		MaxTokens:   maxTokens,
	}

	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Printf("CreateCompletion error: %v\n", err)
		return "file"
	}

	return resp.Choices[0].Message.Content
}

func drawImage(message string, size string) ([]*discordgo.File, error) {
	// OpenAI API key
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	// Create a new OpenAI client
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	reqURL := openai.ImageRequest{
		Prompt:         message,
		Model:          openai.CreateImageModelDallE3,
		Quality:        openai.CreateImageQualityStandard,
		Size:           size,
		ResponseFormat: openai.CreateImageResponseFormatB64JSON,
		N:              1,
	}

	respBase64, err := c.CreateImage(ctx, reqURL)
	if err != nil {
		return nil, err
	}

	resp, err := base64.StdEncoding.DecodeString(respBase64.Data[0].B64JSON)
	if err != nil {
		return nil, err
	}

	files := []*discordgo.File{
		{
			Name:   "image.png",
			Reader: bytes.NewReader(resp),
		},
	}

	return files, nil
}

// AppendMessage appends a message to the end of the messages array
func AppendMessage(role string, name string, content config.RequestContent, messages *[]openai.ChatCompletionMessage) {
	newMessages := append(*messages, createMessage(role, name, content)...)
	*messages = newMessages
}

// PrependMessage prepends a message to the beginning of the messages array
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

	for _, u := range content.URL {
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

// CreateBatchMessages creates a batch messages from the given messages
func CreateBatchMessages(s *discordgo.Session, messages []*discordgo.Message) []openai.ChatCompletionMessage {
	var batchMessages []openai.ChatCompletionMessage

	for _, message := range messages {
		images, _ := utility.GetMessageMediaURL(message)
		content := config.RequestContent{
			Text: message.Content,
			URL:  images,
		}
		if message.Author.ID == s.State.User.ID {
			PrependMessage(openai.ChatMessageRoleAssistant, message.Author.Username, content, &batchMessages)
		}
		PrependMessage(openai.ChatMessageRoleUser, message.Author.Username, content, &batchMessages)
	}

	return batchMessages
}

func messagesToString(messages []openai.ChatCompletionMessage) string {
	var sb strings.Builder
	for _, message := range messages {
		sb.WriteString(fmt.Sprintf("From: %s, Role: %s: %s\n", message.Name, message.Role, message.MultiContent[0].Text))
	}
	return sb.String()
}

// GetMaxModelTokens returns the max tokens for the given model
func GetMaxModelTokens(model string) (maxTokens int) {
	switch model {
	case anthropic.ModelClaude3Haiku20240307, anthropic.ModelClaude3Sonnet20240229, anthropic.ModelClaude3Opus20240229:
		maxTokens = 200000
	case openai.GPT4:
		maxTokens = 8192
	case openai.GPT4Turbo:
		maxTokens = 128000
	case openai.GPT3Dot5Turbo0125:
		maxTokens = 16385
	default:
		maxTokens = 4096
	}
	return maxTokens
}

// IsOutputLimited returns true if the model is limited to 4096 tokens
func IsOutputLimited(model string) bool {
	switch model {
	case openai.GPT4Turbo, openai.GPT3Dot5Turbo0125, anthropic.ModelClaude3Haiku20240307, anthropic.ModelClaude3Sonnet20240229, anthropic.ModelClaude3Opus20240229:
		return true
	default:
		return false
	}
}

// GetRequestMaxTokensString returns the max tokens for the given model
func GetRequestMaxTokensString(message string, model string) (maxTokens int, err error) {
	maxTokens = GetMaxModelTokens(model)
	usedTokens := NumTokensFromString(message)

	availableTokens := maxTokens - usedTokens

	if availableTokens < 0 {
		availableTokens = 0
		err = fmt.Errorf("not enough tokens")
		return availableTokens, err
	}

	if IsOutputLimited(model) {
		availableTokens = 4096
	}

	return availableTokens, nil
}

func getRequestMaxTokens(message []openai.ChatCompletionMessage, model string) (maxTokens int, err error) {
	maxTokens = GetMaxModelTokens(model)
	usedTokens := NumTokensFromMessages(message, model)

	availableTokens := maxTokens - usedTokens

	if availableTokens < 0 {
		availableTokens = 0
		err = fmt.Errorf("not enough tokens")
		return availableTokens, err
	}

	if IsOutputLimited(model) {
		availableTokens = 4096
	}

	return availableTokens, nil
}

// NumTokensFromString returns the number of tokens in the given string
func NumTokensFromString(s string) (numTokens int) {
	encoding := "p50k_base"
	tkm, err := tiktoken.GetEncoding(encoding)
	if err != nil {
		err = fmt.Errorf("encoding for model: %v", err)
		log.Println(err)
		return
	}
	numTokens = len(tkm.Encode(s, nil, nil))

	return numTokens
}

// NumTokensFromMessages returns the number of tokens in the given messages
func NumTokensFromMessages(messages []openai.ChatCompletionMessage, model string) (numTokens int) {
	tkm, err := tiktoken.EncodingForModel(model)
	if err != nil {
		err = fmt.Errorf("encoding for model: %v", err)
		log.Println(err)
		return
	}

	var tokensPerMessage, tokensPerName int
	switch model {
	case
		"gpt-3.5-turbo-0613",
		"gpt-3.5-turbo-16k-0613",
		"gpt-3.5-turbo-1106",
		"gpt-4-0314",
		"gpt-4-32k-0314",
		"gpt-4-0613",
		"gpt-4-32k-0613",
		"gpt-4-turbo",
		"gpt-4-turbo-2024-04-09":
		tokensPerMessage = 3
		tokensPerName = 1
	case "gpt-3.5-turbo-0301":
		tokensPerMessage = 4 // every message follows <|start|>{role/name}\n{content}<|end|>\n
		tokensPerName = -1   // if there's a name, the role is omitted
	default:
		if strings.Contains(model, "gpt-3.5-turbo") {
			log.Println("warning: gpt-3.5-turbo may update over time. Returning num tokens assuming gpt-3.5-turbo-0613")
			return NumTokensFromMessages(messages, "gpt-3.5-turbo-0613")
		} else if strings.Contains(model, "gpt-4") {
			log.Println("warning: gpt-4 may update over time. Returning num tokens assuming gpt-4-0613")
			return NumTokensFromMessages(messages, "gpt-4-0613")
		} else {
			err = fmt.Errorf("num_tokens_from_messages() is not implemented for model %s. See https://github.com/openai/openai-python/blob/main/chatml.md for information on how messages are converted to tokens", model)
			log.Println(err)
			return
		}
	}

	for _, message := range messages {
		numTokens += tokensPerMessage
		numTokens += len(tkm.Encode(message.Content, nil, nil))
		numTokens += len(tkm.Encode(message.Role, nil, nil))
		numTokens += len(tkm.Encode(message.Name, nil, nil))
		if message.Name != "" {
			numTokens += tokensPerName
		}
	}
	numTokens += 3 // every reply is primed with <|start|>assistant<|message|>
	return numTokens
}

// SplitTTS takes a string and a boolean flag hd to chunk up the message into parts no longer than maxLength characters separated by newlines and return a slice of discordgo.File pointers.
func SplitTTS(message string, hd bool) []*discordgo.File {
	// Chunk up message into maxLength character chunks separated by newlines
	separator := "\n\n"
	maxLength := 4000
	var files []*discordgo.File
	var messageChunks []string

	// Split message into chunks of up to maxLength characters
	for len(message) > 0 {
		var chunk string
		if len(message) > maxLength {
			// Find the last separator before the maxLength character limit
			end := strings.LastIndex(message[:maxLength], separator)
			if end == -1 {
				// No separator found, so just cut at maxLength characters
				end = maxLength
			}
			chunk = message[:end]
			message = message[end:]
		} else {
			chunk = message
			message = ""
		}
		// Add chunk to messageChunks
		messageChunks = append(messageChunks, chunk)
	}

	var wg sync.WaitGroup
	filesChan := make(chan *discordgo.File, len(messageChunks))

	for count, chunk := range messageChunks {
		wg.Add(1)
		go func(count int, chunk string) {
			defer wg.Done()
			files := getTTSFile(chunk, fmt.Sprintf("%d", count+1), hd)
			for _, file := range files {
				filesChan <- file
			}
		}(count, chunk)
	}

	wg.Wait()
	close(filesChan)

	for file := range filesChan {
		files = append(files, file)
	}

	return files
}
