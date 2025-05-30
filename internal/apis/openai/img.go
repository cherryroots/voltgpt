package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"
	"os"

	cfg "voltgpt/internal/config"

	"github.com/bwmarrin/discordgo"
	"github.com/sashabaranov/go-openai"
)

func DrawImage(prompt string, resolution cfg.Resolution, quality cfg.Quality) ([]*discordgo.File, error) {
	token := os.Getenv("OPENAI_TOKEN")
	if token == "" {
		log.Fatal("OPENAI_TOKEN is not set")
	}
	c := openai.NewClient(token)
	ctx := context.Background()

	req := openai.ImageRequest{
		Model:   openai.CreateImageModelGptImage1,
		Prompt:  prompt,
		Size:    string(resolution),
		Quality: string(quality),
	}

	res, err := c.CreateImage(ctx, req)
	if err != nil {
		return nil, err
	}

	b, err := base64.StdEncoding.DecodeString(res.Data[0].B64JSON)
	if err != nil {
		return nil, err
	}

	file := &discordgo.File{
		Name:   "image.png",
		Reader: bytes.NewReader(b),
	}

	return []*discordgo.File{file}, nil
}
