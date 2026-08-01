package coding

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreevaluation "github.com/interviewcraft/interviewcraft/internal/core/evaluation"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestDraftEditingFormattingTemplateResetAndRestartRecovery(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store := openCodingStore(t, dataDir)
	seedCodingSession(t, store)
	now := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	service := newCodingService(t, store, Options{Now: func() time.Time { return now }})

	initial, err := service.Open(ctx, "session-coding", "pair_sum")
	if err != nil {
		t.Fatalf("Open initial: %v", err)
	}
	if initial.LatestRun != nil || initial.Draft.ActiveLanguage != LanguagePython {
		t.Fatalf("initial workspace=%#v", initial)
	}
	for _, language := range Languages() {
		if initial.Draft.Sources[language] != initial.Question.Templates[language] {
			t.Errorf("initial %s source does not match template", language)
		}
	}

	var phases []async.Phase
	workspace, err := service.SaveSource(
		ctx,
		"session-coding",
		"pair_sum",
		LanguagePython,
		"def pair_sum(nums, target):  \r\n    return []\t\r\n",
		func(state async.State[Progress]) { phases = append(phases, state.Phase) },
	)
	if err != nil || !reflect.DeepEqual(phases, []async.Phase{
		async.Pending, async.Streaming, async.Succeeded,
	}) {
		t.Fatalf("SaveSource phases=%v workspace=%#v err=%v", phases, workspace, err)
	}
	workspace, err = service.FormatSource(
		ctx, "session-coding", "pair_sum", LanguagePython, nil,
	)
	if err != nil || workspace.Draft.Sources[LanguagePython] !=
		"def pair_sum(nums, target):\n    return []\n" {
		t.Fatalf("FormatSource workspace=%#v err=%v", workspace, err)
	}
	if _, err := service.SaveSource(
		ctx, "session-coding", "pair_sum", LanguageJavaScript,
		"function pairSum(nums, target) {\n  return [];\n}\n", nil,
	); err != nil {
		t.Fatalf("Save JavaScript: %v", err)
	}
	if _, err := service.SaveSource(
		ctx, "session-coding", "pair_sum", LanguageJava,
		"class Solution { int[] pairSum(int[] nums, int target) { return null; } }\n", nil,
	); err != nil {
		t.Fatalf("Save Java: %v", err)
	}
	workspace, err = service.ResetTemplate(
		ctx, "session-coding", "pair_sum", LanguageJava, nil,
	)
	if err != nil || workspace.Draft.Sources[LanguageJava] !=
		workspace.Question.Templates[LanguageJava] {
		t.Fatalf("ResetTemplate workspace=%#v err=%v", workspace, err)
	}
	workspace, err = service.SelectLanguage(
		ctx, "session-coding", "pair_sum", LanguageJavaScript, nil,
	)
	if err != nil || workspace.Draft.ActiveLanguage != LanguageJavaScript {
		t.Fatalf("SelectLanguage workspace=%#v err=%v", workspace, err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	reopened := openCodingStore(t, dataDir)
	restarted := newCodingService(t, reopened, Options{})
	restored, err := restarted.Open(ctx, "session-coding", "pair_sum")
	if err != nil {
		t.Fatalf("Open restored: %v", err)
	}
	if restored.Draft.ActiveLanguage != LanguageJavaScript ||
		restored.Draft.Sources[LanguagePython] !=
			"def pair_sum(nums, target):\n    return []\n" ||
		restored.Draft.Sources[LanguageJavaScript] !=
			"function pairSum(nums, target) {\n  return [];\n}\n" ||
		restored.Draft.Sources[LanguageJava] != restored.Question.Templates[LanguageJava] {
		t.Fatalf("restored=%#v", restored.Draft)
	}
}

func TestRunnerDisabledIsActionableAndNeverPersistsUnrunDraft(t *testing.T) {
	ctx := context.Background()
	store := openCodingStore(t, t.TempDir())
	seedCodingSession(t, store)
	service := newCodingService(t, store, Options{})
	status := service.RunnerStatus()
	if status.Enabled || status.Message != "代码执行未启用。" ||
		!strings.Contains(status.RecoveryAction, "RUNNER_MODE") ||
		!strings.Contains(status.RecoveryAction, "文字面试和 Coach") {
		t.Fatalf("RunnerStatus=%#v", status)
	}
	secret := "unexecuted draft must stay local"
	if _, err := service.SaveSource(
		ctx, "session-coding", "pair_sum", LanguagePython, secret, nil,
	); err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	var phases []async.Phase
	_, err := service.Run(ctx, RunRequest{
		SessionID: "session-coding", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "disabled-run",
	}, func(state async.State[Progress]) { phases = append(phases, state.Phase) })
	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) ||
		!reflect.DeepEqual(phases, []async.Phase{async.Pending, async.Failed}) {
		t.Fatalf("disabled err=%#v phases=%v", err, phases)
	}
	var typed *domainerr.Error
	if !errors.As(err, &typed) || typed.Message != "代码执行未启用。" ||
		strings.Contains(typed.Error(), secret) {
		t.Fatalf("disabled error=%#v", typed)
	}
	submissions, err := store.ListCodeSubmissions(ctx, "session-coding")
	if err != nil || len(submissions) != 0 {
		t.Fatalf("unrun submissions=%#v err=%v", submissions, err)
	}
	workspace, err := service.Open(ctx, "session-coding", "pair_sum")
	if err != nil || workspace.LatestRun != nil ||
		workspace.Draft.Sources[LanguagePython] != secret {
		t.Fatalf("unrun workspace=%#v err=%v", workspace, err)
	}
}

func TestThreeLanguageEditFormatAndTemplateReset(t *testing.T) {
	ctx := context.Background()
	store := openCodingStore(t, t.TempDir())
	seedCodingSession(t, store)
	service := newCodingService(t, store, Options{})
	for _, language := range Languages() {
		custom := "custom " + string(language) + "  \r\n"
		workspace, err := service.SaveSource(
			ctx, "session-coding", "pair_sum", language, custom, nil,
		)
		if err != nil || workspace.Draft.Sources[language] != custom {
			t.Fatalf("SaveSource %s=%#v err=%v", language, workspace.Draft, err)
		}
		workspace, err = service.FormatSource(
			ctx, "session-coding", "pair_sum", language, nil,
		)
		if err != nil || workspace.Draft.Sources[language] !=
			"custom "+string(language)+"\n" {
			t.Fatalf("FormatSource %s=%#v err=%v", language, workspace.Draft, err)
		}
		workspace, err = service.ResetTemplate(
			ctx, "session-coding", "pair_sum", language, nil,
		)
		if err != nil || workspace.Draft.Sources[language] !=
			workspace.Question.Templates[language] {
			t.Fatalf("ResetTemplate %s=%#v err=%v", language, workspace.Draft, err)
		}
	}
}

func TestExecutedSnapshotIsSafePersistedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	store := openCodingStore(t, t.TempDir())
	seedCodingSession(t, store)
	runner := &runnerStub{hiddenInput: "HIDDEN_INPUT_DO_NOT_LEAK"}
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	service := newCodingService(t, store, Options{
		Now: func() time.Time { return now }, Runner: runner,
	})
	source := "def pair_sum(nums, target):\n    return [0, 1]\n"
	if _, err := service.SaveSource(
		ctx, "session-coding", "pair_sum", LanguagePython, source, nil,
	); err != nil {
		t.Fatalf("SaveSource: %v", err)
	}
	var phases []async.Phase
	snapshot, err := service.Run(ctx, RunRequest{
		SessionID: "session-coding", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "run-1",
	}, func(state async.State[Progress]) { phases = append(phases, state.Phase) })
	if err != nil || !reflect.DeepEqual(phases, []async.Phase{
		async.Pending, async.Streaming, async.Streaming, async.Succeeded,
	}) {
		t.Fatalf("Run snapshot=%#v err=%v phases=%v", snapshot, err, phases)
	}
	if snapshot.Idempotent || snapshot.Result.HiddenTests.Passed != 2 ||
		snapshot.Result.HiddenTests.Failed != 0 || runner.Calls() != 1 {
		t.Fatalf("snapshot=%#v runner calls=%d", snapshot, runner.Calls())
	}
	encoded, err := json.Marshal(snapshot)
	if err != nil || strings.Contains(string(encoded), runner.hiddenInput) ||
		strings.Contains(string(encoded), "hidden_input") ||
		strings.Contains(string(encoded), "expected_output") {
		t.Fatalf("safe snapshot=%s err=%v", encoded, err)
	}
	stored, err := store.ListCodeSubmissions(ctx, "session-coding")
	if err != nil || len(stored) != 1 ||
		strings.Contains(string(stored[0].TestResult), runner.hiddenInput) {
		t.Fatalf("stored=%#v err=%v", stored, err)
	}
	workspace, err := service.Open(ctx, "session-coding", "pair_sum")
	if err != nil || workspace.LatestRun == nil ||
		workspace.LatestRun.SubmissionID != snapshot.SubmissionID {
		t.Fatalf("restored run=%#v err=%v", workspace.LatestRun, err)
	}

	retry, err := service.Run(ctx, RunRequest{
		SessionID: "session-coding", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "run-1",
	}, nil)
	if err != nil || !retry.Idempotent || runner.Calls() != 1 {
		t.Fatalf("retry=%#v err=%v calls=%d", retry, err, runner.Calls())
	}
	if _, err := service.SaveSource(
		ctx, "session-coding", "pair_sum", LanguagePython, source+"# changed\n", nil,
	); err != nil {
		t.Fatalf("change source: %v", err)
	}
	if _, err := service.Run(ctx, RunRequest{
		SessionID: "session-coding", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "run-1",
	}, nil); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("changed idempotent run err=%#v", err)
	}
}

func TestDraftAndRunnerFailuresAreTypedAndDoNotOverwriteState(t *testing.T) {
	ctx := context.Background()
	repository := &repositoryStub{saveErr: errors.New("secret SQLite failure")}
	service := newCodingService(t, repository, Options{})
	var phases []async.Phase
	_, err := service.SaveSource(
		ctx, "session-coding", "pair_sum", LanguagePython, "pass",
		func(state async.State[Progress]) { phases = append(phases, state.Phase) },
	)
	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) ||
		!reflect.DeepEqual(phases, []async.Phase{
			async.Pending, async.Streaming, async.Failed,
		}) || strings.Contains(err.Error(), "secret SQLite failure") {
		t.Fatalf("save err=%#v phases=%v", err, phases)
	}

	store := openCodingStore(t, t.TempDir())
	seedCodingSession(t, store)
	if err := store.SaveDraft(ctx, db.Draft{
		SessionID: "session-coding", QuestionID: "pair_sum", Kind: db.DraftCode,
		Content: `{"version":"broken"}`, UpdatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Save corrupt draft: %v", err)
	}
	corrupt := newCodingService(t, store, Options{})
	if _, err := corrupt.Open(ctx, "session-coding", "pair_sum"); !domainerr.IsCode(err, domainerr.CodePersistenceFailed) ||
		strings.Contains(err.Error(), "broken") {
		t.Fatalf("corrupt draft err=%#v", err)
	}

	second := openCodingStore(t, t.TempDir())
	seedCodingSession(t, second)
	invalidRunner := &runnerStub{invalid: true}
	invalidService := newCodingService(t, second, Options{Runner: invalidRunner})
	if _, err := invalidService.Run(ctx, RunRequest{
		SessionID: "session-coding", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "invalid-result",
	}, nil); !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("invalid runner err=%#v", err)
	}
	values, err := second.ListCodeSubmissions(ctx, "session-coding")
	if err != nil || len(values) != 0 {
		t.Fatalf("invalid runner persisted=%#v err=%v", values, err)
	}
}

func TestServiceDependenciesCancellationAndStoredRunCorruptionAreSafe(t *testing.T) {
	ctx := context.Background()
	var nilService *Service
	if len(nilService.Questions()) != 0 {
		t.Fatal("nil service returned catalog")
	}
	if _, err := nilService.Open(ctx, "session-coding", "pair_sum"); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil Open err=%#v", err)
	}
	if _, err := nilService.SaveSource(
		ctx, "session-coding", "pair_sum", LanguagePython, "pass", nil,
	); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil SaveSource err=%#v", err)
	}
	if _, err := nilService.Run(ctx, RunRequest{
		SessionID: "session-coding", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "nil-run",
	}, nil); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil Run err=%#v", err)
	}

	runnerFailureText := "secret runner transport detail"
	runner := &runnerStub{runErr: errors.New(runnerFailureText)}
	service := newCodingService(t, &repositoryStub{}, Options{Runner: runner})
	if !service.RunnerStatus().Enabled {
		t.Fatal("configured runner reported disabled")
	}
	questions := service.Questions()
	questions[0].Title = "mutated"
	if service.Questions()[0].Title == "mutated" {
		t.Fatal("Questions returned mutable catalog state")
	}
	if _, err := service.Run(ctx, RunRequest{
		SessionID: "session-coding", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "runner-failure",
	}, nil); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) ||
		strings.Contains(err.Error(), runnerFailureText) {
		t.Fatalf("runner failure err=%#v", err)
	}
	if _, err := service.Run(ctx, RunRequest{
		SessionID: "bad id", QuestionID: "pair_sum",
		Language: LanguagePython, RunID: "invalid-id",
	}, nil); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("invalid request err=%#v", err)
	}

	formatService := newCodingService(t, &repositoryStub{}, Options{
		Formatter: formatterStub{err: errors.New("secret formatter detail")},
	})
	if _, err := formatService.FormatSource(
		ctx, "session-coding", "pair_sum", LanguagePython, nil,
	); !domainerr.IsCode(err, domainerr.CodeInvalidState) ||
		strings.Contains(err.Error(), "secret formatter detail") {
		t.Fatalf("formatter failure err=%#v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := newCodingService(t, &repositoryStub{}, Options{}).FormatSource(
		cancelled, "session-coding", "pair_sum", LanguagePython, nil,
	); !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
		t.Fatalf("cancelled formatter err=%#v", err)
	}

	store := openCodingStore(t, t.TempDir())
	seedCodingSession(t, store)
	if err := store.AddCodeSubmission(ctx, db.CodeSubmission{
		ID: "corrupt-run", SessionID: "session-coding", QuestionID: "pair_sum",
		Language: string(LanguagePython), Source: "pass", SnapshotID: "corrupt-snapshot",
		TestResult:   json.RawMessage(`{"version":"wrong"}`),
		RuntimeStats: json.RawMessage(`{"duration_ms":0,"peak_memory_kb":0}`),
		CreatedAt:    time.Now().UTC(),
	}); err != nil {
		t.Fatalf("Add corrupt code submission: %v", err)
	}
	if _, err := newCodingService(t, store, Options{}).Open(
		ctx, "session-coding", "pair_sum",
	); !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("corrupt stored run err=%#v", err)
	}
}

func TestLiteDisabledRunnerKeepsTextCoachAndReportFlow(t *testing.T) {
	ctx := context.Background()
	store := openCodingStore(t, t.TempDir())
	seedCodingSession(t, store)
	now := time.Date(2026, 8, 2, 11, 0, 0, 0, time.UTC)
	codingService := newCodingService(t, store, Options{Now: func() time.Time { return now }})
	draftSecret := "unexecuted code must not enter Coach"
	if _, err := codingService.SaveSource(
		ctx, "session-coding", "pair_sum", LanguagePython, draftSecret, nil,
	); err != nil {
		t.Fatalf("Save code draft: %v", err)
	}

	interviewer := &interviewProviderStub{}
	interviewService := coreinterview.NewService(store, interviewer, coreinterview.Options{
		Now: func() time.Time { return now.Add(time.Minute) },
	})
	if _, err := interviewService.Start(ctx, "session-coding"); err != nil {
		t.Fatalf("Start text interview: %v", err)
	}
	coachProvider := &coachProviderStub{forbidden: draftSecret}
	coachService := corecoach.NewService(store, coachProvider, corecoach.Options{
		Now: func() time.Time { return now.Add(2 * time.Minute) },
	})
	if _, err := coachService.Ask(ctx, corecoach.AskRequest{
		SessionID: "session-coding", QuestionID: "pair_sum", RequestID: "lite-coach",
		Intent: contracts.CoachGiveHint, RequestedLevel: contracts.HelpL1,
		UserRequest: "Give me a small clarification hint.",
	}, nil); err != nil {
		t.Fatalf("Coach Ask without Runner: %v", err)
	}
	if coachProvider.Calls() != 1 {
		t.Fatalf("Coach calls=%d", coachProvider.Calls())
	}
	if _, err := interviewService.SaveDraft(
		ctx, "session-coding", "I would clarify constraints and explain a hash map.",
	); err != nil {
		t.Fatalf("Save text draft: %v", err)
	}
	if _, err := interviewService.Submit(ctx, coreinterview.SubmitRequest{
		SessionID: "session-coding", SubmissionID: "text-answer",
		Answer: "I would clarify constraints and use a one-pass hash map.",
	}, nil); err != nil {
		t.Fatalf("Submit text answer: %v", err)
	}

	evaluator := &evaluationProviderStub{}
	evaluationService := coreevaluation.NewService(store, evaluator, coreevaluation.Options{
		Now: func() time.Time { return now.Add(5 * time.Minute) },
	})
	result, err := evaluationService.Generate(ctx, "session-coding", nil)
	if err != nil {
		t.Fatalf("Generate report without Runner: %v", err)
	}
	if result.Report.Summary.CodeRunCount != 0 || evaluator.CodeRuns() != 0 {
		t.Fatalf("code counts report=%d evaluator=%d", result.Report.Summary.CodeRunCount, evaluator.CodeRuns())
	}
	var codeQuality *corereport.ScorecardItem
	for index := range result.Report.Scorecard {
		if result.Report.Scorecard[index].Dimension == contracts.DimensionCodeQuality {
			codeQuality = &result.Report.Scorecard[index]
			break
		}
	}
	if codeQuality == nil || codeQuality.Status != corereport.StatusNotApplicable {
		t.Fatalf("code quality=%#v", codeQuality)
	}
	session, found, err := store.GetSession(ctx, "session-coding")
	if err != nil || !found || session.Status != db.SessionCompleted {
		t.Fatalf("completed session=%#v found=%v err=%v", session, found, err)
	}
}

type runnerStub struct {
	mu          sync.Mutex
	calls       int
	hiddenInput string
	invalid     bool
	runErr      error
}

func (runner *runnerStub) Run(
	_ context.Context,
	request ExecutionRequest,
) (ExecutionResult, error) {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	runner.calls++
	_ = runner.hiddenInput
	if runner.runErr != nil {
		return ExecutionResult{}, runner.runErr
	}
	if runner.invalid {
		return ExecutionResult{Result: SafeResult{Version: "wrong"}}, nil
	}
	if request.QuestionID != "pair_sum" || request.Language != LanguagePython {
		return ExecutionResult{}, errors.New("unexpected execution request")
	}
	return ExecutionResult{
		Result: SafeResult{
			Version: ResultVersion, Status: RunPassed,
			PublicTests: []PublicTestResult{
				{Name: "example-1", Status: TestPassed},
				{Name: "example-2", Status: TestPassed},
			},
			HiddenTests: HiddenTestSummary{Passed: 2},
			ErrorKind:   ErrorNone,
		},
		Runtime: RuntimeStats{DurationMilliseconds: 12, PeakMemoryKB: 4096},
	}, nil
}

type formatterStub struct {
	err error
}

func (formatter formatterStub) Format(
	context.Context,
	Language,
	string,
) (string, error) {
	return "", formatter.err
}

func (runner *runnerStub) Calls() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.calls
}

type repositoryStub struct {
	saveErr error
}

func (repository *repositoryStub) SaveDraft(context.Context, db.Draft) error {
	return repository.saveErr
}

func (*repositoryStub) LoadDraft(
	context.Context,
	string,
	string,
	db.DraftKind,
) (db.Draft, bool, error) {
	return db.Draft{}, false, nil
}

func (*repositoryStub) AddCodeSubmission(context.Context, db.CodeSubmission) error {
	return nil
}

func (*repositoryStub) ListCodeSubmissions(context.Context, string) ([]db.CodeSubmission, error) {
	return []db.CodeSubmission{}, nil
}

type interviewProviderStub struct{}

func (*interviewProviderStub) Respond(
	_ context.Context,
	input coreinterview.Input,
) (contracts.InterviewerAction, error) {
	if len(input.SubmittedAnswers) != 1 || len(input.CodeRuns) != 0 {
		return contracts.InterviewerAction{}, errors.New("unsafe text interview input")
	}
	return contracts.InterviewerAction{
		Action:       contracts.ActionFinishSession,
		QuestionID:   input.CurrentQuestion.ID,
		Message:      "The text-only session is complete.",
		EvidenceIDs:  []contracts.EvidenceID{input.SubmittedAnswers[0].EventID},
		SessionState: contracts.SessionComplete,
	}, nil
}

type coachProviderStub struct {
	mu        sync.Mutex
	calls     int
	forbidden string
}

func (provider *coachProviderStub) Respond(
	_ context.Context,
	input corecoach.Input,
) (contracts.CoachResponse, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	payload, _ := json.Marshal(input)
	if len(input.CodeRuns) != 0 || strings.Contains(string(payload), provider.forbidden) {
		return contracts.CoachResponse{}, errors.New("unexecuted draft leaked to Coach")
	}
	return contracts.CoachResponse{
		Intent: input.Intent, HelpLevel: contracts.HelpL1,
		KnowledgeTags:     []string{"hash map invariant"},
		RecommendedAction: "State the complement invariant before coding.",
	}, nil
}

func (provider *coachProviderStub) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

type evaluationProviderStub struct {
	mu       sync.Mutex
	codeRuns int
}

func (provider *evaluationProviderStub) Evaluate(
	_ context.Context,
	input coreevaluation.Input,
) (coreevaluation.Draft, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.codeRuns = len(input.CodeRuns)
	return coreevaluation.Draft{
		QuestionReviews: []coreevaluation.DraftQuestionReview{},
		Findings:        []contracts.EvaluationFinding{},
		CrossInsights:   []coreevaluation.DraftInsight{},
		PracticePlan:    []coreevaluation.DraftPracticeItem{},
	}, nil
}

func (provider *evaluationProviderStub) CodeRuns() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.codeRuns
}

func newCodingService(t *testing.T, repository Repository, options Options) *Service {
	t.Helper()
	service, err := NewService(repository, options)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func openCodingStore(t *testing.T, dataDir string) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{
		DataDir: dataDir, DatabaseName: "coding.db",
	}, nil)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func seedCodingSession(t *testing.T, store *db.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 2, 8, 0, 0, 0, time.UTC)
	confirmed := now.Add(time.Minute)
	sourceText := "Built a reliable payment API with Go."
	aggregate := coreprofile.Aggregate{
		ID: "profile-coding",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID: "fact-coding", Field: "project", Value: "payment API",
				SourceSpan: contracts.SourceSpan{Start: 17, End: 28, Text: "payment API"},
			}},
			Inferences: []contracts.ProfileInference{},
			Projects:   []string{"payment API"}, Skills: []string{"Go"},
		},
		Metadata: coreprofile.Metadata{
			Source: coreprofile.Source{
				Kind: coreprofile.SourcePaste, Name: "coding fixture", Text: sourceText,
			},
			LockedFactIDs:      []contracts.EvidenceID{"fact-coding"},
			LockedInferenceIDs: []string{}, CreatedAt: now, UpdatedAt: now,
		},
		ConfirmedAt: &confirmed,
	}
	if err := store.SaveProfileAggregate(ctx, aggregate); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	scenario := contracts.Scenario{
		Template: "algorithm_coding", Mode: contracts.ScenarioStandard,
		TimeBudgetSeconds: 1200, PromptVersion: "scenario-v1",
		Questions: []contracts.ScenarioQuestion{{
			ID: "pair_sum", Prompt: "Explain and solve Pair Sum Indices.",
			Intent: "Assess clarification and problem solving", EstimatedSeconds: 600,
			Rubric:       []string{"Clarifies constraints", "Explains a one-pass map"},
			EvidenceIDs:  []contracts.EvidenceID{"fact-coding"},
			MaxFollowUps: 1, EndCondition: "Explains a verifiable approach",
		}},
	}
	if err := store.SaveScenario(ctx, "scenario-coding", aggregate.ID, scenario, confirmed); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID: "session-coding", ScenarioID: "scenario-coding", Status: db.SessionActive,
		StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

func TestOpenCodingStoreUsesExpectedPath(t *testing.T) {
	dataDir := t.TempDir()
	store := openCodingStore(t, dataDir)
	if store.Paths().Database != filepath.Join(dataDir, "coding.db") {
		t.Fatalf("database path=%q", store.Paths().Database)
	}
}
