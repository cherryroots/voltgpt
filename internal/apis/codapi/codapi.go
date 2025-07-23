package codapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
)

type Request struct {
	Sandbox string            `json:"sandbox"`
	Command string            `json:"command"`
	Files   map[string]string `json:"files"`
}

type Response struct {
	ID       string `json:"id"`
	OK       bool   `json:"ok"`
	Duration int    `json:"duration"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
}

func (r *Request) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

func (r *Response) FromJSON(data []byte) error {
	return json.Unmarshal(data, r)
}

func ExecuteRequest(request *Request) (*Response, error) {
	u := "http://localhost:1313/v1/exec"

	jsonData, err := request.ToJSON()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", u, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response Response
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, err
	}

	return &response, nil
}
