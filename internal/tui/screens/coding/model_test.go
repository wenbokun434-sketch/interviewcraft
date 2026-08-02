package coding

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestWorkbenchKeyboardThreeLanguagesDraftRestoreResetRunExplainAndReturn(t *testing.T) {
	ctx := context.Background()
	service := newWorkspaceStub(true)
	service.result = failedRun(corecoding.ErrorNone)
	coach := &coachStub{result: corecoach.AskResult{
		Response: contracts.CoachResponse{
			Intent: contracts.CoachExplainFailure, HelpLevel: contracts.HelpL1,
			KnowledgeTags:     []string{"hash-map invariant"},
			RecommendedAction: "先检查补数查找与写入映射的先后顺序，不提供完整实现。",
		},
	}}
	model := newWorkbenchModel(t, service, coach, 120, 36, false, nil)
	var phases []async.Phase
	if err := model.Load(ctx, func(state async.State[Progress]) {
		phases = append(phases, state.Phase)
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(screenPhases(phases), ","); got != "pending,streaming,succeeded" {
		t.Fatalf("load phases=%s", got)
	}
	if model.ActiveFocus() != focusEditor {
		t.Fatalf("initial focus=%q", model.ActiveFocus())
	}

	python := "def pair_sum(nums, target):\n    return [0, 1]\n"
	if err := model.UpdateSource(python); err != nil {
		t.Fatalf("UpdateSource python: %v", err)
	}
	if action := model.HandleKey("ctrl+s"); action.Intent != IntentSave {
		t.Fatalf("save action=%#v", action)
	} else if err := model.Execute(ctx, action, nil); err != nil {
		t.Fatalf("Save execute: %v", err)
	}

	action := model.HandleKey("ctrl+2")
	if action.Intent != IntentSelectLanguage || action.Language != corecoding.LanguageJavaScript {
		t.Fatalf("javascript action=%#v", action)
	}
	if err := model.Execute(ctx, action, nil); err != nil {
		t.Fatalf("Select JavaScript: %v", err)
	}
	javascript := "function pairSum(nums, target) { return [0, 1]; }\n"
	if err := model.UpdateSource(javascript); err != nil {
		t.Fatalf("UpdateSource JavaScript: %v", err)
	}
	if err := model.SaveDraft(ctx, nil); err != nil {
		t.Fatalf("Save JavaScript: %v", err)
	}

	if err := model.Execute(ctx, model.HandleKey("ctrl+3"), nil); err != nil {
		t.Fatalf("Select Java: %v", err)
	}
	java := "class Solution { int[] pairSum(int[] nums, int target) { return new int[]{0,1}; } }\n"
	if err := model.UpdateSource(java); err != nil {
		t.Fatalf("UpdateSource Java: %v", err)
	}
	if err := model.SaveDraft(ctx, nil); err != nil {
		t.Fatalf("Save Java: %v", err)
	}
	if err := model.SelectLanguage(ctx, corecoding.LanguagePython, nil); err != nil {
		t.Fatalf("Select Python: %v", err)
	}
	if model.Source() != python {
		t.Fatalf("python draft=%q want %q", model.Source(), python)
	}

	if err := model.FormatSource(ctx, nil); err != nil {
		t.Fatalf("FormatSource: %v", err)
	}
	if !strings.HasSuffix(model.Source(), "# formatted\n") {
		t.Fatalf("formatted source=%q", model.Source())
	}
	if err := model.ResetTemplate(ctx, nil); err != nil {
		t.Fatalf("ResetTemplate: %v", err)
	}
	if model.Source() != service.question.Templates[corecoding.LanguagePython] {
		t.Fatalf("reset source=%q", model.Source())
	}
	if err := model.UpdateSource(python); err != nil {
		t.Fatalf("restore custom python: %v", err)
	}
	if err := model.SaveDraft(ctx, nil); err != nil {
		t.Fatalf("persist custom python: %v", err)
	}

	restored := newWorkbenchModel(t, service, coach, 120, 36, false, nil)
	if err := restored.Load(ctx, nil); err != nil {
		t.Fatalf("restored Load: %v", err)
	}
	if restored.Source() != python {
		t.Fatalf("restored source=%q", restored.Source())
	}
	if err := restored.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if restored.Workspace().LatestRun == nil {
		t.Fatal("latest run not retained")
	}
	if action := restored.HandleKey("ctrl+e"); action.Intent != IntentExplain {
		t.Fatalf("explain action=%#v", action)
	} else if err := restored.Execute(ctx, action, nil); err != nil {
		t.Fatalf("Explain execute: %v", err)
	}
	coach.mu.Lock()
	request := coach.request
	coach.mu.Unlock()
	if request.Intent != contracts.CoachExplainFailure ||
		request.RequestedLevel != contracts.HelpL1 ||
		!strings.Contains(request.UserRequest, "不要提供完整实现") {
		t.Fatalf("Coach request=%#v", request)
	}
	rendered, err := restored.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, want := range []string{"RUN SUMMARY", "example_1", "Coach", "不提供完整实现"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}
	if action := restored.HandleKey("ctrl+h"); action.Destination != DestinationInterview {
		t.Fatalf("return action=%#v", action)
	}
}

func TestRunStreamsElapsedPreventsDuplicateAndKeepsEditorWritable(t *testing.T) {
	clock := &fakeClock{value: time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)}
	service := newWorkspaceStub(true)
	service.result = passedRun()
	service.started = make(chan struct{}, 1)
	service.release = make(chan struct{})
	model := newWorkbenchModel(t, service, nil, 120, 36, false, clock.Now)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	runSource := "def pair_sum(nums, target):\n    return [0, 1]\n"
	if err := model.UpdateSource(runSource); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- model.Run(context.Background(), nil) }()
	select {
	case <-service.started:
	case <-time.After(3 * time.Second):
		t.Fatal("run did not reach blocking runner")
	}
	clock.Advance(2400 * time.Millisecond)
	model.Tick(clock.Now())
	if err := model.UpdateSource(runSource + "# edit while running\n"); err != nil {
		t.Fatalf("editing during run: %v", err)
	}
	if action := model.HandleKey("ctrl+r"); action.Intent != IntentNone {
		t.Fatalf("duplicate run action=%#v", action)
	}
	if action := model.HandleKey("ctrl+s"); action.Intent != IntentNone {
		t.Fatalf("save during run action=%#v", action)
	}
	if err := model.FormatSource(context.Background(), nil); !domainerr.IsCode(err, domainerr.CodePolicyDenied) || !model.IsRunning() {
		t.Fatalf("format while running err=%v running=%v", err, model.IsRunning())
	}
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render running: %v", err)
	}
	for _, want := range []string{"running public tests", "2.4s", "editable while tests run", "edit while running"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("running render missing %q:\n%s", want, rendered)
		}
	}
	close(service.release)
	if err := <-errCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if service.runCalls != 1 {
		t.Fatalf("run calls=%d", service.runCalls)
	}
	if !strings.Contains(model.Source(), "edit while running") {
		t.Fatalf("local edit was overwritten: %q", model.Source())
	}
	latest := model.Workspace().LatestRun
	if latest == nil || latest.Source != runSource {
		t.Fatalf("executed snapshot=%#v want original source", latest)
	}
}

func TestRunSummaryFourStatesAndErrorsNeverLeakCause(t *testing.T) {
	ctx := context.Background()

	t.Run("empty", func(t *testing.T) {
		model := newWorkbenchModel(t, newWorkspaceStub(true), nil, 80, 24, false, nil)
		if err := model.Load(ctx, nil); err != nil {
			t.Fatalf("Load: %v", err)
		}
		assertRenderedContains(t, model, "public tests not run")
	})

	t.Run("disabled", func(t *testing.T) {
		model := newWorkbenchModel(t, newWorkspaceStub(false), nil, 80, 24, false, nil)
		if err := model.Load(ctx, nil); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if err := model.Run(ctx, nil); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
			t.Fatalf("Run err=%v", err)
		}
		assertRenderedContains(t, model, "Docker runner is disabled")
	})

	for _, test := range []struct {
		name   string
		result corecoding.RunSnapshot
		want   string
	}{
		{name: "failed", result: failedRun(corecoding.ErrorNone), want: "1/2 public tests passed"},
		{name: "timeout", result: failedRun(corecoding.ErrorTimeout), want: "execution time limit reached"},
		{name: "oom", result: failedRun(corecoding.ErrorOutOfMemory), want: "memory limit reached"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := newWorkspaceStub(true)
			service.result = test.result
			model := newWorkbenchModel(t, service, nil, 80, 24, false, nil)
			if err := model.Load(ctx, nil); err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := model.Run(ctx, nil); err != nil {
				t.Fatalf("Run: %v", err)
			}
			assertRenderedContains(t, model, test.want)
		})
	}

	t.Run("typed dependency cause", func(t *testing.T) {
		service := newWorkspaceStub(true)
		secret := `C:\host\keys\token.txt /tmp/runner stderr hidden_input expected=42`
		service.runErr = domainerr.Wrap(
			domainerr.CodeDependencyUnavailable,
			"run code",
			"docker",
			"代码执行器当前不可用。",
			"返回编辑器或检查 Runner 健康状态。",
			true,
			errors.New(secret),
		)
		model := newWorkbenchModel(t, service, nil, 120, 36, false, nil)
		if err := model.Load(ctx, nil); err != nil {
			t.Fatalf("Load: %v", err)
		}
		if err := model.Run(ctx, nil); err == nil {
			t.Fatal("Run should fail")
		}
		rendered, err := model.Render()
		if err != nil {
			t.Fatalf("Render: %v", err)
		}
		for _, forbidden := range []string{secret, "/tmp/runner", "stderr", "hidden_input", "expected=42"} {
			if strings.Contains(rendered, forbidden) {
				t.Fatalf("leaked %q:\n%s", forbidden, rendered)
			}
		}
		if !strings.Contains(rendered, "代码执行器当前不可用") {
			t.Fatalf("safe message missing:\n%s", rendered)
		}
	})
}

func TestResponsiveCJKASCIIHelpFocusAndLongSafeErrorSnapshots(t *testing.T) {
	service := newWorkspaceStub(true)
	service.question.Title = "两数之和索引"
	service.question.Description = "给定整数数组与目标值，返回两个不同元素的索引；请先说明不变量，再编写实现。"
	service.workspace.Question = service.question
	service.workspace.Draft = defaultStubDraft(service.question)
	service.result = failedRun(corecoding.ErrorCompile)

	tests := []struct {
		name         string
		width        int
		height       int
		ascii        bool
		reduceMotion bool
	}{
		{name: "wide", width: 160, height: 48},
		{name: "split", width: 120, height: 36},
		{name: "narrow-ascii", width: 80, height: 24, ascii: true, reduceMotion: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			model := newWorkbenchModel(t, service.Copy(), nil, test.width, test.height, test.ascii, nil)
			model.Theme.ReduceMotion = test.reduceMotion
			if err := model.Load(context.Background(), nil); err != nil {
				t.Fatalf("Load: %v", err)
			}
			if err := model.InsertText("# 中文思路\n"); err != nil {
				t.Fatalf("InsertText: %v", err)
			}
			if err := model.Run(context.Background(), nil); err != nil {
				t.Fatalf("Run: %v", err)
			}
			rendered, err := model.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertCodingGeometry(t, rendered, test.width, test.height)
			for _, want := range []string{"两数之和索引", "SPEC", "EDITOR", "RUN SUMMARY", "example_1"} {
				if !strings.Contains(rendered, want) {
					t.Fatalf("%s snapshot missing %q:\n%s", test.name, want, rendered)
				}
			}
			if test.ascii && (strings.Contains(rendered, "┌") || strings.Contains(rendered, "─")) {
				t.Fatalf("ASCII snapshot contains Unicode borders:\n%s", rendered)
			}

			model.HandleKey("tab")
			if model.ActiveFocus() != focusSpec {
				t.Fatalf("focus after Tab=%q", model.ActiveFocus())
			}
			model.HandleKey("down")
			model.HandleKey("tab")
			if model.ActiveFocus() != focusSummary {
				t.Fatalf("focus after second Tab=%q", model.ActiveFocus())
			}
			cursor := model.CursorRune()
			model.HandleKey("?")
			if model.ActiveFocus() != focusHelp {
				t.Fatalf("help focus=%q", model.ActiveFocus())
			}
			model.Resize(test.width, test.height)
			model.HandleKey("escape")
			if model.ActiveFocus() != focusSummary || model.CursorRune() != cursor {
				t.Fatalf("focus/cursor not restored focus=%q cursor=%d want=%d", model.ActiveFocus(), model.CursorRune(), cursor)
			}
		})
	}
}

func TestBlockedTerminalShowsActionableMinimum(t *testing.T) {
	model := newWorkbenchModel(t, newWorkspaceStub(false), nil, 72, 22, true, nil)
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertCodingGeometry(t, rendered, 72, 22)
	if !strings.Contains(rendered, "needs at least 80×24") || !strings.Contains(rendered, "[r] Retry") {
		t.Fatalf("blocked guidance missing:\n%s", rendered)
	}
}

type workspaceStub struct {
	mu        sync.Mutex
	question  corecoding.Question
	workspace corecoding.Workspace
	status    corecoding.RunnerStatus
	result    corecoding.RunSnapshot
	runErr    error
	started   chan struct{}
	release   chan struct{}
	runCalls  int
}

func newWorkspaceStub(enabled bool) *workspaceStub {
	question := stubQuestion()
	status := corecoding.RunnerStatus{
		Enabled: enabled, Message: "代码执行器已配置。",
	}
	if !enabled {
		status.Message = "代码执行未启用。"
		status.RecoveryAction = "在设置中将 RUNNER_MODE 设为 docker。"
	}
	return &workspaceStub{
		question: question,
		workspace: corecoding.Workspace{
			Question: question, Draft: defaultStubDraft(question),
		},
		status: status,
	}
}

func (stub *workspaceStub) Copy() *workspaceStub {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return &workspaceStub{
		question: stub.question, workspace: cloneWorkspace(stub.workspace),
		status: stub.status, result: cloneSnapshot(stub.result), runErr: stub.runErr,
	}
}

func (stub *workspaceStub) RunnerStatus() corecoding.RunnerStatus {
	return stub.status
}

func (stub *workspaceStub) Open(
	_ context.Context,
	_, _ string,
) (corecoding.Workspace, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return cloneWorkspace(stub.workspace), nil
}

func (stub *workspaceStub) SaveSource(
	_ context.Context,
	_, _ string,
	language corecoding.Language,
	source string,
	observer corecoding.Observer,
) (corecoding.Workspace, error) {
	if observer != nil {
		observer(async.NewPending[corecoding.Progress]())
	}
	stub.mu.Lock()
	stub.workspace.Draft.Sources[language] = source
	stub.workspace.Draft.ActiveLanguage = language
	result := cloneWorkspace(stub.workspace)
	stub.mu.Unlock()
	if observer != nil {
		observer(async.NewSucceeded(corecoding.Progress{Stage: "saved", Message: "saved"}))
	}
	return result, nil
}

func (stub *workspaceStub) SelectLanguage(
	_ context.Context,
	_, _ string,
	language corecoding.Language,
	_ corecoding.Observer,
) (corecoding.Workspace, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.workspace.Draft.ActiveLanguage = language
	return cloneWorkspace(stub.workspace), nil
}

func (stub *workspaceStub) FormatSource(
	_ context.Context,
	_, _ string,
	language corecoding.Language,
	_ corecoding.Observer,
) (corecoding.Workspace, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.workspace.Draft.Sources[language] = strings.TrimSpace(stub.workspace.Draft.Sources[language]) + "\n# formatted\n"
	return cloneWorkspace(stub.workspace), nil
}

func (stub *workspaceStub) ResetTemplate(
	_ context.Context,
	_, _ string,
	language corecoding.Language,
	_ corecoding.Observer,
) (corecoding.Workspace, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.workspace.Draft.Sources[language] = stub.question.Templates[language]
	return cloneWorkspace(stub.workspace), nil
}

func (stub *workspaceStub) Run(
	ctx context.Context,
	request corecoding.RunRequest,
	observer corecoding.Observer,
) (corecoding.RunSnapshot, error) {
	if observer != nil {
		observer(async.NewPending[corecoding.Progress]())
		progress := corecoding.Progress{Stage: "running", Message: "正在运行公开测试"}
		observer(async.NewStreaming(&progress))
	}
	stub.mu.Lock()
	stub.runCalls++
	started := stub.started
	release := stub.release
	runErr := stub.runErr
	result := cloneSnapshot(stub.result)
	source := stub.workspace.Draft.Sources[request.Language]
	stub.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return corecoding.RunSnapshot{}, ctx.Err()
		}
	}
	if runErr != nil {
		return corecoding.RunSnapshot{}, runErr
	}
	result.SubmissionID = "submission-" + request.RunID
	result.SnapshotID = "snapshot-" + request.RunID
	result.SessionID = request.SessionID
	result.QuestionID = request.QuestionID
	result.Language = request.Language
	result.Source = source
	result.CreatedAt = time.Date(2026, 8, 3, 9, 0, 3, 0, time.UTC)
	stub.mu.Lock()
	copy := cloneSnapshot(result)
	stub.workspace.LatestRun = &copy
	stub.mu.Unlock()
	if observer != nil {
		observer(async.NewSucceeded(corecoding.Progress{Stage: "done", Message: "done"}))
	}
	return result, nil
}

type coachStub struct {
	mu      sync.Mutex
	request corecoach.AskRequest
	result  corecoach.AskResult
	err     error
}

func (stub *coachStub) Ask(
	_ context.Context,
	request corecoach.AskRequest,
	observer corecoach.Observer,
) (corecoach.AskResult, error) {
	stub.mu.Lock()
	stub.request = request
	stub.mu.Unlock()
	if observer != nil {
		progress := corecoach.Progress{Stage: "responding", Message: "正在解释公开测试错误"}
		observer(async.NewStreaming(&progress))
	}
	return stub.result, stub.err
}

type fakeClock struct {
	mu    sync.Mutex
	value time.Time
}

func (clock *fakeClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.value
}

func (clock *fakeClock) Advance(delta time.Duration) {
	clock.mu.Lock()
	clock.value = clock.value.Add(delta)
	clock.mu.Unlock()
}

func newWorkbenchModel(
	t *testing.T,
	service WorkspaceService,
	coach CoachService,
	width, height int,
	ascii bool,
	now func() time.Time,
) *Model {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode: theme.Auto, ColorMode: theme.NoColor, UseASCII: ascii,
	})
	if err != nil {
		t.Fatalf("theme.Resolve: %v", err)
	}
	model, err := New(Options{
		SessionID: "session-ui", QuestionID: "pair_sum",
		Service: service, Coach: coach, Width: width, Height: height,
		Theme: current, Now: now,
		NextRunID:          func() string { return "run-ui" },
		NextCoachRequestID: func() string { return "coach-ui" },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func stubQuestion() corecoding.Question {
	return corecoding.Question{
		ID: "pair_sum", Title: "Pair Sum Indices",
		Description:  "Given integers and a target, return two distinct indices.",
		InputFormat:  "nums and target",
		OutputFormat: "two indices",
		Constraints:  []string{"2 <= nums.length", "exactly one pair"},
		Examples: []corecoding.Example{
			{Input: "[2,7], 9", Output: "[0,1]", Explanation: "2 + 7 = 9"},
			{Input: "[3,2,4], 6", Output: "[1,2]", Explanation: "2 + 4 = 6"},
		},
		TargetComplexity: corecoding.Complexity{Time: "O(n)", Space: "O(n)"},
		Rubric:           []corecoding.RubricItem{{Dimension: "correctness", Description: "correct"}},
		Templates: map[corecoding.Language]string{
			corecoding.LanguagePython:     "def pair_sum(nums, target):\n    pass\n",
			corecoding.LanguageJavaScript: "function pairSum(nums, target) {}\n",
			corecoding.LanguageJava:       "class Solution { int[] pairSum(int[] nums, int target) { return new int[0]; } }\n",
		},
	}
}

func defaultStubDraft(question corecoding.Question) corecoding.DraftDocument {
	sources := make(map[corecoding.Language]string, len(question.Templates))
	for language, source := range question.Templates {
		sources[language] = source
	}
	return corecoding.DraftDocument{
		Version: corecoding.DraftVersion, QuestionID: question.ID,
		ActiveLanguage: corecoding.LanguagePython, Sources: sources,
	}
}

func failedRun(kind corecoding.ErrorKind) corecoding.RunSnapshot {
	status := corecoding.RunFailed
	if kind != corecoding.ErrorNone {
		status = corecoding.RunError
	}
	return corecoding.RunSnapshot{
		Result: corecoding.SafeResult{
			Version: corecoding.ResultVersion, Status: status,
			PublicTests: []corecoding.PublicTestResult{
				{Name: "example_1", Status: corecoding.TestPassed},
				{Name: "example_2", Status: corecoding.TestFailed},
			},
			HiddenTests: corecoding.HiddenTestSummary{Passed: 2, Failed: 1},
			ErrorKind:   kind,
		},
		Runtime: corecoding.RuntimeStats{DurationMilliseconds: 124, PeakMemoryKB: 32 * 1024},
	}
}

func passedRun() corecoding.RunSnapshot {
	result := failedRun(corecoding.ErrorNone)
	result.Result.Status = corecoding.RunPassed
	result.Result.ErrorKind = corecoding.ErrorNone
	for index := range result.Result.PublicTests {
		result.Result.PublicTests[index].Status = corecoding.TestPassed
	}
	result.Result.HiddenTests = corecoding.HiddenTestSummary{Passed: 3}
	return result
}

func assertRenderedContains(t *testing.T, model *Model, want string) {
	t.Helper()
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, want) {
		t.Fatalf("render missing %q:\n%s", want, rendered)
	}
}

func assertCodingGeometry(t *testing.T, rendered string, width, height int) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("height=%d want=%d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("line %d width=%d want=%d: %q", index, got, width, line)
		}
	}
}

func screenPhases(values []async.Phase) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, string(value))
	}
	return result
}
