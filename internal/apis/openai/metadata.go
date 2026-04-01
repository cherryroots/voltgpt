package openai

import "github.com/openai/openai-go/shared"

func ResponseMetadata(responseType string) shared.Metadata {
	return shared.Metadata{
		"response_type": responseType,
	}
}
