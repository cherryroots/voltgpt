package openai

import (
	"time"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

var functionMap = map[string]func([]any) string{
	"date-time": func(args []any) string {
		return time.Now().Format("2006-01-02 15:04:05")
	},
}

var functionDefinitions = map[string]openai.FunctionDefinition{
	"date-time": {
		Name:        "date-time",
		Description: "Get the current date and time",
		Parameters: jsonschema.Definition{
			Type:       jsonschema.Object,
			Properties: map[string]jsonschema.Definition{},
		},
		Strict: true,
	},
}

func GetTools() []openai.Tool {
	var tools []openai.Tool
	for _, tool := range functionDefinitions {
		tools = append(tools, openai.Tool{
			Type:     openai.ToolTypeFunction,
			Function: &tool,
		})
	}
	return tools
}
