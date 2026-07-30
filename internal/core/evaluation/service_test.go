package evaluation

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestGeneratePersistsCompleteEvidenceReport(t *testing.T) {
	t.Parallel()

	store, fixture := seedEvaluationStore(t, true)
	provider := &evaluationProvider{
		evaluate: func(input Input) (Draft, error) {
			if strings.Contains(marshalInput(t, input), "deleted Coach text") {
				t.Fatal("deleted Coach event reached Evaluator input")
			}
			if len(input.CoachEvents) != 2 {
				t.Fatalf("Coach events=%d, want 2", len(input.CoachEvents))
			}
			return validDraft(fixture), nil
		},
	}
	states := make([]async.State[Progress], 0)
	service := NewService(store, provider, Options{
		Now: func() time.Time { return fixture.now.Add(30 * time.Minute) },
	})
	result, err := service.Generate(
		context.Background(),
		fixture.sessionID,
		func(state async.State[Progress]) {
			states = append(states, state)
		},
	)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if result.Degraded || result.Idempotent {
		t.Fatalf("result=%#v", result)
	}
	if result.Report.Summary.QuestionCount != 2 ||
		result.Report.Summary.CoachPromptCount != 2 ||
		result.Report.Summary.CodeRunCount != 0 {
		t.Fatalf("summary=%#v", result.Report.Summary)
	}
	if len(result.Report.Scorecard) != 8 {
		t.Fatalf("scorecard length=%d", len(result.Report.Scorecard))
	}
	code := result.Report.Scorecard[5]
	if code.Dimension != contracts.DimensionCodeQuality ||
		code.Status != report.StatusNotApplicable ||
		code.Score != nil {
		t.Fatalf("code quality=%#v", code)
	}
	totalAsks := 0
	for _, gap := range result.Report.LearningMap {
		totalAsks += gap.AskCount
	}
	if totalAsks != 2 {
		t.Fatalf("learning map asks=%d", totalAsks)
	}
	if len(result.Report.Transfer) != 2 ||
		result.Report.Transfer[0].Status != report.TransferEvidenceObserved ||
		result.Report.Transfer[1].Status != report.TransferInsufficient {
		t.Fatalf("transfer=%#v", result.Report.Transfer)
	}
	if len(result.Report.PracticePlan) < 3 {
		t.Fatalf("practice plan=%#v", result.Report.PracticePlan)
	}
	assertProgressStages(t, states, []string{
		"scoring_evidence",
		"grouping_learning_gaps",
		"planning_next_run",
		"saving_report",
	})
	session, found, err := store.GetSession(
		context.Background(),
		fixture.sessionID,
	)
	if err != nil || !found || session.Status != db.SessionCompleted {
		t.Fatalf("session=%#v found=%v err=%v", session, found, err)
	}
	persisted, found, err := report.NewService(
		store,
		report.Options{},
	).Get(context.Background(), fixture.sessionID)
	if err != nil || !found || persisted.ID != result.Report.ID {
		t.Fatalf("persisted=%#v found=%v err=%v", persisted, found, err)
	}
	restored, err := service.Generate(
		context.Background(),
		fixture.sessionID,
		nil,
	)
	if err != nil || !restored.Idempotent || provider.Calls() != 1 {
		t.Fatalf(
			"restore=%#v calls=%d err=%v",
			restored,
			provider.Calls(),
			err,
		)
	}
}

func TestUnknownEvidenceAndInvalidOutputDegradeConservatively(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		provider Provider
	}{
		{
			name: "unknown evidence",
			provider: &evaluationProvider{
				evaluate: func(Input) (Draft, error) {
					score := 5
					notApplicable := true
					findings := make([]contracts.EvaluationFinding, 0, 8)
					for _, dimension := range report.FixedDimensions() {
						finding := contracts.EvaluationFinding{
							Dimension:   dimension,
							Score:       &score,
							EvidenceIDs: []contracts.EvidenceID{"missing"},
							Confidence:  1,
							NextAction:  "Repeat the evidence-backed behavior.",
						}
						if dimension == contracts.DimensionCodeQuality {
							finding.Score = nil
							finding.NotApplicable = &notApplicable
							finding.EvidenceIDs = []contracts.EvidenceID{}
						}
						findings = append(findings, finding)
					}
					return Draft{
						QuestionReviews: []DraftQuestionReview{{
							QuestionID: "Q1",
							Summary: DraftInsight{
								Text:        "Unsupported summary",
								EvidenceIDs: []contracts.EvidenceID{"missing"},
								Confidence:  1,
							},
							NextAction: DraftInsight{
								Text:        "Unsupported action",
								EvidenceIDs: []contracts.EvidenceID{"missing"},
								Confidence:  1,
							},
						}},
						Findings: findings,
						CrossInsights: []DraftInsight{{
							Text:        "Unsupported insight",
							EvidenceIDs: []contracts.EvidenceID{"missing"},
							Confidence:  1,
						}},
						PracticePlan: []DraftPracticeItem{{
							Topic:              "Unsupported plan",
							Mode:               contracts.ScenarioStrict,
							DurationMinutes:    10,
							CompletionCriteria: "Complete it.",
							EvidenceIDs:        []contracts.EvidenceID{"missing"},
						}},
					}, nil
				},
			},
		},
		{
			name: "invalid model output after retry",
			provider: &evaluationProvider{
				evaluate: func(Input) (Draft, error) {
					return Draft{}, domainerr.New(
						domainerr.CodeInvalidModelOutput,
						"decode evaluation",
						"模型输出非法。",
						"使用保守报告。",
						true,
					)
				},
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			store, fixture := seedEvaluationStore(t, true)
			service := NewService(store, test.provider, Options{
				Now: func() time.Time {
					return fixture.now.Add(30 * time.Minute)
				},
			})
			result, err := service.Generate(
				context.Background(),
				fixture.sessionID,
				nil,
			)
			if err != nil {
				t.Fatalf("Generate: %v", err)
			}
			if !result.Degraded {
				t.Fatal("degraded=false")
			}
			for _, item := range result.Report.Scorecard {
				if item.Dimension == contracts.DimensionCodeQuality {
					if item.Status != report.StatusNotApplicable {
						t.Fatalf("code status=%s", item.Status)
					}
					continue
				}
				if item.Status != report.StatusInsufficient ||
					!strings.Contains(item.NextAction, "不足以判断") {
					t.Fatalf("unsafe scorecard item=%#v", item)
				}
			}
			if len(result.Report.PracticePlan) < 3 {
				t.Fatalf("fallback practice=%#v", result.Report.PracticePlan)
			}
			if err := result.Report.Validate(); err != nil {
				t.Fatalf("degraded report invalid: %v", err)
			}
		})
	}
}

func TestEmptyAndDependencyFailureStates(t *testing.T) {
	t.Parallel()

	t.Run("no completed session", func(t *testing.T) {
		t.Parallel()
		store, fixture := seedEvaluationStore(t, true)
		if _, err := store.UpdateSessionStatus(
			context.Background(),
			fixture.sessionID,
			db.SessionActive,
			fixture.now.Add(20*time.Minute),
		); err != nil {
			t.Fatalf("UpdateSessionStatus: %v", err)
		}
		var states []async.State[Progress]
		_, err := NewService(store, &evaluationProvider{}, Options{}).
			Generate(
				context.Background(),
				fixture.sessionID,
				func(state async.State[Progress]) {
					states = append(states, state)
				},
			)
		if !domainerr.IsCode(err, domainerr.CodeInvalidState) ||
			len(states) != 2 ||
			states[0].Phase != async.Pending ||
			states[1].Phase != async.Failed {
			t.Fatalf("states=%#v err=%v", states, err)
		}
	})

	t.Run("completed metadata without evidence", func(t *testing.T) {
		t.Parallel()
		store, fixture := seedEvaluationStore(t, false)
		_, err := NewService(store, &evaluationProvider{}, Options{}).
			Generate(context.Background(), fixture.sessionID, nil)
		if !domainerr.IsCode(err, domainerr.CodeInvalidState) {
			t.Fatalf("error=%v", err)
		}
	})

	t.Run("Provider unavailable preserves pending session", func(t *testing.T) {
		t.Parallel()
		store, fixture := seedEvaluationStore(t, true)
		provider := &evaluationProvider{
			evaluate: func(Input) (Draft, error) {
				return Draft{}, errors.New("provider offline")
			},
		}
		var states []async.State[Progress]
		_, err := NewService(store, provider, Options{}).Generate(
			context.Background(),
			fixture.sessionID,
			func(state async.State[Progress]) {
				states = append(states, state)
			},
		)
		if !domainerr.IsCode(
			err,
			domainerr.CodeDependencyUnavailable,
		) || states[len(states)-1].Phase != async.Failed {
			t.Fatalf("states=%#v err=%v", states, err)
		}
		if _, found, getErr := store.GetReport(
			context.Background(),
			fixture.sessionID,
		); getErr != nil || found {
			t.Fatalf("report found=%v err=%v", found, getErr)
		}
		session, _, getErr := store.GetSession(
			context.Background(),
			fixture.sessionID,
		)
		if getErr != nil ||
			session.Status != db.SessionEvaluationPending {
			t.Fatalf("session=%#v err=%v", session, getErr)
		}
	})
}

func TestTransferWindowIncludesExactlyFiveMinutesOnly(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	sidebar := []db.SidebarEvent{{
		ID:         "coach-1",
		QuestionID: "Q1",
		OccurredAt: now,
	}}
	events := []db.SessionEvent{
		{
			EventID:    "answer-at-five",
			Speaker:    db.SpeakerUser,
			QuestionID: "Q1",
			OccurredAt: now.Add(5 * time.Minute),
		},
		{
			EventID:    "answer-after-five",
			Speaker:    db.SpeakerUser,
			QuestionID: "Q1",
			OccurredAt: now.Add(5*time.Minute + time.Nanosecond),
		},
		{
			EventID:    "other-question",
			Speaker:    db.SpeakerUser,
			QuestionID: "Q2",
			OccurredAt: now.Add(time.Minute),
		},
	}
	transfer := buildTransfer(sidebar, events, nil)
	if len(transfer) != 1 ||
		transfer[0].Status != report.TransferEvidenceObserved ||
		len(transfer[0].SubsequentEvidence) != 1 ||
		transfer[0].SubsequentEvidence[0] != "answer-at-five" {
		t.Fatalf("transfer=%#v", transfer)
	}
}

type evaluationFixture struct {
	now       time.Time
	sessionID string
	answerQ1  contracts.EvidenceID
	answerQ2  contracts.EvidenceID
	coachQ1   contracts.EvidenceID
}

func seedEvaluationStore(
	t *testing.T,
	withEvidence bool,
) (*db.Store, evaluationFixture) {
	t.Helper()
	ctx := context.Background()
	store, err := db.Open(
		ctx,
		db.Config{DataDir: filepath.Join(t.TempDir(), "data")},
		nil,
	)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	confirmedAt := now.Add(-time.Hour)
	source := "Built a Go service and operated Redis safely."
	profile := coreprofile.Aggregate{
		ID: "profile-evaluation",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID:    "fact-service",
				Field: "project",
				Value: "Go service",
				SourceSpan: contracts.SourceSpan{
					Start: 0,
					End:   len(source),
					Text:  source,
				},
			}},
			Inferences: []contracts.ProfileInference{},
			Projects:   []string{"Go service"},
			Skills:     []string{"Go", "Redis"},
		},
		Metadata: coreprofile.Metadata{
			Source: coreprofile.Source{
				Kind: coreprofile.SourcePaste,
				Name: "resume.txt",
				Text: source,
			},
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
			CreatedAt:          confirmedAt,
			UpdatedAt:          confirmedAt,
		},
		ConfirmedAt: &confirmedAt,
	}
	scenario := contracts.Scenario{
		Template:          "project_deep_dive",
		Mode:              contracts.ScenarioStrict,
		TimeBudgetSeconds: 1200,
		PromptVersion:     "scenario-planner-v1.r1",
		Questions: []contracts.ScenarioQuestion{
			{
				ID:               "Q1",
				Prompt:           "Explain the cache consistency trade-off.",
				Intent:           "technical depth",
				EstimatedSeconds: 300,
				Rubric:           []string{"names a trade-off"},
				EvidenceIDs:      []contracts.EvidenceID{"fact-service"},
				MaxFollowUps:     1,
				EndCondition:     "trade-off explained",
			},
			{
				ID:               "Q2",
				Prompt:           "How would you clarify an outage?",
				Intent:           "problem clarification",
				EstimatedSeconds: 300,
				Rubric:           []string{"clarifies impact"},
				EvidenceIDs:      []contracts.EvidenceID{},
				Generic:          true,
				MaxFollowUps:     1,
				EndCondition:     "constraints clarified",
			},
		},
	}
	if err := store.SaveProfileAggregate(ctx, profile); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	if err := store.SaveScenario(
		ctx,
		"scenario-evaluation",
		profile.ID,
		scenario,
		now,
	); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID:         "session-evaluation",
		ScenarioID: "scenario-evaluation",
		Status:     db.SessionEvaluationPending,
		StartedAt:  now,
		UpdatedAt:  now.Add(20 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	fixture := evaluationFixture{
		now:       now,
		sessionID: "session-evaluation",
		answerQ1:  "answer-q1",
		answerQ2:  "answer-q2",
		coachQ1:   "coach-q1",
	}
	if !withEvidence {
		return store, fixture
	}
	sessionEvents := []db.SessionEvent{
		{
			EventID:      "answer-q1-first",
			SessionID:    fixture.sessionID,
			Speaker:      db.SpeakerUser,
			QuestionID:   "Q1",
			Content:      "I used versioned cache entries.",
			OccurredAt:   now.Add(2 * time.Minute),
			EvidenceRefs: []contracts.EvidenceID{"fact-service"},
		},
		{
			EventID:      string(fixture.answerQ1),
			SessionID:    fixture.sessionID,
			Speaker:      db.SpeakerUser,
			QuestionID:   "Q1",
			Content:      "I would also measure stale-read duration.",
			OccurredAt:   now.Add(8 * time.Minute),
			EvidenceRefs: []contracts.EvidenceID{"fact-service"},
		},
		{
			EventID:      string(fixture.answerQ2),
			SessionID:    fixture.sessionID,
			Speaker:      db.SpeakerUser,
			QuestionID:   "Q2",
			Content:      "I would clarify impact and recovery objectives.",
			OccurredAt:   now.Add(16 * time.Minute),
			EvidenceRefs: []contracts.EvidenceID{"constraint:Q2"},
		},
	}
	for _, event := range sessionEvents {
		if err := store.AppendSessionEvent(ctx, event); err != nil {
			t.Fatalf("AppendSessionEvent: %v", err)
		}
	}
	sidebarEvents := []db.SidebarEvent{
		{
			ID:         string(fixture.coachQ1),
			SessionID:  fixture.sessionID,
			QuestionID: "Q1",
			Intent:     contracts.CoachGiveHint,
			HelpLevel:  contracts.HelpL2,
			Tags:       []string{"Redis"},
			Content:    "Name the stale-read window.",
			Outcome:    "understood",
			OccurredAt: now.Add(3 * time.Minute),
		},
		{
			ID:         "coach-q2",
			SessionID:  fixture.sessionID,
			QuestionID: "Q2",
			Intent:     contracts.CoachAnswerStructure,
			HelpLevel:  contracts.HelpL1,
			Tags:       []string{"clarifying constraints"},
			Content:    "Start with impact.",
			Outcome:    "review",
			OccurredAt: now.Add(10 * time.Minute),
		},
		{
			ID:         "coach-deleted",
			SessionID:  fixture.sessionID,
			QuestionID: "Q2",
			Intent:     contracts.CoachExplainConcept,
			HelpLevel:  contracts.HelpL1,
			Tags:       []string{"deleted"},
			Content:    "deleted Coach text",
			Outcome:    "still_confused",
			OccurredAt: now.Add(11 * time.Minute),
		},
	}
	for _, event := range sidebarEvents {
		if err := store.AddSidebarEvent(ctx, event); err != nil {
			t.Fatalf("AddSidebarEvent: %v", err)
		}
	}
	if deleted, err := store.DeleteSidebarEvent(
		ctx,
		fixture.sessionID,
		"coach-deleted",
	); err != nil || !deleted {
		t.Fatalf("DeleteSidebarEvent deleted=%v err=%v", deleted, err)
	}
	return store, fixture
}

func validDraft(fixture evaluationFixture) Draft {
	findings := make([]contracts.EvaluationFinding, 0, 8)
	notApplicable := true
	for index, dimension := range report.FixedDimensions() {
		if dimension == contracts.DimensionCodeQuality {
			findings = append(findings, contracts.EvaluationFinding{
				Dimension:     dimension,
				NotApplicable: &notApplicable,
				EvidenceIDs:   []contracts.EvidenceID{},
				NextAction:    "No executed code in this session.",
			})
			continue
		}
		score := 3 + index%2
		findings = append(findings, contracts.EvaluationFinding{
			Dimension:   dimension,
			Score:       &score,
			EvidenceIDs: []contracts.EvidenceID{fixture.answerQ1},
			Confidence:  0.8,
			NextAction:  "Repeat the answer with one explicit trade-off.",
		})
	}
	return Draft{
		QuestionReviews: []DraftQuestionReview{
			{
				QuestionID: "Q1",
				Summary: DraftInsight{
					Text:        "The answer named a measurable consistency signal.",
					EvidenceIDs: []contracts.EvidenceID{fixture.answerQ1},
					Confidence:  0.8,
				},
				NextAction: DraftInsight{
					Text:        "Add the failure-mode trade-off.",
					EvidenceIDs: []contracts.EvidenceID{fixture.answerQ1},
					Confidence:  0.8,
				},
			},
			{
				QuestionID: "Q2",
				Summary: DraftInsight{
					Text:        "The answer clarified impact.",
					EvidenceIDs: []contracts.EvidenceID{fixture.answerQ2},
					Confidence:  0.8,
				},
				NextAction: DraftInsight{
					Text:        "Clarify the recovery budget next.",
					EvidenceIDs: []contracts.EvidenceID{fixture.answerQ2},
					Confidence:  0.8,
				},
			},
		},
		Findings: findings,
		CrossInsights: []DraftInsight{{
			Text: "The confirmed Redis skill, submitted answer, and Coach event " +
				"form a reviewable evidence chain.",
			EvidenceIDs: []contracts.EvidenceID{
				"fact-service",
				fixture.answerQ1,
				fixture.coachQ1,
			},
			Confidence: 0.8,
		}},
		PracticePlan: []DraftPracticeItem{
			{
				Topic:              "cache consistency",
				Mode:               contracts.ScenarioStrict,
				DurationMinutes:    15,
				CompletionCriteria: "Explain one trade-off without another hint.",
				EvidenceIDs:        []contracts.EvidenceID{fixture.answerQ1},
			},
			{
				Topic:              "outage clarification",
				Mode:               contracts.ScenarioStandard,
				DurationMinutes:    10,
				CompletionCriteria: "Ask two constraints before proposing a fix.",
				EvidenceIDs:        []contracts.EvidenceID{fixture.answerQ2},
			},
			{
				Topic:              "independent explanation",
				Mode:               contracts.ScenarioStrict,
				DurationMinutes:    15,
				CompletionCriteria: "Complete one answer without a Coach prompt.",
				EvidenceIDs:        []contracts.EvidenceID{fixture.coachQ1},
			},
		},
	}
}

type evaluationProvider struct {
	mu       sync.Mutex
	calls    int
	evaluate func(Input) (Draft, error)
}

func (provider *evaluationProvider) Evaluate(
	_ context.Context,
	input Input,
) (Draft, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	if provider.evaluate == nil {
		return Draft{}, errors.New("Provider unavailable")
	}
	return provider.evaluate(input)
}

func (provider *evaluationProvider) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

func assertProgressStages(
	t *testing.T,
	states []async.State[Progress],
	expected []string,
) {
	t.Helper()
	if len(states) != len(expected)+2 ||
		states[0].Phase != async.Pending ||
		states[len(states)-1].Phase != async.Succeeded {
		t.Fatalf("states=%#v", states)
	}
	for index, stage := range expected {
		state := states[index+1]
		if state.Phase != async.Streaming ||
			state.Value == nil ||
			state.Value.Stage != stage {
			t.Fatalf(
				"state %d=%#v want stage %s",
				index+1,
				state,
				stage,
			)
		}
	}
}

func marshalInput(t *testing.T, input Input) string {
	t.Helper()
	var builder strings.Builder
	for _, event := range input.CoachEvents {
		builder.WriteString(event.Content)
		builder.WriteByte('\n')
	}
	return builder.String()
}
