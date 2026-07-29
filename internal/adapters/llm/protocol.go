package llm

import "encoding/json"

type openAIRequest struct {
	Model          string               `json:"model"`
	Messages       []Message            `json:"messages"`
	ResponseFormat openAIResponseFormat `json:"response_format"`
	Stream         bool                 `json:"stream"`
}

type openAIResponseFormat struct {
	Type       string           `json:"type"`
	JSONSchema openAIJSONSchema `json:"json_schema"`
}

type openAIJSONSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

type openAIResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []Message       `json:"messages"`
	Format   json.RawMessage `json:"format"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options"`
}

type ollamaOptions struct {
	Temperature int `json:"temperature"`
}

type ollamaResponse struct {
	Message struct {
		Content string `json:"content"`
	} `json:"message"`
}
