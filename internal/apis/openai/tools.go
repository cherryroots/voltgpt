package openai

import (
	"strings"
	"time"
	"voltgpt/internal/apis/codapi"
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
	"browse_multiple": func(args map[string]any) string {
		urls := args["urls"].([]any)
		urlsString := make([]string, len(urls))
		for i, url := range urls {
			urlsString[i] = url.(string)
		}
		if args["render_js"] == nil {
			return scrapfly.BrowseMultiple(urlsString, false)
		}
		return scrapfly.BrowseMultiple(urlsString, args["render_js"].(bool))
	},
	"code_execution": func(args map[string]any) string {
		response, err := codapi.ExecuteRequest(&codapi.Request{
			Sandbox: args["sandbox"].(string),
			Command: "run",
			Files:   map[string]string{"": strings.TrimSpace(args["code"].(string))},
		})
		if err != nil {
			return ""
		}

		if response.OK {
			return response.Stdout
		}

		return response.Stderr
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
	"browse_multiple": {
		Name:        "browse_multiple",
		Description: "Browse multiple URLs and return the content as markdown with links to pages.",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"urls": {
					Type:        jsonschema.Array,
					Description: "The URLs to browse, should be an array of strings",
				},
				"render_js": {
					Type:        jsonschema.Boolean,
					Description: "Render JavaScript, useful for sites serving dynamic content. Use if you expect the page to be dynamic.",
				},
			},
			Required: []string{"urls"},
		},
		Strict: true,
	},
	"code_execution": {
		Name:        "code_execution",
		Description: "Execute code and return the output, prefer python for general tasks",
		Parameters: jsonschema.Definition{
			Type: jsonschema.Object,
			Properties: map[string]jsonschema.Definition{
				"code": {
					Type:        jsonschema.String,
					Description: "The code to execute, don't add newlines to the start or end",
				},
				"sandbox": {
					Type:        jsonschema.String,
					Description: "The sandbox to use, select for the language you want to execute",
					Enum:        []string{"python", "javascript", "typescript", "shell", "rust", "odin"},
				},
			},
			Required: []string{"code", "sandbox"},
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
