// Package settings implements the P-07 runtime and Provider settings screen.
package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

const (
	focusProvider = "provider"
	focusRuntime  = "runtime"
)

// ConnectionTester performs one safe Provider diagnostic.
type ConnectionTester interface {
	Diagnose(context.Context) llm.Diagnostic
}

// TesterFactory creates a tester from the current non-secret configuration.
type TesterFactory func(config.LLM) (ConnectionTester, error)

// ConfigSaver persists only config.Runtime, whose LLM field stores a secret
// reference rather than a key value.
type ConfigSaver func(config.Runtime) error

// Observer receives connection-test lifecycle states.
type Observer func(async.State[llm.Diagnostic])

// Options constructs one settings model.
type Options struct {
	Runtime       config.Runtime
	TesterFactory TesterFactory
	SaveConfig    ConfigSaver
	Width         int
	Height        int
	Theme         theme.Theme
}

// Destination is one global settings navigation target.
type Destination string

const (
	DestinationNone     Destination = ""
	DestinationTraining Destination = "training"
	DestinationProfile  Destination = "profile"
	DestinationReport   Destination = "report"
	DestinationSettings Destination = "settings"
	DestinationQuit     Destination = "quit"
)

// Model owns non-secret form state, diagnostics, and responsive focus.
type Model struct {
	runtime       config.Runtime
	testerFactory TesterFactory
	saveConfig    ConfigSaver
	connection    async.State[llm.Diagnostic]
	focus         *layout.FocusModel
	helpOpen      bool

	Width  int
	Height int
	Theme  theme.Theme
}

// New creates a settings screen. An unconfigured Provider is an explicit empty
// state, not a startup error.
func New(options Options) (*Model, error) {
	focus, err := layout.NewFocusModel(focusProvider, focusRuntime)
	if err != nil {
		return nil, err
	}
	initial := llm.Diagnostic{
		Kind:     llm.DiagnosticConfiguration,
		Message:  "还没有配置 LLM Provider。",
		Recovery: "设置 Provider、endpoint 和 model 后按 [t] 测试。",
	}
	if options.Runtime.LLM.Provider != "" {
		initial.Provider = options.Runtime.LLM.Provider
		initial.Model = options.Runtime.LLM.Model
		initial.Message = "Provider 配置尚未测试。"
		initial.Recovery = "按 [t] 测试 endpoint、认证和模型。"
	}
	return &Model{
		runtime:       options.Runtime,
		testerFactory: options.TesterFactory,
		saveConfig:    options.SaveConfig,
		connection:    async.NewSucceeded(initial),
		focus:         focus,
		Width:         options.Width,
		Height:        options.Height,
		Theme:         options.Theme,
	}, nil
}

// Runtime returns a copy of the current non-secret configuration.
func (model *Model) Runtime() config.Runtime {
	if model == nil {
		return config.Runtime{}
	}
	return model.runtime
}

// ConnectionState returns the typed connection-test lifecycle.
func (model *Model) ConnectionState() async.State[llm.Diagnostic] {
	if model == nil {
		return async.State[llm.Diagnostic]{}
	}
	return model.connection
}

// UpdateProvider updates the in-memory form only after full runtime validation.
func (model *Model) UpdateProvider(provider config.LLM) error {
	if model == nil {
		return errors.New("settings model is nil")
	}
	candidate := model.runtime
	candidate.LLM = provider
	if err := candidate.Validate(); err != nil {
		return err
	}
	model.runtime = candidate
	diagnostic := llm.Diagnostic{
		Kind:     llm.DiagnosticConfiguration,
		Provider: provider.Provider,
		Model:    provider.Model,
		Message:  "Provider 配置已更新但尚未测试。",
		Recovery: "按 [t] 测试连接。",
	}
	model.connection = async.NewSucceeded(diagnostic)
	return nil
}

// Save persists the non-secret runtime through the injected configuration
// boundary.
func (model *Model) Save() error {
	if model == nil || model.saveConfig == nil {
		return domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"save Provider settings",
			"设置保存接口不可用。",
			"保留当前输入并重新启动设置页。",
			true,
		)
	}
	if err := model.runtime.Validate(); err != nil {
		return err
	}
	if err := model.saveConfig(model.runtime); err != nil {
		return settingsSaveError(err)
	}
	return nil
}

// TestConnection distinguishes endpoint, authentication, and model failures.
func (model *Model) TestConnection(ctx context.Context, observer Observer) {
	if model == nil {
		return
	}
	model.connection = async.NewPending[llm.Diagnostic]()
	notify(observer, model.connection)

	if model.runtime.LLM.Provider == "" {
		diagnostic := llm.Diagnostic{
			Kind:     llm.DiagnosticConfiguration,
			Message:  "还没有配置 LLM Provider。",
			Recovery: "设置 Provider、endpoint 和 model 后按 [t] 测试。",
		}
		model.connection = async.NewSucceeded(diagnostic)
		notify(observer, model.connection)
		return
	}
	if model.testerFactory == nil {
		failure := domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"test Provider connection",
			"Provider 连接测试接口不可用。",
			"重新打开设置页后重试。",
			true,
		)
		model.connection = async.NewFailed[llm.Diagnostic](failure)
		notify(observer, model.connection)
		return
	}
	tester, err := model.testerFactory(model.runtime.LLM)
	if err != nil {
		model.connection = async.NewFailed[llm.Diagnostic](settingsFailure(err))
		notify(observer, model.connection)
		return
	}
	diagnostic := tester.Diagnose(ctx)
	model.connection = async.NewSucceeded(diagnostic)
	notify(observer, model.connection)
}

// CanStartScenario prevents new model work until diagnostics pass.
func (model *Model) CanStartScenario() bool {
	if model == nil ||
		model.connection.Phase != async.Succeeded ||
		model.connection.Value == nil {
		return false
	}
	return model.connection.Value.Ready
}

// HistoryAvailable remains true regardless of Provider health.
func (model *Model) HistoryAvailable() bool {
	return model != nil
}

// Resize preserves form, diagnostics, and focus.
func (model *Model) Resize(width, height int) {
	if model == nil {
		return
	}
	model.Width = width
	model.Height = height
}

// HandleKey applies global navigation and non-destructive help behavior.
func (model *Model) HandleKey(key string) Destination {
	if model == nil {
		return DestinationNone
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if model.helpOpen {
		if key == "escape" || key == "esc" || key == "?" {
			model.helpOpen = false
			model.focus.CloseOverlay()
		}
		return DestinationNone
	}
	switch key {
	case "?":
		if model.focus.OpenOverlay("help") == nil {
			model.helpOpen = true
		}
	case "tab":
		model.focus.Handle(layout.KeyTab)
	case "shift+tab":
		model.focus.Handle(layout.KeyShiftTab)
	case "t":
		return DestinationSettings
	case "h":
		return DestinationTraining
	case "p":
		return DestinationProfile
	case "r":
		return DestinationReport
	case "s":
		return DestinationSettings
	case "q":
		return DestinationQuit
	}
	return DestinationNone
}

func (model *Model) localRuntimeRows() []runtimeRow {
	rows := make([]runtimeRow, 0, 3)
	dataInfo, dataErr := os.Stat(model.runtime.DataDir)
	switch {
	case dataErr != nil:
		rows = append(rows, runtimeRow{
			Name:     "DATA",
			State:    components.BadgeError,
			Message:  "本地数据目录不可用。",
			Recovery: "运行 `interviewcraft init` 或修正 data_dir。",
		})
	case !dataInfo.IsDir():
		rows = append(rows, runtimeRow{
			Name:     "DATA",
			State:    components.BadgeError,
			Message:  "本地数据路径不是目录。",
			Recovery: "改用可写目录。",
		})
	default:
		rows = append(rows, runtimeRow{
			Name:    "DATA",
			State:   components.BadgeReady,
			Message: filepath.Clean(model.runtime.DataDir),
		})
	}

	databasePath := filepath.Join(
		model.runtime.DataDir,
		model.runtime.DatabaseName,
	)
	if info, err := os.Stat(databasePath); err != nil || info.IsDir() {
		rows = append(rows, runtimeRow{
			Name:     "DB",
			State:    components.BadgeError,
			Message:  "SQLite 数据库不可用。",
			Recovery: "运行 `interviewcraft init` 后重试。",
		})
	} else {
		rows = append(rows, runtimeRow{
			Name:    "DB",
			State:   components.BadgeReady,
			Message: databasePath,
		})
	}

	if model.runtime.RunnerMode == config.RunnerDisabled {
		rows = append(rows, runtimeRow{
			Name:     "RUNNER",
			State:    components.BadgeWarning,
			Message:  "Docker Runner 已禁用；文字面试仍可使用。",
			Recovery: "需要代码执行时设置 RUNNER_MODE=docker。",
		})
	} else {
		rows = append(rows, runtimeRow{
			Name:     "RUNNER",
			State:    components.BadgeWarning,
			Message:  "Docker Runner 已配置，等待 doctor 确认。",
			Recovery: "运行 `interviewcraft doctor`。",
		})
	}
	return rows
}

func settingsFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"test Provider connection",
		"model provider",
		"无法创建 Provider 连接测试。",
		"检查 Provider 配置后重试。",
		true,
		err,
	)
}

func settingsSaveError(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"save Provider settings",
		"local configuration",
		"无法保存 Provider 设置。",
		"当前输入已保留；检查配置文件权限后重试。",
		true,
		err,
	)
}

func notify(observer Observer, state async.State[llm.Diagnostic]) {
	if observer != nil {
		observer(state)
	}
}

type runtimeRow struct {
	Name     string
	State    components.BadgeState
	Message  string
	Recovery string
}
