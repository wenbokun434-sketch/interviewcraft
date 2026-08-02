// Package coding implements the keyboard-first P-05 code interview workbench.
package coding

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

const (
	focusEditor  = "editor"
	focusSpec    = "spec"
	focusSummary = "run-summary"
	focusHelp    = "help"
)

// Stage identifies one visible coding-workbench operation.
type Stage string

const (
	StageIdle       Stage = "idle"
	StageLoading    Stage = "loading"
	StageSaving     Stage = "saving"
	StageSwitching  Stage = "switching-language"
	StageFormatting Stage = "formatting"
	StageResetting  Stage = "resetting-template"
	StageRunning    Stage = "running-public-tests"
	StageExplaining Stage = "explaining-run"
	StageReady      Stage = "ready"
)

// Progress is the screen-level lifecycle payload. Elapsed is updated while a
// run is active even when the editor buffer changes independently.
type Progress struct {
	Stage   Stage
	Message string
	Elapsed time.Duration
}

// Observer receives screen-level loading, streaming, success, and failure.
type Observer func(async.State[Progress])

// WorkspaceService is the P-05 boundary over the T-019 coding domain.
type WorkspaceService interface {
	RunnerStatus() corecoding.RunnerStatus
	Open(context.Context, string, string) (corecoding.Workspace, error)
	SaveSource(
		context.Context,
		string,
		string,
		corecoding.Language,
		string,
		corecoding.Observer,
	) (corecoding.Workspace, error)
	SelectLanguage(
		context.Context,
		string,
		string,
		corecoding.Language,
		corecoding.Observer,
	) (corecoding.Workspace, error)
	FormatSource(
		context.Context,
		string,
		string,
		corecoding.Language,
		corecoding.Observer,
	) (corecoding.Workspace, error)
	ResetTemplate(
		context.Context,
		string,
		string,
		corecoding.Language,
		corecoding.Observer,
	) (corecoding.Workspace, error)
	Run(
		context.Context,
		corecoding.RunRequest,
		corecoding.Observer,
	) (corecoding.RunSnapshot, error)
}

// CoachService is the T-014 policy boundary. P-05 asks only about an already
// persisted run; the service remains responsible for strict-mode enforcement.
type CoachService interface {
	Ask(
		context.Context,
		corecoach.AskRequest,
		corecoach.Observer,
	) (corecoach.AskResult, error)
}

// Intent tells the application controller which asynchronous action to run.
type Intent string

const (
	IntentNone           Intent = ""
	IntentSave           Intent = "save-draft"
	IntentSelectLanguage Intent = "select-language"
	IntentFormat         Intent = "format-source"
	IntentReset          Intent = "reset-template"
	IntentRun            Intent = "run-public-tests"
	IntentExplain        Intent = "explain-run-failure"
)

// Destination identifies the only P-05 navigation outcome.
type Destination string

const (
	DestinationNone      Destination = ""
	DestinationInterview Destination = "interview"
)

// Action is one controller-facing keyboard result.
type Action struct {
	Intent      Intent
	Destination Destination
	Language    corecoding.Language
}

// Options constructs one workbench without enabling Docker implicitly.
type Options struct {
	SessionID          string
	QuestionID         string
	Service            WorkspaceService
	Coach              CoachService
	Now                func() time.Time
	NextRunID          func() string
	NextCoachRequestID func() string
	Width              int
	Height             int
	Theme              theme.Theme
}

// Model owns local editor state, focus, safe run state, and Coach guidance.
type Model struct {
	mu sync.RWMutex

	sessionID          string
	questionID         string
	service            WorkspaceService
	coach              CoachService
	now                func() time.Time
	nextRunID          func() string
	nextCoachRequestID func() string
	focus              *layout.FocusModel

	workspace     corecoding.Workspace
	loaded        bool
	source        string
	language      corecoding.Language
	cursorRune    int
	draftRestored bool
	specOffset    int
	helpOpen      bool

	operation    async.State[Progress]
	operationErr *domainerr.Error
	running      bool
	runAttempted bool
	runStarted   time.Time
	elapsed      time.Duration
	coachNote    string

	Width  int
	Height int
	Theme  theme.Theme
}

// New creates an unloaded code workbench with editor focus.
func New(options Options) (*Model, error) {
	sessionID := strings.TrimSpace(options.SessionID)
	questionID := strings.TrimSpace(options.QuestionID)
	if sessionID == "" || questionID == "" {
		return nil, codingError(
			domainerr.CodeValidation,
			"open coding workbench",
			"会话和代码题不能为空。",
			"返回文字面试并重新打开代码题。",
			false,
		)
	}
	focus, err := layout.NewFocusModel(focusEditor, focusSpec, focusSummary)
	if err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	nextRunID := options.NextRunID
	if nextRunID == nil {
		nextRunID = func() string { return randomID("run") }
	}
	nextCoachID := options.NextCoachRequestID
	if nextCoachID == nil {
		nextCoachID = func() string { return randomID("coach") }
	}
	return &Model{
		sessionID:          sessionID,
		questionID:         questionID,
		service:            options.Service,
		coach:              options.Coach,
		now:                now,
		nextRunID:          nextRunID,
		nextCoachRequestID: nextCoachID,
		focus:              focus,
		language:           corecoding.LanguagePython,
		operation: async.NewSucceeded(Progress{
			Stage: StageIdle, Message: "代码工作台等待恢复",
		}),
		Width: options.Width, Height: options.Height, Theme: options.Theme,
	}, nil
}

// State returns the current typed operation state.
func (model *Model) State() async.State[Progress] {
	if model == nil {
		return async.State[Progress]{}
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.operation
}

// Workspace returns a defensive copy of the restored coding domain state.
func (model *Model) Workspace() corecoding.Workspace {
	if model == nil {
		return corecoding.Workspace{}
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return cloneWorkspace(model.workspace)
}

// ActiveFocus exposes logical focus for controller and regression tests.
func (model *Model) ActiveFocus() string {
	if model == nil || model.focus == nil {
		return ""
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.focus.Active()
}

// Source returns the local editor buffer, including edits made during a run.
func (model *Model) Source() string {
	if model == nil {
		return ""
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.source
}

// CursorRune returns the UTF-8-safe editor cursor offset.
func (model *Model) CursorRune() int {
	if model == nil {
		return 0
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.cursorRune
}

// IsRunning reports whether a public-test operation is active.
func (model *Model) IsRunning() bool {
	if model == nil {
		return false
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.running
}

// RunnerStatus exposes the explicit optional capability state.
func (model *Model) RunnerStatus() corecoding.RunnerStatus {
	if model == nil || model.service == nil {
		return corecoding.RunnerStatus{
			Enabled:        false,
			Message:        "代码执行未启用。",
			RecoveryAction: "在设置中启用 Docker Runner；文字面试和 Coach 仍可继续。",
		}
	}
	return model.service.RunnerStatus()
}

// Load restores all language buffers and the latest immutable run.
func (model *Model) Load(ctx context.Context, observer Observer) error {
	if model == nil || model.service == nil {
		return model.fail(
			codingError(
				domainerr.CodeDependencyUnavailable,
				"load coding workbench",
				"代码工作台当前不可用。",
				"返回文字面试并稍后重试。",
				true,
			),
			observer,
		)
	}
	model.publish(async.NewPending[Progress](), observer)
	model.stream(StageLoading, "正在恢复代码题和本地草稿", observer)
	workspace, err := model.service.Open(ctx, model.sessionID, model.questionID)
	if err != nil {
		return model.fail(safeCodingFailure("load coding workbench", err), observer)
	}
	model.mu.Lock()
	model.workspace = cloneWorkspace(workspace)
	model.loaded = true
	model.language = workspace.Draft.ActiveLanguage
	model.source = workspace.Draft.Sources[model.language]
	model.cursorRune = utf8.RuneCountInString(model.source)
	model.draftRestored = workspaceHasLocalState(workspace)
	model.operationErr = nil
	model.runAttempted = workspace.LatestRun != nil
	model.mu.Unlock()
	model.succeed(StageReady, "代码工作台已恢复", observer)
	return nil
}

// Resize preserves source, cursor, focus, and run state.
func (model *Model) Resize(width, height int) {
	if model == nil {
		return
	}
	model.mu.Lock()
	model.Width = width
	model.Height = height
	model.mu.Unlock()
}

// Tick advances the visible elapsed state without locking the editor.
func (model *Model) Tick(now time.Time) {
	if model == nil {
		return
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if !model.running {
		return
	}
	model.elapsed = max(0, now.UTC().Sub(model.runStarted))
	progress := Progress{
		Stage: StageRunning, Message: "正在运行公开测试", Elapsed: model.elapsed,
	}
	model.operation = async.NewStreaming(&progress)
}

// UpdateSource replaces the local buffer only. It intentionally remains
// available while Run is active; SaveDraft persists it explicitly.
func (model *Model) UpdateSource(source string) error {
	if model == nil {
		return codingError(
			domainerr.CodeInvalidState,
			"edit code",
			"代码编辑器当前不可用。",
			"重新打开代码题。",
			false,
		)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if !model.loaded {
		return codingError(
			domainerr.CodeInvalidState,
			"edit code",
			"代码题尚未加载。",
			"等待代码工作台恢复后再编辑。",
			false,
		)
	}
	model.source = source
	model.cursorRune = min(model.cursorRune, utf8.RuneCountInString(source))
	model.draftRestored = false
	return nil
}

// SetCursorRune moves the editor cursor without splitting UTF-8.
func (model *Model) SetCursorRune(offset int) error {
	if model == nil {
		return codingError(domainerr.CodeInvalidState, "move code cursor", "代码编辑器当前不可用。", "重新打开代码题。", false)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	count := utf8.RuneCountInString(model.source)
	if offset < 0 || offset > count {
		return codingError(domainerr.CodeValidation, "move code cursor", "代码光标位置无效。", "继续在当前代码范围内编辑。", false)
	}
	model.cursorRune = offset
	return nil
}

// InsertText applies keyboard text at the rune cursor, including CJK.
func (model *Model) InsertText(value string) error {
	if model == nil {
		return codingError(domainerr.CodeInvalidState, "insert code", "代码编辑器当前不可用。", "重新打开代码题。", false)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if !model.loaded {
		return codingError(domainerr.CodeInvalidState, "insert code", "代码题尚未加载。", "等待代码工作台恢复后再编辑。", false)
	}
	runes := []rune(model.source)
	offset := min(max(0, model.cursorRune), len(runes))
	inserted := []rune(value)
	updated := make([]rune, 0, len(runes)+len(inserted))
	updated = append(updated, runes[:offset]...)
	updated = append(updated, inserted...)
	updated = append(updated, runes[offset:]...)
	model.source = string(updated)
	model.cursorRune = offset + len(inserted)
	model.draftRestored = false
	return nil
}

// Backspace deletes one complete rune before the cursor.
func (model *Model) Backspace() error {
	if model == nil {
		return codingError(domainerr.CodeInvalidState, "delete code", "代码编辑器当前不可用。", "重新打开代码题。", false)
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.cursorRune <= 0 {
		return nil
	}
	runes := []rune(model.source)
	offset := min(model.cursorRune, len(runes))
	model.source = string(append(append([]rune{}, runes[:offset-1]...), runes[offset:]...))
	model.cursorRune = offset - 1
	model.draftRestored = false
	return nil
}

// SaveDraft persists the current language without changing other buffers.
func (model *Model) SaveDraft(ctx context.Context, observer Observer) error {
	language, source, err := model.editSnapshot("save code draft")
	if err != nil {
		return model.fail(err, observer)
	}
	if model.IsRunning() {
		return busyError("保存草稿")
	}
	model.publish(async.NewPending[Progress](), observer)
	model.stream(StageSaving, "正在保存本地代码草稿", observer)
	workspace, saveErr := model.service.SaveSource(
		ctx, model.sessionID, model.questionID, language, source, nil,
	)
	if saveErr != nil {
		return model.fail(safeCodingFailure("save code draft", saveErr), observer)
	}
	model.mergeWorkspace(workspace, language, source)
	model.succeed(StageReady, "代码草稿已保存", observer)
	return nil
}

// SelectLanguage saves the active buffer before restoring another one.
func (model *Model) SelectLanguage(
	ctx context.Context,
	language corecoding.Language,
	observer Observer,
) error {
	current, source, err := model.editSnapshot("switch code language")
	if err != nil {
		return model.fail(err, observer)
	}
	if !supportedLanguage(language) {
		return model.fail(codingError(
			domainerr.CodeValidation,
			"switch code language",
			"所选代码语言不受支持。",
			"请选择 Python、JavaScript 或 Java。",
			false,
		), observer)
	}
	if model.IsRunning() {
		return busyError("切换语言")
	}
	model.publish(async.NewPending[Progress](), observer)
	model.stream(StageSaving, "正在保存当前语言草稿", observer)
	if _, saveErr := model.service.SaveSource(
		ctx, model.sessionID, model.questionID, current, source, nil,
	); saveErr != nil {
		return model.fail(safeCodingFailure("save before language switch", saveErr), observer)
	}
	model.stream(StageSwitching, "正在切换代码语言", observer)
	workspace, selectErr := model.service.SelectLanguage(
		ctx, model.sessionID, model.questionID, language, nil,
	)
	if selectErr != nil {
		return model.fail(safeCodingFailure("switch code language", selectErr), observer)
	}
	model.mu.Lock()
	model.workspace = cloneWorkspace(workspace)
	model.language = language
	model.source = workspace.Draft.Sources[language]
	model.cursorRune = utf8.RuneCountInString(model.source)
	model.draftRestored = true
	model.operationErr = nil
	model.mu.Unlock()
	model.succeed(StageReady, "代码语言已切换", observer)
	return nil
}

// FormatSource saves then formats the active language using the local formatter.
func (model *Model) FormatSource(ctx context.Context, observer Observer) error {
	language, source, err := model.editSnapshot("format code")
	if err != nil {
		return model.fail(err, observer)
	}
	if model.IsRunning() {
		return busyError("格式化")
	}
	model.publish(async.NewPending[Progress](), observer)
	model.stream(StageSaving, "正在保存格式化前草稿", observer)
	if _, saveErr := model.service.SaveSource(
		ctx, model.sessionID, model.questionID, language, source, nil,
	); saveErr != nil {
		return model.fail(safeCodingFailure("save before format", saveErr), observer)
	}
	model.stream(StageFormatting, "正在格式化代码", observer)
	workspace, formatErr := model.service.FormatSource(
		ctx, model.sessionID, model.questionID, language, nil,
	)
	if formatErr != nil {
		return model.fail(safeCodingFailure("format code", formatErr), observer)
	}
	model.replaceActiveWorkspace(workspace, language, true)
	model.succeed(StageReady, "代码已格式化并保存", observer)
	return nil
}

// ResetTemplate restores only the active language template.
func (model *Model) ResetTemplate(ctx context.Context, observer Observer) error {
	language, _, err := model.editSnapshot("reset code template")
	if err != nil {
		return model.fail(err, observer)
	}
	if model.IsRunning() {
		return busyError("重置模板")
	}
	model.publish(async.NewPending[Progress](), observer)
	model.stream(StageResetting, "正在恢复当前语言模板", observer)
	workspace, resetErr := model.service.ResetTemplate(
		ctx, model.sessionID, model.questionID, language, nil,
	)
	if resetErr != nil {
		return model.fail(safeCodingFailure("reset code template", resetErr), observer)
	}
	model.replaceActiveWorkspace(workspace, language, false)
	model.succeed(StageReady, "当前语言模板已恢复", observer)
	return nil
}

// Run persists the source snapshot, prevents duplicate execution, and leaves
// local edits available throughout the blocking Runner call.
func (model *Model) Run(ctx context.Context, observer Observer) error {
	language, source, err := model.editSnapshot("run public tests")
	if err != nil {
		return model.fail(err, observer)
	}
	model.mu.Lock()
	if model.running {
		model.mu.Unlock()
		return busyError("重复运行")
	}
	model.runAttempted = true
	model.operationErr = nil
	model.coachNote = ""
	model.mu.Unlock()
	status := model.RunnerStatus()
	if !status.Enabled {
		message := strings.TrimSpace(status.Message)
		if message == "" {
			message = "代码执行未启用。"
		}
		recovery := strings.TrimSpace(status.RecoveryAction)
		if recovery == "" {
			recovery = "在设置中启用 Docker Runner；文字面试和 Coach 仍可继续。"
		}
		return model.fail(codingError(
			domainerr.CodeDependencyUnavailable,
			"run public tests",
			message,
			recovery,
			true,
		), observer)
	}
	if strings.TrimSpace(source) == "" {
		return model.fail(codingError(
			domainerr.CodeValidation,
			"run public tests",
			"运行前代码不能为空。",
			"返回编辑器补充实现后重试。",
			false,
		), observer)
	}
	model.mu.Lock()
	model.running = true
	model.runStarted = model.now().UTC()
	model.elapsed = 0
	model.mu.Unlock()
	model.publish(async.NewPending[Progress](), observer)
	model.stream(StageSaving, "正在保存待运行代码快照", observer)
	if _, saveErr := model.service.SaveSource(
		ctx, model.sessionID, model.questionID, language, source, nil,
	); saveErr != nil {
		return model.fail(safeCodingFailure("save before run", saveErr), observer)
	}
	runID := strings.TrimSpace(model.nextRunID())
	if runID == "" {
		return model.fail(codingError(
			domainerr.CodeInvalidState,
			"run public tests",
			"无法创建本次运行标识。",
			"保留草稿并重新运行。",
			true,
		), observer)
	}
	snapshot, runErr := model.service.Run(
		ctx,
		corecoding.RunRequest{
			SessionID: model.sessionID, QuestionID: model.questionID,
			Language: language, RunID: runID,
		},
		func(state async.State[corecoding.Progress]) {
			if state.Phase != async.Streaming || state.Value == nil {
				return
			}
			model.stream(StageRunning, state.Value.Message, observer)
		},
	)
	if runErr != nil {
		return model.fail(safeCodingFailure("run public tests", runErr), observer)
	}
	model.mu.Lock()
	copy := cloneSnapshot(snapshot)
	model.workspace.LatestRun = &copy
	model.running = false
	model.elapsed = max(model.elapsed, model.now().UTC().Sub(model.runStarted))
	model.operationErr = nil
	model.mu.Unlock()
	model.succeed(StageReady, "公开测试运行完成", observer)
	return nil
}

// ExplainFailure asks Coach about the latest persisted failed run only.
func (model *Model) ExplainFailure(ctx context.Context, observer Observer) error {
	if model == nil {
		return codingError(domainerr.CodeInvalidState, "explain code failure", "代码工作台当前不可用。", "重新打开代码题。", false)
	}
	model.mu.RLock()
	latest := cloneSnapshotPointer(model.workspace.LatestRun)
	running := model.running
	model.mu.RUnlock()
	if running {
		return busyError("解释错误")
	}
	if latest == nil || latest.Result.Status == corecoding.RunPassed {
		return model.fail(codingError(
			domainerr.CodePolicyDenied,
			"explain code failure",
			"只有已运行且未通过的代码可以请求错误解释。",
			"先运行公开测试，或继续独立检查当前实现。",
			false,
		), observer)
	}
	if model.coach == nil {
		return model.fail(codingError(
			domainerr.CodeDependencyUnavailable,
			"explain code failure",
			"Coach 当前不可用。",
			"继续独立排查，或返回文字面试后重试。",
			true,
		), observer)
	}
	requestID := strings.TrimSpace(model.nextCoachRequestID())
	if requestID == "" {
		return model.fail(codingError(domainerr.CodeInvalidState, "explain code failure", "无法创建本次 Coach 请求。", "保持当前运行摘要并重试。", true), observer)
	}
	model.publish(async.NewPending[Progress](), observer)
	model.stream(StageExplaining, "Coach 正在解释已运行错误", observer)
	result, err := model.coach.Ask(
		ctx,
		corecoach.AskRequest{
			SessionID: model.sessionID, QuestionID: model.questionID,
			RequestID: requestID, Intent: contracts.CoachExplainFailure,
			RequestedLevel: contracts.HelpL1,
			UserRequest:    "解释已运行代码的失败类型并给出下一步排查方向；不要提供完整实现。",
		},
		func(state async.State[corecoach.Progress]) {
			if state.Phase == async.Streaming && state.Value != nil {
				model.stream(StageExplaining, state.Value.Message, observer)
			}
		},
	)
	if err != nil {
		return model.fail(safeCodingFailure("explain code failure", err), observer)
	}
	note := strings.TrimSpace(result.Response.RecommendedAction)
	if note == "" {
		note = "根据公开测试状态检查边界条件与不变量，不提供完整实现。"
	}
	model.mu.Lock()
	model.coachNote = note
	model.operationErr = nil
	model.mu.Unlock()
	model.succeed(StageReady, "Coach 错误解释已就绪", observer)
	return nil
}

// HandleKey applies focus/edit keys and returns async/controller actions.
// Unmodified letters remain available to the editor; global commands use Ctrl.
func (model *Model) HandleKey(key string) Action {
	if model == nil {
		return Action{}
	}
	key = strings.ToLower(strings.TrimSpace(key))
	model.mu.Lock()
	defer model.mu.Unlock()
	if model.helpOpen {
		if key == "escape" || key == "esc" || key == "?" {
			model.helpOpen = false
			model.focus.CloseOverlay()
		}
		return Action{}
	}
	switch key {
	case "?":
		if model.focus.OpenOverlay(focusHelp) == nil {
			model.helpOpen = true
		}
	case "tab":
		model.focus.Handle(layout.KeyTab)
	case "shift+tab":
		model.focus.Handle(layout.KeyShiftTab)
	case "left":
		if model.focus.Active() == focusEditor {
			model.cursorRune = max(0, model.cursorRune-1)
		}
	case "right":
		if model.focus.Active() == focusEditor {
			model.cursorRune = min(utf8.RuneCountInString(model.source), model.cursorRune+1)
		}
	case "backspace":
		if model.focus.Active() == focusEditor && model.cursorRune > 0 {
			runes := []rune(model.source)
			offset := min(model.cursorRune, len(runes))
			model.source = string(append(append([]rune{}, runes[:offset-1]...), runes[offset:]...))
			model.cursorRune = offset - 1
			model.draftRestored = false
		}
	case "enter":
		if model.focus.Active() == focusEditor {
			runes := []rune(model.source)
			offset := min(model.cursorRune, len(runes))
			model.source = string(append(append(append([]rune{}, runes[:offset]...), '\n'), runes[offset:]...))
			model.cursorRune = offset + 1
			model.draftRestored = false
		}
	case "up", "k":
		if model.focus.Active() == focusSpec {
			model.specOffset = max(0, model.specOffset-1)
		}
	case "down", "j":
		if model.focus.Active() == focusSpec {
			model.specOffset++
		}
	case "ctrl+s":
		if !model.running {
			return Action{Intent: IntentSave}
		}
	case "ctrl+1", "alt+1":
		if !model.running {
			return Action{Intent: IntentSelectLanguage, Language: corecoding.LanguagePython}
		}
	case "ctrl+2", "alt+2":
		if !model.running {
			return Action{Intent: IntentSelectLanguage, Language: corecoding.LanguageJavaScript}
		}
	case "ctrl+3", "alt+3":
		if !model.running {
			return Action{Intent: IntentSelectLanguage, Language: corecoding.LanguageJava}
		}
	case "ctrl+f":
		if !model.running {
			return Action{Intent: IntentFormat}
		}
	case "ctrl+z":
		if !model.running {
			return Action{Intent: IntentReset}
		}
	case "ctrl+r":
		if !model.running {
			return Action{Intent: IntentRun}
		}
	case "ctrl+e":
		if !model.running && model.workspace.LatestRun != nil &&
			model.workspace.LatestRun.Result.Status != corecoding.RunPassed {
			return Action{Intent: IntentExplain}
		}
	case "ctrl+h":
		return Action{Destination: DestinationInterview}
	case "escape", "esc":
		return Action{}
	}
	return Action{}
}

// Execute runs one action returned by HandleKey.
func (model *Model) Execute(
	ctx context.Context,
	action Action,
	observer Observer,
) error {
	switch action.Intent {
	case IntentNone:
		return nil
	case IntentSave:
		return model.SaveDraft(ctx, observer)
	case IntentSelectLanguage:
		return model.SelectLanguage(ctx, action.Language, observer)
	case IntentFormat:
		return model.FormatSource(ctx, observer)
	case IntentReset:
		return model.ResetTemplate(ctx, observer)
	case IntentRun:
		return model.Run(ctx, observer)
	case IntentExplain:
		return model.ExplainFailure(ctx, observer)
	default:
		return codingError(domainerr.CodeValidation, "execute coding action", "代码工作台操作无效。", "使用快捷键帮助中的操作。", false)
	}
}

func (model *Model) editSnapshot(
	operation string,
) (corecoding.Language, string, *domainerr.Error) {
	if model == nil || model.service == nil {
		return "", "", codingError(domainerr.CodeDependencyUnavailable, operation, "代码工作台当前不可用。", "返回文字面试并稍后重试。", true)
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	if !model.loaded {
		return "", "", codingError(domainerr.CodeInvalidState, operation, "代码题尚未加载。", "等待代码工作台恢复后重试。", false)
	}
	return model.language, model.source, nil
}

func (model *Model) mergeWorkspace(
	workspace corecoding.Workspace,
	language corecoding.Language,
	savedSource string,
) {
	model.mu.Lock()
	defer model.mu.Unlock()
	currentSource := model.source
	model.workspace = cloneWorkspace(workspace)
	model.language = language
	if currentSource != savedSource {
		model.workspace.Draft.Sources[language] = currentSource
		model.source = currentSource
	} else {
		model.source = workspace.Draft.Sources[language]
	}
	model.cursorRune = min(model.cursorRune, utf8.RuneCountInString(model.source))
	model.operationErr = nil
}

func (model *Model) replaceActiveWorkspace(
	workspace corecoding.Workspace,
	language corecoding.Language,
	restored bool,
) {
	model.mu.Lock()
	defer model.mu.Unlock()
	model.workspace = cloneWorkspace(workspace)
	model.language = language
	model.source = workspace.Draft.Sources[language]
	model.cursorRune = utf8.RuneCountInString(model.source)
	model.draftRestored = restored
	model.operationErr = nil
}

func (model *Model) publish(state async.State[Progress], observer Observer) {
	if model == nil {
		return
	}
	model.mu.Lock()
	model.operation = state
	model.mu.Unlock()
	if observer != nil {
		observer(state)
	}
}

func (model *Model) stream(stage Stage, message string, observer Observer) {
	model.mu.Lock()
	elapsed := model.elapsed
	if model.running {
		elapsed = max(elapsed, model.now().UTC().Sub(model.runStarted))
		model.elapsed = elapsed
	}
	model.mu.Unlock()
	progress := Progress{Stage: stage, Message: strings.TrimSpace(message), Elapsed: elapsed}
	model.publish(async.NewStreaming(&progress), observer)
}

func (model *Model) succeed(stage Stage, message string, observer Observer) {
	progress := Progress{Stage: stage, Message: message}
	model.publish(async.NewSucceeded(progress), observer)
}

func (model *Model) fail(err error, observer Observer) error {
	typed := safeCodingFailure("update coding workbench", err)
	if model != nil {
		model.mu.Lock()
		model.running = false
		model.operationErr = typed
		model.operation = async.NewFailed[Progress](typed)
		model.mu.Unlock()
	}
	if observer != nil {
		observer(async.NewFailed[Progress](typed))
	}
	return typed
}

func workspaceHasLocalState(workspace corecoding.Workspace) bool {
	if workspace.LatestRun != nil {
		return true
	}
	for _, language := range corecoding.Languages() {
		if workspace.Draft.Sources[language] != workspace.Question.Templates[language] {
			return true
		}
	}
	return false
}

func supportedLanguage(language corecoding.Language) bool {
	for _, supported := range corecoding.Languages() {
		if language == supported {
			return true
		}
	}
	return false
}

func cloneWorkspace(workspace corecoding.Workspace) corecoding.Workspace {
	copy := workspace
	copy.Question.Constraints = append([]string(nil), workspace.Question.Constraints...)
	copy.Question.Examples = append([]corecoding.Example(nil), workspace.Question.Examples...)
	copy.Question.Rubric = append([]corecoding.RubricItem(nil), workspace.Question.Rubric...)
	copy.Question.Templates = make(map[corecoding.Language]string, len(workspace.Question.Templates))
	for language, source := range workspace.Question.Templates {
		copy.Question.Templates[language] = source
	}
	copy.Draft.Sources = make(map[corecoding.Language]string, len(workspace.Draft.Sources))
	for language, source := range workspace.Draft.Sources {
		copy.Draft.Sources[language] = source
	}
	copy.LatestRun = cloneSnapshotPointer(workspace.LatestRun)
	return copy
}

func cloneSnapshotPointer(snapshot *corecoding.RunSnapshot) *corecoding.RunSnapshot {
	if snapshot == nil {
		return nil
	}
	copy := cloneSnapshot(*snapshot)
	return &copy
}

func cloneSnapshot(snapshot corecoding.RunSnapshot) corecoding.RunSnapshot {
	copy := snapshot
	copy.Result.PublicTests = append([]corecoding.PublicTestResult(nil), snapshot.Result.PublicTests...)
	return copy
}

func busyError(action string) *domainerr.Error {
	return codingError(
		domainerr.CodePolicyDenied,
		"update coding workbench",
		"公开测试正在运行，不能"+action+"。",
		"编辑器仍可输入；等待本次运行结束后再操作。",
		false,
	)
}

func safeCodingFailure(operation string, err error) *domainerr.Error {
	if err == nil {
		return codingError(domainerr.CodeInvalidState, operation, "代码工作台状态无效。", "保留草稿并重新打开代码题。", false)
	}
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		operation,
		"coding",
		"代码工作台操作未能安全完成。",
		"当前草稿已保留；返回编辑器后重试。",
		true,
		err,
	)
}

func codingError(
	code domainerr.Code,
	operation string,
	message string,
	recovery string,
	retryable bool,
) *domainerr.Error {
	return domainerr.New(code, operation, message, recovery, retryable)
}

func randomID(prefix string) string {
	buffer := make([]byte, 8)
	if _, err := rand.Read(buffer); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(buffer)
}
