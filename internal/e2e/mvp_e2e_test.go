package e2e_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/adapters/resume"
	"github.com/interviewcraft/interviewcraft/internal/cli"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	coreevaluation "github.com/interviewcraft/interviewcraft/internal/core/evaluation"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	corescenario "github.com/interviewcraft/interviewcraft/internal/core/scenario"
	"github.com/interviewcraft/interviewcraft/internal/db"
	reportscreen "github.com/interviewcraft/interviewcraft/internal/tui/screens/report"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestLiteMVPJourneyFromFreshInitThroughTransfer(t *testing.T) {
	ctx := context.Background()
	base := time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)
	provider := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(provider.Close)

	sourceDir := filepath.Join(t.TempDir(), "source")
	t.Setenv("INTERVIEWCRAFT_DATA_DIR", sourceDir)
	t.Setenv("INTERVIEWCRAFT_LLM_PROVIDER", "ollama")
	t.Setenv("INTERVIEWCRAFT_LLM_ENDPOINT", provider.URL)
	t.Setenv("INTERVIEWCRAFT_LLM_MODEL", "e2e-model")
	t.Setenv("INTERVIEWCRAFT_LLM_API_KEY_ENV", "INTERVIEWCRAFT_E2E_SECRET")
	t.Setenv("INTERVIEWCRAFT_E2E_SECRET", "must-not-enter-transfer-package")
	t.Setenv("RUNNER_MODE", "disabled")
	t.Setenv("COLUMNS", "80")
	t.Setenv("LINES", "24")

	runCLI(t, []string{"init"})
	runCLI(t, []string{"doctor"})
	home := runCLI(t, []string{"run", "--once", "--ascii", "--reduce-motion", "--no-color"})
	assertFrame(t, home, 80, 24)

	store, err := db.Open(ctx, db.Config{DataDir: sourceDir}, nil)
	if err != nil {
		t.Fatalf("db.Open source: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = store.Close()
		}
	})

	resumeText := "Backend engineer. Built payment API in Go. Improved latency by 30%. Uses Redis."
	var resumePhases []async.Phase
	source, err := (resume.Extractor{}).Extract(ctx, resume.Input{
		Kind: coreprofile.SourcePaste,
		Text: resumeText,
	}, func(state async.State[resume.Progress]) {
		resumePhases = append(resumePhases, state.Phase)
	})
	if err != nil {
		t.Fatalf("resume.Extract: %v", err)
	}
	assertTerminalLifecycle(t, "resume", resumePhases)

	profileService := coreprofile.NewService(store, profileStructurerFunc(func(
		_ context.Context,
		input coreprofile.Source,
		targetRole string,
	) (contracts.CandidateProfile, error) {
		return contracts.CandidateProfile{
			TargetRole: targetRole,
			Facts: []contracts.ProfileFact{
				sourceFact(input.Text, "fact-payment", "project", "payment API"),
				sourceFact(input.Text, "fact-latency", "impact", "Improved latency by 30%"),
			},
			Inferences: []contracts.ProfileInference{},
			Projects:   []string{"payment API"},
			Skills:     []string{"Go", "Redis"},
		}, nil
	}), func() time.Time { return base })
	var profilePhases []async.Phase
	profile, err := profileService.Create(
		ctx,
		"profile-e2e",
		source,
		"Backend Engineer",
		func(state async.State[coreprofile.Progress]) {
			profilePhases = append(profilePhases, state.Phase)
		},
	)
	if err != nil {
		t.Fatalf("profile.Create: %v", err)
	}
	assertLifecycle(t, "profile", profilePhases)
	profile, err = profileService.SetLocked(ctx, profile.ID, "fact-payment", true)
	if err != nil {
		t.Fatalf("profile.SetLocked: %v", err)
	}
	profile, err = profileService.Confirm(ctx, profile.ID)
	if err != nil || profile.ConfirmedAt == nil {
		t.Fatalf("profile.Confirm profile=%#v err=%v", profile, err)
	}

	scenarioService, err := corescenario.NewService(store, scenarioGeneratorFunc(func(
		_ context.Context,
		input corescenario.GenerationInput,
	) (corescenario.GeneratedPlan, error) {
		return corescenario.GeneratedPlan{
			Scenario: contracts.Scenario{
				Template:          input.Template.ID,
				Mode:              input.Mode,
				TimeBudgetSeconds: input.TimeBudget,
				PromptVersion:     input.PromptVersion,
				Questions: []contracts.ScenarioQuestion{{
					ID:               "pair_sum",
					Prompt:           "Explain and implement Pair Sum Indices.",
					Intent:           "Assess clarification, reasoning, and executable code.",
					EstimatedSeconds: 600,
					Rubric:           []string{"Clarifies constraints", "Explains the complement invariant"},
					EvidenceIDs:      []contracts.EvidenceID{"fact-payment"},
					MaxFollowUps:     1,
					EndCondition:     "Explains and runs a verifiable linear-time approach.",
				}},
			},
			JDMappings: []corescenario.JDMapping{},
		}, nil
	}), func() time.Time { return base.Add(time.Minute) })
	if err != nil {
		t.Fatalf("scenario.NewService: %v", err)
	}
	var scenarioPhases []async.Phase
	plan, err := scenarioService.Generate(ctx, corescenario.Request{
		PlanID:            "scenario-e2e",
		Profile:           profile,
		TemplateID:        "algorithm_coding",
		Mode:              contracts.ScenarioStrict,
		TimeBudgetSeconds: 1200,
	}, nil, func(state async.State[corescenario.Progress]) {
		scenarioPhases = append(scenarioPhases, state.Phase)
	})
	if err != nil {
		t.Fatalf("scenario.Generate: %v", err)
	}
	assertLifecycle(t, "scenario", scenarioPhases)
	plan, err = scenarioService.Confirm(ctx, plan)
	if err != nil || !plan.Locked {
		t.Fatalf("scenario.Confirm plan=%#v err=%v", plan, err)
	}

	if err := store.CreateSession(ctx, db.Session{
		ID:         "session-e2e",
		ScenarioID: plan.ID,
		Status:     db.SessionActive,
		StartedAt:  base.Add(2 * time.Minute),
		UpdatedAt:  base.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	interviewer := coreinterview.NewService(store, interviewerProviderFunc(func(
		_ context.Context,
		input coreinterview.Input,
	) (contracts.InterviewerAction, error) {
		if len(input.SubmittedAnswers) != 1 || len(input.CodeRuns) != 1 {
			t.Fatalf("interviewer evidence answers=%d code=%d", len(input.SubmittedAnswers), len(input.CodeRuns))
		}
		answerID := input.SubmittedAnswers[0].EventID
		return contracts.InterviewerAction{
			Action:       contracts.ActionFinishSession,
			QuestionID:   "pair_sum",
			Message:      "The planned interview is complete.",
			EvidenceIDs:  []contracts.EvidenceID{answerID},
			SessionState: contracts.SessionComplete,
		}, nil
	}), coreinterview.Options{Now: func() time.Time { return base.Add(8 * time.Minute) }})
	started, err := interviewer.Start(ctx, "session-e2e")
	if err != nil || started.Phase != coreinterview.PhaseAwaitingAnswer {
		t.Fatalf("interview.Start snapshot=%#v err=%v", started, err)
	}

	coachService := corecoach.NewService(store, coachProviderFunc(func(
		_ context.Context,
		input corecoach.Input,
	) (contracts.CoachResponse, error) {
		if input.Mode != contracts.ScenarioStrict || input.AllowedMaxLevel != contracts.HelpL1 {
			t.Fatalf("Coach policy input=%#v", input)
		}
		return contracts.CoachResponse{
			Intent:            input.Intent,
			HelpLevel:         contracts.HelpL1,
			KnowledgeTags:     []string{"complement invariant"},
			RecommendedAction: "State the lookup-before-insert invariant and one edge case.",
		}, nil
	}), corecoach.Options{Now: func() time.Time { return base.Add(3 * time.Minute) }})
	var coachPhases []async.Phase
	coachResult, err := coachService.Ask(ctx, corecoach.AskRequest{
		SessionID:      "session-e2e",
		QuestionID:     "pair_sum",
		RequestID:      "coach-e2e",
		Intent:         contracts.CoachGiveHint,
		RequestedLevel: contracts.HelpL1,
		UserRequest:    "Give me a conceptual nudge.",
	}, func(state async.State[corecoach.Progress]) {
		coachPhases = append(coachPhases, state.Phase)
	})
	if err != nil || coachResult.Event.ID == "" {
		t.Fatalf("coach.Ask result=%#v err=%v", coachResult, err)
	}
	assertLifecycle(t, "coach", coachPhases)

	codingService, err := corecoding.NewService(store, corecoding.Options{
		Runner: codingRunnerFunc(func(_ context.Context, request corecoding.ExecutionRequest) (corecoding.ExecutionResult, error) {
			if request.QuestionID != "pair_sum" || request.Language != corecoding.LanguagePython {
				t.Fatalf("runner request=%#v", request)
			}
			return corecoding.ExecutionResult{
				Result: corecoding.SafeResult{
					Version: corecoding.ResultVersion,
					Status:  corecoding.RunPassed,
					PublicTests: []corecoding.PublicTestResult{
						{Name: "example_1", Status: corecoding.TestPassed},
						{Name: "example_2", Status: corecoding.TestPassed},
					},
					HiddenTests: corecoding.HiddenTestSummary{Passed: 3, Failed: 0},
					ErrorKind:   corecoding.ErrorNone,
				},
				Runtime: corecoding.RuntimeStats{DurationMilliseconds: 37, PeakMemoryKB: 16 * 1024},
			}, nil
		}),
		Now: func() time.Time { return base.Add(5 * time.Minute) },
	})
	if err != nil {
		t.Fatalf("coding.NewService: %v", err)
	}
	workspace, err := codingService.Open(ctx, "session-e2e", "pair_sum")
	if err != nil || workspace.LatestRun != nil {
		t.Fatalf("coding.Open workspace=%#v err=%v", workspace, err)
	}
	sourceCode := "def pair_sum(nums, target):\n    seen = {}\n    for i, value in enumerate(nums):\n        if target - value in seen:\n            return [seen[target - value], i]\n        seen[value] = i\n"
	if _, err := codingService.SaveSource(ctx, "session-e2e", "pair_sum", corecoding.LanguagePython, sourceCode, nil); err != nil {
		t.Fatalf("coding.SaveSource: %v", err)
	}
	var codingPhases []async.Phase
	codeRun, err := codingService.Run(ctx, corecoding.RunRequest{
		SessionID:  "session-e2e",
		QuestionID: "pair_sum",
		Language:   corecoding.LanguagePython,
		RunID:      "run-e2e",
	}, func(state async.State[corecoding.Progress]) {
		codingPhases = append(codingPhases, state.Phase)
	})
	if err != nil || codeRun.Result.Status != corecoding.RunPassed {
		t.Fatalf("coding.Run run=%#v err=%v", codeRun, err)
	}
	assertLifecycle(t, "coding", codingPhases)

	var interviewPhases []async.Phase
	submitted, err := interviewer.Submit(ctx, coreinterview.SubmitRequest{
		SessionID:    "session-e2e",
		SubmissionID: "answer-e2e",
		Answer:       "I clarify uniqueness, then use a complement-to-index map with lookup before insert.",
	}, func(state async.State[coreinterview.Progress]) {
		interviewPhases = append(interviewPhases, state.Phase)
	})
	if err != nil || submitted.Snapshot.Phase != coreinterview.PhaseCompleted {
		t.Fatalf("interview.Submit result=%#v err=%v", submitted, err)
	}
	assertLifecycle(t, "interview", interviewPhases)

	answerID := contracts.EvidenceID("answer-e2e")
	codeID := contracts.EvidenceID(codeRun.SubmissionID)
	evaluator := coreevaluation.NewService(store, evaluationProviderFunc(func(
		_ context.Context,
		input coreevaluation.Input,
	) (coreevaluation.Draft, error) {
		if len(input.CodeRuns) != 1 || len(input.CoachEvents) != 1 {
			t.Fatalf("evaluation evidence code=%d coach=%d", len(input.CodeRuns), len(input.CoachEvents))
		}
		return completeEvaluationDraft(answerID, codeID), nil
	}), coreevaluation.Options{Now: func() time.Time { return base.Add(10 * time.Minute) }})
	var evaluationPhases []async.Phase
	evaluationResult, err := evaluator.Generate(ctx, "session-e2e", func(state async.State[coreevaluation.Progress]) {
		evaluationPhases = append(evaluationPhases, state.Phase)
	})
	if err != nil {
		t.Fatalf("evaluation.Generate: %v", err)
	}
	assertLifecycle(t, "evaluation", evaluationPhases)
	assertConclusionCoverage(t, evaluationResult.Report)

	resolvedTheme, err := theme.Resolve(theme.Options{
		Mode:         theme.Auto,
		ColorMode:    theme.NoColor,
		UseASCII:     true,
		ReduceMotion: true,
	})
	if err != nil {
		t.Fatalf("theme.Resolve: %v", err)
	}
	reportService := corereport.NewService(store, corereport.Options{})
	reportModel, err := reportscreen.New(reportscreen.Options{
		SessionID: "session-e2e",
		ReportID:  evaluationResult.Report.ID,
		Reports:   reportService,
		Width:     120,
		Height:    36,
		Theme:     resolvedTheme,
	})
	if err != nil {
		t.Fatalf("report.New: %v", err)
	}
	reportModel.Load(ctx, nil)
	action := reportModel.HandleKey("n")
	if action.Intent != reportscreen.IntentStartPractice || action.Practice == nil {
		t.Fatalf("report next practice action=%#v", action)
	}
	nextScenario := plan.Scenario
	nextScenario.Mode = action.Practice.Mode
	nextScenario.TimeBudgetSeconds = action.Practice.DurationMinutes * 60
	nextScenario.PromptVersion = "practice-from-" + evaluationResult.Report.ID
	if err := store.SaveScenario(ctx, "scenario-next", profile.ID, nextScenario, base.Add(11*time.Minute)); err != nil {
		t.Fatalf("SaveScenario next round: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID:         "session-next",
		ScenarioID: "scenario-next",
		Status:     db.SessionActive,
		StartedAt:  base.Add(12 * time.Minute),
		UpdatedAt:  base.Add(12 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession next round: %v", err)
	}

	if err := store.Close(); err != nil {
		t.Fatalf("close source: %v", err)
	}
	closed = true
	packagePath := filepath.Join(t.TempDir(), "mvp-transfer.json")
	runCLI(t, []string{"export", "--format", "package", "--output", packagePath})
	payload, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatalf("read transfer package: %v", err)
	}
	if bytes.Contains(payload, []byte("must-not-enter-transfer-package")) || bytes.Contains(payload, []byte(provider.URL)) {
		t.Fatal("transfer package leaked runtime configuration or secret")
	}

	targetDir := filepath.Join(t.TempDir(), "target")
	t.Setenv("INTERVIEWCRAFT_DATA_DIR", targetDir)
	runCLI(t, []string{"init"})
	runCLI(t, []string{"import", "--input", packagePath})
	restored, err := db.Open(ctx, db.Config{DataDir: targetDir}, nil)
	if err != nil {
		t.Fatalf("db.Open target: %v", err)
	}
	defer restored.Close()
	restoredReport, found, err := corereport.NewService(restored, corereport.Options{}).Get(ctx, "session-e2e")
	if err != nil || !found || restoredReport.ID != evaluationResult.Report.ID {
		t.Fatalf("restored report found=%v report=%#v err=%v", found, restoredReport, err)
	}
	restoredNext, found, err := restored.GetSession(ctx, "session-next")
	if err != nil || !found || restoredNext.Status != db.SessionActive {
		t.Fatalf("restored next session found=%v session=%#v err=%v", found, restoredNext, err)
	}
	restoredHome := runCLI(t, []string{"run", "--once", "--ascii", "--reduce-motion", "--no-color"})
	assertFrame(t, restoredHome, 80, 24)
}

func runCLI(t *testing.T, args []string) string {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if code := cli.Run(args, &stdout, &stderr); code != cli.ExitOK {
		t.Fatalf("cli.Run(%v) exit=%d stdout=%q stderr=%q", args, code, stdout.String(), stderr.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("cli.Run(%v) stderr=%q", args, stderr.String())
	}
	return stdout.String()
}

func assertFrame(t *testing.T, output string, width, height int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	if len(lines) != height {
		t.Fatalf("frame rows=%d want=%d", len(lines), height)
	}
	for index, line := range lines {
		if widthBytes := len([]rune(line)); widthBytes > width {
			t.Fatalf("frame row %d rune width=%d exceeds=%d", index, widthBytes, width)
		}
	}
}

func assertLifecycle(t *testing.T, name string, phases []async.Phase) {
	t.Helper()
	assertTerminalLifecycle(t, name, phases)
	if len(phases) < 3 || phases[0] != async.Pending || phases[len(phases)-1] != async.Succeeded {
		t.Fatalf("%s lifecycle=%#v", name, phases)
	}
	for _, phase := range phases {
		if phase == async.Streaming {
			return
		}
	}
	t.Fatalf("%s lifecycle lacks streaming: %#v", name, phases)
}

func assertTerminalLifecycle(t *testing.T, name string, phases []async.Phase) {
	t.Helper()
	if len(phases) < 2 || phases[0] != async.Pending || phases[len(phases)-1] != async.Succeeded {
		t.Fatalf("%s lifecycle=%#v", name, phases)
	}
}

func sourceFact(source string, id contracts.EvidenceID, field, text string) contracts.ProfileFact {
	start := strings.Index(source, text)
	return contracts.ProfileFact{
		ID:         id,
		Field:      field,
		Value:      text,
		SourceSpan: contracts.SourceSpan{Start: start, End: start + len(text), Text: text},
	}
}

func completeEvaluationDraft(answerID, codeID contracts.EvidenceID) coreevaluation.Draft {
	findings := make([]contracts.EvaluationFinding, 0, len(corereport.FixedDimensions()))
	for _, dimension := range corereport.FixedDimensions() {
		evidenceID := answerID
		if dimension == contracts.DimensionCodeQuality {
			evidenceID = codeID
		}
		score := 4
		findings = append(findings, contracts.EvaluationFinding{
			Dimension:   dimension,
			Score:       &score,
			EvidenceIDs: []contracts.EvidenceID{evidenceID},
			Confidence:  0.85,
			NextAction:  "Repeat the reasoning with one explicit boundary condition.",
		})
	}
	return coreevaluation.Draft{
		QuestionReviews: []coreevaluation.DraftQuestionReview{{
			QuestionID: "pair_sum",
			Summary: coreevaluation.DraftInsight{
				Text:        "The answer and executed snapshot explain the complement invariant.",
				EvidenceIDs: []contracts.EvidenceID{answerID, codeID},
				Confidence:  0.9,
			},
			NextAction: coreevaluation.DraftInsight{
				Text:        "Add an explicit duplicate-value boundary case.",
				EvidenceIDs: []contracts.EvidenceID{codeID},
				Confidence:  0.85,
			},
		}},
		Findings: findings,
		CrossInsights: []coreevaluation.DraftInsight{{
			Text:        "The spoken invariant matches the executed code snapshot.",
			EvidenceIDs: []contracts.EvidenceID{answerID, codeID},
			Confidence:  0.85,
		}},
		PracticePlan: []coreevaluation.DraftPracticeItem{
			{Topic: "Boundary cases", Mode: contracts.ScenarioStrict, DurationMinutes: 10, CompletionCriteria: "Explain and run two boundary cases.", EvidenceIDs: []contracts.EvidenceID{codeID}},
			{Topic: "Complexity", Mode: contracts.ScenarioStandard, DurationMinutes: 10, CompletionCriteria: "Explain linear time and space precisely.", EvidenceIDs: []contracts.EvidenceID{answerID}},
			{Topic: "Failure diagnosis", Mode: contracts.ScenarioStrict, DurationMinutes: 15, CompletionCriteria: "Diagnose one intentionally failing public example.", EvidenceIDs: []contracts.EvidenceID{codeID}},
		},
	}
}

func assertConclusionCoverage(t *testing.T, document corereport.Document) {
	t.Helper()
	if err := document.Validate(); err != nil {
		t.Fatalf("report.Validate: %v", err)
	}
	total := 0
	covered := 0
	check := func(status corereport.AssessmentStatus, evidence []contracts.EvidenceID) {
		total++
		if status == corereport.StatusEvidenceBacked && len(evidence) > 0 {
			covered++
			return
		}
		if status == corereport.StatusInsufficient && len(evidence) == 0 {
			covered++
		}
	}
	for _, item := range document.Scorecard {
		check(item.Status, item.EvidenceIDs)
	}
	for _, review := range document.QuestionReview {
		check(review.Summary.Status, review.Summary.EvidenceIDs)
		check(review.NextAction.Status, review.NextAction.EvidenceIDs)
	}
	for _, insight := range document.CrossInsights {
		check(insight.Status, insight.EvidenceIDs)
	}
	for _, item := range document.PracticePlan {
		check(item.Status, item.EvidenceIDs)
	}
	if total == 0 || covered != total {
		t.Fatalf("conclusion evidence coverage=%d/%d, want 100%%", covered, total)
	}
	t.Logf("conclusion evidence coverage=%d/%d (100%%)", covered, total)
}

type profileStructurerFunc func(context.Context, coreprofile.Source, string) (contracts.CandidateProfile, error)

func (function profileStructurerFunc) Structure(ctx context.Context, source coreprofile.Source, targetRole string) (contracts.CandidateProfile, error) {
	return function(ctx, source, targetRole)
}

type scenarioGeneratorFunc func(context.Context, corescenario.GenerationInput) (corescenario.GeneratedPlan, error)

func (function scenarioGeneratorFunc) Generate(ctx context.Context, input corescenario.GenerationInput) (corescenario.GeneratedPlan, error) {
	return function(ctx, input)
}

type interviewerProviderFunc func(context.Context, coreinterview.Input) (contracts.InterviewerAction, error)

func (function interviewerProviderFunc) Respond(ctx context.Context, input coreinterview.Input) (contracts.InterviewerAction, error) {
	return function(ctx, input)
}

type coachProviderFunc func(context.Context, corecoach.Input) (contracts.CoachResponse, error)

func (function coachProviderFunc) Respond(ctx context.Context, input corecoach.Input) (contracts.CoachResponse, error) {
	return function(ctx, input)
}

type codingRunnerFunc func(context.Context, corecoding.ExecutionRequest) (corecoding.ExecutionResult, error)

func (function codingRunnerFunc) Run(ctx context.Context, request corecoding.ExecutionRequest) (corecoding.ExecutionResult, error) {
	return function(ctx, request)
}

type evaluationProviderFunc func(context.Context, coreevaluation.Input) (coreevaluation.Draft, error)

func (function evaluationProviderFunc) Evaluate(ctx context.Context, input coreevaluation.Input) (coreevaluation.Draft, error) {
	return function(ctx, input)
}
