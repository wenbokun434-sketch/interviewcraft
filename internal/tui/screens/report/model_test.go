package report

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/evaluation"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

type loaderFunc func(context.Context, string) (corereport.Document, bool, error)

func (loader loaderFunc) Get(
	ctx context.Context,
	sessionID string,
) (corereport.Document, bool, error) {
	return loader(ctx, sessionID)
}

type generatorFunc func(
	context.Context,
	string,
	evaluation.Observer,
) (evaluation.Result, error)

func (generator generatorFunc) Generate(
	ctx context.Context,
	sessionID string,
	observer evaluation.Observer,
) (evaluation.Result, error) {
	return generator(ctx, sessionID, observer)
}

type deleterFunc func(context.Context, string, string) (bool, error)

func (deleter deleterFunc) DeleteReport(
	ctx context.Context,
	sessionID string,
	reportID string,
) (bool, error) {
	return deleter(ctx, sessionID, reportID)
}

func TestReportMainFlowBrowsesEvidenceAndStartsNextPractice(t *testing.T) {
	document := reportFixture()
	model := newLoadedModel(t, document, 160, 48, false, false, nil)

	if model.ActiveFocus() != focusScorecard {
		t.Fatalf("initial focus=%q", model.ActiveFocus())
	}
	model.HandleKey("down")
	model.HandleKey("e")
	if model.ActiveFocus() != focusEvidenceDetail {
		t.Fatalf("evidence focus=%q", model.ActiveFocus())
	}
	action := model.HandleKey("enter")
	if action.Destination != DestinationEvidence ||
		action.EvidenceID != "answer-q1" || action.QuestionID != "Q1" {
		t.Fatalf("evidence action=%#v", action)
	}
	model.HandleKey("esc")
	if model.ActiveFocus() != focusScorecard {
		t.Fatalf("restored focus=%q", model.ActiveFocus())
	}

	model.HandleKey("tab")
	model.HandleKey("tab")
	model.HandleKey("tab")
	if model.ActiveFocus() != focusPractice {
		t.Fatalf("practice focus=%q", model.ActiveFocus())
	}
	model.HandleKey("down")
	action = model.HandleKey("n")
	if action.Intent != IntentStartPractice ||
		action.Destination != DestinationScenario || action.Practice == nil {
		t.Fatalf("practice action=%#v", action)
	}
	if action.Practice.Topic != "系统设计取舍" ||
		action.Practice.Mode != contracts.ScenarioStandard ||
		action.Practice.DurationMinutes != 20 ||
		action.Practice.ReportID != document.ID {
		t.Fatalf("practice seed=%#v", action.Practice)
	}
}

func TestCompletedSQLiteSessionOpensReportStartsPracticeAndDeletesDerivedQueue(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(ctx, db.Config{
		DataDir: filepath.Join(t.TempDir(), "data"),
	}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})

	document := reportFixture()
	now := document.Summary.StartedAt
	profile := contracts.CandidateProfile{
		TargetRole: "Backend Engineer",
		Facts: []contracts.ProfileFact{{
			ID: "fact-17", Field: "project", Value: "Built a cache service",
			SourceSpan: contracts.SourceSpan{Start: 0, End: 21, Text: "Built a cache service"},
		}},
		Inferences: []contracts.ProfileInference{},
		Projects:   []string{"Cache service"}, Skills: []string{"Go", "Redis"},
	}
	if err := store.SaveProfile(ctx, "profile-17", profile, &now); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	scenario := contracts.Scenario{
		Template: "project_deep_dive", Mode: contracts.ScenarioStrict,
		TimeBudgetSeconds: 1800, PromptVersion: "scenario-v1",
		Questions: []contracts.ScenarioQuestion{{
			ID: "Q1", Prompt: "Explain the cache consistency design.",
			Intent: "Assess technical trade-offs", EstimatedSeconds: 300,
			Rubric:      []string{"Names a failure mode"},
			EvidenceIDs: []contracts.EvidenceID{"fact-17"}, MaxFollowUps: 2,
			EndCondition: "A verifiable trade-off is explained",
		}},
	}
	if err := store.SaveScenario(ctx, "scenario-17", "profile-17", scenario, now); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID: "session-17", ScenarioID: "scenario-17", Status: db.SessionCompleted,
		StartedAt: now, UpdatedAt: document.Summary.CompletedAt,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	service := corereport.NewService(store, corereport.Options{
		Now: func() time.Time { return document.GeneratedAt },
	})
	if err := service.Save(ctx, document); err != nil {
		t.Fatalf("Save report: %v", err)
	}

	model, err := New(Options{
		SessionID: "session-17", ReportID: "report-17",
		Reports: service, Deleter: store, Width: 120, Height: 36,
		Theme: screenTheme(t, false, false),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	model.Load(ctx, nil)
	action := model.HandleKey("n")
	if action.Destination != DestinationScenario || action.Practice == nil ||
		action.Practice.Topic != document.PracticePlan[0].Topic {
		t.Fatalf("one-click next practice=%#v", action)
	}
	home, err := store.LoadTrainingHome(ctx, 5)
	if err != nil || len(home.PracticeQueue) != len(document.PracticePlan) {
		t.Fatalf("practice queue=%#v err=%v", home.PracticeQueue, err)
	}
	model.HandleKey("d")
	if action := model.HandleKey("y"); action.Intent != IntentDeleteReport {
		t.Fatalf("delete action=%#v", action)
	}
	model.Delete(ctx, nil)
	if model.document() != nil {
		t.Fatal("report remained visible after deletion")
	}
	home, err = store.LoadTrainingHome(ctx, 5)
	if err != nil || len(home.PracticeQueue) != 0 || home.Recent[0].ReportID != "" {
		t.Fatalf("home after delete=%#v err=%v", home, err)
	}
}

func TestReportRendersNormalAtAllBreakpointsWithoutHeroScore(t *testing.T) {
	document := reportFixture()
	tests := []struct {
		name         string
		width        int
		height       int
		ascii        bool
		reduceMotion bool
	}{
		{name: "wide", width: 160, height: 48},
		{name: "split", width: 120, height: 36},
		{name: "narrow-ascii-reduced", width: 80, height: 24, ascii: true, reduceMotion: true},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			model := newLoadedModel(
				t, document, test.width, test.height, test.ascii, test.reduceMotion, nil,
			)
			rendered, err := model.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertGeometry(t, rendered, test.width, test.height)
			for _, forbidden := range []string{"总分", "TOTAL SCORE"} {
				if strings.Contains(strings.ToUpper(rendered), forbidden) {
					t.Fatalf("rendered forbidden hero %q", forbidden)
				}
			}
			for _, expected := range []string{"SESSION FACTS", "回答结构", "evidence"} {
				if !strings.Contains(rendered, expected) {
					t.Errorf("missing %q in %s render", expected, test.name)
				}
			}
		})
	}
}

func TestReportLoadingGenerationEmptyAndFailureStates(t *testing.T) {
	current := screenTheme(t, false, true)
	document := reportFixture()

	t.Run("pending and staged generation", func(t *testing.T) {
		generator := generatorFunc(func(
			_ context.Context,
			sessionID string,
			observer evaluation.Observer,
		) (evaluation.Result, error) {
			if sessionID != "session-17" {
				t.Fatalf("sessionID=%q", sessionID)
			}
			for _, progress := range []evaluation.Progress{
				{Stage: "scoring_evidence", Message: "正在校验评分证据"},
				{Stage: "planning_next_run", Message: "正在规划下一轮训练"},
			} {
				observer(async.NewStreaming(&progress))
			}
			return evaluation.Result{Report: document}, nil
		})
		model, err := New(Options{
			SessionID: "session-17",
			Reports: loaderFunc(func(context.Context, string) (corereport.Document, bool, error) {
				return corereport.Document{}, false, nil
			}),
			Evaluator: generator,
			Width:     80, Height: 24, Theme: current,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		pending, err := model.Render()
		if err != nil || !strings.Contains(pending, "正在读取会话事实与报告证据") {
			t.Fatalf("pending render err=%v output=%q", err, pending)
		}
		var phases []async.Phase
		var stages []string
		model.Load(context.Background(), func(state async.State[Data]) {
			phases = append(phases, state.Phase)
			if state.Value != nil && state.Value.Stage != "" {
				stages = append(stages, state.Value.Stage)
			}
		})
		wantPhases := []async.Phase{async.Pending, async.Streaming, async.Streaming, async.Succeeded}
		if strings.Join(phasesToStrings(phases), ",") != strings.Join(phasesToStrings(wantPhases), ",") {
			t.Fatalf("phases=%v want=%v", phases, wantPhases)
		}
		if len(stages) != 2 || stages[1] != "正在规划下一轮训练" {
			t.Fatalf("stages=%v", stages)
		}
	})

	t.Run("empty", func(t *testing.T) {
		model, err := New(Options{Width: 80, Height: 24, Theme: current})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		model.Load(context.Background(), nil)
		rendered, err := model.Render()
		if err != nil || !strings.Contains(rendered, "还没有可用报告") ||
			!strings.Contains(rendered, "[t] 开始训练") {
			t.Fatalf("empty err=%v output=%q", err, rendered)
		}
	})

	t.Run("read failure", func(t *testing.T) {
		model, err := New(Options{
			SessionID: "session-17",
			Reports: loaderFunc(func(context.Context, string) (corereport.Document, bool, error) {
				return corereport.Document{}, false, errors.New("database offline")
			}),
			Width: 120, Height: 36, Theme: current,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		model.Load(context.Background(), nil)
		if model.State().Phase != async.Failed ||
			!domainerr.IsCode(model.State().Err, domainerr.CodePersistenceFailed) {
			t.Fatalf("state=%#v", model.State())
		}
		rendered, err := model.Render()
		if err != nil || !strings.Contains(rendered, "无法读取或更新报告") ||
			strings.Contains(rendered, "database offline") {
			t.Fatalf("failure err=%v output=%q", err, rendered)
		}
	})

	t.Run("generation failure", func(t *testing.T) {
		failure := domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"generate report",
			"评估 Provider 暂时不可用。",
			"检查模型设置后重试。",
			true,
		)
		model, err := New(Options{
			SessionID: "session-17",
			Reports: loaderFunc(func(context.Context, string) (corereport.Document, bool, error) {
				return corereport.Document{}, false, nil
			}),
			Evaluator: generatorFunc(func(context.Context, string, evaluation.Observer) (evaluation.Result, error) {
				return evaluation.Result{}, failure
			}),
			Width: 80, Height: 24, Theme: current,
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		model.Load(context.Background(), nil)
		if model.State().Phase != async.Failed || model.State().Err != failure {
			t.Fatalf("state=%#v", model.State())
		}
	})
}

func TestReportEvidenceUnavailableIsExplicitAndRestoresFocus(t *testing.T) {
	document := reportFixture()
	document.Scorecard[0].Status = corereport.StatusInsufficient
	document.Scorecard[0].Score = nil
	document.Scorecard[0].EvidenceIDs = []contracts.EvidenceID{}
	model := newLoadedModel(t, document, 80, 24, true, true, nil)

	model.HandleKey("e")
	rendered, err := model.Render()
	if err != nil || !strings.Contains(rendered, "evidence unavailable") {
		t.Fatalf("missing evidence err=%v output=%q", err, rendered)
	}
	model.HandleKey("esc")
	if model.ActiveFocus() != focusScorecard {
		t.Fatalf("restored focus=%q", model.ActiveFocus())
	}
}

func TestReportDeletionRequiresConfirmationAndHandlesFailure(t *testing.T) {
	document := reportFixture()
	deleteCalls := 0
	deleter := deleterFunc(func(
		_ context.Context,
		sessionID string,
		reportID string,
	) (bool, error) {
		deleteCalls++
		if sessionID != "session-17" || reportID != "report-17" {
			t.Fatalf("delete target=%s/%s", sessionID, reportID)
		}
		return false, errors.New("disk busy")
	})
	model := newLoadedModel(t, document, 120, 36, false, false, deleter)

	model.Delete(context.Background(), nil)
	if deleteCalls != 0 || model.operationErr == nil ||
		model.operationErr.Code != domainerr.CodePolicyDenied {
		t.Fatalf("direct delete calls=%d failure=%#v", deleteCalls, model.operationErr)
	}
	model.operationErr = nil
	if action := model.HandleKey("y"); action.Intent != IntentNone {
		t.Fatalf("unconfirmed action=%#v", action)
	}
	model.HandleKey("d")
	if model.ActiveFocus() != focusDelete {
		t.Fatalf("delete focus=%q", model.ActiveFocus())
	}
	model.HandleKey("enter")
	if model.ActiveFocus() != focusScorecard || deleteCalls != 0 {
		t.Fatalf("default cancel focus=%q calls=%d", model.ActiveFocus(), deleteCalls)
	}
	model.HandleKey("d")
	action := model.HandleKey("y")
	if action.Intent != IntentDeleteReport || action.ReportID != document.ID {
		t.Fatalf("confirmed action=%#v", action)
	}
	model.Delete(context.Background(), nil)
	if deleteCalls != 1 || model.document() == nil {
		t.Fatalf("failed delete calls=%d document=%#v", deleteCalls, model.document())
	}
	rendered, err := model.Render()
	if err != nil || !strings.Contains(rendered, "无法读取或更新报告") ||
		strings.Contains(rendered, "disk busy") {
		t.Fatalf("delete failure err=%v output=%q", err, rendered)
	}
}

func TestReportConfirmedDeletionBecomesActionableEmptyState(t *testing.T) {
	document := reportFixture()
	model := newLoadedModel(t, document, 80, 24, false, false, deleterFunc(
		func(context.Context, string, string) (bool, error) { return true, nil },
	))
	model.HandleKey("d")
	if action := model.HandleKey("y"); action.Intent != IntentDeleteReport {
		t.Fatalf("confirmed action=%#v", action)
	}
	var phases []async.Phase
	model.Delete(context.Background(), func(state async.State[Data]) {
		phases = append(phases, state.Phase)
	})
	if len(phases) != 2 || phases[0] != async.Streaming || phases[1] != async.Succeeded {
		t.Fatalf("delete phases=%v", phases)
	}
	rendered, err := model.Render()
	if err != nil || !strings.Contains(rendered, "还没有可用报告") {
		t.Fatalf("deleted render err=%v output=%q", err, rendered)
	}
}

func TestReportKeepImproveAndPracticeNextAreCappedAtThree(t *testing.T) {
	groups := groupReview(reportFixture())
	if len(groups.Keep) > 3 || len(groups.Improve) > 3 || len(groups.PracticeNext) > 3 {
		t.Fatalf("groups=%#v", groups)
	}
	if len(groups.Keep) != 3 || len(groups.Improve) != 3 || len(groups.PracticeNext) != 3 {
		t.Fatalf("fixture did not exercise cap: %#v", groups)
	}
}

func newLoadedModel(
	t *testing.T,
	document corereport.Document,
	width int,
	height int,
	ascii bool,
	reduceMotion bool,
	deleter Deleter,
) *Model {
	t.Helper()
	model, err := New(Options{
		SessionID: document.Summary.SessionID,
		ReportID:  document.ID,
		Reports: loaderFunc(func(_ context.Context, sessionID string) (corereport.Document, bool, error) {
			if sessionID != document.Summary.SessionID {
				t.Fatalf("sessionID=%q", sessionID)
			}
			return document, true, nil
		}),
		Deleter: deleter,
		Width:   width, Height: height,
		Theme: screenTheme(t, ascii, reduceMotion),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	model.Load(context.Background(), nil)
	return model
}

func screenTheme(t *testing.T, ascii, reduceMotion bool) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode: theme.Auto, ColorMode: theme.NoColor,
		UseASCII: ascii, ReduceMotion: reduceMotion,
	})
	if err != nil {
		t.Fatalf("Resolve theme: %v", err)
	}
	return current
}

func assertGeometry(t *testing.T, rendered string, width, height int) {
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

func phasesToStrings(values []async.Phase) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, string(value))
	}
	return result
}

func reportFixture() corereport.Document {
	base := time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)
	evidence := []corereport.EvidenceLink{
		{ID: "answer-q1", Kind: "session_user", QuestionID: "Q1", Label: "Q1 submitted answer", OccurredAt: base.Add(5 * time.Minute)},
		{ID: "coach-q1", Kind: "sidebar_event", QuestionID: "Q1", Label: "Q1 Coach L2", OccurredAt: base.Add(7 * time.Minute)},
		{ID: "code-q1", Kind: "code_submission", QuestionID: "Q1", Label: "Q1 code run", OccurredAt: base.Add(9 * time.Minute)},
	}
	scores := []int{5, 4, 4, 3, 3, 2, 2, 1}
	scorecard := make([]corereport.ScorecardItem, 0, len(scores))
	for index, dimension := range corereport.FixedDimensions() {
		score := scores[index]
		scorecard = append(scorecard, corereport.ScorecardItem{
			Dimension: dimension, Status: corereport.StatusEvidenceBacked,
			Score: &score, EvidenceIDs: []contracts.EvidenceID{"answer-q1"},
			Confidence: 0.8, NextAction: "补充一个可验证的取舍和结果。",
		})
	}
	return corereport.Document{
		ID: "report-17", SchemaVersion: corereport.SchemaVersion,
		GeneratedAt: base.Add(31 * time.Minute),
		Summary: corereport.SessionSummary{
			SessionID: "session-17", ScenarioID: "scenario-17",
			Template: "project_deep_dive", Mode: contracts.ScenarioStrict,
			StartedAt: base, CompletedAt: base.Add(30 * time.Minute),
			DurationSeconds: 1800, QuestionCount: 1,
			CoachPromptCount: 1, CodeRunCount: 1,
		},
		Evidence: evidence,
		QuestionReview: []corereport.QuestionReview{{
			QuestionID: "Q1", Prompt: "说明一次缓存一致性设计。",
			Summary: corereport.Insight{
				Text: "给出了失效策略和实际结果。", Status: corereport.StatusEvidenceBacked,
				EvidenceIDs: []contracts.EvidenceID{"answer-q1"}, Confidence: 0.8,
			},
			NextAction: corereport.Insight{
				Text: "补充故障恢复路径。", Status: corereport.StatusEvidenceBacked,
				EvidenceIDs: []contracts.EvidenceID{"answer-q1"}, Confidence: 0.8,
			},
		}},
		Scorecard: scorecard,
		LearningMap: []corereport.LearningGap{{
			Topic: "Redis consistency", AskCount: 1,
			MaxHelpLevel: contracts.HelpL2, ReviewCount: 1,
			QuestionIDs: []string{"Q1"}, EvidenceIDs: []contracts.EvidenceID{"coach-q1"},
			RelatedSkills: []string{"Redis"}, RelatedJDNeeds: []string{},
		}},
		Transfer: []corereport.TransferEvidence{{
			SidebarEventID: "coach-q1", QuestionID: "Q1",
			Status:             corereport.TransferEvidenceObserved,
			SubsequentEvidence: []contracts.EvidenceID{"code-q1"},
			Summary:            "提示后五分钟内记录了同题代码事件。",
		}},
		CrossInsights: []corereport.Insight{{
			Text:        "回答、代码与 Coach 记录共同指向一致性取舍。",
			Status:      corereport.StatusEvidenceBacked,
			EvidenceIDs: []contracts.EvidenceID{"answer-q1", "code-q1", "coach-q1"},
			Confidence:  0.8,
		}},
		PracticePlan: []corereport.PracticeItem{
			{Topic: "故障恢复", Mode: contracts.ScenarioStrict, DurationMinutes: 15, CompletionCriteria: "说清失败模式和恢复路径。", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"answer-q1"}},
			{Topic: "系统设计取舍", Mode: contracts.ScenarioStandard, DurationMinutes: 20, CompletionCriteria: "比较两种方案并给出验证指标。", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"answer-q1"}},
			{Topic: "独立作答", Mode: contracts.ScenarioStrict, DurationMinutes: 15, CompletionCriteria: "不新增提示完成一题。", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"coach-q1"}},
			{Topic: "时间管理", Mode: contracts.ScenarioCoach, DurationMinutes: 10, CompletionCriteria: "保留三十秒总结。", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"answer-q1"}},
		},
	}
}
