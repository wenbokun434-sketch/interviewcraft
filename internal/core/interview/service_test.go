package interview

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestCompleteMultiQuestionFlowPersistsBeforeProvider(t *testing.T) {
	t.Parallel()

	store := openInterviewStore(t)
	fixture := seedInterviewSession(t, store)
	clock := newStepClock()
	provider := &scriptedProvider{
		respond: func(call int, input Input) (contracts.InterviewerAction, error) {
			if len(input.SubmittedAnswers) != call {
				t.Fatalf(
					"provider call %d answers=%d",
					call,
					len(input.SubmittedAnswers),
				)
			}
			last := input.SubmittedAnswers[len(input.SubmittedAnswers)-1]
			events, err := store.ListSessionEvents(
				context.Background(),
				fixture.sessionID,
			)
			if err != nil {
				t.Fatalf("ListSessionEvents in Provider: %v", err)
			}
			if _, found := findEvent(events, string(last.EventID)); !found {
				t.Fatal("Provider called before answer event was persisted")
			}
			if strings.Contains(mustInputText(t, input), "unsubmitted secret") {
				t.Fatal("Provider input exposed unsubmitted draft")
			}
			action := contracts.InterviewerAction{
				EvidenceIDs: []contracts.EvidenceID{last.EventID},
			}
			switch call {
			case 1:
				action.Action = contracts.ActionFollowUp
				action.QuestionID = "Q1"
				action.Message = "Which failure mode did you prioritize?"
				action.SessionState = contracts.SessionInterviewing
			case 2:
				action.Action = contracts.ActionCloseQuestion
				action.QuestionID = "Q1"
				action.Message = "The trade-off is now clear."
				action.SessionState = contracts.SessionQuestionComplete
			case 3:
				action.Action = contracts.ActionNextQuestion
				action.QuestionID = "Q3"
				action.Message = fixture.scenario.Questions[2].Prompt
				action.SessionState = contracts.SessionInterviewing
			case 4:
				action.Action = contracts.ActionFinishSession
				action.QuestionID = "Q3"
				action.Message = "The planned interview is complete."
				action.SessionState = contracts.SessionComplete
			default:
				t.Fatalf("unexpected Provider call %d", call)
			}
			return action, nil
		},
	}
	service := NewService(store, provider, Options{
		Now:     clock.Now,
		Latency: &LatencyWindow{},
	})
	started, err := service.Start(context.Background(), fixture.sessionID)
	if err != nil || started.Phase != PhaseAwaitingAnswer ||
		started.CurrentQuestion == nil ||
		started.CurrentQuestion.ID != "Q1" {
		t.Fatalf("Start: snapshot=%#v err=%v", started, err)
	}
	if err := store.SaveDraft(context.Background(), db.Draft{
		SessionID:  fixture.sessionID,
		QuestionID: "Q1",
		Kind:       db.DraftAnswer,
		Content:    "unsubmitted secret",
		UpdatedAt:  clock.Now(),
	}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	tests := []struct {
		submission string
		answer     string
		question   string
		phase      Phase
	}{
		{"answer-1", "I prioritized stale reads.", "Q1", PhaseAwaitingAnswer},
		{"answer-2", "I used version checks.", "Q2", PhaseAwaitingAnswer},
		{"answer-3", "I clarified the outage impact.", "Q3", PhaseAwaitingAnswer},
		{"answer-4", "I would measure the recovery budget.", "Q3", PhaseCompleted},
	}
	for _, test := range tests {
		result, submitErr := service.Submit(
			context.Background(),
			SubmitRequest{
				SessionID:    fixture.sessionID,
				SubmissionID: test.submission,
				Answer:       test.answer,
			},
			nil,
		)
		if submitErr != nil {
			t.Fatalf("Submit %s: %v", test.submission, submitErr)
		}
		if result.Snapshot.Phase != test.phase ||
			result.Snapshot.CurrentQuestion == nil ||
			result.Snapshot.CurrentQuestion.ID != test.question {
			t.Fatalf(
				"Submit %s snapshot=%#v",
				test.submission,
				result.Snapshot,
			)
		}
	}

	session, found, err := store.GetSession(
		context.Background(),
		fixture.sessionID,
	)
	if err != nil || !found ||
		session.Status != db.SessionEvaluationPending {
		t.Fatalf("completed session=%#v found=%v err=%v", session, found, err)
	}
	events, err := store.ListSessionEvents(
		context.Background(),
		fixture.sessionID,
	)
	if err != nil {
		t.Fatalf("ListSessionEvents: %v", err)
	}
	if countSpeaker(events, db.SpeakerUser) != 4 ||
		countOwned(events, fixture.sessionID, "action") != 4 ||
		countOwned(events, fixture.sessionID, "question") != 2 {
		t.Fatalf("event stream = %#v", events)
	}
	for index := 1; index < len(events); index++ {
		if events[index].OccurredAt.Before(events[index-1].OccurredAt) ||
			events[index].Sequence <= events[index-1].Sequence {
			t.Fatalf("events are out of order at %d: %#v", index, events)
		}
	}
	if service.P95Latency() != 100*time.Millisecond {
		t.Fatalf("P95Latency=%s", service.P95Latency())
	}
}

func TestProviderFailureCancellationAndIdempotentRetry(t *testing.T) {
	t.Parallel()

	store := openInterviewStore(t)
	fixture := seedInterviewSession(t, store)
	clock := newStepClock()
	provider := &scriptedProvider{}
	provider.respond = func(call int, input Input) (
		contracts.InterviewerAction,
		error,
	) {
		if call == 1 {
			return contracts.InterviewerAction{}, errors.New("provider offline")
		}
		last := input.SubmittedAnswers[len(input.SubmittedAnswers)-1]
		return contracts.InterviewerAction{
			Action:       contracts.ActionFollowUp,
			QuestionID:   "Q1",
			Message:      "Explain one concrete trade-off.",
			EvidenceIDs:  []contracts.EvidenceID{last.EventID},
			SessionState: contracts.SessionInterviewing,
		}, nil
	}
	service := NewService(store, provider, Options{Now: clock.Now})
	if _, err := service.Start(context.Background(), fixture.sessionID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	var states []async.Phase
	request := SubmitRequest{
		SessionID:    fixture.sessionID,
		SubmissionID: "retry-1",
		Answer:       "My durable answer",
	}
	_, err := service.Submit(
		context.Background(),
		request,
		func(state async.State[Progress]) {
			states = append(states, state.Phase)
		},
	)
	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("first Submit error=%v", err)
	}
	if !reflect.DeepEqual(states, []async.Phase{
		async.Pending,
		async.Streaming,
		async.Failed,
	}) {
		t.Fatalf("failure phases=%#v", states)
	}
	events, _ := store.ListSessionEvents(
		context.Background(),
		fixture.sessionID,
	)
	if countSpeaker(events, db.SpeakerUser) != 1 ||
		countOwned(events, fixture.sessionID, "action") != 0 {
		t.Fatalf("events after failure=%#v", events)
	}

	result, err := service.Submit(context.Background(), request, nil)
	if err != nil || result.Idempotent {
		t.Fatalf("retry result=%#v err=%v", result, err)
	}
	events, _ = store.ListSessionEvents(
		context.Background(),
		fixture.sessionID,
	)
	if countSpeaker(events, db.SpeakerUser) != 1 ||
		countOwned(events, fixture.sessionID, "action") != 1 {
		t.Fatalf("events after retry=%#v", events)
	}
	result, err = service.Submit(context.Background(), request, nil)
	if err != nil || !result.Idempotent || provider.calls != 2 {
		t.Fatalf(
			"idempotent Submit result=%#v calls=%d err=%v",
			result,
			provider.calls,
			err,
		)
	}
	conflict := request
	conflict.Answer = "different answer"
	if _, err := service.Submit(
		context.Background(),
		conflict,
		nil,
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("idempotency conflict error=%v", err)
	}

	cancelStore := openInterviewStore(t)
	cancelFixture := seedInterviewSession(t, cancelStore)
	cancelProvider := &scriptedProvider{
		respond: func(_ int, _ Input) (contracts.InterviewerAction, error) {
			return contracts.InterviewerAction{}, context.Canceled
		},
	}
	cancelService := NewService(
		cancelStore,
		cancelProvider,
		Options{Now: newStepClock().Now},
	)
	if _, err := cancelService.Start(
		context.Background(),
		cancelFixture.sessionID,
	); err != nil {
		t.Fatalf("cancel Start: %v", err)
	}
	_, err = cancelService.Submit(
		context.Background(),
		SubmitRequest{
			SessionID:    cancelFixture.sessionID,
			SubmissionID: "cancel-1",
			Answer:       "persist before cancel",
		},
		nil,
	)
	if !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
		t.Fatalf("cancel Submit error=%v", err)
	}
	cancelEvents, _ := cancelStore.ListSessionEvents(
		context.Background(),
		cancelFixture.sessionID,
	)
	if countSpeaker(cancelEvents, db.SpeakerUser) != 1 {
		t.Fatalf("cancelled answer was not persisted: %#v", cancelEvents)
	}
}

func TestDraftPauseEndConfirmationAndRestartRecovery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	fixture := seedInterviewSession(t, store)
	clock := newStepClock()
	service := NewService(store, nil, Options{Now: clock.Now})
	if _, err := service.Start(ctx, fixture.sessionID); err != nil {
		t.Fatalf("Start: %v", err)
	}
	snapshot, err := service.SaveDraft(
		ctx,
		fixture.sessionID,
		"local draft survives restart",
	)
	if err != nil || snapshot.Draft == nil {
		t.Fatalf("SaveDraft snapshot=%#v err=%v", snapshot, err)
	}
	snapshot, err = service.Pause(ctx, fixture.sessionID, "pause-1")
	if err != nil || snapshot.Phase != PhasePaused {
		t.Fatalf("Pause snapshot=%#v err=%v", snapshot, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restarted := NewService(reopened, nil, Options{Now: clock.Now})
	snapshot, err = restarted.Load(ctx, fixture.sessionID)
	if err != nil ||
		snapshot.Phase != PhasePaused ||
		snapshot.Draft == nil ||
		snapshot.Draft.Content != "local draft survives restart" {
		t.Fatalf("restarted snapshot=%#v err=%v", snapshot, err)
	}
	snapshot, err = restarted.Resume(ctx, fixture.sessionID, "resume-1")
	if err != nil || snapshot.Phase != PhaseAwaitingAnswer {
		t.Fatalf("Resume snapshot=%#v err=%v", snapshot, err)
	}
	snapshot, err = restarted.RequestEnd(
		ctx,
		fixture.sessionID,
		EndQuestion,
		"end-q1",
	)
	if err != nil ||
		snapshot.Phase != PhaseAwaitingEndConfirmation ||
		snapshot.PendingEnd == nil {
		t.Fatalf("RequestEnd snapshot=%#v err=%v", snapshot, err)
	}
	if _, err := restarted.ConfirmEnd(
		ctx,
		fixture.sessionID,
		EndQuestion,
		"wrong-id",
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("ConfirmEnd without matching request error=%v", err)
	}
	snapshot, err = restarted.CancelEnd(
		ctx,
		fixture.sessionID,
		EndQuestion,
		"end-q1",
	)
	if err != nil || snapshot.Phase != PhaseAwaitingAnswer {
		t.Fatalf("CancelEnd snapshot=%#v err=%v", snapshot, err)
	}
	if _, err := restarted.RequestEnd(
		ctx,
		fixture.sessionID,
		EndQuestion,
		"end-q1-again",
	); err != nil {
		t.Fatalf("second RequestEnd: %v", err)
	}
	snapshot, err = restarted.ConfirmEnd(
		ctx,
		fixture.sessionID,
		EndQuestion,
		"end-q1-again",
	)
	if err != nil ||
		snapshot.Phase != PhaseAwaitingAnswer ||
		snapshot.CurrentQuestion == nil ||
		snapshot.CurrentQuestion.ID != "Q2" {
		t.Fatalf("Confirm question snapshot=%#v err=%v", snapshot, err)
	}
	if _, err := restarted.RequestEnd(
		ctx,
		fixture.sessionID,
		EndSession,
		"end-session",
	); err != nil {
		t.Fatalf("RequestEnd session: %v", err)
	}
	snapshot, err = restarted.ConfirmEnd(
		ctx,
		fixture.sessionID,
		EndSession,
		"end-session",
	)
	if err != nil || snapshot.Phase != PhaseCompleted {
		t.Fatalf("Confirm session snapshot=%#v err=%v", snapshot, err)
	}
}

func TestEvidenceBoundaryFollowUpLimitAndFallbackEnd(t *testing.T) {
	t.Parallel()

	store := openInterviewStore(t)
	fixture := seedInterviewSession(t, store)
	fixture.scenario.Questions[0].MaxFollowUps = 0
	if err := store.SaveScenario(
		context.Background(),
		fixture.scenarioID,
		fixture.profile.ID,
		fixture.scenario,
		fixture.now,
	); err != nil {
		t.Fatalf("replace scenario: %v", err)
	}
	if err := store.AddSidebarEvent(context.Background(), db.SidebarEvent{
		ID:         "coach-secret",
		SessionID:  fixture.sessionID,
		QuestionID: "Q1",
		Intent:     contracts.CoachGiveHint,
		HelpLevel:  contracts.HelpL1,
		Tags:       []string{"secret"},
		Outcome:    "understood",
		OccurredAt: fixture.now,
	}); err != nil {
		t.Fatalf("AddSidebarEvent: %v", err)
	}
	provider := &scriptedProvider{
		respond: func(_ int, _ Input) (contracts.InterviewerAction, error) {
			return contracts.InterviewerAction{
				Action:       contracts.ActionFollowUp,
				QuestionID:   "Q1",
				Message:      "Unsafe follow-up",
				EvidenceIDs:  []contracts.EvidenceID{"coach-secret"},
				SessionState: contracts.SessionInterviewing,
			}, nil
		},
	}
	service := NewService(
		store,
		provider,
		Options{Now: newStepClock().Now},
	)
	if _, err := service.Start(
		context.Background(),
		fixture.sessionID,
	); err != nil {
		t.Fatalf("Start: %v", err)
	}
	_, err := service.Submit(
		context.Background(),
		SubmitRequest{
			SessionID:    fixture.sessionID,
			SubmissionID: "unsafe-1",
			Answer:       "Persisted even when action is rejected",
		},
		nil,
	)
	if !domainerr.IsCode(err, domainerr.CodeInvalidModelOutput) {
		t.Fatalf("unsafe action error=%v", err)
	}
	if provider.lastInput.FollowUpCount != 0 ||
		slices.Contains(
			provider.lastInput.AllowedEvidenceIDs,
			contracts.EvidenceID("coach-secret"),
		) {
		t.Fatalf("Provider input exposed Coach evidence: %#v", provider.lastInput)
	}
	if _, err := service.RequestEnd(
		context.Background(),
		fixture.sessionID,
		EndQuestion,
		"safe-end",
	); err != nil {
		t.Fatalf("fallback RequestEnd: %v", err)
	}
	snapshot, err := service.ConfirmEnd(
		context.Background(),
		fixture.sessionID,
		EndQuestion,
		"safe-end",
	)
	if err != nil ||
		snapshot.CurrentQuestion == nil ||
		snapshot.CurrentQuestion.ID != "Q2" {
		t.Fatalf("fallback ConfirmEnd snapshot=%#v err=%v", snapshot, err)
	}

	nilStore := openInterviewStore(t)
	nilFixture := seedInterviewSession(t, nilStore)
	nilService := NewService(
		nilStore,
		nil,
		Options{Now: newStepClock().Now},
	)
	if _, err := nilService.Start(
		context.Background(),
		nilFixture.sessionID,
	); err != nil {
		t.Fatalf("nil Provider Start: %v", err)
	}
	_, err = nilService.Submit(
		context.Background(),
		SubmitRequest{
			SessionID:    nilFixture.sessionID,
			SubmissionID: "nil-provider",
			Answer:       "answer remains local",
		},
		nil,
	)
	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil Provider Submit error=%v", err)
	}
	nilEvents, _ := nilStore.ListSessionEvents(
		context.Background(),
		nilFixture.sessionID,
	)
	if countSpeaker(nilEvents, db.SpeakerUser) != 1 {
		t.Fatalf("nil Provider lost answer: %#v", nilEvents)
	}
}

func TestEmptyScenarioAndLatencyWindow(t *testing.T) {
	t.Parallel()

	store := openInterviewStore(t)
	fixture := seedInterviewSession(t, store)
	override := scenarioOverride{
		Repository: store,
		scenario: contracts.Scenario{
			Template:          "empty",
			Mode:              contracts.ScenarioStandard,
			TimeBudgetSeconds: 60,
			PromptVersion:     "empty-v1",
			Questions:         []contracts.ScenarioQuestion{},
		},
	}
	service := NewService(&override, nil, Options{})
	if _, err := service.Load(
		context.Background(),
		fixture.sessionID,
	); !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("empty scenario Load error=%v", err)
	}

	window := &LatencyWindow{}
	for _, value := range []time.Duration{
		10 * time.Millisecond,
		40 * time.Millisecond,
		20 * time.Millisecond,
		30 * time.Millisecond,
		100 * time.Millisecond,
	} {
		window.Observe(value)
	}
	if window.Count() != 5 || window.P95() != 100*time.Millisecond {
		t.Fatalf("latency count=%d p95=%s", window.Count(), window.P95())
	}
}

type interviewFixture struct {
	now        time.Time
	profile    coreprofile.Aggregate
	scenario   contracts.Scenario
	scenarioID string
	sessionID  string
}

func seedInterviewSession(
	t *testing.T,
	store *db.Store,
) interviewFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	source := "Built a Go payment service with PostgreSQL."
	confirmedAt := now.Add(time.Minute)
	profile := coreprofile.Aggregate{
		ID: "profile-interview",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID:    "fact-payment",
				Field: "project",
				Value: "Go payment service",
				SourceSpan: contracts.SourceSpan{
					Start: 0,
					End:   len(source),
					Text:  source,
				},
			}},
			Inferences: []contracts.ProfileInference{{
				ID:                "inference-secret",
				Field:             "leadership",
				Value:             "unconfirmed leadership",
				Confidence:        0.5,
				NeedsConfirmation: true,
			}},
			Projects: []string{"payment service"},
			Skills:   []string{"Go", "PostgreSQL"},
		},
		Metadata: coreprofile.Metadata{
			Source: coreprofile.Source{
				Kind: coreprofile.SourcePaste,
				Name: "pasted-resume.txt",
				Text: source,
			},
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
			CreatedAt:          now,
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
				Prompt:           "Explain the payment reliability trade-off.",
				Intent:           "Assess confirmed project depth",
				EstimatedSeconds: 300,
				Rubric:           []string{"Explains one trade-off"},
				EvidenceIDs:      []contracts.EvidenceID{"fact-payment"},
				MaxFollowUps:     2,
				EndCondition:     "One trade-off is explained",
			},
			{
				ID:               "Q2",
				Prompt:           "How would you diagnose an outage?",
				Intent:           "Assess diagnostic structure",
				EstimatedSeconds: 300,
				Rubric:           []string{"Clarifies impact"},
				EvidenceIDs:      []contracts.EvidenceID{},
				Generic:          true,
				MaxFollowUps:     1,
				EndCondition:     "A diagnostic sequence is explained",
			},
			{
				ID:               "Q3",
				Prompt:           "How would you validate recovery?",
				Intent:           "Assess recovery validation",
				EstimatedSeconds: 300,
				Rubric:           []string{"Names a recovery signal"},
				EvidenceIDs:      []contracts.EvidenceID{},
				Generic:          true,
				MaxFollowUps:     1,
				EndCondition:     "A recovery signal is named",
			},
		},
	}
	if err := store.SaveProfileAggregate(ctx, profile); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	if err := store.SaveScenario(
		ctx,
		"scenario-interview",
		profile.ID,
		scenario,
		now,
	); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID:         "session-interview",
		ScenarioID: "scenario-interview",
		Status:     db.SessionActive,
		StartedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return interviewFixture{
		now:        now,
		profile:    profile,
		scenario:   scenario,
		scenarioID: "scenario-interview",
		sessionID:  "session-interview",
	}
}

type scriptedProvider struct {
	mu        sync.Mutex
	calls     int
	lastInput Input
	respond   func(int, Input) (contracts.InterviewerAction, error)
}

func (provider *scriptedProvider) Respond(
	_ context.Context,
	input Input,
) (contracts.InterviewerAction, error) {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	provider.calls++
	provider.lastInput = input
	return provider.respond(provider.calls, input)
}

type stepClock struct {
	mu      sync.Mutex
	current time.Time
}

func newStepClock() *stepClock {
	return &stepClock{
		current: time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC),
	}
}

func (clock *stepClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	value := clock.current
	clock.current = clock.current.Add(100 * time.Millisecond)
	return value
}

type scenarioOverride struct {
	Repository
	scenario contracts.Scenario
}

func (override *scenarioOverride) GetScenario(
	context.Context,
	string,
) (contracts.Scenario, bool, error) {
	return override.scenario, true, nil
}

func openInterviewStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(
		context.Background(),
		db.Config{DataDir: filepath.Join(t.TempDir(), "data")},
		nil,
	)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func countSpeaker(events []db.SessionEvent, speaker db.EventSpeaker) int {
	count := 0
	for _, event := range events {
		if event.Speaker == speaker {
			count++
		}
	}
	return count
}

func countOwned(
	events []db.SessionEvent,
	sessionID string,
	kind string,
) int {
	count := 0
	for _, event := range events {
		owned, ok := parseOwnedEvent(sessionID, event.EventID)
		if ok && owned.kind == kind {
			count++
		}
	}
	return count
}

func mustInputText(t *testing.T, input Input) string {
	t.Helper()
	var builder strings.Builder
	for _, answer := range input.SubmittedAnswers {
		builder.WriteString(answer.Content)
	}
	for _, fact := range input.ConfirmedFacts {
		builder.WriteString(fact.Value)
	}
	return builder.String()
}
