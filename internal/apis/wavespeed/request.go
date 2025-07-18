package wavespeed

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"voltgpt/internal/utility"

	"github.com/bwmarrin/discordgo"
)

// generateSeedDanceModelType generates a modeltype for seeddance using the template
// and the provided version, type, and resolution
func generateSeedDanceModelType(version SeedDanceVersion, dreamType SeedDanceType, resolution SeedDanceResolution) ModelType {
	template := string(SeedDanceTemplate)
	// Replace placeholders in template: "seedance-v1-%1-%2-%3"
	modelTypeStr := strings.ReplaceAll(template, "%1", string(version))
	modelTypeStr = strings.ReplaceAll(modelTypeStr, "%2", string(dreamType))
	modelTypeStr = strings.ReplaceAll(modelTypeStr, "%3", string(resolution))
	return ModelType(modelTypeStr)
}

// sendWaveSpeedRequest sends a request to the wavespeed API
// using the base URL, model family, and modeltype
func sendWaveSpeedRequest(family ModelFamily, modelType ModelType, payload interface{}) (*WaveSpeedResponse, error) {
	// Get API key from environment
	apiKey := os.Getenv("WAVESPEED_TOKEN")
	if apiKey == "" {
		return nil, fmt.Errorf("WAVESPEED_TOKEN environment variable is not set")
	}

	// Construct the full URL
	url := fmt.Sprintf("%s/%s/%s", baseURL, string(family), string(modelType))

	// Marshal the payload to JSON
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request payload: %w", err)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Header.Set("User-Agent", "VoltGPT/1.0")

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 60 * time.Second,
	}

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response JSON
	var waveSpeedResp WaveSpeedResponse
	if err := json.Unmarshal(body, &waveSpeedResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &waveSpeedResp, nil
}

// SendSeedDreamRequest sends a request for SeedDream image generation
func SendSeedDreamRequest(request SeedDreamSubmissionRequest) (*WaveSpeedResponse, error) {
	return sendWaveSpeedRequest(BytedanceModels, SeedDreamModel, request)
}

// SendSeedDanceT2VRequest sends a request for SeedDance Text-to-Video generation
func SendSeedDanceT2VRequest(request SeedDanceT2VSubmissionRequest, version SeedDanceVersion, dreamType SeedDanceType, resolution SeedDanceResolution) (*WaveSpeedResponse, error) {
	modelType := generateSeedDanceModelType(version, dreamType, resolution)
	return sendWaveSpeedRequest(BytedanceModels, modelType, request)
}

// SendSeedDanceI2VRequest sends a request for SeedDance Image-to-Video generation
// The request.Image should contain a base64 encoded image
func SendSeedDanceI2VRequest(request SeedDanceI2VSubmissionRequest, version SeedDanceVersion, dreamType SeedDanceType, resolution SeedDanceResolution) (*WaveSpeedResponse, error) {
	modelType := generateSeedDanceModelType(version, dreamType, resolution)
	return sendWaveSpeedRequest(BytedanceModels, modelType, request)
}

// QueryWaveSpeedResult queries the result of a wavespeed request by ID
func QueryWaveSpeedResult(id string) (*WaveSpeedResponse, error) {
	// Get API key from environment
	apiKey := os.Getenv("WAVESPEED_TOKEN")
	if apiKey == "" {
		return nil, fmt.Errorf("WAVESPEED_TOKEN environment variable is not set")
	}

	// Construct the query URL
	url := fmt.Sprintf("%s/predictions/%s/result", baseURL, id)

	// Create HTTP request
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Set headers
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
	req.Header.Set("User-Agent", "VoltGPT/1.0")

	// Create HTTP client with timeout
	client := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP request failed with status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response JSON
	var waveSpeedResp WaveSpeedResponse
	if err := json.Unmarshal(body, &waveSpeedResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	return &waveSpeedResp, nil
}

func WaitForComplete(id string) (*WaveSpeedResponse, error) {
	maxRetries := 100
	retryDelay := 3 * time.Second

	for i := 0; i < maxRetries; i++ {
		resp, err := QueryWaveSpeedResult(id)
		if err != nil {
			return nil, fmt.Errorf("failed to query result: %w", err)
		}

		if resp.Data.Status == "completed" {
			return resp, nil
		}

		if resp.Data.Status == "failed" {
			return nil, fmt.Errorf("task failed: %s", resp.Data.Error)
		}

		// Wait before retrying
		time.Sleep(retryDelay)
	}

	return nil, fmt.Errorf("timed out waiting for task completion after %d attempts (%d seconds)", maxRetries, maxRetries*3)
}

func DownloadResult(resp *WaveSpeedResponse) ([]*discordgo.File, error) {
	files := make([]*discordgo.File, len(resp.Data.Outputs))
	for i, output := range resp.Data.Outputs {
		data, err := utility.DownloadURL(output)
		if err != nil {
			return nil, err
		}
		ext, err := utility.UrlToExt(output)
		if err != nil {
			return nil, err
		}
		filename := fmt.Sprintf("file_%d.%s", i, ext)
		files[i] = &discordgo.File{
			Name:   filename,
			Reader: bytes.NewReader(data),
		}
	}
	return files, nil
}

func IsImageURL(urlStr string) bool {
	fileExt, err := utility.UrlToExt(urlStr)
	if err != nil {
		return false
	}

	switch fileExt {
	case ".jpg", ".jpeg", ".png":
		return true
	default:
		return false
	}
}
