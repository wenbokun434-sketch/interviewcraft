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
	"github.com/interviewcraft/interviewcraft/internal/core/transfer"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

const (
	focusProvider   = "provider"
	focusRuntime    = "runtime"
	focusData       = "data"
	focusDataDelete = "data-delete"
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

// DataManager is the local transfer boundary used by the Data vault area.
type DataManager interface {
	Inventory(context.Context) (transfer.Inventory, error)
	Export(
		context.Context,
		transfer.ExportOptions,
		transfer.Observer,
	) (transfer.ExportResult, error)
	Import(
		context.Context,
		string,
		transfer.Observer,
	) (transfer.ImportResult, error)
	Delete(
		context.Context,
		transfer.Confirmation,
		transfer.Observer,
	) (int64, error)
}

// Observer receives connection-test lifecycle states.
type Observer func(async.State[llm.Diagnostic])

// DataObserver receives inventory loading states.
type DataObserver func(async.State[transfer.Inventory])

// DataOperationObserver receives export/import/delete states.
type DataOperationObserver func(async.State[transfer.Progress])

// Options constructs one settings model.
type Options struct {
	Runtime       config.Runtime
	TesterFactory TesterFactory
	SaveConfig    ConfigSaver
	Data          DataManager
	Width         int
	Height        int
	Theme         theme.Theme
}

// Destination is one global settings navigation target.
type Destination string

const (
	DestinationNone       Destination = ""
	DestinationTraining   Destination = "training"
	DestinationProfile    Destination = "profile"
	DestinationReport     Destination = "report"
	DestinationSettings   Destination = "settings"
	DestinationDataExport Destination = "data-export"
	DestinationDataImport Destination = "data-import"
	DestinationDataDelete Destination = "data-delete"
	DestinationDataReload Destination = "data-reload"
	DestinationQuit       Destination = "quit"
)

// Model owns non-secret form state, diagnostics, and responsive focus.
type Model struct {
	runtime             config.Runtime
	testerFactory       TesterFactory
	saveConfig          ConfigSaver
	dataManager         DataManager
	connection          async.State[llm.Diagnostic]
	dataState           async.State[transfer.Inventory]
	dataOperation       async.State[transfer.Progress]
	focus               *layout.FocusModel
	helpOpen            bool
	dataConfirmOpen     bool
	deleteAuthorized    bool
	pendingDelete       transfer.DeleteScope
	pendingSessionID    string
	selectedSession     int
	includeCoachContent bool

	Width  int
	Height int
	Theme  theme.Theme
}

// New creates a settings screen. An unconfigured Provider is an explicit empty
// state, not a startup error.
func New(options Options) (*Model, error) {
	focus, err := layout.NewFocusModel(focusProvider, focusRuntime, focusData)
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
		dataManager:   options.Data,
		connection:    async.NewSucceeded(initial),
		dataState:     async.NewSucceeded(transfer.Inventory{SessionIDs: []string{}}),
		dataOperation: async.NewSucceeded(transfer.Progress{
			Stage: "idle", Message: "尚未执行数据操作",
		}),
		focus:  focus,
		Width:  options.Width,
		Height: options.Height,
		Theme:  options.Theme,
	}, nil
}

// DataState returns the local data inventory lifecycle.
func (model *Model) DataState() async.State[transfer.Inventory] {
	if model == nil {
		return async.State[transfer.Inventory]{}
	}
	return model.dataState
}

// DataOperationState returns the latest transfer/delete lifecycle.
func (model *Model) DataOperationState() async.State[transfer.Progress] {
	if model == nil {
		return async.State[transfer.Progress]{}
	}
	return model.dataOperation
}

// LoadData refreshes the non-sensitive local inventory.
func (model *Model) LoadData(ctx context.Context, observer DataObserver) {
	if model == nil {
		return
	}
	model.dataState = async.NewPending[transfer.Inventory]()
	notifyData(observer, model.dataState)
	if model.dataManager == nil {
		model.dataState = async.NewFailed[transfer.Inventory](domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"load local data inventory",
			"本地 Data 服务不可用。",
			"重新打开设置页后按 [l] 重试。",
			true,
		))
		notifyData(observer, model.dataState)
		return
	}
	inventory, err := model.dataManager.Inventory(ctx)
	if err != nil {
		model.dataState = async.NewFailed[transfer.Inventory](dataFailure("load local data inventory", err))
		notifyData(observer, model.dataState)
		return
	}
	if inventory.SessionIDs == nil {
		inventory.SessionIDs = []string{}
	}
	model.selectedSession = clampDataSelection(model.selectedSession, len(inventory.SessionIDs))
	model.dataState = async.NewSucceeded(inventory)
	notifyData(observer, model.dataState)
}

// IncludeCoachContent reports the explicit export privacy choice.
func (model *Model) IncludeCoachContent() bool {
	return model != nil && model.includeCoachContent
}

// ExportData exports the selected report or full package.
func (model *Model) ExportData(
	ctx context.Context,
	format transfer.Format,
	outputPath string,
	observer DataOperationObserver,
) (transfer.ExportResult, error) {
	if model == nil || model.dataManager == nil {
		return transfer.ExportResult{}, model.dataDependencyFailure("export local data", observer)
	}
	result, err := model.dataManager.Export(ctx, transfer.ExportOptions{
		Format: format, OutputPath: outputPath,
		SessionID:           model.selectedSessionID(),
		IncludeCoachContent: model.includeCoachContent,
	}, model.observeDataOperation(observer))
	if err != nil {
		model.ensureDataOperationFailure("export local data", err, observer)
	}
	return result, err
}

// ImportData imports one package and refreshes the inventory on success.
func (model *Model) ImportData(
	ctx context.Context,
	inputPath string,
	observer DataOperationObserver,
) (transfer.ImportResult, error) {
	if model == nil || model.dataManager == nil {
		return transfer.ImportResult{}, model.dataDependencyFailure("import local data", observer)
	}
	result, err := model.dataManager.Import(
		ctx,
		inputPath,
		model.observeDataOperation(observer),
	)
	if err != nil {
		model.ensureDataOperationFailure("import local data", err, observer)
		return transfer.ImportResult{}, err
	}
	model.LoadData(ctx, nil)
	return result, nil
}

// DeleteData consumes one UI confirmation authorization and refreshes inventory.
func (model *Model) DeleteData(
	ctx context.Context,
	observer DataOperationObserver,
) (int64, error) {
	if model == nil || !model.deleteAuthorized {
		failure := domainerr.New(
			domainerr.CodePolicyDenied,
			"delete local data",
			"删除训练数据前必须再次确认。",
			"在 Data 区按 [d] 或 [x]，再按 [y] 确认。",
			false,
		)
		if model != nil {
			model.dataOperation = async.NewFailed[transfer.Progress](failure)
			notifyDataOperation(observer, model.dataOperation)
		}
		return 0, failure
	}
	model.deleteAuthorized = false
	if model.dataManager == nil {
		return 0, model.dataDependencyFailure("delete local data", observer)
	}
	confirmation := transfer.Confirmation{Scope: model.pendingDelete}
	if model.pendingDelete == transfer.DeleteSession {
		confirmation.SessionID = model.pendingSessionID
		confirmation.Phrase = transfer.SessionDeletePhrase(model.pendingSessionID)
	} else {
		confirmation.Phrase = transfer.AllDeletePhrase()
	}
	affected, err := model.dataManager.Delete(
		ctx,
		confirmation,
		model.observeDataOperation(observer),
	)
	if err != nil {
		model.ensureDataOperationFailure("delete local data", err, observer)
		return 0, err
	}
	model.pendingDelete = ""
	model.pendingSessionID = ""
	model.LoadData(ctx, nil)
	return affected, nil
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
	if model.dataConfirmOpen {
		switch key {
		case "y":
			model.dataConfirmOpen = false
			model.focus.CloseOverlay()
			model.deleteAuthorized = true
			return DestinationDataDelete
		case "n", "escape", "esc", "enter":
			model.closeDataConfirmation()
		}
		return DestinationNone
	}
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
	case "up", "k":
		if model.focus.Active() == focusData {
			model.moveDataSelection(-1)
		}
	case "down", "j":
		if model.focus.Active() == focusData {
			model.moveDataSelection(1)
		}
	case "c":
		if model.focus.Active() == focusData {
			model.includeCoachContent = !model.includeCoachContent
		}
	case "e":
		if model.focus.Active() == focusData && model.hasLocalData() {
			return DestinationDataExport
		}
	case "i":
		if model.focus.Active() == focusData && !model.hasLocalData() {
			return DestinationDataImport
		}
	case "l":
		if model.focus.Active() == focusData {
			return DestinationDataReload
		}
	case "d":
		if model.focus.Active() == focusData && model.selectedSessionID() != "" {
			model.openDataConfirmation(transfer.DeleteSession, model.selectedSessionID())
		}
	case "x":
		if model.focus.Active() == focusData && model.hasLocalData() {
			model.openDataConfirmation(transfer.DeleteAll, "")
		}
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

func (model *Model) openDataConfirmation(scope transfer.DeleteScope, sessionID string) {
	if model.focus.OpenOverlay(focusDataDelete) != nil {
		return
	}
	model.dataConfirmOpen = true
	model.deleteAuthorized = false
	model.pendingDelete = scope
	model.pendingSessionID = sessionID
}

func (model *Model) closeDataConfirmation() {
	model.dataConfirmOpen = false
	model.deleteAuthorized = false
	model.pendingDelete = ""
	model.pendingSessionID = ""
	model.focus.CloseOverlay()
}

func (model *Model) moveDataSelection(delta int) {
	if model.dataState.Value == nil || len(model.dataState.Value.SessionIDs) == 0 {
		return
	}
	count := len(model.dataState.Value.SessionIDs)
	model.selectedSession = (model.selectedSession + delta%count + count) % count
}

func (model *Model) selectedSessionID() string {
	if model == nil || model.dataState.Value == nil ||
		len(model.dataState.Value.SessionIDs) == 0 {
		return ""
	}
	index := clampDataSelection(model.selectedSession, len(model.dataState.Value.SessionIDs))
	return model.dataState.Value.SessionIDs[index]
}

func (model *Model) hasLocalData() bool {
	if model == nil || model.dataState.Value == nil {
		return false
	}
	value := model.dataState.Value
	return value.Profiles > 0 || value.Scenarios > 0 || value.Sessions > 0 || value.Reports > 0
}

func (model *Model) observeDataOperation(observer DataOperationObserver) transfer.Observer {
	return func(state async.State[transfer.Progress]) {
		model.dataOperation = state
		notifyDataOperation(observer, state)
	}
}

func (model *Model) ensureDataOperationFailure(
	operation string,
	err error,
	observer DataOperationObserver,
) {
	if model.dataOperation.Phase == async.Failed {
		return
	}
	model.dataOperation = async.NewFailed[transfer.Progress](dataFailure(operation, err))
	notifyDataOperation(observer, model.dataOperation)
}

func (model *Model) dataDependencyFailure(
	operation string,
	observer DataOperationObserver,
) error {
	failure := domainerr.New(
		domainerr.CodeDependencyUnavailable,
		operation,
		"本地 Data 服务不可用。",
		"重新打开设置页后重试。",
		true,
	)
	if model != nil {
		model.dataOperation = async.NewFailed[transfer.Progress](failure)
		notifyDataOperation(observer, model.dataOperation)
	}
	return failure
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

func notifyData(observer DataObserver, state async.State[transfer.Inventory]) {
	if observer != nil {
		observer(state)
	}
}

func notifyDataOperation(
	observer DataOperationObserver,
	state async.State[transfer.Progress],
) {
	if observer != nil {
		observer(state)
	}
}

func dataFailure(operation string, err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"local data service",
		"无法完成本地数据操作。",
		"检查数据库和目标路径后重试；原数据保持不变。",
		true,
		err,
	)
}

func clampDataSelection(selected, count int) int {
	if count <= 0 {
		return 0
	}
	if selected < 0 {
		return 0
	}
	if selected >= count {
		return count - 1
	}
	return selected
}

type runtimeRow struct {
	Name     string
	State    components.BadgeState
	Message  string
	Recovery string
}
