package scrapfly

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"

	"voltgpt/internal/utility"

	"jaytaylor.com/html2text"
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
	replacementStrings := []string{"https://", "http://", "www.", "https.", "http.", "https", "http", "www"}
	cleanURL := utility.ReplaceMultiple(u, replacementStrings, "")
	if cleanURL == "" {
		return ""
	}
	cleanURL = "https://" + cleanURL

	encodedURL := url.QueryEscape(cleanURL)
	var reqURL string
	format := url.QueryEscape("clean_html")
	if renderJS {
		reqURL = fmt.Sprintf("%s?format=%s&cache=true&lang=en&asp=true&render_js=true&auto_scroll=true&key=%s&url=%s", baseURL, format, token, encodedURL)
	} else {
		reqURL = fmt.Sprintf("%s?format=%s&cache=true&lang=en&asp=true&key=%s&url=%s", baseURL, format, token, encodedURL)
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
	body, err := io.ReadAll(res.Body)
	if err != nil {
		log.Println(err)
		return err.Error()
	}

	var response ScrapflyResponse
	err = json.Unmarshal(body, &response)
	if err != nil {
		log.Println(err)
		return err.Error()
	}
	if response.Result.StatusCode != 200 {
		log.Println(response.Result.StatusCode)
		return fmt.Sprintf("Scrapfly returned status code %d", response.Result.StatusCode)
	}

	text, err := html2text.FromString(response.Result.Content, html2text.Options{PrettyTables: true})
	if err != nil {
		log.Println(err)
		return err.Error()
	}

	return text
}
