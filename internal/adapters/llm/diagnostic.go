package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// DiagnosticKind identifies the configuration element that needs attention.
type DiagnosticKind string

const (
	DiagnosticReady          DiagnosticKind = "ready"
	DiagnosticConfiguration  DiagnosticKind = "configuration"
	DiagnosticEndpoint       DiagnosticKind = "endpoint"
	DiagnosticAuthentication DiagnosticKind = "authentication"
	DiagnosticModel          DiagnosticKind = "model"
)

// Diagnostic is safe to render: it contains no request body, secret, or raw
// Provider response.
type Diagnostic struct {
	Ready    bool
	Kind     DiagnosticKind
	Provider string
	Model    string
	Message  string
	Recovery string
}

// Diagnose checks connectivity, authentication, and configured model presence.
func (client *Client) Diagnose(ctx context.Context) Diagnostic {
	if client == nil {
		return Diagnostic{
			Kind:     DiagnosticConfiguration,
			Message:  "尚未配置 LLM Provider。",
			Recovery: "设置 Provider、endpoint 和 model 后按 [t] 测试。",
		}
	}
	diagnostic := Diagnostic{
		Provider: client.config.Provider,
		Model:    client.config.Model,
	}
	target := joinEndpoint(client.config.Endpoint, "models")
	if client.config.Provider == config.ProviderOllama {
		target = joinEndpoint(client.config.Endpoint, "api/tags")
	}
	response, err := client.doJSON(ctx, http.MethodGet, target, nil)
	if err != nil {
		var typed *domainerr.Error
		if errors.As(err, &typed) &&
			typed.Operation == "authenticate Provider request" {
			diagnostic.Kind = DiagnosticAuthentication
			diagnostic.Message = "OpenAI-compatible Provider 缺少认证信息。"
			diagnostic.Recovery = "设置 API Key 环境变量后按 [t] 重试。"
			return diagnostic
		}
		diagnostic.Kind = DiagnosticEndpoint
		diagnostic.Message = "无法连接模型 Provider。"
		diagnostic.Recovery = "检查 endpoint 和服务状态后按 [t] 重试。"
		return diagnostic
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		diagnostic.Kind = DiagnosticAuthentication
		diagnostic.Message = "模型 Provider 认证失败。"
		diagnostic.Recovery = "检查 API Key 环境变量后按 [t] 重试。"
		return diagnostic
	case http.StatusOK:
	default:
		diagnostic.Kind = DiagnosticEndpoint
		diagnostic.Message = fmt.Sprintf(
			"模型 Provider endpoint 返回 HTTP %d。",
			response.StatusCode,
		)
		diagnostic.Recovery = "检查 endpoint 后按 [t] 重试。"
		return diagnostic
	}

	payload, err := readLimited(response.Body)
	if err != nil {
		diagnostic.Kind = DiagnosticEndpoint
		diagnostic.Message = "无法读取模型列表。"
		diagnostic.Recovery = "检查 Provider 兼容性后按 [t] 重试。"
		return diagnostic
	}
	models, err := decodeModelNames(client.config.Provider, payload)
	if err != nil {
		diagnostic.Kind = DiagnosticEndpoint
		diagnostic.Message = "模型列表响应格式无效。"
		diagnostic.Recovery = "检查 Provider 类型和 endpoint 后重试。"
		return diagnostic
	}
	if !containsModel(models, client.config.Model, client.config.Provider) {
		diagnostic.Kind = DiagnosticModel
		diagnostic.Message = "配置的模型不在 Provider 模型列表中。"
		diagnostic.Recovery = "修正 model 名称后按 [t] 重试。"
		return diagnostic
	}
	diagnostic.Ready = true
	diagnostic.Kind = DiagnosticReady
	diagnostic.Message = "LLM Provider 与模型可用。"
	return diagnostic
}

func decodeModelNames(provider string, payload []byte) ([]string, error) {
	switch provider {
	case config.ProviderOpenAICompatible:
		var response struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if err := json.Unmarshal(payload, &response); err != nil {
			return nil, err
		}
		models := make([]string, 0, len(response.Data))
		for _, item := range response.Data {
			if strings.TrimSpace(item.ID) != "" {
				models = append(models, item.ID)
			}
		}
		return models, nil
	case config.ProviderOllama:
		var response struct {
			Models []struct {
				Name  string `json:"name"`
				Model string `json:"model"`
			} `json:"models"`
		}
		if err := json.Unmarshal(payload, &response); err != nil {
			return nil, err
		}
		models := make([]string, 0, len(response.Models)*2)
		for _, item := range response.Models {
			if strings.TrimSpace(item.Name) != "" {
				models = append(models, item.Name)
			}
			if strings.TrimSpace(item.Model) != "" && item.Model != item.Name {
				models = append(models, item.Model)
			}
		}
		return models, nil
	default:
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
}

func containsModel(models []string, configured string, provider string) bool {
	configured = strings.TrimSpace(configured)
	for _, candidate := range models {
		if candidate == configured {
			return true
		}
		if provider == config.ProviderOllama &&
			!strings.Contains(configured, ":") &&
			strings.HasPrefix(candidate, configured+":") {
			return true
		}
	}
	return false
}
