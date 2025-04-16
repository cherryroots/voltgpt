package openai

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
	"strings"
	"sync"

	"voltgpt/internal/config"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

func getTTSFile(message string, index int) *discordgo.File {
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	c := openai.NewClient(openaiToken)
	ctx := context.Background()
	var model openai.SpeechModel = "gpt-4o-mini-tts"

	res, err := c.CreateSpeech(ctx, openai.CreateSpeechRequest{
		Model: model,
		Input: message,
		Voice: "sage",
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

	file := &discordgo.File{
		Name:   fmt.Sprintf("%d-%s.mp3", index+1, filename),
		Reader: strings.NewReader(string(buf)),
	}

	return file
}

func getFilenameSummary(message string) string {
	openaiToken := os.Getenv("OPENAI_TOKEN")
	if openaiToken == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	c := openai.NewClient(openaiToken)
	ctx := context.Background()

	prompt := "Summarize this text as a filename but without a file extension: " + message

	maxTokens := 20

	req := openai.ChatCompletionRequest{
		Model:       openai.GPT3Dot5Turbo0125,
		Messages:    createMessage(openai.ChatMessageRoleUser, "", config.RequestContent{Text: prompt}),
		Temperature: float32(config.DefaultTemp),
		MaxTokens:   maxTokens,
	}

	resp, err := c.CreateChatCompletion(ctx, req)
	if err != nil {
		log.Printf("CreateCompletion error: %v\n", err)
		return "file"
	}

	return resp.Choices[0].Message.Content
}

func SplitTTS(message string) []*discordgo.File {
	separator := "\n\n"
	maxLength := 4000
	type fileIndex struct {
		file  *discordgo.File
		index int
	}
	var fileIndexes []fileIndex
	var messageChunks []string

	for len(message) > 0 {
		var chunk string
		if len(message) > maxLength {
			// Find the last separator before the maxLength character limit
			end := strings.LastIndex(message[:maxLength], separator)
			if end == -1 {
				end = maxLength
			}
			chunk = message[:end]
			message = message[end:]
		} else {
			chunk = message
			message = ""
		}
		messageChunks = append(messageChunks, chunk)
	}

	var wg sync.WaitGroup

	for count, chunk := range messageChunks {
		wg.Add(1)
		go func(count int, chunk string) {
			defer wg.Done()
			file := getTTSFile(chunk, count+1)
			fileIndexes = append(fileIndexes, fileIndex{file: file, index: count + 1})
		}(count, chunk)
	}

	wg.Wait()

	sort.Slice(fileIndexes, func(i, j int) bool { return fileIndexes[i].index < fileIndexes[j].index })

	var filePointers []*discordgo.File
	for _, file := range fileIndexes {
		filePointers = append(filePointers, file.file)
	}

	return filePointers
}
