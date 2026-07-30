package interview

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

// Service coordinates persisted events, Provider actions, and recovery.
type Service struct {
	repository Repository
	provider   Provider
	now        func() time.Time
	latency    LatencyRecorder
	mu         sync.Mutex
}

// NewService constructs the text interview state machine.
func NewService(
	repository Repository,
	provider Provider,
	options Options,
) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	latency := options.Latency
	if latency == nil {
		latency = &LatencyWindow{}
	}
	return &Service{
		repository: repository,
		provider:   provider,
		now:        now,
		latency:    latency,
	}
}

// Load restores the latest persisted question, events, and local draft.
func (service *Service) Load(
	ctx context.Context,
	sessionID string,
) (Snapshot, error) {
	loaded, err := service.loadInternal(ctx, sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	return cloneSnapshot(loaded.snapshot), nil
}

// Start appends the first planned question exactly once.
func (service *Service) Start(
	ctx context.Context,
	sessionID string,
) (Snapshot, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	loaded, err := service.loadInternal(ctx, sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	if loaded.snapshot.Phase == PhaseCompleted {
		return cloneSnapshot(loaded.snapshot), nil
	}
	if loaded.snapshot.Phase != PhaseNotStarted {
		return cloneSnapshot(loaded.snapshot), nil
	}
	question := loaded.snapshot.Scenario.Questions[0]
	if err := service.appendQuestion(
		ctx,
		loaded.snapshot.Session.ID,
		question,
		loaded.snapshot.Events,
	); err != nil {
		return Snapshot{}, err
	}
	return service.Load(ctx, sessionID)
}

// SaveDraft persists an unsubmitted local answer without exposing it to
// Provider input or immutable session events.
func (service *Service) SaveDraft(
	ctx context.Context,
	sessionID string,
	content string,
) (Snapshot, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	loaded, err := service.loadInternal(ctx, sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	if loaded.snapshot.CurrentQuestion == nil ||
		loaded.snapshot.Phase == PhaseCompleted ||
		loaded.snapshot.Phase == PhaseNotStarted {
		return Snapshot{}, interviewError(
			domainerr.CodeInvalidState,
			"当前没有可保存回答的题目。",
			"开始或恢复一道文字题后再编辑。",
			false,
		)
	}
	err = service.repository.SaveDraft(ctx, db.Draft{
		SessionID:  sessionID,
		QuestionID: loaded.snapshot.CurrentQuestion.ID,
		Kind:       db.DraftAnswer,
		Content:    content,
		UpdatedAt:  service.now().UTC(),
	})
	if err != nil {
		return Snapshot{}, interviewFailure(err)
	}
	return service.Load(ctx, sessionID)
}

// Submit durably records an answer before calling Interviewer. Reusing the
// same SubmissionID never writes the answer or action twice.
func (service *Service) Submit(
	ctx context.Context,
	request SubmitRequest,
	observer Observer,
) (SubmitResult, error) {
	service.mu.Lock()
	defer service.mu.Unlock()

	notify(observer, async.NewPending[Progress]())
	if err := validateOperationID(request.SubmissionID); err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	answer := strings.TrimSpace(request.Answer)
	if answer == "" {
		return SubmitResult{}, fail(observer, interviewError(
			domainerr.CodeValidation,
			"回答不能为空。",
			"填写回答后重新提交。",
			false,
		))
	}
	loaded, err := service.loadInternal(ctx, request.SessionID)
	if err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	answerEvent, answerFound := findAnswer(
		loaded.snapshot.Events,
		request.SessionID,
		request.SubmissionID,
	)
	actionEvent, actionType, actionFound, err := findAction(
		loaded.snapshot.Events,
		request.SessionID,
		request.SubmissionID,
	)
	if err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	if answerFound && answerEvent.Content != answer {
		return SubmitResult{}, fail(observer, interviewError(
			domainerr.CodePolicyDenied,
			"同一提交 ID 不能对应不同回答。",
			"使用原回答重试，或为新回答生成新的提交 ID。",
			false,
		))
	}
	if actionFound {
		if !answerFound {
			return SubmitResult{}, fail(observer, interviewError(
				domainerr.CodeInvalidState,
				"面试官动作缺少对应的已提交回答。",
				"停止当前会话并检查事件日志。",
				false,
			))
		}
		action, decodeErr := actionFromEvent(actionEvent, actionType)
		if decodeErr != nil {
			return SubmitResult{}, fail(observer, decodeErr)
		}
		if loaded.snapshot.Session.Status == db.SessionActive {
			if err := service.finalizeAction(
				ctx,
				loaded,
				action,
			); err != nil {
				return SubmitResult{}, fail(observer, err)
			}
		}
		snapshot, loadErr := service.Load(ctx, request.SessionID)
		if loadErr != nil {
			return SubmitResult{}, fail(observer, loadErr)
		}
		result := SubmitResult{
			Action:     action,
			Snapshot:   snapshot,
			Idempotent: true,
		}
		notify(observer, async.NewSucceeded(Progress{
			Stage:   "restored",
			Message: "已恢复该提交的面试官动作",
		}))
		return result, nil
	}
	if loaded.snapshot.Phase != PhaseAwaitingAnswer {
		return SubmitResult{}, fail(observer, interviewError(
			domainerr.CodeInvalidState,
			"当前状态不能提交回答。",
			"恢复正在进行的题目，或完成当前确认操作。",
			false,
		))
	}
	current := loaded.snapshot.CurrentQuestion
	if current == nil {
		return SubmitResult{}, fail(observer, interviewError(
			domainerr.CodeInvalidState,
			"当前题目不存在。",
			"返回场景工厂检查 Run Plan。",
			false,
		))
	}
	if !answerFound {
		answerEvent = db.SessionEvent{
			EventID:      answerEventID(request.SessionID, request.SubmissionID),
			SessionID:    request.SessionID,
			Speaker:      db.SpeakerUser,
			QuestionID:   current.ID,
			Content:      answer,
			OccurredAt:   service.now().UTC(),
			EvidenceRefs: []contracts.EvidenceID{},
		}
		if err := service.repository.AppendSessionEvent(
			ctx,
			answerEvent,
		); err != nil {
			return SubmitResult{}, fail(observer, interviewFailure(err))
		}
	}
	if _, err := service.repository.DeleteDraft(
		ctx,
		request.SessionID,
		current.ID,
		db.DraftAnswer,
	); err != nil {
		return SubmitResult{}, fail(observer, interviewFailure(err))
	}
	loaded, err = service.loadInternal(ctx, request.SessionID)
	if err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	input, err := service.providerInput(ctx, loaded)
	if err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	progress := Progress{
		Stage:   "thinking",
		Message: "答案已记录，面试官正在思考",
	}
	notify(observer, async.NewStreaming(&progress))
	if service.provider == nil {
		return SubmitResult{}, fail(observer, interviewError(
			domainerr.CodeDependencyUnavailable,
			"面试官 Provider 不可用。",
			"已提交回答已保存；重试或安全结束本题。",
			true,
		))
	}
	startedAt := service.now()
	action, err := service.provider.Respond(ctx, input)
	elapsed := service.now().Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	service.latency.Observe(elapsed)
	if err != nil {
		return SubmitResult{}, fail(
			observer,
			providerFailure(ctx, err),
		)
	}
	if err := validateAction(loaded.snapshot, input, action); err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	event := db.SessionEvent{
		EventID: actionEventID(
			request.SessionID,
			request.SubmissionID,
			action.Action,
		),
		SessionID:    request.SessionID,
		Speaker:      db.SpeakerInterviewer,
		QuestionID:   action.QuestionID,
		Content:      action.Message,
		OccurredAt:   service.now().UTC(),
		EvidenceRefs: slices.Clone(action.EvidenceIDs),
	}
	if err := service.repository.AppendSessionEvent(ctx, event); err != nil {
		return SubmitResult{}, fail(observer, interviewFailure(err))
	}
	loaded.snapshot.Events = append(loaded.snapshot.Events, event)
	if err := service.finalizeAction(ctx, loaded, action); err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	snapshot, err := service.Load(ctx, request.SessionID)
	if err != nil {
		return SubmitResult{}, fail(observer, err)
	}
	result := SubmitResult{
		Action:          action,
		Snapshot:        snapshot,
		ProviderLatency: elapsed,
	}
	notify(observer, async.NewSucceeded(Progress{
		Stage:   "ready",
		Message: "面试官动作已保存",
	}))
	return result, nil
}

// Pause records an explicit pause without involving Provider.
func (service *Service) Pause(
	ctx context.Context,
	sessionID string,
	operationID string,
) (Snapshot, error) {
	return service.control(
		ctx,
		sessionID,
		"pause",
		"",
		operationID,
		"面试已暂停。",
	)
}

// Resume records a timer/session resume without involving Provider.
func (service *Service) Resume(
	ctx context.Context,
	sessionID string,
	operationID string,
) (Snapshot, error) {
	return service.control(
		ctx,
		sessionID,
		"resume",
		"",
		operationID,
		"面试已恢复。",
	)
}

// RequestEnd persists the first step of the required end confirmation.
func (service *Service) RequestEnd(
	ctx context.Context,
	sessionID string,
	scope EndScope,
	operationID string,
) (Snapshot, error) {
	if scope != EndQuestion && scope != EndSession {
		return Snapshot{}, interviewError(
			domainerr.CodeValidation,
			"结束范围无效。",
			"选择结束本题或结束面试。",
			false,
		)
	}
	return service.control(
		ctx,
		sessionID,
		"end-request",
		scope,
		operationID,
		"等待确认结束"+endScopeLabel(scope)+"。",
	)
}

// CancelEnd cancels a matching first-step request.
func (service *Service) CancelEnd(
	ctx context.Context,
	sessionID string,
	scope EndScope,
	operationID string,
) (Snapshot, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := validateOperationID(operationID); err != nil {
		return Snapshot{}, err
	}
	loaded, err := service.loadInternal(ctx, sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	if !matchingPending(loaded.snapshot.PendingEnd, scope, operationID) {
		return Snapshot{}, interviewError(
			domainerr.CodeInvalidState,
			"没有匹配的结束确认可取消。",
			"继续当前题目，或重新发起结束操作。",
			false,
		)
	}
	if err := service.appendControl(
		ctx,
		loaded.snapshot,
		"end-cancel",
		scope,
		operationID,
		"已取消结束操作。",
	); err != nil {
		return Snapshot{}, err
	}
	return service.Load(ctx, sessionID)
}

// ConfirmEnd applies the second step and advances or completes the session.
func (service *Service) ConfirmEnd(
	ctx context.Context,
	sessionID string,
	scope EndScope,
	operationID string,
) (Snapshot, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := validateOperationID(operationID); err != nil {
		return Snapshot{}, err
	}
	loaded, err := service.loadInternal(ctx, sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	if !matchingPending(loaded.snapshot.PendingEnd, scope, operationID) {
		return Snapshot{}, interviewError(
			domainerr.CodePolicyDenied,
			"结束操作需要先请求并再次确认。",
			"先选择结束本题或结束面试，再执行确认。",
			false,
		)
	}
	if err := service.appendControl(
		ctx,
		loaded.snapshot,
		"end-confirm",
		scope,
		operationID,
		"已确认结束"+endScopeLabel(scope)+"。",
	); err != nil {
		return Snapshot{}, err
	}
	if scope == EndSession {
		if err := service.markEvaluationPending(ctx, loaded.snapshot.Session.ID); err != nil {
			return Snapshot{}, err
		}
	} else if err := service.advanceAfterClose(ctx, loaded.snapshot); err != nil {
		return Snapshot{}, err
	}
	return service.Load(ctx, sessionID)
}

// P95Latency exposes the Provider timing quality signal.
func (service *Service) P95Latency() time.Duration {
	if service == nil || service.latency == nil {
		return 0
	}
	return service.latency.P95()
}

func (service *Service) control(
	ctx context.Context,
	sessionID string,
	action string,
	scope EndScope,
	operationID string,
	content string,
) (Snapshot, error) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := validateOperationID(operationID); err != nil {
		return Snapshot{}, err
	}
	loaded, err := service.loadInternal(ctx, sessionID)
	if err != nil {
		return Snapshot{}, err
	}
	if loaded.snapshot.Phase == PhaseCompleted ||
		loaded.snapshot.Phase == PhaseNotStarted {
		return Snapshot{}, interviewError(
			domainerr.CodeInvalidState,
			"当前会话不能执行该控制操作。",
			"开始一道题，或返回训练主页。",
			false,
		)
	}
	if action == "pause" && loaded.snapshot.Phase == PhasePaused {
		return cloneSnapshot(loaded.snapshot), nil
	}
	if action == "resume" && loaded.snapshot.Phase != PhasePaused {
		return cloneSnapshot(loaded.snapshot), nil
	}
	if action == "end-request" &&
		loaded.snapshot.PendingEnd != nil {
		if matchingPending(
			loaded.snapshot.PendingEnd,
			scope,
			operationID,
		) {
			return cloneSnapshot(loaded.snapshot), nil
		}
		return Snapshot{}, interviewError(
			domainerr.CodeInvalidState,
			"已有结束操作正在等待确认。",
			"先确认或取消当前结束操作。",
			false,
		)
	}
	if err := service.appendControl(
		ctx,
		loaded.snapshot,
		action,
		scope,
		operationID,
		content,
	); err != nil {
		return Snapshot{}, err
	}
	return service.Load(ctx, sessionID)
}

func (service *Service) appendControl(
	ctx context.Context,
	snapshot Snapshot,
	action string,
	scope EndScope,
	operationID string,
	content string,
) error {
	if snapshot.CurrentQuestion == nil {
		return interviewError(
			domainerr.CodeInvalidState,
			"当前题目不存在。",
			"返回训练主页恢复会话。",
			false,
		)
	}
	eventID := controlEventID(
		snapshot.Session.ID,
		action,
		scope,
		operationID,
	)
	if _, found := findEvent(snapshot.Events, eventID); found {
		return nil
	}
	err := service.repository.AppendSessionEvent(ctx, db.SessionEvent{
		EventID:      eventID,
		SessionID:    snapshot.Session.ID,
		Speaker:      db.SpeakerSystem,
		QuestionID:   snapshot.CurrentQuestion.ID,
		Content:      content,
		OccurredAt:   service.now().UTC(),
		EvidenceRefs: []contracts.EvidenceID{},
	})
	if err != nil {
		return interviewFailure(err)
	}
	return nil
}

func (service *Service) appendQuestion(
	ctx context.Context,
	sessionID string,
	question contracts.ScenarioQuestion,
	events []db.SessionEvent,
) error {
	eventID := questionEventID(sessionID, question.ID)
	if _, found := findEvent(events, eventID); found {
		return nil
	}
	err := service.repository.AppendSessionEvent(ctx, db.SessionEvent{
		EventID:      eventID,
		SessionID:    sessionID,
		Speaker:      db.SpeakerInterviewer,
		QuestionID:   question.ID,
		Content:      question.Prompt,
		OccurredAt:   service.now().UTC(),
		EvidenceRefs: questionEvidence(question),
	})
	if err != nil {
		return interviewFailure(err)
	}
	return nil
}

func (service *Service) providerInput(
	ctx context.Context,
	loaded loadedState,
) (Input, error) {
	current := loaded.snapshot.CurrentQuestion
	if current == nil {
		return Input{}, interviewError(
			domainerr.CodeInvalidState,
			"当前题目不存在。",
			"返回场景工厂检查 Run Plan。",
			false,
		)
	}
	input := Input{
		SessionID:        loaded.snapshot.Session.ID,
		Mode:             loaded.snapshot.Scenario.Mode,
		CurrentQuestion:  cloneQuestion(*current),
		FollowUpCount:    loaded.snapshot.FollowUpCount,
		MaxFollowUps:     current.MaxFollowUps,
		ConfirmedFacts:   slices.Clone(loaded.profile.Candidate.Facts),
		SubmittedAnswers: []AnswerEvidence{},
		CodeRuns:         []CodeEvidence{},
	}
	if loaded.snapshot.CurrentIndex+1 <
		len(loaded.snapshot.Scenario.Questions) {
		next := cloneQuestion(
			loaded.snapshot.Scenario.Questions[loaded.snapshot.CurrentIndex+1],
		)
		input.NextQuestion = &next
	}
	allowed := make(map[contracts.EvidenceID]struct{})
	for _, fact := range input.ConfirmedFacts {
		allowed[fact.ID] = struct{}{}
	}
	for _, event := range loaded.snapshot.Events {
		owned, ok := parseOwnedEvent(
			loaded.snapshot.Session.ID,
			event.EventID,
		)
		if !ok || owned.kind != "answer" ||
			event.Speaker != db.SpeakerUser {
			continue
		}
		id := contracts.EvidenceID(event.EventID)
		input.SubmittedAnswers = append(
			input.SubmittedAnswers,
			AnswerEvidence{
				EventID:    id,
				QuestionID: event.QuestionID,
				Content:    event.Content,
			},
		)
		allowed[id] = struct{}{}
	}
	submissions, err := service.repository.ListCodeSubmissions(
		ctx,
		loaded.snapshot.Session.ID,
	)
	if err != nil {
		return Input{}, interviewFailure(err)
	}
	for _, submission := range submissions {
		item := CodeEvidence{
			SubmissionID: contracts.EvidenceID(submission.ID),
			QuestionID:   submission.QuestionID,
			Language:     submission.Language,
			Source:       submission.Source,
			TestResult:   slices.Clone(submission.TestResult),
			RuntimeStats: slices.Clone(submission.RuntimeStats),
			SnapshotID:   contracts.EvidenceID(submission.SnapshotID),
		}
		input.CodeRuns = append(input.CodeRuns, item)
		allowed[item.SubmissionID] = struct{}{}
		allowed[item.SnapshotID] = struct{}{}
	}
	for _, question := range []contracts.ScenarioQuestion{
		input.CurrentQuestion,
	} {
		allowed[constraintEvidenceID(question.ID)] = struct{}{}
		for _, id := range question.EvidenceIDs {
			allowed[id] = struct{}{}
		}
	}
	if input.NextQuestion != nil {
		allowed[constraintEvidenceID(input.NextQuestion.ID)] = struct{}{}
		for _, id := range input.NextQuestion.EvidenceIDs {
			allowed[id] = struct{}{}
		}
	}
	input.AllowedEvidenceIDs = make(
		[]contracts.EvidenceID,
		0,
		len(allowed),
	)
	for id := range allowed {
		input.AllowedEvidenceIDs = append(input.AllowedEvidenceIDs, id)
	}
	slices.Sort(input.AllowedEvidenceIDs)
	return input, nil
}

func validateAction(
	snapshot Snapshot,
	input Input,
	action contracts.InterviewerAction,
) error {
	if err := action.Validate(); err != nil {
		return invalidModelAction(err)
	}
	current := snapshot.CurrentQuestion
	if current == nil {
		return interviewError(
			domainerr.CodeInvalidState,
			"当前题目不存在。",
			"返回场景工厂检查 Run Plan。",
			false,
		)
	}
	switch action.Action {
	case contracts.ActionFollowUp:
		if action.QuestionID != current.ID {
			return invalidModelAction(errors.New(
				"follow-up must target current question",
			))
		}
		if snapshot.FollowUpCount >= current.MaxFollowUps {
			return invalidModelAction(errors.New(
				"follow-up limit reached",
			))
		}
	case contracts.ActionCloseQuestion:
		if action.QuestionID != current.ID {
			return invalidModelAction(errors.New(
				"close must target current question",
			))
		}
	case contracts.ActionNextQuestion:
		if input.NextQuestion == nil ||
			action.QuestionID != input.NextQuestion.ID ||
			strings.TrimSpace(action.Message) !=
				strings.TrimSpace(input.NextQuestion.Prompt) {
			return invalidModelAction(errors.New(
				"next question must match confirmed Scenario Plan",
			))
		}
	case contracts.ActionFinishSession:
		if action.QuestionID != current.ID ||
			input.NextQuestion != nil {
			return invalidModelAction(errors.New(
				"finish_session is only valid after the final question",
			))
		}
	default:
		return invalidModelAction(errors.New("unsupported action"))
	}
	allowed := make(
		map[contracts.EvidenceID]struct{},
		len(input.AllowedEvidenceIDs),
	)
	for _, id := range input.AllowedEvidenceIDs {
		allowed[id] = struct{}{}
	}
	for _, id := range action.EvidenceIDs {
		if _, ok := allowed[id]; !ok {
			return invalidModelAction(fmt.Errorf(
				"evidence %q is outside allowed context",
				id,
			))
		}
	}
	return nil
}

func (service *Service) finalizeAction(
	ctx context.Context,
	loaded loadedState,
	action contracts.InterviewerAction,
) error {
	switch action.Action {
	case contracts.ActionFollowUp, contracts.ActionNextQuestion:
		return nil
	case contracts.ActionCloseQuestion:
		return service.advanceAfterClose(ctx, loaded.snapshot)
	case contracts.ActionFinishSession:
		return service.markEvaluationPending(
			ctx,
			loaded.snapshot.Session.ID,
		)
	default:
		return invalidModelAction(errors.New("unsupported action"))
	}
}

func (service *Service) advanceAfterClose(
	ctx context.Context,
	snapshot Snapshot,
) error {
	nextIndex := snapshot.CurrentIndex + 1
	if nextIndex >= len(snapshot.Scenario.Questions) {
		return service.markEvaluationPending(ctx, snapshot.Session.ID)
	}
	return service.appendQuestion(
		ctx,
		snapshot.Session.ID,
		snapshot.Scenario.Questions[nextIndex],
		snapshot.Events,
	)
}

func (service *Service) markEvaluationPending(
	ctx context.Context,
	sessionID string,
) error {
	updated, err := service.repository.UpdateSessionStatus(
		ctx,
		sessionID,
		db.SessionEvaluationPending,
		service.now().UTC(),
	)
	if err != nil {
		return interviewFailure(err)
	}
	if !updated {
		return interviewError(
			domainerr.CodeInvalidState,
			"无法更新会话为待评估状态。",
			"确认会话仍存在后重试。",
			true,
		)
	}
	return nil
}

func matchingPending(
	pending *PendingEnd,
	scope EndScope,
	operationID string,
) bool {
	return pending != nil &&
		pending.Scope == scope &&
		pending.OperationID == operationID
}

func endScopeLabel(scope EndScope) string {
	if scope == EndSession {
		return "面试"
	}
	return "本题"
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func fail(observer Observer, err error) error {
	typed := interviewFailure(err)
	notify(observer, async.NewFailed[Progress](typed))
	return typed
}

func providerFailure(
	ctx context.Context,
	err error,
) *domainerr.Error {
	if ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return domainerr.Wrap(
			domainerr.CodeOperationCancelled,
			"wait for Interviewer",
			"Interviewer Provider",
			"已停止等待面试官响应。",
			"已提交回答已保存；可重试或安全结束本题。",
			true,
			err,
		)
	}
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"generate Interviewer action",
		"Interviewer Provider",
		"面试官 Provider 暂时不可用。",
		"已提交回答已保存；重试或安全结束本题。",
		true,
		err,
	)
}

func invalidModelAction(cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeInvalidModelOutput,
		"validate Interviewer action",
		"Interviewer Provider",
		"模型返回了不安全或无效的面试动作。",
		"重试当前回答，或安全结束本题。",
		true,
		cause,
	)
}

func interviewError(
	code domainerr.Code,
	message string,
	recovery string,
	retryable bool,
) *domainerr.Error {
	return domainerr.New(
		code,
		"update text interview",
		message,
		recovery,
		retryable,
	)
}

func interviewFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"update text interview",
		"interview storage",
		"无法保存或恢复文字面试。",
		"已提交事件不会被改写；检查数据库后重试。",
		true,
		err,
	)
}
