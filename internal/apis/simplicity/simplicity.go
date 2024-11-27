// Package simplicity is a package for interacting with the Stability AI API.
package simplicity

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"

	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

func DrawImage(prompt string, negativePrompt string, ratio string) ([]*discordgo.File, error) {
	stabilityToken := os.Getenv("STABILITY_TOKEN")
	if stabilityToken == "" {
		log.Fatal("STABILITY_TOKEN is not set")
	}

	url := "https://api.stability.ai/v2beta/stable-image/generate/ultra"
	ratios := []string{"16:9", "1:1", "21:9", "2:3", "3:2", "4:5", "5:4", "9:16", "9:21"}

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("prompt", prompt)
	_ = writer.WriteField("output_format", "png")
	if negativePrompt != "" && negativePrompt != "none" {
		_ = writer.WriteField("negative_prompt", negativePrompt)
	}
	if utility.MatchMultiple(ratio, ratios) {
		_ = writer.WriteField("aspect_ratio", ratio)
	}
	_ = writer.Close()

	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())
	req.Header.Add("Accept", "image/*")
	req.Header.Add("Authorization", stabilityToken)

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errInterface := make(map[string]interface{})
		err = json.NewDecoder(resp.Body).Decode(&errInterface)
		return nil, fmt.Errorf("unexpected status code: %d\n%s", resp.StatusCode, errInterface)
	}

	imageBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	files := []*discordgo.File{
		{
			Name:   "image.png",
			Reader: bytes.NewReader(imageBytes),
		},
	}

	return files, nil
}
