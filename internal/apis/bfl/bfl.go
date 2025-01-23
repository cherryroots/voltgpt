// Package bfl is a package for interacting with the BFL API.
package bfl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

type statusResponse string

const (
	statusTaskNotFound     statusResponse = "Task not found"
	statusPending          statusResponse = "Pending"
	statusRequestModerated statusResponse = "Request Moderated"
	statusContentModerated statusResponse = "Content Moderated"
	statusReady            statusResponse = "Ready"
	statusError            statusResponse = "Error"
)

type resultResponse struct {
	ID     string         `json:"id"`
	Status statusResponse `json:"status"`
	Result interface{}    `json:"result,omitempty"`
}

type asyncResponse struct {
	ID string `json:"id"`
}

func getResult(id string) (string, error) {
	bflToken := os.Getenv("BFL_TOKEN")
	if bflToken == "" {
		log.Fatal("BFL_TOKEN is not set")
	}
	url := "https://api.bfl.ml/v1/get_result?id=" + id

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}

	req.Header.Add("X-Key", bflToken)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	var result resultResponse
	err = json.NewDecoder(res.Body).Decode(&result)
	if err != nil {
		return "", err
	}

	if result.Status == statusPending {
		return "", nil
	}

	if result.Status != statusReady {
		return "", fmt.Errorf("Unexpected image status: %s", result.Status)
	}

	return result.Result.(map[string]interface{})["sample"].(string), nil
}

// DrawImage makes a request to the BFL API to generate an image
func DrawImage(prompt string, aspectRatio string, raw string, image string) ([]*discordgo.File, error) {
	bflToken := os.Getenv("BFL_TOKEN")
	if bflToken == "" {
		log.Fatal("BFL_TOKEN is not set")
	}

	url := "https://api.bfl.ml/v1/flux-pro-1.1-ultra"
	ratios := []string{"16:9", "1:1", "21:9", "2:3", "3:2", "4:5", "5:4", "9:16", "9:21"}

	payload := map[string]string{
		"prompt":           prompt,
		"output_format":    "png",
		"safety_tolerance": "6",
	}
	if utility.MatchMultiple(aspectRatio, ratios) {
		payload["aspect_ratio"] = aspectRatio
	}
	if raw != "false" {
		payload["raw"] = raw
	}
	if image != "" {
		payload["image_prompt"] = image
	}

	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("X-Key", bflToken)

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

	// unmarshal into struct
	var result asyncResponse
	err = json.NewDecoder(resp.Body).Decode(&result)
	if err != nil {
		return nil, err
	}

	var sample string

	for {
		sample, err = getResult(result.ID)
		if err != nil {
			return nil, err
		}
		if sample != "" {
			break
		}
		time.Sleep(time.Second)
	}

	data, err := utility.DownloadURL(sample)
	if err != nil {
		return nil, err
	}

	files := make([]*discordgo.File, 1)
	files[0] = &discordgo.File{
		Name:   "image.png",
		Reader: bytes.NewReader(data),
	}

	return files, nil
}
