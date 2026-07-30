package coach

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

const coachEventRoot = "ic/coach/"

var coachRequestIDPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`,
)

// Service enforces Coach policy before and after every Provider call.
type Service struct {
	repository Repository
	provider   Provider
	now        func() time.Time
	mu         sync.Mutex
}

// NewService constructs the isolated Coach workflow.
func NewService(
	repository Repository,
	provider Provider,
	options Options,
) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		repository: repository,
		provider:   provider,
		now:        now,
	}
}

// Ask validates quota and context, optionally pauses the interview, calls
// Provider, and persists one learning event.
func (service *Service) Ask(
	ctx context.Context,
	request AskRequest,
	observer Observer,
) (AskResult, error) {
	notify(observer, async.NewPending[Progress]())
	if service == nil || service.repository == nil {
		return AskResult{}, fail(observer, unavailableRepository())
	}
	service.mu.Lock()
	defer service.mu.Unlock()

	if err := validateAskRequest(request); err != nil {
		return AskResult{}, fail(observer, err)
	}
	eventID := coachEventID(request.SessionID, request.RequestID)
	existing, found, err := service.repository.GetSidebarEvent(
		ctx,
		request.SessionID,
		eventID,
	)
	if err != nil {
		return AskResult{}, fail(observer, coachFailure(err))
	}
	if found {
		if existing.QuestionID != request.QuestionID ||
			existing.Intent != request.Intent {
			return AskResult{}, fail(observer, coachError(
				domainerr.CodePolicyDenied,
				"同一 Coach 请求 ID 不能对应不同问题或意图。",
				"使用原请求重试，或为新请求生成新的 ID。",
				false,
			))
		}
		used, countErr := service.repository.
			CountSidebarEventsForQuestion(
				ctx,
				request.SessionID,
				request.QuestionID,
			)
		if countErr != nil {
			return AskResult{}, fail(
				observer,
				coachFailure(countErr),
			)
		}
		session, _, loadErr := service.repository.GetSession(
			ctx,
			request.SessionID,
		)
		if loadErr != nil {
			return AskResult{}, fail(
				observer,
				coachFailure(loadErr),
			)
		}
		scenario, _, loadErr := service.repository.GetScenario(
			ctx,
			session.ScenarioID,
		)
		if loadErr != nil {
			return AskResult{}, fail(
				observer,
				coachFailure(loadErr),
			)
		}
		policy, policyErr := PolicyFor(
			scenario.Mode,
			QuestionActive,
		)
		if policyErr != nil {
			return AskResult{}, fail(observer, policyErr)
		}
		result := AskResult{
			Response: contracts.CoachResponse{
				Intent:            existing.Intent,
				HelpLevel:         existing.HelpLevel,
				KnowledgeTags:     slices.Clone(existing.Tags),
				RecommendedAction: existing.Content,
				PolicyNote:        existing.PolicyNote,
			},
			Event:      cloneSidebarEvent(existing),
			Usage:      usageFor(policy, used),
			Idempotent: true,
		}
		notify(observer, async.NewSucceeded(Progress{
			Stage:   "restored",
			Message: "已恢复这次 Coach 回复",
		}))
		return result, nil
	}

	loaded, err := service.loadInput(ctx, request)
	if err != nil {
		return AskResult{}, fail(observer, err)
	}
	if err := ensureQuota(loaded.policy, loaded.input.Usage); err != nil {
		return AskResult{}, fail(observer, err)
	}
	if request.PauseForHelp {
		if loaded.input.QuestionState != QuestionActive {
			return AskResult{}, fail(observer, coachError(
				domainerr.CodeInvalidState,
				"题目结束后不需要暂停主计时。",
				"直接查看复盘帮助，或返回当前活动题。",
				false,
			))
		}
		if err := service.pauseForHelp(
			ctx,
			request,
			loaded.events,
		); err != nil {
			return AskResult{}, fail(observer, err)
		}
	}
	progress := Progress{
		Stage:   "thinking",
		Message: "coach: thinking",
	}
	notify(observer, async.NewStreaming(&progress))
	if service.provider == nil {
		return AskResult{}, fail(observer, coachError(
			domainerr.CodeDependencyUnavailable,
			"Coach Provider 不可用。",
			"继续独立作答，或检查模型设置后重试。",
			true,
		))
	}
	response, err := service.provider.Respond(ctx, loaded.input)
	if err != nil {
		return AskResult{}, fail(observer, coachProviderFailure(ctx, err))
	}
	if err := validateResponse(loaded.input, response); err != nil {
		return AskResult{}, fail(observer, err)
	}
	event := db.SidebarEvent{
		ID:          eventID,
		SessionID:   request.SessionID,
		QuestionID:  request.QuestionID,
		Intent:      response.Intent,
		HelpLevel:   response.HelpLevel,
		Tags:        slices.Clone(response.KnowledgeTags),
		Content:     response.RecommendedAction,
		PolicyNote:  response.PolicyNote,
		Outcome:     string(OutcomeUnmarked),
		PausedTimer: request.PauseForHelp,
		OccurredAt:  service.now().UTC(),
	}
	if err := service.repository.AddSidebarEvent(ctx, event); err != nil {
		return AskResult{}, fail(observer, coachFailure(err))
	}
	usage := usageFor(loaded.policy, loaded.input.Usage.Used+1)
	result := AskResult{
		Response: response,
		Event:    cloneSidebarEvent(event),
		Usage:    usage,
	}
	notify(observer, async.NewSucceeded(Progress{
		Stage:   "ready",
		Message: "Coach 回复与学习事件已保存",
	}))
	return result, nil
}

// History returns only events that remain eligible for reports/recommendations.
func (service *Service) History(
	ctx context.Context,
	sessionID string,
) ([]db.SidebarEvent, error) {
	if service == nil || service.repository == nil {
		return nil, unavailableRepository()
	}
	if strings.TrimSpace(sessionID) == "" {
		return nil, coachError(
			domainerr.CodeValidation,
			"会话 ID 不能为空。",
			"重新打开训练会话。",
			false,
		)
	}
	events, err := service.repository.ListSidebarEvents(ctx, sessionID)
	if err != nil {
		return nil, coachFailure(err)
	}
	result := make([]db.SidebarEvent, len(events))
	for index, event := range events {
		result[index] = cloneSidebarEvent(event)
	}
	return result, nil
}

// MarkOutcome records understood, confused, or review without changing text.
func (service *Service) MarkOutcome(
	ctx context.Context,
	sessionID string,
	eventID string,
	outcome LearningOutcome,
) (db.SidebarEvent, error) {
	if service == nil || service.repository == nil {
		return db.SidebarEvent{}, unavailableRepository()
	}
	if outcome == OutcomeUnmarked || !validOutcome(outcome) {
		return db.SidebarEvent{}, coachError(
			domainerr.CodeValidation,
			"理解状态无效。",
			"选择已理解、仍困惑或加入复习。",
			false,
		)
	}
	updated, err := service.repository.UpdateSidebarEventOutcome(
		ctx,
		sessionID,
		eventID,
		string(outcome),
	)
	if err != nil {
		return db.SidebarEvent{}, coachFailure(err)
	}
	if !updated {
		return db.SidebarEvent{}, coachError(
			domainerr.CodeInvalidState,
			"找不到要标记的 Coach 事件。",
			"刷新 Coach 历史后重试。",
			false,
		)
	}
	event, found, err := service.repository.GetSidebarEvent(
		ctx,
		sessionID,
		eventID,
	)
	if err != nil {
		return db.SidebarEvent{}, coachFailure(err)
	}
	if !found {
		return db.SidebarEvent{}, coachError(
			domainerr.CodeInvalidState,
			"理解状态已保存，但无法恢复对应事件。",
			"刷新 Coach 历史。",
			true,
		)
	}
	return cloneSidebarEvent(event), nil
}

// DeleteEvent removes one Coach event from report/recommendation history.
func (service *Service) DeleteEvent(
	ctx context.Context,
	sessionID string,
	eventID string,
) (bool, error) {
	if service == nil || service.repository == nil {
		return false, unavailableRepository()
	}
	deleted, err := service.repository.DeleteSidebarEvent(
		ctx,
		sessionID,
		eventID,
	)
	if err != nil {
		return false, coachFailure(err)
	}
	return deleted, nil
}

// DeleteQuestion removes one question's complete Coach history.
func (service *Service) DeleteQuestion(
	ctx context.Context,
	sessionID string,
	questionID string,
) (int64, error) {
	if service == nil || service.repository == nil {
		return 0, unavailableRepository()
	}
	deleted, err := service.repository.DeleteSidebarEventsByQuestion(
		ctx,
		sessionID,
		questionID,
	)
	if err != nil {
		return 0, coachFailure(err)
	}
	return deleted, nil
}

// DeleteSession removes the complete Coach history for one session.
func (service *Service) DeleteSession(
	ctx context.Context,
	sessionID string,
) (int64, error) {
	if service == nil || service.repository == nil {
		return 0, unavailableRepository()
	}
	deleted, err := service.repository.DeleteSidebarEventsBySession(
		ctx,
		sessionID,
	)
	if err != nil {
		return 0, coachFailure(err)
	}
	return deleted, nil
}

type loadedCoach struct {
	input  Input
	policy Policy
	events []db.SessionEvent
}

func (service *Service) loadInput(
	ctx context.Context,
	request AskRequest,
) (loadedCoach, error) {
	session, found, err := service.repository.GetSession(
		ctx,
		request.SessionID,
	)
	if err != nil {
		return loadedCoach{}, coachFailure(err)
	}
	if !found {
		return loadedCoach{}, coachError(
			domainerr.CodeInvalidState,
			"找不到 Coach 所属的训练会话。",
			"返回训练主页选择仍存在的会话。",
			false,
		)
	}
	scenario, found, err := service.repository.GetScenario(
		ctx,
		session.ScenarioID,
	)
	if err != nil {
		return loadedCoach{}, coachFailure(err)
	}
	if !found {
		return loadedCoach{}, coachError(
			domainerr.CodeInvalidState,
			"会话关联的确认场景不存在。",
			"返回场景工厂创建新会话。",
			false,
		)
	}
	question, found := scenarioQuestion(scenario, request.QuestionID)
	if !found {
		return loadedCoach{}, coachError(
			domainerr.CodeValidation,
			"Coach 请求引用了场景外的题目。",
			"回到当前题目后重新提问。",
			false,
		)
	}
	profile, found, err := service.repository.GetSessionProfile(
		ctx,
		request.SessionID,
	)
	if err != nil {
		return loadedCoach{}, coachFailure(err)
	}
	if !found || profile.ConfirmedAt == nil ||
		profile.ConfirmedAt.IsZero() {
		return loadedCoach{}, coachError(
			domainerr.CodeInvalidState,
			"Coach 无法读取已确认画像。",
			"返回画像页确认事实后创建新会话。",
			false,
		)
	}
	events, err := service.repository.ListSessionEvents(
		ctx,
		request.SessionID,
	)
	if err != nil {
		return loadedCoach{}, coachFailure(err)
	}
	state, err := deriveQuestionState(
		session,
		scenario,
		events,
		request.QuestionID,
	)
	if err != nil {
		return loadedCoach{}, err
	}
	policy, err := PolicyFor(scenario.Mode, state)
	if err != nil {
		return loadedCoach{}, err
	}
	maxLevel, err := allowedLevel(request, policy)
	if err != nil {
		return loadedCoach{}, err
	}
	used, err := service.repository.CountSidebarEventsForQuestion(
		ctx,
		request.SessionID,
		request.QuestionID,
	)
	if err != nil {
		return loadedCoach{}, coachFailure(err)
	}
	input := Input{
		SessionID:        request.SessionID,
		Mode:             scenario.Mode,
		Question:         cloneQuestion(question),
		QuestionState:    state,
		Intent:           request.Intent,
		RequestedLevel:   request.RequestedLevel,
		AllowedMaxLevel:  maxLevel,
		UserRequest:      strings.TrimSpace(request.UserRequest),
		ConfirmedFacts:   slices.Clone(profile.Candidate.Facts),
		SubmittedAnswers: []AnswerEvidence{},
		CodeRuns:         []CodeEvidence{},
		Usage:            usageFor(policy, used),
		PausedForHelp:    request.PauseForHelp,
	}
	answerPrefix := "ic/interview/" + request.SessionID + "/answer/"
	for _, event := range events {
		if event.Speaker != db.SpeakerUser ||
			event.QuestionID != request.QuestionID ||
			!strings.HasPrefix(event.EventID, answerPrefix) {
			continue
		}
		input.SubmittedAnswers = append(
			input.SubmittedAnswers,
			AnswerEvidence{
				EventID:    contracts.EvidenceID(event.EventID),
				QuestionID: event.QuestionID,
				Content:    event.Content,
			},
		)
	}
	submissions, err := service.repository.ListCodeSubmissions(
		ctx,
		request.SessionID,
	)
	if err != nil {
		return loadedCoach{}, coachFailure(err)
	}
	for _, submission := range submissions {
		if submission.QuestionID != request.QuestionID {
			continue
		}
		input.CodeRuns = append(input.CodeRuns, CodeEvidence{
			SubmissionID: contracts.EvidenceID(submission.ID),
			QuestionID:   submission.QuestionID,
			Language:     submission.Language,
			Source:       submission.Source,
			TestResult:   slices.Clone(submission.TestResult),
			RuntimeStats: slices.Clone(submission.RuntimeStats),
			SnapshotID:   contracts.EvidenceID(submission.SnapshotID),
		})
	}
	return loadedCoach{
		input:  input,
		policy: policy,
		events: slices.Clone(events),
	}, nil
}

func (service *Service) pauseForHelp(
	ctx context.Context,
	request AskRequest,
	events []db.SessionEvent,
) error {
	eventID := pauseEventID(request.SessionID, request.RequestID)
	for _, event := range events {
		if event.EventID == eventID {
			return nil
		}
	}
	err := service.repository.AppendSessionEvent(
		ctx,
		db.SessionEvent{
			EventID:      eventID,
			SessionID:    request.SessionID,
			Speaker:      db.SpeakerSystem,
			QuestionID:   request.QuestionID,
			Content:      "pause_reason=coach_help",
			OccurredAt:   service.now().UTC(),
			EvidenceRefs: []contracts.EvidenceID{},
		},
	)
	if err != nil {
		return coachFailure(err)
	}
	return nil
}

func validateAskRequest(request AskRequest) error {
	for field, value := range map[string]string{
		"session ID":   request.SessionID,
		"question ID":  request.QuestionID,
		"user request": request.UserRequest,
	} {
		if strings.TrimSpace(value) == "" {
			return coachError(
				domainerr.CodeValidation,
				field+" 不能为空。",
				"补全 Coach 请求后重试。",
				false,
			)
		}
	}
	if !coachRequestIDPattern.MatchString(
		strings.TrimSpace(request.RequestID),
	) {
		return coachError(
			domainerr.CodeValidation,
			"Coach 请求 ID 无效。",
			"使用 1–64 位字母、数字、连字符或下划线。",
			false,
		)
	}
	if !validIntent(request.Intent) || !validLevel(request.RequestedLevel) {
		return coachError(
			domainerr.CodeValidation,
			"Coach 意图或帮助层级无效。",
			"选择六类意图和 L1–L4 帮助层级。",
			false,
		)
	}
	return nil
}

func deriveQuestionState(
	session db.Session,
	scenario contracts.Scenario,
	events []db.SessionEvent,
	questionID string,
) (QuestionState, error) {
	if session.Status != db.SessionActive {
		return SessionClosed, nil
	}
	requestedIndex := questionIndex(scenario, questionID)
	if requestedIndex < 0 {
		return "", coachError(
			domainerr.CodeValidation,
			"Coach 请求引用了场景外的题目。",
			"返回当前题目后重试。",
			false,
		)
	}
	currentIndex := 0
	closed := make(map[string]struct{})
	for _, event := range events {
		switch {
		case strings.Contains(event.EventID, "/question/"):
			if index := questionIndex(
				scenario,
				event.QuestionID,
			); index >= 0 {
				currentIndex = index
			}
		case strings.HasSuffix(event.EventID, "/next_question"):
			if index := questionIndex(
				scenario,
				event.QuestionID,
			); index >= 0 {
				currentIndex = index
			}
		case strings.HasSuffix(event.EventID, "/close_question"),
			strings.HasSuffix(event.EventID, "/finish_session"):
			closed[event.QuestionID] = struct{}{}
		case strings.Contains(event.EventID, "/control/end-confirm/question/"):
			closed[event.QuestionID] = struct{}{}
		}
	}
	if requestedIndex > currentIndex {
		return "", coachError(
			domainerr.CodeInvalidState,
			"不能为尚未开始的题目请求 Coach。",
			"回到当前活动题目后重试。",
			false,
		)
	}
	if requestedIndex < currentIndex {
		return QuestionClosed, nil
	}
	if _, found := closed[questionID]; found {
		return QuestionClosed, nil
	}
	return QuestionActive, nil
}

func scenarioQuestion(
	scenario contracts.Scenario,
	questionID string,
) (contracts.ScenarioQuestion, bool) {
	for _, question := range scenario.Questions {
		if question.ID == questionID {
			return cloneQuestion(question), true
		}
	}
	return contracts.ScenarioQuestion{}, false
}

func questionIndex(
	scenario contracts.Scenario,
	questionID string,
) int {
	for index, question := range scenario.Questions {
		if question.ID == questionID {
			return index
		}
	}
	return -1
}

func cloneQuestion(
	value contracts.ScenarioQuestion,
) contracts.ScenarioQuestion {
	value.Rubric = slices.Clone(value.Rubric)
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	return value
}

func cloneSidebarEvent(value db.SidebarEvent) db.SidebarEvent {
	value.Tags = slices.Clone(value.Tags)
	return value
}

func coachEventID(sessionID, requestID string) string {
	return coachEventRoot + sessionID + "/" + requestID
}

func pauseEventID(sessionID, requestID string) string {
	return "ic/interview/" + sessionID +
		"/control/pause/coach-" + requestID
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func fail(observer Observer, err error) error {
	typed := coachFailure(err)
	notify(observer, async.NewFailed[Progress](typed))
	return typed
}

func unavailableRepository() *domainerr.Error {
	return coachError(
		domainerr.CodeDependencyUnavailable,
		"Coach 存储不可用。",
		"重新启动 InterviewCraft 后重试。",
		true,
	)
}

func coachProviderFailure(
	ctx context.Context,
	err error,
) *domainerr.Error {
	if ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return domainerr.Wrap(
			domainerr.CodeOperationCancelled,
			"wait for Coach",
			"Coach Provider",
			"已停止等待 Coach 回复。",
			"主回答与面试状态未改变，可继续独立作答。",
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
		"generate Coach response",
		"Coach Provider",
		"Coach Provider 暂时不可用。",
		"主回答仍可继续；检查模型设置后重试。",
		true,
		err,
	)
}

func invalidCoachOutput(cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeInvalidModelOutput,
		"validate Coach response",
		"Coach Provider",
		"模型返回了超出 Coach 政策的内容。",
		"继续独立作答，或降低帮助层级后重试。",
		true,
		cause,
	)
}

func coachError(
	code domainerr.Code,
	message string,
	recovery string,
	retryable bool,
) *domainerr.Error {
	return domainerr.New(
		code,
		"update Coach",
		message,
		recovery,
		retryable,
	)
}

func coachFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"update Coach",
		"Coach storage",
		"无法保存或恢复 Coach 学习事件。",
		"主回答不受影响；检查数据库后重试。",
		true,
		err,
	)
}
