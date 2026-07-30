package interview

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
)

// CoachRoom is the TUI boundary over the policy-enforcing Coach service.
type CoachRoom interface {
	Ask(
		context.Context,
		corecoach.AskRequest,
		corecoach.Observer,
	) (corecoach.AskResult, error)
	History(context.Context, string) ([]db.SidebarEvent, error)
	MarkOutcome(
		context.Context,
		string,
		string,
		corecoach.LearningOutcome,
	) (db.SidebarEvent, error)
}

// CoachState returns the independent Coach lifecycle.
func (model *Model) CoachState() async.State[string] {
	if model == nil {
		return async.State[string]{}
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return cloneCoachState(model.coachOperation)
}

// CoachDraft returns the local Coach input without exposing the main draft.
func (model *Model) CoachDraft() string {
	if model == nil {
		return ""
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	return model.coachDraft
}

// UpdateCoachDraft changes only the local Coach composer.
func (model *Model) UpdateCoachDraft(value string) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	model.mu.Lock()
	defer model.mu.Unlock()
	if coachBusy(model.coachOperation) {
		return coachScreenError(
			domainerr.CodeInvalidState,
			"Coach 正在处理上一条问题。",
			"返回主回答继续作答，或等待 Coach 完成。",
			false,
		)
	}
	model.coachDraft = value
	return nil
}

// AskCoach sends one shortcut or free-form request without blocking main input.
func (model *Model) AskCoach(
	ctx context.Context,
	intent contracts.CoachIntent,
	pause bool,
) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	model.mu.Lock()
	if coachBusy(model.coachOperation) {
		model.mu.Unlock()
		return coachScreenError(
			domainerr.CodeInvalidState,
			"Coach 仍在处理上一条问题。",
			"继续填写主回答，或等待 Coach 完成。",
			false,
		)
	}
	questionID := model.coachQuestionIDLocked()
	if questionID == "" {
		failure := coachScreenError(
			domainerr.CodeInvalidState,
			"当前没有可以求教的题目。",
			"返回仍在进行的题目，或结束当前会话。",
			false,
		)
		model.coachOperation = async.NewFailed[string](failure)
		model.mu.Unlock()
		return failure
	}
	if !validCoachShortcut(intent) {
		failure := coachScreenError(
			domainerr.CodeValidation,
			"Coach 快捷意图无效。",
			"选择 1–6 的快捷入口，或自由输入后提问。",
			false,
		)
		model.coachOperation = async.NewFailed[string](failure)
		model.mu.Unlock()
		return failure
	}
	model.refreshCoachPolicyLocked()
	requestText := strings.TrimSpace(model.coachDraft)
	if requestText == "" {
		requestText = coachShortcutPrompt(intent)
	}
	level := model.coachPolicy.MaxLevel
	if level == "" {
		level = contracts.HelpL1
	}
	request := corecoach.AskRequest{
		SessionID:      model.sessionID,
		QuestionID:     questionID,
		RequestID:      model.nextCoachRequestIDLocked(),
		Intent:         intent,
		RequestedLevel: level,
		UserRequest:    requestText,
		PauseForHelp:   pause,
	}
	model.lastCoachRequest = request
	model.coachOperation = async.NewPending[string]()
	coach := model.coach
	model.mu.Unlock()

	if coach == nil {
		return model.failCoach(unavailableCoach())
	}
	return model.executeCoachRequest(ctx, coach, request)
}

// RetryCoach reuses the stable request ID after a recoverable failure.
func (model *Model) RetryCoach(ctx context.Context) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	model.mu.Lock()
	if !model.canRetryCoachLocked() {
		failure := coachScreenError(
			domainerr.CodeInvalidState,
			"没有可重试的 Coach 请求。",
			"选择快捷入口，或输入新问题后提问。",
			false,
		)
		model.coachOperation = async.NewFailed[string](failure)
		model.mu.Unlock()
		return failure
	}
	request := model.lastCoachRequest
	model.coachOperation = async.NewPending[string]()
	coach := model.coach
	model.mu.Unlock()
	if coach == nil {
		return model.failCoach(unavailableCoach())
	}
	return model.executeCoachRequest(ctx, coach, request)
}

// MarkCoachOutcome records one explicit learning state for the selected event.
func (model *Model) MarkCoachOutcome(
	ctx context.Context,
	outcome corecoach.LearningOutcome,
) error {
	if model == nil {
		return errors.New("interview model is nil")
	}
	model.mu.RLock()
	event, found := model.selectedCoachEventLocked()
	coach := model.coach
	model.mu.RUnlock()
	if !found {
		return model.failCoach(coachScreenError(
			domainerr.CodeInvalidState,
			"还没有可标记的 Coach 回复。",
			"先向 Coach 提问，再标记学习结果。",
			false,
		))
	}
	if coach == nil {
		return model.failCoach(unavailableCoach())
	}
	updated, err := coach.MarkOutcome(
		ctx,
		model.sessionID,
		event.ID,
		outcome,
	)
	if err != nil {
		return model.failCoach(err)
	}
	model.mu.Lock()
	for index := range model.coachHistory {
		if model.coachHistory[index].ID == updated.ID {
			model.coachHistory[index] = cloneCoachEvent(updated)
			break
		}
	}
	message := "学习状态已更新"
	model.coachOperation = async.NewSucceeded(message)
	model.mu.Unlock()
	return nil
}

func (model *Model) executeCoachRequest(
	ctx context.Context,
	coach CoachRoom,
	request corecoach.AskRequest,
) error {
	result, err := coach.Ask(
		ctx,
		request,
		func(state async.State[corecoach.Progress]) {
			model.applyCoachProgress(state)
		},
	)
	if request.PauseForHelp {
		model.refreshMainAfterCoachPause(context.WithoutCancel(ctx))
	}
	if err != nil {
		return model.failCoach(err)
	}
	model.mu.Lock()
	model.upsertCoachEventLocked(result.Event)
	model.coachUsage = result.Usage
	model.coachDraft = ""
	model.lastCoachRequest = corecoach.AskRequest{}
	message := "Coach 回复已保存"
	model.coachOperation = async.NewSucceeded(message)
	model.mu.Unlock()
	return nil
}

func (model *Model) applyCoachProgress(
	state async.State[corecoach.Progress],
) {
	model.mu.Lock()
	defer model.mu.Unlock()
	switch state.Phase {
	case async.Pending:
		model.coachOperation = async.NewPending[string]()
	case async.Streaming:
		message := "coach: thinking"
		if state.Value != nil &&
			strings.TrimSpace(state.Value.Message) != "" {
			message = state.Value.Message
		}
		model.coachOperation = async.NewStreaming(&message)
	case async.Succeeded:
		message := "Coach 回复已保存"
		if state.Value != nil &&
			strings.TrimSpace(state.Value.Message) != "" {
			message = state.Value.Message
		}
		model.coachOperation = async.NewSucceeded(message)
	case async.Failed:
		if state.Err != nil {
			model.coachOperation = async.NewFailed[string](state.Err)
		}
	}
}

func (model *Model) loadCoachHistory(ctx context.Context) {
	model.mu.RLock()
	coach := model.coach
	model.mu.RUnlock()
	if coach == nil {
		model.mu.Lock()
		model.coachHistory = []db.SidebarEvent{}
		model.refreshCoachPolicyLocked()
		model.coachOperation = async.NewFailed[string](unavailableCoach())
		model.mu.Unlock()
		return
	}
	history, err := coach.History(ctx, model.sessionID)
	if err != nil {
		_ = model.failCoach(err)
		return
	}
	model.mu.Lock()
	model.coachHistory = cloneCoachHistory(history)
	model.refreshCoachPolicyLocked()
	message := "Coach ready"
	model.coachOperation = async.NewSucceeded(message)
	model.mu.Unlock()
}

func (model *Model) refreshCoachPolicyLocked() {
	if model.snapshot.Scenario.Mode == "" {
		model.coachPolicy = corecoach.Policy{}
		model.coachUsage = corecoach.Usage{}
		return
	}
	state := corecoach.QuestionActive
	if model.snapshot.Phase == coreinterview.PhaseQuestionComplete {
		state = corecoach.QuestionClosed
	}
	if model.snapshot.Phase == coreinterview.PhaseCompleted {
		state = corecoach.SessionClosed
	}
	policy, err := corecoach.PolicyFor(model.snapshot.Scenario.Mode, state)
	if err != nil {
		return
	}
	model.coachPolicy = policy
	questionID := model.coachQuestionIDLocked()
	used := 0
	for _, event := range model.coachHistory {
		if event.QuestionID == questionID {
			used++
		}
	}
	model.coachUsage = corecoach.Usage{
		Used:      used,
		Limit:     policy.Limit,
		Unlimited: policy.Limit == 0,
	}
	if policy.Limit > 0 {
		model.coachUsage.Remaining = max(0, policy.Limit-used)
	}
}

func (model *Model) refreshMainAfterCoachPause(ctx context.Context) {
	if model.room == nil {
		return
	}
	snapshot, err := model.room.Load(ctx, model.sessionID)
	if err != nil {
		return
	}
	model.mu.Lock()
	model.applySnapshotLocked(snapshot)
	model.refreshCoachPolicyLocked()
	stage, message := readyStage(snapshot)
	model.operation = async.NewSucceeded(Progress{
		Stage:   stage,
		Message: message,
	})
	model.mu.Unlock()
}

func (model *Model) handleCoachKeyLocked(key string) (bool, Action) {
	plan := layout.Calculate(model.Width, model.Height)
	coachFocused := model.focus.Active() == focusCoach
	if key == "c" && !coachFocused {
		if plan.Mode == layout.Narrow {
			if model.focus.OpenOverlay(focusCoach) == nil {
				model.coachOverlay = true
			}
		} else {
			model.focusTargetLocked(focusCoach)
		}
		return true, Action{}
	}
	if !coachFocused {
		return false, Action{}
	}
	switch key {
	case "escape", "esc", "c":
		model.closeCoachLocked()
		return true, Action{}
	case "tab":
		if !model.coachOverlay {
			model.focus.Handle(layout.KeyTab)
		}
		return true, Action{}
	case "shift+tab":
		if !model.coachOverlay {
			model.focus.Handle(layout.KeyShiftTab)
		}
		return true, Action{}
	case "?":
		if model.coachOverlay {
			model.focus.CloseOverlay()
			model.coachOverlay = false
		}
		if model.focus.OpenOverlay(focusHelp) == nil {
			model.helpOpen = true
		}
		return true, Action{}
	case "up", "k":
		model.moveCoachHistoryLocked(-1)
		return true, Action{}
	case "down", "j":
		model.moveCoachHistoryLocked(1)
		return true, Action{}
	case "ctrl+enter":
		if strings.TrimSpace(model.coachDraft) != "" {
			return true, Action{
				Intent:      IntentCoachAsk,
				CoachIntent: contracts.CoachCheckReasoning,
			}
		}
		return true, Action{}
	case "ctrl+p":
		if strings.TrimSpace(model.coachDraft) != "" {
			return true, Action{
				Intent:      IntentCoachAskPaused,
				CoachIntent: contracts.CoachCheckReasoning,
			}
		}
		return true, Action{}
	case "t":
		if model.canRetryCoachLocked() {
			return true, Action{Intent: IntentCoachRetry}
		}
		return true, Action{}
	case "u":
		if _, found := model.selectedCoachEventLocked(); found {
			return true, Action{
				Intent:       IntentCoachMark,
				CoachOutcome: corecoach.OutcomeUnderstood,
			}
		}
		return true, Action{}
	case "d":
		if _, found := model.selectedCoachEventLocked(); found {
			return true, Action{
				Intent:       IntentCoachMark,
				CoachOutcome: corecoach.OutcomeConfused,
			}
		}
		return true, Action{}
	case "r":
		if _, found := model.selectedCoachEventLocked(); found {
			return true, Action{
				Intent:       IntentCoachMark,
				CoachOutcome: corecoach.OutcomeReview,
			}
		}
		return true, Action{}
	}
	if intent, found := coachIntentForKey(key); found {
		return true, Action{
			Intent:      IntentCoachAsk,
			CoachIntent: intent,
		}
	}
	return false, Action{}
}

func (model *Model) restoreCoachFocusAfterResizeLocked(
	coachWasActive bool,
) {
	plan := layout.Calculate(model.Width, model.Height)
	if plan.Mode == layout.Narrow {
		if coachWasActive && !model.coachOverlay {
			if model.focus.OpenOverlay(focusCoach) == nil {
				model.coachOverlay = true
			}
		}
		return
	}
	if model.coachOverlay {
		model.focus.CloseOverlay()
		model.coachOverlay = false
	}
	if coachWasActive {
		model.focusTargetLocked(focusCoach)
	}
}

func (model *Model) closeCoachLocked() {
	if model.coachOverlay {
		model.focus.CloseOverlay()
		model.coachOverlay = false
		return
	}
	model.focusTargetLocked(focusComposer)
}

func (model *Model) focusTargetLocked(target string) {
	for index := 0; index < 4 && model.focus.Active() != target; index++ {
		model.focus.Next()
	}
}

func (model *Model) moveCoachHistoryLocked(delta int) {
	events := model.currentQuestionCoachEventsLocked()
	if len(events) == 0 {
		return
	}
	count := len(events)
	model.coachSelected = (model.coachSelected + delta%count + count) % count
}

func (model *Model) selectedCoachEventLocked() (db.SidebarEvent, bool) {
	events := model.currentQuestionCoachEventsLocked()
	if len(events) == 0 {
		return db.SidebarEvent{}, false
	}
	selected := min(max(model.coachSelected, 0), len(events)-1)
	return cloneCoachEvent(events[selected]), true
}

func (model *Model) currentQuestionCoachEventsLocked() []db.SidebarEvent {
	questionID := model.coachQuestionIDLocked()
	events := make([]db.SidebarEvent, 0)
	for _, event := range model.coachHistory {
		if event.QuestionID == questionID {
			events = append(events, cloneCoachEvent(event))
		}
	}
	return events
}

func (model *Model) coachQuestionIDLocked() string {
	if questionID := currentQuestionID(model.snapshot); questionID != "" {
		return questionID
	}
	if model.snapshot.Phase != coreinterview.PhaseCompleted ||
		len(model.snapshot.Scenario.Questions) == 0 {
		return ""
	}
	index := min(
		max(model.snapshot.CurrentIndex, 0),
		len(model.snapshot.Scenario.Questions)-1,
	)
	return model.snapshot.Scenario.Questions[index].ID
}

func (model *Model) upsertCoachEventLocked(event db.SidebarEvent) {
	for index := range model.coachHistory {
		if model.coachHistory[index].ID == event.ID {
			model.coachHistory[index] = cloneCoachEvent(event)
			model.coachSelected = len(model.currentQuestionCoachEventsLocked()) - 1
			model.traceSelected = len(model.traceItemsLocked()) - 1
			return
		}
	}
	model.coachHistory = append(model.coachHistory, cloneCoachEvent(event))
	model.coachSelected = len(model.currentQuestionCoachEventsLocked()) - 1
	model.traceSelected = len(model.traceItemsLocked()) - 1
}

func (model *Model) canRetryCoachLocked() bool {
	return model.coachOperation.Phase == async.Failed &&
		strings.TrimSpace(model.lastCoachRequest.RequestID) != ""
}

func (model *Model) nextCoachRequestIDLocked() string {
	value := strings.TrimSpace(model.nextCoachRequest())
	if value != "" {
		return value
	}
	return randomID("coach")
}

func (model *Model) failCoach(err error) error {
	typed := coachUIFailure(err)
	model.mu.Lock()
	model.coachOperation = async.NewFailed[string](typed)
	model.mu.Unlock()
	return typed
}

func validCoachShortcut(intent contracts.CoachIntent) bool {
	_, found := coachShortcutLabels()[intent]
	return found
}

func coachIntentForKey(key string) (contracts.CoachIntent, bool) {
	intents := []contracts.CoachIntent{
		contracts.CoachExplainConcept,
		contracts.CoachGiveHint,
		contracts.CoachAnswerStructure,
		contracts.CoachCheckReasoning,
		contracts.CoachExplainFailure,
		contracts.CoachAddToReview,
	}
	if len(key) != 1 || key[0] < '1' || key[0] > '6' {
		return "", false
	}
	return intents[int(key[0]-'1')], true
}

func coachShortcutLabels() map[contracts.CoachIntent]string {
	return map[contracts.CoachIntent]string{
		contracts.CoachExplainConcept:  "解释概念",
		contracts.CoachGiveHint:        "给我提示",
		contracts.CoachAnswerStructure: "梳理回答结构",
		contracts.CoachCheckReasoning:  "检查我的思路",
		contracts.CoachExplainFailure:  "解释失败",
		contracts.CoachAddToReview:     "加入复习",
	}
}

func coachShortcutPrompt(intent contracts.CoachIntent) string {
	label := coachShortcutLabels()[intent]
	if label == "" {
		label = "检查我的思路"
	}
	return "请" + label + "，保持当前练习模式的帮助边界。"
}

func coachBusy(state async.State[string]) bool {
	return state.Phase == async.Pending || state.Phase == async.Streaming
}

func unavailableCoach() *domainerr.Error {
	return coachScreenError(
		domainerr.CodeDependencyUnavailable,
		"Coach Provider 暂不可用。",
		"继续独立作答，或检查模型设置后重试。",
		true,
	)
}

func coachScreenError(
	code domainerr.Code,
	message string,
	recovery string,
	retryable bool,
) *domainerr.Error {
	return domainerr.New(
		code,
		"update Coach pane",
		message,
		recovery,
		retryable,
	)
}

func coachUIFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"update Coach pane",
		"Coach Provider",
		"Coach 暂时不可用。",
		"主回答不受影响；继续独立作答或稍后重试。",
		true,
		err,
	)
}

func cloneCoachState(state async.State[string]) async.State[string] {
	copy := async.State[string]{
		Phase: state.Phase,
		Err:   state.Err,
	}
	if state.Value != nil {
		value := *state.Value
		copy.Value = &value
	}
	return copy
}

func cloneCoachHistory(events []db.SidebarEvent) []db.SidebarEvent {
	result := make([]db.SidebarEvent, len(events))
	for index, event := range events {
		result[index] = cloneCoachEvent(event)
	}
	return result
}

func cloneCoachEvent(event db.SidebarEvent) db.SidebarEvent {
	event.Tags = slices.Clone(event.Tags)
	return event
}
