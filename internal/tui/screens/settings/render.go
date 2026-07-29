package settings

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
)

// Render draws P-07 without exposing API key values or references.
func (model *Model) Render() (string, error) {
	if model == nil {
		return "", fmt.Errorf("settings model is nil")
	}
	if err := model.connection.Validate(); err != nil {
		return "", err
	}
	plan := layout.Calculate(model.Width, model.Height)
	if err := plan.Validate(); err != nil {
		return "", err
	}
	mainWidth := max(1, plan.MainWidth-2)
	if plan.Mode == layout.Narrow || plan.Mode == layout.Blocked {
		mainWidth = max(1, model.Width-2)
	}

	providerLines, err := model.providerLines(mainWidth)
	if err != nil {
		return "", err
	}
	runtimeLines := model.runtimeLines()
	if plan.Mode == layout.Narrow {
		providerLines = append(providerLines, "")
		providerLines = append(providerLines, components.SectionLabel{
			Text: "Local runtime",
		}.Render(model.Theme))
		providerLines = append(providerLines, model.runtimeLines()...)
	}

	trace := components.Pane{
		Title: "导航",
		Lines: []string{
			"[h] 训练",
			"[p] 画像",
			"[r] 报告",
			"[s] 设置",
			"",
			"[?] 快捷键",
		},
	}
	main := components.Pane{
		Title:  "LLM Provider",
		Status: model.providerStatus(),
		State:  paneState(model.focus.Active() == focusProvider),
		Lines:  providerLines,
	}
	local := components.Pane{
		Title: "Local runtime",
		State: paneState(model.focus.Active() == focusRuntime),
		Lines: runtimeLines,
	}
	var overlay *components.Pane
	if model.helpOpen {
		help := components.Pane{
			Title: "快捷键",
			State: components.PaneOverlay,
			Lines: []string{
				"[t] 测试连接 · [e] 编辑 Provider · [w] 保存",
				"[Tab] 切换区域 · [h] 训练主页 · [s] 设置",
				"[Esc] 返回之前的焦点",
			},
		}
		if plan.Mode == layout.Narrow {
			overlay = &help
		} else {
			main = help
		}
	}
	shell := components.AppShell{
		Width:           model.Width,
		Height:          model.Height,
		Screen:          "SETTINGS / runtime",
		Provider:        model.providerBadge(),
		ActivitySummary: model.providerStatus(),
		Trace:           trace,
		Main:            main,
		Coach:           local,
		Overlay:         overlay,
		Commands:        model.commands(),
		Theme:           model.Theme,
	}
	return shell.Render()
}

func (model *Model) providerLines(width int) ([]string, error) {
	lines := []string{
		"provider  " + nonBlank(model.runtime.LLM.Provider, "未配置"),
		"endpoint  " + nonBlank(safeEndpoint(model.runtime.LLM.Endpoint), "未配置"),
		"model     " + nonBlank(model.runtime.LLM.Model, "未配置"),
	}
	if model.runtime.LLM.Provider != "" {
		lines = append(lines, "auth      环境变量引用（值不显示）")
	}
	lines = append(lines, "")

	switch model.connection.Phase {
	case async.Pending:
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在测试 Provider 连接",
		}).Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	case async.Streaming:
		status := "正在确认 endpoint、认证和模型"
		line, err := (components.ActivityLine{
			State: async.NewStreaming(&status),
			Label: status,
		}).Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	case async.Failed:
		notice := components.ErrorNotice(
			model.connection.Err,
			&components.KeyHint{Key: "t", Action: "重试", Enabled: true},
		)
		rendered, err := notice.Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		lines = append(lines, rendered...)
	case async.Succeeded:
		diagnostic := model.connection.Value
		if diagnostic == nil {
			return nil, fmt.Errorf("succeeded diagnostic is nil")
		}
		notice := diagnosticNotice(*diagnostic)
		rendered, err := notice.Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		lines = append(lines, rendered...)
	}
	if !model.CanStartScenario() {
		lines = append(lines, "")
		lines = append(lines,
			"新场景已禁用；历史训练和本地报告仍可浏览。",
		)
	}
	return lines, nil
}

func (model *Model) runtimeLines() []string {
	lines := make([]string, 0, 12)
	for _, row := range model.localRuntimeRows() {
		badge := components.StatusBadge{
			State: row.State,
			Text:  row.Name,
		}.Render(model.Theme)
		lines = append(lines, badge+" · "+row.Message)
		if row.Recovery != "" {
			lines = append(lines, "  "+row.Recovery)
		}
		lines = append(lines, "")
	}
	return lines
}

func diagnosticNotice(diagnostic llm.Diagnostic) components.InlineNotice {
	if diagnostic.Ready {
		return components.InlineNotice{
			Kind:    components.NoticeSuccess,
			Message: diagnostic.Message,
		}
	}
	action := &components.KeyHint{Key: "t", Action: "重试", Enabled: true}
	if diagnostic.Kind == llm.DiagnosticConfiguration {
		return components.InlineNotice{
			Kind:     components.NoticeWarning,
			Message:  diagnostic.Message,
			Recovery: diagnostic.Recovery,
			Action:   action,
		}
	}
	return components.ErrorNotice(domainerr.New(
		domainerr.CodeDependencyUnavailable,
		"diagnose model Provider",
		diagnostic.Message,
		diagnostic.Recovery,
		true,
	), action)
}

func (model *Model) providerBadge() components.StatusBadge {
	badge := components.StatusBadge{
		State: components.BadgeWarning,
		Text:  "model not ready",
	}
	switch model.connection.Phase {
	case async.Pending, async.Streaming:
		badge.Text = "testing model"
	case async.Failed:
		badge.State = components.BadgeError
		badge.Text = "model test failed"
	case async.Succeeded:
		if model.connection.Value != nil && model.connection.Value.Ready {
			badge.State = components.BadgeReady
			badge.Text = "model ready"
		} else if model.connection.Value != nil &&
			model.connection.Value.Kind != llm.DiagnosticConfiguration {
			badge.State = components.BadgeError
		}
	}
	return badge
}

func (model *Model) providerStatus() string {
	switch model.connection.Phase {
	case async.Pending, async.Streaming:
		return "连接测试中"
	case async.Failed:
		return "连接测试失败"
	case async.Succeeded:
		if model.connection.Value == nil {
			return "状态不可用"
		}
		if model.connection.Value.Ready {
			return "ready"
		}
		switch model.connection.Value.Kind {
		case llm.DiagnosticAuthentication:
			return "认证错误"
		case llm.DiagnosticModel:
			return "模型错误"
		case llm.DiagnosticEndpoint:
			return "endpoint 错误"
		default:
			return "未配置"
		}
	default:
		return "状态不可用"
	}
}

func (model *Model) commands() []components.KeyHint {
	if model.helpOpen {
		return []components.KeyHint{
			{Key: "Esc", Action: "返回", Enabled: true},
		}
	}
	return []components.KeyHint{
		{Key: "t", Action: "测试连接", Enabled: model.runtime.LLM.Provider != ""},
		{Key: "e", Action: "编辑 Provider", Enabled: true},
		{Key: "w", Action: "保存", Enabled: true},
		{Key: "Tab", Action: "下一栏", Enabled: true},
		{Key: "?", Action: "快捷键", Enabled: true},
	}
}

func paneState(focused bool) components.PaneState {
	if focused {
		return components.PaneFocused
	}
	return components.PaneInactive
}

func nonBlank(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func safeEndpoint(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return "已配置（地址无效）"
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}
