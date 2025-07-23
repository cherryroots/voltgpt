package openai

import (
	"time"
	"voltgpt/internal/apis/scrapfly"

	"github.com/sashabaranov/go-openai"
	"github.com/sashabaranov/go-openai/jsonschema"
)

var functionMap = map[string]func(map[string]any) string{
	"date-time": func(args map[string]any) string {
		return time.Now().Format("2006-01-02 15:04:05")
	},
	"browse": func(args map[string]any) string {
		if args["render_js"] == nil {
			return scrapfly.Browse(args["url"].(string), false)
		}
		return scrapfly.Browse(args["url"].(string), args["render_js"].(bool))
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
	"browse": {
		Name:        "browse",
		Description: "Browse the web and return the content as markdown with links to pages.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"url": {
					Type:        jsonschema.String,
					Description: "The URL to browse",
				},
				"render_js": {
					Type:        jsonschema.Boolean,
					Description: "Render JavaScript, useful for sites serving dynamic content. Use if you expect the page to be dynamic.",
				},
			},
			Required: []string{"url"},
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
