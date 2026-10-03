package openai

import (
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	oa "github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

const (
	chatModel       = "gpt-6.1-sol"
	reasoningEffort = responses.ReasoningEffortMedium
	serviceTier     = responses.ResponseServiceTierFast
)

// ChatRequest describes one stored chat turn sent to the Responses API.
type ChatRequest struct {
	Input              []responses.ResponseInputItemUnionParam
	Instructions       string
	PreviousResponseID string
	PromptCacheKey     string
}

// StreamChat streams a chat response, calling onDelta with each piece of
// output text, and returns the completed response.
func StreamChat(ctx context.Context, c *oa.Client, req ChatRequest, onDelta func(string)) (responses.Response, error) {
	if err := ctx.Err(); err != nil {
		return responses.Response{}, err
	}
	if len(req.Input) == 0 {
		return responses.Response{}, fmt.Errorf("no messages to send")
	}

	params := responses.ResponseNewParams{
		Input: responses.ResponseNewParamsInputUnion{
			OfInputItemList: responses.ResponseInputParam(req.Input),
		},
		Instructions:      oa.String(req.Instructions),
		Metadata:          ResponseMetadata("chat"),
		Model:             responses.ChatModel(chatModel),
		ServiceTier:       responses.ResponseNewParamsServiceTier(serviceTier),
		Store:             oa.Bool(true),
		Reasoning:         shared.ReasoningParam{Effort: reasoningEffort},
		Text:              responses.ResponseTextConfigParam{Verbosity: responses.ResponseTextConfigVerbosityLow},
		Truncation:        responses.ResponseNewParamsTruncationAuto,
		ParallelToolCalls: oa.Bool(true),
		ToolChoice: responses.ResponseNewParamsToolChoiceUnion{
			OfToolChoiceMode: param.NewOpt(responses.ToolChoiceOptionsAuto),
		},
		Tools: builtInTools(),
	}
	if req.PromptCacheKey != "" {
		params.PromptCacheKey = oa.String(req.PromptCacheKey)
	}
	if req.PreviousResponseID != "" {
		params.PreviousResponseID = oa.String(req.PreviousResponseID)
	}

	stream := c.Responses.NewStreaming(ctx, params)

	var completed responses.Response
	for stream.Next() {
		switch e := stream.Current().AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			onDelta(e.Delta)
		case responses.ResponseRefusalDeltaEvent:
			onDelta(e.Delta)
		case responses.ResponseCompletedEvent:
			completed = e.Response
		case responses.ResponseErrorEvent:
			return responses.Response{}, fmt.Errorf("openai response error: %s", e.Message)
		case responses.ResponseFailedEvent:
			return responses.Response{}, fmt.Errorf("openai response failed: status=%s", e.Response.Status)
		}
	}
	if err := stream.Err(); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return responses.Response{}, ctxErr
		}
		return responses.Response{}, fmt.Errorf("stream error: %w", err)
	}
	if completed.ID == "" {
		return responses.Response{}, fmt.Errorf("missing response ID from OpenAI stream")
	}
	return completed, nil
}

func builtInTools() []responses.ToolUnionParam {
	return []responses.ToolUnionParam{
		responses.ToolParamOfWebSearch("web_search"),
		responses.ToolParamOfCodeInterpreter(responses.ToolCodeInterpreterContainerCodeInterpreterContainerAutoParam{}),
	}
}

// Artifact is a file the code interpreter generated and cited in a response.
type Artifact struct {
	ContainerID string
	FileID      string
	Filename    string
}

// GeneratedArtifacts returns the unique container files cited in a response.
func GeneratedArtifacts(response responses.Response) []Artifact {
	artifacts := make([]Artifact, 0)
	seen := make(map[string]struct{})

	for _, output := range response.Output {
		if output.Type != "message" {
			continue
		}
		for _, content := range output.Content {
			if content.Type != "output_text" {
				continue
			}
			for _, annotation := range content.Annotations {
				if annotation.Type != "container_file_citation" || annotation.ContainerID == "" || annotation.FileID == "" {
					continue
				}

				key := annotation.ContainerID + "\x00" + annotation.FileID
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				artifacts = append(artifacts, Artifact{
					ContainerID: annotation.ContainerID,
					FileID:      annotation.FileID,
					Filename:    annotation.Filename,
				})
			}
		}
	}

	return artifacts
}

// Name returns the artifact's base filename, falling back to its file ID.
func (a Artifact) Name() string {
	filename := path.Base(strings.ReplaceAll(strings.TrimSpace(a.Filename), "\\", "/"))
	if filename == "" || filename == "." || filename == "/" {
		return a.FileID
	}
	return filename
}

// DownloadArtifact opens the artifact's content. The caller closes the body.
func DownloadArtifact(ctx context.Context, c *oa.Client, a Artifact) (body io.ReadCloser, contentType string, err error) {
	response, err := c.Containers.Files.Content.Get(ctx, a.ContainerID, a.FileID)
	if err != nil {
		return nil, "", fmt.Errorf("download generated file %s: %w", a.FileID, err)
	}
	contentType = response.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	return response.Body, contentType, nil
}

// Image is base64-encoded image data for an input message.
type Image struct {
	MIME string
	Data string
}

// InputMessage builds a chat input message. Images are dropped from assistant
// messages because the API only accepts them from users.
func InputMessage(role, text string, images []Image) responses.ResponseInputItemUnionParam {
	var parts responses.ResponseInputMessageContentListParam

	if strings.TrimSpace(text) != "" {
		parts = append(parts, responses.ResponseInputContentParamOfInputText(text))
	}

	if role != "assistant" {
		for _, image := range images {
			part := responses.ResponseInputContentParamOfInputImage(responses.ResponseInputImageDetailAuto)
			part.OfInputImage.ImageURL = param.NewOpt(fmt.Sprintf("data:%s;base64,%s", image.MIME, image.Data))
			parts = append(parts, part)
		}
	}

	if len(parts) == 0 {
		parts = append(parts, responses.ResponseInputContentParamOfInputText("(unsupported content omitted)"))
	}

	return responses.ResponseInputItemParamOfMessage(parts, toRole(role))
}

func toRole(role string) responses.EasyInputMessageRole {
	switch role {
	case "assistant", "model":
		return responses.EasyInputMessageRoleAssistant
	default:
		return responses.EasyInputMessageRoleUser
	}
}
