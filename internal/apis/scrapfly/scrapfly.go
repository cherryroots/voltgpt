package scrapfly

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sync"
	"voltgpt/internal/utility"
)

const (
	baseURL = "https://api.scrapfly.io/scrape"
)

type ScrapflyResponse struct {
	Result struct {
		Content    string `json:"content"`
		StatusCode int    `json:"status_code"`
	} `json:"result"`
}

func Browse(u string, renderJS bool) string {
	token := os.Getenv("SCRAPFLY_TOKEN")
	replacementStrings := []string{"https://", "http://", "www.", "http", "https", "www."}
	cleanURL := utility.ReplaceMultiple(u, replacementStrings, "")
	if cleanURL == "" {
		return ""
	}
	cleanURL = "https://" + cleanURL
	log.Printf("Cleaned URL: %s to %s", u, cleanURL)

	encodedUrl := url.QueryEscape(cleanURL)
	var reqURL string
	format := url.QueryEscape("markdown:only_content")
	if renderJS {
		reqURL = fmt.Sprintf("%s?format=%s&cache=true&lang=en&asp=true&render_js=true&auto_scroll=true&key=%s&url=%s", baseURL, format, token, encodedUrl)
	} else {
		reqURL = fmt.Sprintf("%s?format=%s&cache=true&lang=en&asp=true&key=%s&url=%s", baseURL, format, token, encodedUrl)
	}
	method := "GET"
	client := &http.Client{}
	req, err := http.NewRequest(method, reqURL, nil)
	if err != nil {
		log.Println(err)
		return ""
	}
	res, err := client.Do(req)
	if err != nil {
		log.Println(err)
		return ""
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		log.Println(res.StatusCode)
		return ""
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return ""
	}

	var response ScrapflyResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		log.Println(err)
		return ""
	}
	if response.Result.StatusCode != 200 {
		log.Println(response.Result.StatusCode)
		return ""
	}
	return response.Result.Content
}

func BrowseMultiple(urls []string, renderJS bool) string {
	var content string
	var wg sync.WaitGroup
	wg.Add(len(urls))
	for i, u := range urls {
		go func(u string, i int) {
			defer wg.Done()
			content += fmt.Sprintf("%d. %s\n\n", i+1, Browse(u, renderJS))
		}(u, i)
	}
	wg.Wait()
	return content
}
