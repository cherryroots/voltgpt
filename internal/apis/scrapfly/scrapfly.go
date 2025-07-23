package scrapfly

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
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
	encoded_url := url.QueryEscape(u)
	var reqURL string
	format := url.QueryEscape("markdown:only_content")
	if renderJS {
		reqURL = fmt.Sprintf("%s?format=%s&cache=true&lang=en&asp=true&render_js=true&auto_scroll=true&key=%s&url=%s", baseURL, format, token, encoded_url)
	} else {
		reqURL = fmt.Sprintf("%s?format=%s&cache=true&lang=en&asp=true&key=%s&url=%s", baseURL, format, token, encoded_url)
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
