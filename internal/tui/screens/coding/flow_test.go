package coding

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreevaluation "github.com/interviewcraft/interviewcraft/internal/core/evaluation"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestCompleteCodingSessionProducesReportCodeEvidenceAndStrictCoachBlocksSolution(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 10, 0, 0, 0, time.UTC)
	store, err := db.Open(ctx, db.Config{
		DataDir: filepath.Join(t.TempDir(), "data"), DatabaseName: "coding-ui.db",
	}, nil)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	seedFullCodingSession(t, store, base)

	runner := coreRunnerFunc(func(
		_ context.Context,
		request corecoding.ExecutionRequest,
	) (corecoding.ExecutionResult, error) {
		if request.QuestionID != "pair_sum" || request.Language != corecoding.LanguagePython {
			t.Fatalf("runner request=%#v", request)
		}
		return corecoding.ExecutionResult{
			Result: corecoding.SafeResult{
				Version: corecoding.ResultVersion, Status: corecoding.RunFailed,
				PublicTests: []corecoding.PublicTestResult{
					{Name: "example_1", Status: corecoding.TestPassed},
					{Name: "example_2", Status: corecoding.TestFailed},
				},
				HiddenTests: corecoding.HiddenTestSummary{Passed: 2, Failed: 1},
				ErrorKind:   corecoding.ErrorNone,
			},
			Runtime: corecoding.RuntimeStats{
				DurationMilliseconds: 81, PeakMemoryKB: 18 * 1024,
			},
		}, nil
	})
	codingService, err := corecoding.NewService(store, corecoding.Options{
		Runner: runner, Now: func() time.Time { return base.Add(12 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("coding.NewService: %v", err)
	}

	forbiddenSolution := "完整实现：def pair_sum(nums, target): return [0, 1]"
	coachService := corecoach.NewService(
		store,
		coachProviderFunc(func(
			_ context.Context,
			input corecoach.Input,
		) (contracts.CoachResponse, error) {
			if len(input.CodeRuns) != 1 || input.CodeRuns[0].QuestionID != "pair_sum" {
				t.Fatalf("Coach CodeRuns=%#v", input.CodeRuns)
			}
			if input.Mode != contracts.ScenarioStrict || input.AllowedMaxLevel != contracts.HelpL1 {
				t.Fatalf("Coach policy input=%#v", input)
			}
			return contracts.CoachResponse{
				Intent: input.Intent, HelpLevel: contracts.HelpL1,
				KnowledgeTags:     []string{"pair sum"},
				RecommendedAction: forbiddenSolution,
			}, nil
		}),
		corecoach.Options{Now: func() time.Time { return base.Add(13 * time.Minute) }},
	)
	current, err := theme.Resolve(theme.Options{Mode: theme.Auto, ColorMode: theme.NoColor})
	if err != nil {
		t.Fatalf("theme.Resolve: %v", err)
	}
	model, err := New(Options{
		SessionID: "session-coding-ui", QuestionID: "pair_sum",
		Service: codingService, Coach: coachService,
		Now:                func() time.Time { return base.Add(12 * time.Minute) },
		NextRunID:          func() string { return "screen-run" },
		NextCoachRequestID: func() string { return "strict-explain" },
		Width:              120, Height: 36, Theme: current,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := model.Load(ctx, nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	executedSource := "def pair_sum(nums, target):\n    # intentionally incomplete for evidence\n    return [0, 1]\n"
	if err := model.UpdateSource(executedSource); err != nil {
		t.Fatalf("UpdateSource: %v", err)
	}
	if err := model.Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	submissions, err := store.ListCodeSubmissions(ctx, "session-coding-ui")
	if err != nil {
		t.Fatalf("ListCodeSubmissions: %v", err)
	}
	if len(submissions) != 1 || submissions[0].Source != executedSource {
		t.Fatalf("submissions=%#v", submissions)
	}
	submissionID := contracts.EvidenceID(submissions[0].ID)

	if err := model.ExplainFailure(ctx, nil); !domainerr.IsCode(err, domainerr.CodeInvalidModelOutput) {
		t.Fatalf("strict ExplainFailure err=%v", err)
	}
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render after strict rejection: %v", err)
	}
	if strings.Contains(rendered, forbiddenSolution) || strings.Contains(rendered, "完整实现：") {
		t.Fatalf("strict complete solution leaked:\n%s", rendered)
	}
	coachEvents, err := store.ListSidebarEvents(ctx, "session-coding-ui")
	if err != nil || len(coachEvents) != 0 {
		t.Fatalf("rejected Coach response persisted events=%#v err=%v", coachEvents, err)
	}

	answerID := contracts.EvidenceID("answer-coding-ui")
	if err := store.AppendSessionEvent(ctx, db.SessionEvent{
		EventID: string(answerID), SessionID: "session-coding-ui",
		Speaker: db.SpeakerUser, QuestionID: "pair_sum",
		Content:      "I would use a complement-to-index map and update it after lookup.",
		OccurredAt:   base.Add(8 * time.Minute),
		EvidenceRefs: []contracts.EvidenceID{"fact-coding-ui"},
	}); err != nil {
		t.Fatalf("AppendSessionEvent: %v", err)
	}
	updated, err := store.UpdateSessionStatus(
		ctx, "session-coding-ui", db.SessionEvaluationPending, base.Add(20*time.Minute),
	)
	if err != nil || !updated {
		t.Fatalf("UpdateSessionStatus updated=%v err=%v", updated, err)
	}

	evaluator := coreevaluation.NewService(
		store,
		evaluationProviderFunc(func(
			_ context.Context,
			input coreevaluation.Input,
		) (coreevaluation.Draft, error) {
			if input.SessionID != "session-coding-ui" || len(input.CodeRuns) != 1 {
				t.Fatalf("evaluation input session=%q code runs=%#v", input.SessionID, input.CodeRuns)
			}
			if input.CodeRuns[0].SubmissionID != submissionID || input.CodeRuns[0].Source != executedSource {
				t.Fatalf("evaluation code evidence=%#v", input.CodeRuns[0])
			}
			return evaluationDraftWithCode(answerID, submissionID), nil
		}),
		coreevaluation.Options{Now: func() time.Time { return base.Add(21 * time.Minute) }},
	)
	result, err := evaluator.Generate(ctx, "session-coding-ui", nil)
	if err != nil {
		t.Fatalf("evaluation.Generate: %v", err)
	}
	if result.Report.Summary.CodeRunCount != 1 || result.Report.Summary.QuestionCount != 1 {
		t.Fatalf("report summary=%#v", result.Report.Summary)
	}
	var codeFinding *corereport.ScorecardItem
	for index := range result.Report.Scorecard {
		if result.Report.Scorecard[index].Dimension == contracts.DimensionCodeQuality {
			codeFinding = &result.Report.Scorecard[index]
			break
		}
	}
	if codeFinding == nil || codeFinding.Status != corereport.StatusEvidenceBacked ||
		len(codeFinding.EvidenceIDs) != 1 || codeFinding.EvidenceIDs[0] != submissionID {
		t.Fatalf("code finding=%#v", codeFinding)
	}
	persisted, found, err := corereport.NewService(store, corereport.Options{}).Get(ctx, "session-coding-ui")
	if err != nil || !found || persisted.Summary.CodeRunCount != 1 {
		t.Fatalf("persisted report found=%v summary=%#v err=%v", found, persisted.Summary, err)
	}
	session, found, err := store.GetSession(ctx, "session-coding-ui")
	if err != nil || !found || session.Status != db.SessionCompleted {
		t.Fatalf("completed session=%#v found=%v err=%v", session, found, err)
	}
}

func seedFullCodingSession(t *testing.T, store *db.Store, now time.Time) {
	t.Helper()
	ctx := context.Background()
	confirmedAt := now.Add(-time.Hour)
	source := "Built a production API and used hash maps for indexing."
	profile := coreprofile.Aggregate{
		ID: "profile-coding-ui",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID: "fact-coding-ui", Field: "project", Value: "production API",
				SourceSpan: contracts.SourceSpan{Start: 8, End: 22, Text: "production API"},
			}},
			Inferences: []contracts.ProfileInference{},
			Projects:   []string{"production API"}, Skills: []string{"hash maps"},
		},
		Metadata: coreprofile.Metadata{
			Source:             coreprofile.Source{Kind: coreprofile.SourcePaste, Name: "coding-ui", Text: source},
			LockedFactIDs:      []contracts.EvidenceID{"fact-coding-ui"},
			LockedInferenceIDs: []string{}, CreatedAt: confirmedAt, UpdatedAt: confirmedAt,
		},
		ConfirmedAt: &confirmedAt,
	}
	if err := store.SaveProfileAggregate(ctx, profile); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	scenario := contracts.Scenario{
		Template: "algorithm_coding", Mode: contracts.ScenarioStrict,
		TimeBudgetSeconds: 1200, PromptVersion: "scenario-v1",
		Questions: []contracts.ScenarioQuestion{{
			ID: "pair_sum", Prompt: "Explain and solve Pair Sum Indices.",
			Intent: "Assess clarification and code quality", EstimatedSeconds: 600,
			Rubric:       []string{"clarifies constraints", "explains complement invariant"},
			EvidenceIDs:  []contracts.EvidenceID{"fact-coding-ui"},
			MaxFollowUps: 1, EndCondition: "explains and runs a verifiable approach",
		}},
	}
	if err := store.SaveScenario(ctx, "scenario-coding-ui", profile.ID, scenario, confirmedAt); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID: "session-coding-ui", ScenarioID: "scenario-coding-ui",
		Status: db.SessionActive, StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
}

func evaluationDraftWithCode(
	answerID contracts.EvidenceID,
	codeID contracts.EvidenceID,
) coreevaluation.Draft {
	findings := make([]contracts.EvaluationFinding, 0, len(corereport.FixedDimensions()))
	for _, dimension := range corereport.FixedDimensions() {
		score := 4
		evidence := answerID
		if dimension == contracts.DimensionCodeQuality {
			evidence = codeID
		}
		findings = append(findings, contracts.EvaluationFinding{
			Dimension: dimension, Score: &score,
			EvidenceIDs: []contracts.EvidenceID{evidence}, Confidence: 0.85,
			NextAction: "继续用可运行证据说明边界条件与取舍。",
		})
	}
	return coreevaluation.Draft{
		QuestionReviews: []coreevaluation.DraftQuestionReview{{
			QuestionID: "pair_sum",
			Summary: coreevaluation.DraftInsight{
				Text:        "解释了补数映射并留下实际运行证据。",
				EvidenceIDs: []contracts.EvidenceID{answerID, codeID}, Confidence: 0.9,
			},
			NextAction: coreevaluation.DraftInsight{
				Text:        "补充失败公开样例的边界分析。",
				EvidenceIDs: []contracts.EvidenceID{codeID}, Confidence: 0.85,
			},
		}},
		Findings: findings,
		CrossInsights: []coreevaluation.DraftInsight{{
			Text:        "口头不变量与执行快照相互印证。",
			EvidenceIDs: []contracts.EvidenceID{answerID, codeID}, Confidence: 0.85,
		}},
		PracticePlan: []coreevaluation.DraftPracticeItem{
			{Topic: "边界条件", Mode: contracts.ScenarioStrict, DurationMinutes: 10, CompletionCriteria: "解释并通过两个公开样例。", EvidenceIDs: []contracts.EvidenceID{codeID}},
			{Topic: "复杂度", Mode: contracts.ScenarioStandard, DurationMinutes: 10, CompletionCriteria: "说明 O(n) 时间和空间。", EvidenceIDs: []contracts.EvidenceID{answerID}},
			{Topic: "失败诊断", Mode: contracts.ScenarioStrict, DurationMinutes: 15, CompletionCriteria: "独立定位一个运行失败。", EvidenceIDs: []contracts.EvidenceID{codeID}},
		},
	}
}

type coreRunnerFunc func(
	context.Context,
	corecoding.ExecutionRequest,
) (corecoding.ExecutionResult, error)

func (run coreRunnerFunc) Run(
	ctx context.Context,
	request corecoding.ExecutionRequest,
) (corecoding.ExecutionResult, error) {
	return run(ctx, request)
}

type coachProviderFunc func(
	context.Context,
	corecoach.Input,
) (contracts.CoachResponse, error)

func (provider coachProviderFunc) Respond(
	ctx context.Context,
	input corecoach.Input,
) (contracts.CoachResponse, error) {
	return provider(ctx, input)
}

type evaluationProviderFunc func(
	context.Context,
	coreevaluation.Input,
) (coreevaluation.Draft, error)

func (provider evaluationProviderFunc) Evaluate(
	ctx context.Context,
	input coreevaluation.Input,
) (coreevaluation.Draft, error) {
	return provider(ctx, input)
}
