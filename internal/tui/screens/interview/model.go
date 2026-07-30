// Package interview implements the P-04 keyboard-first text interview room.
package interview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

const (
	focusComposer = "composer"
	focusTrace    = "trace"
	focusSession  = "session"
	focusHelp     = "help"
)

// Stage identifies one visible interview-room operation.
type Stage string

const (
	StageIdle     Stage = "idle"
	StageLoading  Stage = "loading"
	StageSaving   Stage = "saving"
	StageThinking Stage = "thinking"
	StageReady    Stage = "ready"
	StagePaused   Stage = "paused"
	StageEnding   Stage = "ending"
	StageComplete Stage = "complete"
)

// Progress is the typed screen lifecycle payload.
type Progress struct {
	Stage   Stage
	Message string
}

// Observer receives screen-level lifecycle states.
type Observer func(async.State[Progress])

// Room is the complete P-04 boundary over the core interview state machine.
type Room interface {
	Load(context.Context, string) (coreinterview.Snapshot, error)
	Start(context.Context, string) (coreinterview.Snapshot, error)
	SaveDraft(
		context.Context,
		string,
		string,
	) (coreinterview.Snapshot, error)
	Submit(
		context.Context,
		coreinterview.SubmitRequest,
		coreinterview.Observer,
	) (coreinterview.SubmitResult, error)
	Pause(context.Context, string, string) (coreinterview.Snapshot, error)
	Resume(context.Context, string, string) (coreinterview.Snapshot, error)
	RequestEnd(
		context.Context,
		string,
		coreinterview.EndScope,
		string,
	) (coreinterview.Snapshot, error)
	CancelEnd(
		context.Context,
		string,
		coreinterview.EndScope,
		string,
	) (coreinterview.Snapshot, error)
	ConfirmEnd(
		context.Context,
		string,
		coreinterview.EndScope,
		string,
	) (coreinterview.Snapshot, error)
}

// Intent tells the application controller which room command to execute.
type Intent string

const (
	IntentNone               Intent = ""
	IntentSubmit             Intent = "submit"
	IntentRetry              Intent = "retry"
	IntentCancelWait         Intent = "cancel-wait"
	IntentPause              Intent = "pause"
	IntentResume             Intent = "resume"
	IntentRequestEndQuestion Intent = "request-end-question"
	IntentRequestEndSession  Intent = "request-end-session"
	IntentCancelEnd          Intent = "cancel-end"
	IntentConfirmEnd         Intent = "confirm-end"
)

// Destination is a global navigation target.
type Destination string

const (
	DestinationNone     Destination = ""
	DestinationTraining Destination = "training"
)

// Action is the controller-facing result of one key.
type Action struct {
	Intent      Intent
	Destination Destination
}

// Options constructs an interview room around one persisted Session.
type Options struct {
	SessionID       string
	Room            Room
	Now             func() time.Time
	NextSubmission  func() string
	NextOperationID func() string
	Width           int
	Height          int
	Theme           theme.Theme
}

// Model owns P-04 draft, focus, trace selection, async state, and recovery.
type Model struct {
	mu sync.RWMutex

	sessionID       string
	room            Room
	now             func() time.Time
	nextSubmission  func() string
	nextOperationID func() string
	focus           *layout.FocusModel
	snapshot        coreinterview.Snapshot
	draft           string
	lastSubmission  coreinterview.SubmitRequest
	operation       async.State[Progress]
	cancelWaiting   context.CancelFunc
	traceSelected   int
	helpOpen        bool
	currentTime     time.Time

	Width  int
	Height int
	Theme  theme.Theme
}

// New creates a room model without reading storage.
func New(options Options) (*Model, error) {
	sessionID := strings.TrimSpace(options.SessionID)
	if sessionID == "" {
		return nil, roomError(
			domainerr.CodeValidation,
			"会话 ID 不能为空。",
			"返回训练主页选择一场会话。",
			false,
		)
	}
	focus, err := layout.NewFocusModel(
		focusComposer,
		focusTrace,
		focusSession,
	)
	if err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	nextSubmission := options.NextSubmission
	if nextSubmission == nil {
		nextSubmission = func() string {
			return randomID("answer")
		}
	}
	nextOperationID := options.NextOperationID
	if nextOperationID == nil {
		nextOperationID = func() string {
			return randomID("control")
		}
	}
	model := &Model{
		sessionID:       sessionID,
		room:            options.Room,
		now:             now,
		nextSubmission:  nextSubmission,
		nextOperationID: nextOperationID,
		focus:           focus,
		operation: async.NewSucceeded(Progress{
			Stage:   StageIdle,
			Message: "文字面试室等待恢复",
		}),
		currentTime: now().UTC(),
		Width:       options.Width,
		Height:      options.Height,
		Theme:       options.Theme,
	}
	model.updateFocusOrderLocked()
	return model, nil
}

// State returns the current typed lifecycle.
func (model *Model) State() async.State[Progress] {
	if model == nil {
		return async.State[Progress]{}
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return cloneState(model.operation)
}

// Snapshot returns a defensive copy of the recovered core state.
func (model *Model) Snapshot() coreinterview.Snapshot {
	if model == nil {
		return coreinterview.Snapshot{}
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return cloneSnapshot(model.snapshot)
}

// Draft returns the preserved local answer buffer.
func (model *Model) Draft() string {
	if model == nil {
		return ""
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.draft
}

// Resize updates responsive focus order without losing draft or selection.
func (model *Model) Resize(width, height int) {
	if model == nil {
		return
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	model.Width = width
	model.Height = height
	model.updateFocusOrderLocked()
}

// Tick advances the visible timer without mutating persisted events.
func (model *Model) Tick(now time.Time) {
	if model == nil {
		return
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if now.IsZero() {
		now = model.now()
	}
	model.currentTime = now.UTC()
}

// Load restores Session, current question, draft, and any retryable answer.
func (model *Model) Load(ctx context.Context, observer Observer) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	model.setState(async.NewPending[Progress](), observer)
	if model.room == nil {
		return model.fail(unavailableRoom(), observer)
	}
	snapshot, err := model.room.Load(ctx, model.sessionID)
	if err != nil {
		return model.fail(roomFailure(err), observer)
	}
	if snapshot.Phase == coreinterview.PhaseNotStarted {
		progress := Progress{
			Stage:   StageLoading,
			Message: "正在载入第一道题",
		}
		model.setState(async.NewStreaming(&progress), observer)
		snapshot, err = model.room.Start(ctx, model.sessionID)
		if err != nil {
			return model.fail(roomFailure(err), observer)
		}
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	if model.lastSubmission.SubmissionID != "" {
		model.operation = async.NewFailed[Progress](roomError(
			domainerr.CodeDependencyUnavailable,
			"已恢复一条尚未收到面试官动作的已提交回答。",
			"按 [t] 重试，或按 [x] 安全结束本题。",
			true,
		))
	} else {
		stage, message := readyStage(snapshot)
		model.operation = async.NewSucceeded(Progress{
			Stage:   stage,
			Message: message,
		})
	}
	state := cloneState(model.operation)
	model.mu.Unlock()
	notify(observer, state)
	return nil
}

// UpdateDraft persists the current buffer. Changing a failed submitted answer
// starts a new append-only correction rather than reusing its idempotency key.
func (model *Model) UpdateDraft(
	ctx context.Context,
	value string,
) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	model.mu.Lock()
	if model.isBusyLocked() {
		model.mu.Unlock()
		return roomError(
			domainerr.CodeInvalidState,
			"面试官思考时不能修改当前输入。",
			"按 [Esc] 停止等待，或等待面试官动作完成。",
			false,
		)
	}
	model.draft = value
	if model.lastSubmission.SubmissionID != "" &&
		strings.TrimSpace(value) != model.lastSubmission.Answer {
		model.lastSubmission = coreinterview.SubmitRequest{}
	}
	model.mu.Unlock()
	if model.room == nil {
		return model.recordFailure(unavailableRoom())
	}
	snapshot, err := model.room.SaveDraft(ctx, model.sessionID, value)
	if err != nil {
		return model.recordFailure(roomFailure(err))
	}
	model.mu.Lock()
	model.snapshot = cloneSnapshot(snapshot)
	model.draft = value
	model.mu.Unlock()
	return nil
}

// Submit records a new answer and waits for one Interviewer action.
func (model *Model) Submit(ctx context.Context, observer Observer) error {
	return model.submit(ctx, false, observer)
}

// Retry reuses the same submission ID after Provider failure.
func (model *Model) Retry(ctx context.Context, observer Observer) error {
	return model.submit(ctx, true, observer)
}

func (model *Model) submit(
	ctx context.Context,
	retry bool,
	observer Observer,
) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	model.mu.Lock()
	if model.isBusyLocked() {
		model.mu.Unlock()
		return roomError(
			domainerr.CodeInvalidState,
			"面试官仍在处理上一份回答。",
			"等待完成，或按 [Esc] 停止等待。",
			false,
		)
	}
	answer := strings.TrimSpace(model.draft)
	if answer == "" {
		model.mu.Unlock()
		return model.recordFailure(roomError(
			domainerr.CodeValidation,
			"回答不能为空。",
			"填写回答后按 [Ctrl+Enter] 提交。",
			false,
		))
	}
	request := model.lastSubmission
	if !retry || request.SubmissionID == "" {
		request = coreinterview.SubmitRequest{
			SessionID:    model.sessionID,
			SubmissionID: strings.TrimSpace(model.nextSubmission()),
			Answer:       answer,
		}
		model.lastSubmission = request
	} else if request.Answer != answer {
		model.mu.Unlock()
		return model.recordFailure(roomError(
			domainerr.CodePolicyDenied,
			"重试必须使用已经提交的原回答。",
			"恢复原回答重试，或把修改后的内容作为新回答提交。",
			false,
		))
	}
	if request.SubmissionID == "" {
		model.mu.Unlock()
		return model.recordFailure(roomError(
			domainerr.CodeInvalidState,
			"无法生成回答提交标识。",
			"重新打开当前会话后重试。",
			true,
		))
	}
	waitContext, cancel := context.WithCancel(ctx)
	model.cancelWaiting = cancel
	model.operation = async.NewPending[Progress]()
	state := cloneState(model.operation)
	model.mu.Unlock()
	notify(observer, state)

	if model.room == nil {
		cancel()
		return model.fail(unavailableRoom(), observer)
	}
	result, err := model.room.Submit(
		waitContext,
		request,
		func(coreState async.State[coreinterview.Progress]) {
			if coreState.Phase != async.Streaming {
				return
			}
			progress := Progress{
				Stage:   StageThinking,
				Message: "interviewer: ▌",
			}
			model.setState(async.NewStreaming(&progress), observer)
		},
	)
	cancel()
	model.mu.Lock()
	model.cancelWaiting = nil
	model.mu.Unlock()
	if err != nil {
		cleanupContext := context.WithoutCancel(ctx)
		model.restoreFailedAnswer(cleanupContext, answer)
		return model.fail(roomFailure(err), observer)
	}

	model.mu.Lock()
	model.applySnapshotLocked(result.Snapshot)
	model.draft = ""
	model.lastSubmission = coreinterview.SubmitRequest{}
	stage, message := readyStage(result.Snapshot)
	model.operation = async.NewSucceeded(Progress{
		Stage:   stage,
		Message: message,
	})
	state = cloneState(model.operation)
	model.mu.Unlock()
	notify(observer, state)
	return nil
}

// CancelWaiting stops only the Provider wait. The core answer event remains.
func (model *Model) CancelWaiting() bool {
	if model == nil {
		return false
	}
	model.mu.RLock()
	cancel := model.cancelWaiting
	model.mu.RUnlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

// TogglePause persists an explicit pause or resume event.
func (model *Model) TogglePause(ctx context.Context) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	model.mu.RLock()
	phase := model.snapshot.Phase
	model.mu.RUnlock()
	if phase == coreinterview.PhasePaused {
		return model.resume(ctx)
	}
	return model.pause(ctx)
}

func (model *Model) pause(ctx context.Context) error {
	if model.room == nil {
		return model.recordFailure(unavailableRoom())
	}
	snapshot, err := model.room.Pause(
		ctx,
		model.sessionID,
		model.nextControlID(),
	)
	if err != nil {
		return model.recordFailure(roomFailure(err))
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	model.operation = async.NewSucceeded(Progress{
		Stage:   StagePaused,
		Message: "面试已暂停",
	})
	model.mu.Unlock()
	return nil
}

func (model *Model) resume(ctx context.Context) error {
	if model.room == nil {
		return model.recordFailure(unavailableRoom())
	}
	snapshot, err := model.room.Resume(
		ctx,
		model.sessionID,
		model.nextControlID(),
	)
	if err != nil {
		return model.recordFailure(roomFailure(err))
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	model.operation = async.NewSucceeded(Progress{
		Stage:   StageReady,
		Message: "面试已恢复",
	})
	model.mu.Unlock()
	return nil
}

// RequestEnd begins the required two-step question/session confirmation.
func (model *Model) RequestEnd(
	ctx context.Context,
	scope coreinterview.EndScope,
) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	if model.room == nil {
		return model.recordFailure(unavailableRoom())
	}
	snapshot, err := model.room.RequestEnd(
		ctx,
		model.sessionID,
		scope,
		model.nextControlID(),
	)
	if err != nil {
		return model.recordFailure(roomFailure(err))
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	model.operation = async.NewSucceeded(Progress{
		Stage:   StageEnding,
		Message: "等待确认结束" + endLabel(scope),
	})
	model.mu.Unlock()
	return nil
}

// CancelEnd cancels the persisted first confirmation step.
func (model *Model) CancelEnd(ctx context.Context) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	model.mu.RLock()
	pending := clonePending(model.snapshot.PendingEnd)
	model.mu.RUnlock()
	if pending == nil {
		return model.recordFailure(roomError(
			domainerr.CodeInvalidState,
			"没有等待取消的结束操作。",
			"继续当前题目，或重新发起结束操作。",
			false,
		))
	}
	if model.room == nil {
		return model.recordFailure(unavailableRoom())
	}
	snapshot, err := model.room.CancelEnd(
		ctx,
		model.sessionID,
		pending.Scope,
		pending.OperationID,
	)
	if err != nil {
		return model.recordFailure(roomFailure(err))
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	model.operation = async.NewSucceeded(Progress{
		Stage:   StageReady,
		Message: "已取消结束操作",
	})
	model.mu.Unlock()
	return nil
}

// ConfirmEnd applies the matching second confirmation step.
func (model *Model) ConfirmEnd(ctx context.Context) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	model.mu.RLock()
	pending := clonePending(model.snapshot.PendingEnd)
	questionID := currentQuestionID(model.snapshot)
	model.mu.RUnlock()
	if pending == nil {
		return model.recordFailure(roomError(
			domainerr.CodePolicyDenied,
			"结束操作需要先请求再确认。",
			"按 [x] 结束本题，或按 [q] 结束面试。",
			false,
		))
	}
	if model.room == nil {
		return model.recordFailure(unavailableRoom())
	}
	snapshot, err := model.room.ConfirmEnd(
		ctx,
		model.sessionID,
		pending.Scope,
		pending.OperationID,
	)
	if err != nil {
		return model.recordFailure(roomFailure(err))
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	if snapshot.Phase == coreinterview.PhaseCompleted ||
		currentQuestionID(snapshot) != questionID {
		model.draft = ""
		model.lastSubmission = coreinterview.SubmitRequest{}
	}
	stage, message := readyStage(snapshot)
	model.operation = async.NewSucceeded(Progress{
		Stage:   stage,
		Message: message,
	})
	model.mu.Unlock()
	return nil
}

// HandleKey applies keyboard-only focus and command behavior.
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
	if model.isBusyLocked() {
		if key == "escape" || key == "esc" {
			return Action{Intent: IntentCancelWait}
		}
		return Action{}
	}
	if model.snapshot.PendingEnd != nil {
		switch key {
		case "y", "enter":
			return Action{Intent: IntentConfirmEnd}
		case "n", "escape", "esc":
			return Action{Intent: IntentCancelEnd}
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
	case "up", "k":
		model.moveTraceLocked(-1)
	case "down", "j":
		model.moveTraceLocked(1)
	case "ctrl+enter":
		if strings.TrimSpace(model.draft) != "" &&
			model.snapshot.Phase == coreinterview.PhaseAwaitingAnswer {
			return Action{Intent: IntentSubmit}
		}
	case "t":
		if model.operation.Phase == async.Failed &&
			model.canRetryLocked() {
			return Action{Intent: IntentRetry}
		}
	case "p":
		if model.snapshot.Phase == coreinterview.PhasePaused {
			return Action{Intent: IntentResume}
		}
		if model.snapshot.Phase == coreinterview.PhaseAwaitingAnswer {
			return Action{Intent: IntentPause}
		}
	case "x":
		if model.snapshot.CurrentQuestion != nil &&
			model.snapshot.Phase != coreinterview.PhaseCompleted {
			return Action{Intent: IntentRequestEndQuestion}
		}
	case "q":
		if model.snapshot.Phase == coreinterview.PhaseCompleted {
			return Action{Destination: DestinationTraining}
		}
		if model.snapshot.CurrentQuestion != nil {
			return Action{Intent: IntentRequestEndSession}
		}
	case "h":
		if model.snapshot.Phase == coreinterview.PhaseCompleted ||
			model.snapshot.CurrentQuestion == nil {
			return Action{Destination: DestinationTraining}
		}
	case "escape", "esc":
		return Action{}
	}
	return Action{}
}

func (model *Model) restoreFailedAnswer(
	ctx context.Context,
	answer string,
) {
	if model.room == nil {
		return
	}
	snapshot, err := model.room.SaveDraft(ctx, model.sessionID, answer)
	if err != nil {
		return
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	model.draft = answer
	model.mu.Unlock()
}

func (model *Model) applySnapshotLocked(
	snapshot coreinterview.Snapshot,
) {
	model.snapshot = cloneSnapshot(snapshot)
	model.currentTime = model.now().UTC()
	if snapshot.Draft != nil {
		model.draft = snapshot.Draft.Content
	} else if snapshot.Phase == coreinterview.PhaseCompleted {
		model.draft = ""
	}
	model.restorePendingSubmissionLocked()
	if count := len(snapshot.Events); count > 0 {
		model.traceSelected = count - 1
	} else {
		model.traceSelected = 0
	}
}

func (model *Model) restorePendingSubmissionLocked() {
	answerPrefix := "ic/interview/" + model.sessionID + "/answer/"
	actionPrefix := "ic/interview/" + model.sessionID + "/action/"
	acted := make(map[string]struct{})
	for _, event := range model.snapshot.Events {
		value, found := strings.CutPrefix(event.EventID, actionPrefix)
		if !found {
			continue
		}
		submissionID, _, found := strings.Cut(value, "/")
		if found {
			acted[submissionID] = struct{}{}
		}
	}
	for index := len(model.snapshot.Events) - 1; index >= 0; index-- {
		event := model.snapshot.Events[index]
		submissionID, found := strings.CutPrefix(
			event.EventID,
			answerPrefix,
		)
		if !found || event.Speaker != db.SpeakerUser {
			continue
		}
		if _, completed := acted[submissionID]; completed {
			continue
		}
		model.lastSubmission = coreinterview.SubmitRequest{
			SessionID:    model.sessionID,
			SubmissionID: submissionID,
			Answer:       event.Content,
		}
		if strings.TrimSpace(model.draft) == "" {
			model.draft = event.Content
		}
		return
	}
	if model.operation.Phase != async.Failed {
		model.lastSubmission = coreinterview.SubmitRequest{}
	}
}

func (model *Model) updateFocusOrderLocked() {
	if model.focus == nil {
		return
	}
	plan := layout.Calculate(model.Width, model.Height)
	targets := []string{focusComposer, focusSession}
	if plan.Mode == layout.Wide {
		targets = []string{focusComposer, focusTrace, focusSession}
	}
	_ = model.focus.SetVisible(targets...)
}

func (model *Model) moveTraceLocked(delta int) {
	if model.focus.Active() != focusTrace ||
		len(model.snapshot.Events) == 0 {
		return
	}
	count := len(model.snapshot.Events)
	model.traceSelected = (model.traceSelected + delta%count + count) % count
}

func (model *Model) nextControlID() string {
	value := strings.TrimSpace(model.nextOperationID())
	if value != "" {
		return value
	}
	return randomID("control")
}

func (model *Model) setState(
	state async.State[Progress],
	observer Observer,
) {
	model.mu.Lock()
	model.operation = state
	copy := cloneState(state)
	model.mu.Unlock()
	notify(observer, copy)
}

func (model *Model) fail(err error, observer Observer) error {
	typed := roomFailure(err)
	model.setState(async.NewFailed[Progress](typed), observer)
	return typed
}

func (model *Model) recordFailure(err error) error {
	typed := roomFailure(err)
	model.mu.Lock()
	model.operation = async.NewFailed[Progress](typed)
	model.mu.Unlock()
	return typed
}

func (model *Model) isBusyLocked() bool {
	return model.operation.Phase == async.Pending ||
		model.operation.Phase == async.Streaming
}

func (model *Model) canRetryLocked() bool {
	return model.lastSubmission.SubmissionID != "" &&
		strings.TrimSpace(model.draft) == model.lastSubmission.Answer
}

func readyStage(snapshot coreinterview.Snapshot) (Stage, string) {
	switch snapshot.Phase {
	case coreinterview.PhasePaused:
		return StagePaused, "面试已暂停"
	case coreinterview.PhaseCompleted:
		return StageComplete, "文字面试已完成，等待评估"
	default:
		return StageReady, "当前题目可以作答"
	}
}

func currentQuestionID(snapshot coreinterview.Snapshot) string {
	if snapshot.CurrentQuestion == nil {
		return ""
	}
	return snapshot.CurrentQuestion.ID
}

func endLabel(scope coreinterview.EndScope) string {
	if scope == coreinterview.EndSession {
		return "面试"
	}
	return "本题"
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func unavailableRoom() *domainerr.Error {
	return roomError(
		domainerr.CodeDependencyUnavailable,
		"文字面试服务不可用。",
		"返回训练主页后重新打开会话。",
		true,
	)
}

func roomError(
	code domainerr.Code,
	message string,
	recovery string,
	retryable bool,
) *domainerr.Error {
	return domainerr.New(
		code,
		"update interview room",
		message,
		recovery,
		retryable,
	)
}

func roomFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"update interview room",
		"interview room",
		"无法完成文字面试操作。",
		"已提交回答和本地草稿仍保留；重试或安全结束本题。",
		true,
		err,
	)
}

func cloneState(
	state async.State[Progress],
) async.State[Progress] {
	if state.Value != nil {
		value := *state.Value
		state.Value = &value
	}
	return state
}

func cloneSnapshot(
	value coreinterview.Snapshot,
) coreinterview.Snapshot {
	questions := value.Scenario.Questions
	value.Scenario.Questions = make(
		[]contracts.ScenarioQuestion,
		len(questions),
	)
	for index, question := range questions {
		question.Rubric = slices.Clone(question.Rubric)
		question.EvidenceIDs = slices.Clone(question.EvidenceIDs)
		value.Scenario.Questions[index] = question
	}
	events := value.Events
	value.Events = make([]db.SessionEvent, len(events))
	for index, event := range events {
		event.EvidenceRefs = slices.Clone(event.EvidenceRefs)
		value.Events[index] = event
	}
	if value.CurrentQuestion != nil {
		question := *value.CurrentQuestion
		question.Rubric = slices.Clone(question.Rubric)
		question.EvidenceIDs = slices.Clone(question.EvidenceIDs)
		value.CurrentQuestion = &question
	}
	value.PendingEnd = clonePending(value.PendingEnd)
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	return value
}

func clonePending(
	value *coreinterview.PendingEnd,
) *coreinterview.PendingEnd {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func randomID(prefix string) string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err == nil {
		return prefix + "-" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UTC().UnixNano())
}
