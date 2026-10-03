package openai

import (
	"fmt"
	"log"
	"net/url"
	"os"
	"strings"
	"sync"

	oa "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

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
			log.Printf("openai: chat client using base URL %s", safeBaseURLForLog(baseURL))
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

func safeBaseURLForLog(baseURL string) string {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "<custom endpoint>"
	}
	return parsed.Scheme + "://" + parsed.Host
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
