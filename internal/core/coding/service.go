package coding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

// Service coordinates the embedded catalog, local drafts, and immutable run
// evidence. It does not start Docker or probe a runner implicitly.
type Service struct {
	repository Repository
	formatter  Formatter
	runner     Runner
	now        func() time.Time
	questions  []Question
	byID       map[string]Question
	mu         sync.Mutex
}

// NewService loads the strict embedded catalog without enabling execution.
func NewService(repository Repository, options Options) (*Service, error) {
	questions, err := LoadQuestions()
	if err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	formatter := options.Formatter
	if formatter == nil {
		formatter = BasicFormatter{}
	}
	byID := make(map[string]Question, len(questions))
	for _, question := range questions {
		byID[question.ID] = cloneQuestion(question)
	}
	return &Service{
		repository: repository,
		formatter:  formatter,
		runner:     options.Runner,
		now:        now,
		questions:  questions,
		byID:       byID,
	}, nil
}

// Questions returns a defensive catalog copy.
func (service *Service) Questions() []Question {
	if service == nil {
		return []Question{}
	}
	result := make([]Question, len(service.questions))
	for index, question := range service.questions {
		result[index] = cloneQuestion(question)
	}
	return result
}

// RunnerStatus exposes Lite's explicit disabled state without attempting a run.
func (service *Service) RunnerStatus() RunnerStatus {
	if service != nil && service.runner != nil {
		return RunnerStatus{
			Enabled: true,
			Message: "代码执行器已配置。",
		}
	}
	return RunnerStatus{
		Enabled:        false,
		Message:        "代码执行未启用。",
		RecoveryAction: "在设置中将 RUNNER_MODE 设为 docker 并完成健康检查；文字面试和 Coach 仍可继续。",
	}
}

// Open restores all three language buffers and the latest executed snapshot.
// A missing draft starts from templates in memory and LatestRun remains nil
// until a real execution is persisted.
func (service *Service) Open(
	ctx context.Context,
	sessionID string,
	questionID string,
) (Workspace, error) {
	if service == nil {
		return Workspace{}, unavailableStorage("open coding workspace")
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	workspace, err := service.openInternal(ctx, sessionID, questionID)
	if err != nil {
		return Workspace{}, err
	}
	return cloneWorkspace(workspace), nil
}

// SaveSource persists one language buffer while preserving the other two.
func (service *Service) SaveSource(
	ctx context.Context,
	sessionID string,
	questionID string,
	language Language,
	source string,
	observer Observer,
) (Workspace, error) {
	return service.updateDraft(
		ctx,
		sessionID,
		questionID,
		language,
		observer,
		[]Progress{{Stage: "saving_draft", Message: "正在保存本地代码草稿"}},
		func(document *DraftDocument, _ Question) error {
			document.Sources[language] = source
			return nil
		},
	)
}

// SelectLanguage persists editor focus without changing any source buffer.
func (service *Service) SelectLanguage(
	ctx context.Context,
	sessionID string,
	questionID string,
	language Language,
	observer Observer,
) (Workspace, error) {
	return service.updateDraft(
		ctx,
		sessionID,
		questionID,
		language,
		observer,
		[]Progress{{Stage: "saving_draft", Message: "正在保存语言选择"}},
		func(_ *DraftDocument, _ Question) error { return nil },
	)
}

// FormatSource applies the safe dependency-free formatter and saves the result.
func (service *Service) FormatSource(
	ctx context.Context,
	sessionID string,
	questionID string,
	language Language,
	observer Observer,
) (Workspace, error) {
	return service.updateDraft(
		ctx,
		sessionID,
		questionID,
		language,
		observer,
		[]Progress{
			{Stage: "formatting", Message: "正在格式化代码草稿"},
			{Stage: "saving_draft", Message: "正在保存格式化结果"},
		},
		func(document *DraftDocument, _ Question) error {
			formatted, err := service.formatter.Format(
				ctx,
				language,
				document.Sources[language],
			)
			if err != nil {
				return formattingFailure(ctx, err)
			}
			document.Sources[language] = formatted
			return nil
		},
	)
}

// ResetTemplate replaces only the selected language with the catalog template.
func (service *Service) ResetTemplate(
	ctx context.Context,
	sessionID string,
	questionID string,
	language Language,
	observer Observer,
) (Workspace, error) {
	return service.updateDraft(
		ctx,
		sessionID,
		questionID,
		language,
		observer,
		[]Progress{
			{Stage: "resetting_template", Message: "正在恢复语言模板"},
			{Stage: "saving_draft", Message: "正在保存模板草稿"},
		},
		func(document *DraftDocument, question Question) error {
			document.Sources[language] = question.Templates[language]
			return nil
		},
	)
}

// Run executes and persists one immutable snapshot only when a Runner was
// explicitly supplied. A nil Runner is the normal Lite state and writes no
// code evidence.
func (service *Service) Run(
	ctx context.Context,
	request RunRequest,
	observer Observer,
) (RunSnapshot, error) {
	notify(observer, async.NewPending[Progress]())
	if service == nil || service.repository == nil {
		return RunSnapshot{}, fail(observer, unavailableStorage("run code"))
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if err := validateRunRequest(request); err != nil {
		return RunSnapshot{}, fail(observer, err)
	}
	workspace, err := service.openInternal(ctx, request.SessionID, request.QuestionID)
	if err != nil {
		return RunSnapshot{}, fail(observer, err)
	}
	source := workspace.Draft.Sources[request.Language]
	if strings.TrimSpace(source) == "" {
		return RunSnapshot{}, fail(observer, validationError(
			"run code",
			"运行前代码不能为空。",
		))
	}
	submissionID := submissionID(request.SessionID, request.RunID)
	submissions, err := service.repository.ListCodeSubmissions(ctx, request.SessionID)
	if err != nil {
		return RunSnapshot{}, fail(observer, persistenceFailure("list code runs", err))
	}
	for _, submission := range submissions {
		if submission.ID != submissionID {
			continue
		}
		existing, err := decodeSubmission(submission)
		if err != nil {
			return RunSnapshot{}, fail(observer, corruptStoredRun(err))
		}
		if existing.QuestionID != request.QuestionID ||
			existing.Language != request.Language ||
			existing.Source != source {
			return RunSnapshot{}, fail(observer, domainerr.New(
				domainerr.CodePolicyDenied,
				"retry code run",
				"同一运行 ID 不能对应不同题目、语言或代码。",
				"使用原代码重试，或为新代码生成新的运行 ID。",
				false,
			))
		}
		existing.Idempotent = true
		notify(observer, async.NewSucceeded(Progress{
			Stage: "ready", Message: "已恢复持久化运行结果",
		}))
		return *cloneSnapshot(&existing), nil
	}
	if service.runner == nil {
		return RunSnapshot{}, fail(observer, runnerDisabled())
	}
	progress(observer, "snapshotting_code", "正在创建不可变代码快照")
	result, err := service.runner.Run(ctx, ExecutionRequest{
		QuestionID: request.QuestionID,
		Language:   request.Language,
		Source:     source,
	})
	if err != nil {
		return RunSnapshot{}, fail(observer, runnerFailure(ctx, err))
	}
	if err := result.validate(); err != nil {
		return RunSnapshot{}, fail(observer, err)
	}
	testResult, err := json.Marshal(result.Result)
	if err != nil {
		return RunSnapshot{}, fail(observer, persistenceFailure("encode code run", err))
	}
	runtime, err := json.Marshal(result.Runtime)
	if err != nil {
		return RunSnapshot{}, fail(observer, persistenceFailure("encode runtime stats", err))
	}
	progress(observer, "saving_run", "正在保存公开结果与安全运行摘要")
	now := service.now().UTC()
	snapshot := RunSnapshot{
		SubmissionID: submissionID,
		SnapshotID:   submissionID + "/snapshot",
		SessionID:    request.SessionID,
		QuestionID:   request.QuestionID,
		Language:     request.Language,
		Source:       source,
		Result:       result.Result,
		Runtime:      result.Runtime,
		CreatedAt:    now,
	}
	if err := service.repository.AddCodeSubmission(ctx, db.CodeSubmission{
		ID:           snapshot.SubmissionID,
		SessionID:    snapshot.SessionID,
		QuestionID:   snapshot.QuestionID,
		Language:     string(snapshot.Language),
		Source:       snapshot.Source,
		TestResult:   testResult,
		RuntimeStats: runtime,
		SnapshotID:   snapshot.SnapshotID,
		CreatedAt:    snapshot.CreatedAt,
	}); err != nil {
		return RunSnapshot{}, fail(observer, persistenceFailure("save code run", err))
	}
	notify(observer, async.NewSucceeded(Progress{
		Stage: "ready", Message: "代码运行结果已保存",
	}))
	return *cloneSnapshot(&snapshot), nil
}

func (service *Service) updateDraft(
	ctx context.Context,
	sessionID string,
	questionID string,
	language Language,
	observer Observer,
	stages []Progress,
	mutate func(*DraftDocument, Question) error,
) (Workspace, error) {
	notify(observer, async.NewPending[Progress]())
	if service == nil || service.repository == nil {
		return Workspace{}, fail(observer, unavailableStorage("save code draft"))
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if !supportedLanguage(language) {
		return Workspace{}, fail(observer, validationError(
			"save code draft",
			"不支持该代码语言。",
		))
	}
	workspace, err := service.openInternal(ctx, sessionID, questionID)
	if err != nil {
		return Workspace{}, fail(observer, err)
	}
	for index, stage := range stages {
		progress(observer, stage.Stage, stage.Message)
		if index == 0 {
			if err := mutate(&workspace.Draft, workspace.Question); err != nil {
				return Workspace{}, fail(observer, err)
			}
			workspace.Draft.ActiveLanguage = language
		}
	}
	if err := ctx.Err(); err != nil {
		return Workspace{}, fail(observer, cancelledFailure("save code draft", err))
	}
	payload, err := encodeDraft(workspace.Draft)
	if err != nil {
		return Workspace{}, fail(observer, err)
	}
	if err := service.repository.SaveDraft(ctx, db.Draft{
		SessionID:  strings.TrimSpace(sessionID),
		QuestionID: strings.TrimSpace(questionID),
		Kind:       db.DraftCode,
		Content:    string(payload),
		UpdatedAt:  service.now().UTC(),
	}); err != nil {
		return Workspace{}, fail(observer, persistenceFailure("save code draft", err))
	}
	notify(observer, async.NewSucceeded(Progress{
		Stage: "saved", Message: "本地代码草稿已保存",
	}))
	return cloneWorkspace(workspace), nil
}

func (service *Service) openInternal(
	ctx context.Context,
	sessionID string,
	questionID string,
) (Workspace, error) {
	if service.repository == nil {
		return Workspace{}, unavailableStorage("open coding workspace")
	}
	sessionID = strings.TrimSpace(sessionID)
	questionID = strings.TrimSpace(questionID)
	if err := validateIdentifier("open coding workspace", "会话 ID", sessionID); err != nil {
		return Workspace{}, err
	}
	if err := validateIdentifier("open coding workspace", "代码题 ID", questionID); err != nil {
		return Workspace{}, err
	}
	question, found := service.byID[questionID]
	if !found {
		return Workspace{}, validationError("open coding workspace", "代码题不存在。")
	}
	document := defaultDraft(question)
	stored, found, err := service.repository.LoadDraft(
		ctx,
		sessionID,
		questionID,
		db.DraftCode,
	)
	if err != nil {
		return Workspace{}, persistenceFailure("load code draft", err)
	}
	if found {
		document, err = decodeDraft([]byte(stored.Content))
		if err != nil || document.QuestionID != questionID {
			return Workspace{}, corruptStoredDraft(err)
		}
	}
	latest, err := latestRun(ctx, service.repository, sessionID, questionID)
	if err != nil {
		return Workspace{}, err
	}
	return Workspace{
		Question:  cloneQuestion(question),
		Draft:     cloneDraft(document),
		LatestRun: latest,
	}, nil
}

func defaultDraft(question Question) DraftDocument {
	sources := make(map[Language]string, len(languages))
	for _, language := range languages {
		sources[language] = question.Templates[language]
	}
	return DraftDocument{
		Version: DraftVersion, QuestionID: question.ID,
		ActiveLanguage: LanguagePython, Sources: sources,
	}
}

func latestRun(
	ctx context.Context,
	repository Repository,
	sessionID string,
	questionID string,
) (*RunSnapshot, error) {
	values, err := repository.ListCodeSubmissions(ctx, sessionID)
	if err != nil {
		return nil, persistenceFailure("list code runs", err)
	}
	var latest *RunSnapshot
	for _, value := range values {
		if value.QuestionID != questionID {
			continue
		}
		snapshot, err := decodeSubmission(value)
		if err != nil {
			return nil, corruptStoredRun(err)
		}
		latest = cloneSnapshot(&snapshot)
	}
	return latest, nil
}

func decodeSubmission(value db.CodeSubmission) (RunSnapshot, error) {
	var result SafeResult
	if err := strictDecode(value.TestResult, &result); err != nil {
		return RunSnapshot{}, err
	}
	var runtime RuntimeStats
	if err := strictDecode(value.RuntimeStats, &runtime); err != nil {
		return RunSnapshot{}, err
	}
	execution := ExecutionResult{Result: result, Runtime: runtime}
	if err := execution.validate(); err != nil {
		return RunSnapshot{}, err
	}
	language := Language(value.Language)
	if !supportedLanguage(language) || strings.TrimSpace(value.Source) == "" ||
		value.CreatedAt.IsZero() {
		return RunSnapshot{}, errors.New("stored code run metadata is invalid")
	}
	return RunSnapshot{
		SubmissionID: value.ID,
		SnapshotID:   value.SnapshotID,
		SessionID:    value.SessionID,
		QuestionID:   value.QuestionID,
		Language:     language,
		Source:       value.Source,
		Result:       result,
		Runtime:      runtime,
		CreatedAt:    value.CreatedAt,
	}, nil
}

func strictDecode(payload []byte, target any) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("stored coding payload must be a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func validateRunRequest(request RunRequest) error {
	if err := validateIdentifier("run code", "会话 ID", request.SessionID); err != nil {
		return err
	}
	if err := validateIdentifier("run code", "代码题 ID", request.QuestionID); err != nil {
		return err
	}
	if err := validateIdentifier("run code", "运行 ID", request.RunID); err != nil {
		return err
	}
	if !supportedLanguage(request.Language) {
		return validationError("run code", "不支持该代码语言。")
	}
	return nil
}

func submissionID(sessionID, runID string) string {
	return "ic/coding/" + strings.TrimSpace(sessionID) + "/" + strings.TrimSpace(runID)
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func progress(observer Observer, stage, message string) {
	value := Progress{Stage: stage, Message: message}
	notify(observer, async.NewStreaming(&value))
}

func fail(observer Observer, err error) error {
	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		typed = persistenceFailure("coding operation", err)
	}
	notify(observer, async.NewFailed[Progress](typed))
	return typed
}

func unavailableStorage(operation string) *domainerr.Error {
	return domainerr.New(
		domainerr.CodeDependencyUnavailable,
		operation,
		"本地代码草稿存储不可用。",
		"重新打开训练会话后重试。",
		true,
	)
}

func runnerDisabled() *domainerr.Error {
	return domainerr.New(
		domainerr.CodeDependencyUnavailable,
		"run code",
		"代码执行未启用。",
		"在设置中将 RUNNER_MODE 设为 docker 并完成健康检查；文字面试和 Coach 仍可继续。",
		false,
	)
}

func persistenceFailure(operation string, err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"local coding storage",
		"无法保存或恢复本地代码状态。",
		"保留当前编辑内容，检查 SQLite 后重试。",
		true,
		err,
	)
}

func corruptStoredDraft(err error) *domainerr.Error {
	if err == nil {
		err = errors.New("draft question mismatch")
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"decode code draft",
		"local coding storage",
		"本地代码草稿已损坏，无法安全恢复。",
		"保留数据库文件并从模板重新开始该题。",
		false,
		err,
	)
}

func corruptStoredRun(err error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"decode code run",
		"local coding storage",
		"已保存的代码运行结果无效。",
		"保留草稿并重新运行；不要使用该结果作为 Coach 证据。",
		false,
		err,
	)
}

func runnerFailure(ctx context.Context, err error) *domainerr.Error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return cancelledFailure("run code", err)
	}
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"run code",
		"code runner",
		"代码执行器暂时不可用。",
		"检查 Runner 健康状态后重试；文字面试和 Coach 仍可继续。",
		true,
		err,
	)
}

func formattingFailure(ctx context.Context, err error) *domainerr.Error {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) {
		return cancelledFailure("format code draft", err)
	}
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeInvalidState,
		"format code draft",
		"code formatter",
		"无法格式化当前代码草稿。",
		"原草稿保持不变，可继续编辑或重试。",
		true,
		err,
	)
}

func cancelledFailure(operation string, err error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeOperationCancelled,
		operation,
		"",
		"代码操作已停止，未覆盖本地草稿。",
		"可以继续编辑并稍后重试。",
		true,
		err,
	)
}
