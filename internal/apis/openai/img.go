package openai

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
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
		Model:      openai.CreateImageModelGptImage1,
		Moderation: "low",
		Prompt:     prompt,
		Size:       string(resolution),
		Quality:    string(quality),
		N:          2,
	}

	res, err := c.CreateImage(ctx, req)
	if err != nil {
		return nil, err
	}

	files := make([]*discordgo.File, len(res.Data))

	for i, image := range res.Data {
		b, err := base64.StdEncoding.DecodeString(image.B64JSON)
		if err != nil {
			return nil, err
		}

		files[i] = &discordgo.File{
			Name:   fmt.Sprintf("%d.png", i+1),
			Reader: bytes.NewReader(b),
		}
	}

	return files, nil
}
