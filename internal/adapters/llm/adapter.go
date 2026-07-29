// Package llm implements the two MVP model Provider protocols without
// persisting or rendering secrets.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

const (
	defaultTimeout   = 30 * time.Second
	maxResponseBytes = 4 << 20
)

var schemaNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// Role identifies a model conversation participant.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// Message is one submitted model message.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

// Request asks a Provider for one JSON object matching Schema.
type Request struct {
	SchemaName string
	Schema     json.RawMessage
	Messages   []Message
}

// Generator is the structured-byte boundary consumed by core workflows.
type Generator interface {
	Generate(context.Context, Request) ([]byte, error)
}

// SecretResolver reads an API key by environment-variable name.
type SecretResolver func(string) (string, bool)

// Options injects transport, timeout, and secret lookup for deterministic
// tests. Secrets are never copied into exported state.
type Options struct {
	HTTPClient    *http.Client
	Timeout       time.Duration
	ResolveSecret SecretResolver
}

// Client is one configured OpenAI-compatible or Ollama adapter.
type Client struct {
	config        config.LLM
	httpClient    *http.Client
	timeout       time.Duration
	resolveSecret SecretResolver
}

// New validates non-secret Provider configuration.
func New(providerConfig config.LLM, options Options) (*Client, error) {
	if err := validateProviderConfig(providerConfig); err != nil {
		return nil, err
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}
	resolvedHTTPClient := *httpClient
	if resolvedHTTPClient.Timeout <= 0 ||
		resolvedHTTPClient.Timeout > timeout {
		resolvedHTTPClient.Timeout = timeout
	}
	resolver := options.ResolveSecret
	if resolver == nil {
		resolver = func(string) (string, bool) { return "", false }
	}
	return &Client{
		config:        providerConfig,
		httpClient:    &resolvedHTTPClient,
		timeout:       timeout,
		resolveSecret: resolver,
	}, nil
}

// Generate performs one non-streaming structured request.
func (client *Client) Generate(
	ctx context.Context,
	request Request,
) ([]byte, error) {
	if client == nil {
		return nil, providerError(
			domainerr.CodeDependencyUnavailable,
			"generate structured output",
			"模型 Provider 尚未初始化。",
			"打开设置并重新测试连接。",
			true,
			errors.New("nil LLM client"),
		)
	}
	if err := validateRequest(request); err != nil {
		return nil, err
	}

	var (
		target  string
		payload []byte
		err     error
	)
	switch client.config.Provider {
	case config.ProviderOpenAICompatible:
		target = joinEndpoint(client.config.Endpoint, "chat/completions")
		payload, err = json.Marshal(openAIRequest{
			Model:    client.config.Model,
			Messages: request.Messages,
			ResponseFormat: openAIResponseFormat{
				Type: "json_schema",
				JSONSchema: openAIJSONSchema{
					Name:   request.SchemaName,
					Schema: request.Schema,
					Strict: true,
				},
			},
			Stream: false,
		})
	case config.ProviderOllama:
		target = joinEndpoint(client.config.Endpoint, "api/chat")
		payload, err = json.Marshal(ollamaRequest{
			Model:    client.config.Model,
			Messages: request.Messages,
			Format:   request.Schema,
			Stream:   false,
			Options:  ollamaOptions{Temperature: 0},
		})
	default:
		return nil, invalidProviderConfiguration("provider is unsupported")
	}
	if err != nil {
		return nil, providerError(
			domainerr.CodeValidation,
			"encode Provider request",
			"无法编码模型请求。",
			"检查结构化 Schema 后重试。",
			false,
			err,
		)
	}

	response, err := client.doJSON(ctx, http.MethodPost, target, payload)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK ||
		response.StatusCode >= http.StatusMultipleChoices {
		return nil, client.generationStatusError(response.StatusCode)
	}
	body, err := readLimited(response.Body)
	if err != nil {
		return nil, providerError(
			domainerr.CodeDependencyUnavailable,
			"read Provider response",
			"无法读取模型响应。",
			"检查 Provider 后重试。",
			true,
			err,
		)
	}
	return client.decodeContent(body)
}

// GenerateStructured validates the model result and retries one time only for
// a Schema violation. Transport failures are never duplicated.
func GenerateStructured[T any](
	ctx context.Context,
	generator Generator,
	request Request,
	decode contracts.Decoder[T],
) (T, error) {
	if generator == nil {
		var zero T
		return zero, providerError(
			domainerr.CodeDependencyUnavailable,
			"generate structured output",
			"模型 Provider 尚未配置。",
			"打开设置并配置 Provider。",
			true,
			errors.New("nil structured generator"),
		)
	}
	if decode == nil {
		var zero T
		return zero, providerError(
			domainerr.CodeValidation,
			"generate structured output",
			"结构化输出校验器不能为空。",
			"选择已发布的 Agent 契约后重试。",
			false,
			errors.New("nil structured decoder"),
		)
	}

	return contracts.DecodeWithSchemaRetry(
		func(attempt int) ([]byte, error) {
			current := cloneRequest(request)
			if attempt > 0 {
				current.Messages = append(current.Messages, Message{
					Role: RoleSystem,
					Content: "The previous response did not match the required JSON Schema. " +
						"Return only one corrected JSON object.",
				})
			}
			return generator.Generate(ctx, current)
		},
		decode,
	)
}

func (client *Client) doJSON(
	ctx context.Context,
	method string,
	target string,
	payload []byte,
) (*http.Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(
		ctx,
		method,
		target,
		body,
	)
	if err != nil {
		return nil, providerError(
			domainerr.CodeValidation,
			"create Provider request",
			"Provider endpoint 无效。",
			"修正 endpoint 后重试。",
			false,
			err,
		)
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if client.config.Provider == config.ProviderOpenAICompatible {
		secret, ok := client.resolveSecret(client.config.APIKeyEnv)
		if !ok || strings.TrimSpace(secret) == "" {
			return nil, providerError(
				domainerr.CodeDependencyUnavailable,
				"authenticate Provider request",
				"OpenAI-compatible Provider 缺少认证信息。",
				"设置 "+client.config.APIKeyEnv+" 后重试。",
				true,
				errors.New("configured API key environment variable is empty"),
			)
		}
		request.Header.Set("Authorization", "Bearer "+secret)
	}

	response, err := client.httpClient.Do(request)
	if err == nil {
		return response, nil
	}
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return nil, providerError(
			domainerr.CodeOperationCancelled,
			"call Provider",
			"模型请求已取消。",
			"输入和历史已保留，可在需要时重试。",
			true,
			err,
		)
	case errors.Is(ctx.Err(), context.DeadlineExceeded),
		errors.Is(err, context.DeadlineExceeded):
		return nil, providerError(
			domainerr.CodeDependencyUnavailable,
			"call Provider",
			"模型请求超时。",
			"检查 Provider 状态或增加超时后重试。",
			true,
			err,
		)
	default:
		return nil, providerError(
			domainerr.CodeDependencyUnavailable,
			"call Provider",
			"无法连接模型 Provider。",
			"检查 endpoint 和服务状态后重试。",
			true,
			err,
		)
	}
}

func (client *Client) decodeContent(body []byte) ([]byte, error) {
	var content string
	switch client.config.Provider {
	case config.ProviderOpenAICompatible:
		var response openAIResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, invalidProviderResponse(err)
		}
		if len(response.Choices) != 1 {
			return nil, invalidProviderResponse(
				fmt.Errorf("choices count is %d", len(response.Choices)),
			)
		}
		content = response.Choices[0].Message.Content
	case config.ProviderOllama:
		var response ollamaResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, invalidProviderResponse(err)
		}
		content = response.Message.Content
	}
	if strings.TrimSpace(content) == "" {
		return nil, invalidProviderResponse(errors.New("response content is blank"))
	}
	return []byte(content), nil
}

func (client *Client) generationStatusError(status int) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return providerError(
			domainerr.CodeDependencyUnavailable,
			"authenticate Provider request",
			"模型 Provider 认证失败。",
			"检查 API Key 环境变量后重试。",
			true,
			fmt.Errorf("HTTP %d", status),
		)
	case http.StatusNotFound:
		return providerError(
			domainerr.CodeDependencyUnavailable,
			"select Provider model",
			"配置的模型不可用。",
			"检查 model 名称后重新测试连接。",
			true,
			fmt.Errorf("HTTP %d", status),
		)
	default:
		return providerError(
			domainerr.CodeDependencyUnavailable,
			"call Provider",
			fmt.Sprintf("模型 Provider 返回 HTTP %d。", status),
			"检查 endpoint、服务状态和限流后重试。",
			true,
			fmt.Errorf("HTTP %d", status),
		)
	}
}

func validateProviderConfig(providerConfig config.LLM) error {
	if providerConfig.Provider != config.ProviderOpenAICompatible &&
		providerConfig.Provider != config.ProviderOllama {
		return invalidProviderConfiguration(
			"provider must be openai-compatible or ollama",
		)
	}
	if strings.TrimSpace(providerConfig.Endpoint) == "" {
		return invalidProviderConfiguration("endpoint is blank")
	}
	parsed, err := url.ParseRequestURI(providerConfig.Endpoint)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" ||
		parsed.User != nil ||
		parsed.RawQuery != "" ||
		parsed.Fragment != "" {
		return invalidProviderConfiguration("endpoint must be an http(s) URL")
	}
	if strings.TrimSpace(providerConfig.Model) == "" {
		return invalidProviderConfiguration("model is blank")
	}
	if providerConfig.Provider == config.ProviderOpenAICompatible &&
		strings.TrimSpace(providerConfig.APIKeyEnv) == "" {
		return invalidProviderConfiguration("API key environment reference is blank")
	}
	return nil
}

func validateRequest(request Request) error {
	if !schemaNamePattern.MatchString(request.SchemaName) {
		return providerError(
			domainerr.CodeValidation,
			"validate Provider request",
			"结构化 Schema 名称无效。",
			"使用 1–64 位字母、数字、连字符或下划线。",
			false,
			errors.New("invalid schema name"),
		)
	}
	schema := bytes.TrimSpace(request.Schema)
	if !json.Valid(schema) || len(schema) == 0 || schema[0] != '{' {
		return providerError(
			domainerr.CodeValidation,
			"validate Provider request",
			"结构化 Schema 必须是 JSON 对象。",
			"使用已发布的 Agent JSON Schema 后重试。",
			false,
			errors.New("invalid JSON Schema"),
		)
	}
	if len(request.Messages) == 0 {
		return providerError(
			domainerr.CodeValidation,
			"validate Provider request",
			"模型消息不能为空。",
			"提供明确的系统指令和用户输入后重试。",
			false,
			errors.New("messages are empty"),
		)
	}
	for _, message := range request.Messages {
		if (message.Role != RoleSystem &&
			message.Role != RoleUser &&
			message.Role != RoleAssistant) ||
			strings.TrimSpace(message.Content) == "" {
			return providerError(
				domainerr.CodeValidation,
				"validate Provider request",
				"模型消息角色或内容无效。",
				"修正消息后重试。",
				false,
				errors.New("invalid message"),
			)
		}
	}
	return nil
}

func cloneRequest(request Request) Request {
	result := request
	result.Schema = append(json.RawMessage(nil), request.Schema...)
	result.Messages = append([]Message(nil), request.Messages...)
	return result
}

func joinEndpoint(endpoint string, suffix string) string {
	return strings.TrimRight(strings.TrimSpace(endpoint), "/") + "/" +
		strings.TrimLeft(suffix, "/")
}

func readLimited(reader io.Reader) ([]byte, error) {
	limited := io.LimitReader(reader, maxResponseBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(payload) > maxResponseBytes {
		return nil, errors.New("Provider response exceeds 4 MiB")
	}
	return payload, nil
}

func invalidProviderConfiguration(cause string) error {
	return providerError(
		domainerr.CodeValidation,
		"configure model Provider",
		"LLM Provider 配置无效。",
		"在设置中修正 provider、endpoint 和 model。",
		false,
		errors.New(cause),
	)
}

func invalidProviderResponse(cause error) error {
	return providerError(
		domainerr.CodeInvalidModelOutput,
		"decode Provider response",
		"模型 Provider 返回了无法读取的响应。",
		"检查 Provider 兼容性后重试。",
		true,
		cause,
	)
}

func providerError(
	code domainerr.Code,
	operation string,
	message string,
	recovery string,
	retryable bool,
	cause error,
) *domainerr.Error {
	return domainerr.Wrap(
		code,
		operation,
		"model provider",
		message,
		recovery,
		retryable,
		cause,
	)
}
