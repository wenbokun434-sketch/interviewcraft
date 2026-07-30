package scenario

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestGenerateUsesConfirmedFactsAndProducesCompletePlan(t *testing.T) {
	t.Parallel()

	profile := confirmedProfile()
	repository := newScenarioRepository()
	var captured GenerationInput
	service := newScenarioService(t, repository, generatorFunc(func(
		_ context.Context,
		input GenerationInput,
	) (GeneratedPlan, error) {
		captured = input
		return generatedPlan(input), nil
	}))
	request := scenarioRequest(profile, "Backend Engineer with Go and SQL")
	var states []async.State[Progress]

	plan, err := service.Generate(
		context.Background(),
		request,
		nil,
		func(state async.State[Progress]) {
			states = append(states, state)
			if controls := ControlsFor(state, nil); !controls.BackEnabled ||
				controls.StartEnabled {
				t.Fatalf("generation controls = %#v", controls)
			}
		},
	)

	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if plan.ID != "scenario-main-v001" ||
		plan.Revision != 1 ||
		plan.Scenario.PromptVersion != "scenario-planner-v1.r1" ||
		plan.Locked ||
		len(plan.JDMappings) != 3 {
		t.Fatalf("plan = %#v", plan)
	}
	if len(captured.Facts) != len(profile.Candidate.Facts) ||
		captured.TargetRole != profile.Candidate.TargetRole ||
		captured.Template.ID != "project_deep_dive" {
		t.Fatalf("generation input = %#v", captured)
	}
	for _, fact := range captured.Facts {
		if strings.Contains(fact.Value, "unconfirmed leadership") {
			t.Fatal("generation input leaked an inference")
		}
	}
	assertPlannerLifecycle(t, states, async.Succeeded)

	confirmed, err := service.Confirm(context.Background(), plan)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if !confirmed.Locked || confirmed.ConfirmedAt == nil {
		t.Fatalf("confirmed plan = %#v", confirmed)
	}
	if len(repository.saves) != 1 ||
		repository.saves[0].id != confirmed.ID ||
		repository.saves[0].profileID != profile.ID {
		t.Fatalf("repository saves = %#v", repository.saves)
	}
	ready := async.NewSucceeded(Progress{Stage: "ready"})
	if controls := ControlsFor(ready, &confirmed); !controls.StartEnabled ||
		!controls.BackEnabled {
		t.Fatalf("confirmed controls = %#v", controls)
	}
	if _, err := service.EditQuestion(
		confirmed,
		confirmed.Scenario.Questions[0],
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("locked EditQuestion error = %v", err)
	}
	if _, err := service.UpdateRules(
		confirmed,
		contracts.ScenarioCoach,
		900,
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("locked UpdateRules error = %v", err)
	}
}

func TestGenerateRequiresConfirmedProfileAndSupportsNoJD(t *testing.T) {
	t.Parallel()

	profile := confirmedProfile()
	calls := 0
	service := newScenarioService(t, newScenarioRepository(), generatorFunc(func(
		_ context.Context,
		input GenerationInput,
	) (GeneratedPlan, error) {
		calls++
		return generatedPlan(input), nil
	}))
	unconfirmed := profile
	unconfirmed.ConfirmedAt = nil

	_, err := service.Generate(
		context.Background(),
		scenarioRequest(unconfirmed, ""),
		nil,
		nil,
	)
	if !domainerr.IsCode(err, domainerr.CodeValidation) || calls != 0 {
		t.Fatalf("unconfirmed Generate: calls=%d err=%v", calls, err)
	}

	plan, err := service.Generate(
		context.Background(),
		scenarioRequest(profile, ""),
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Generate without JD: %v", err)
	}
	if plan.JDProvided || plan.JDMappings == nil ||
		len(plan.JDMappings) != 0 {
		t.Fatalf("no-JD mappings = %#v", plan.JDMappings)
	}
}

func TestEvidenceAndJDPolicyRejectsUnsafeProviderOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		jd     string
		mutate func(*GeneratedPlan)
	}{
		{
			name: "unknown question evidence",
			jd:   "Backend Engineer with Go and SQL",
			mutate: func(generated *GeneratedPlan) {
				generated.Scenario.Questions[0].EvidenceIDs =
					[]contracts.EvidenceID{"inference-lead"}
			},
		},
		{
			name: "generic question with fake evidence",
			jd:   "Backend Engineer with Go and SQL",
			mutate: func(generated *GeneratedPlan) {
				generated.Scenario.Questions[1].EvidenceIDs =
					[]contracts.EvidenceID{"fact-payment"}
			},
		},
		{
			name: "too few JD mappings",
			jd:   "Backend Engineer with Go and SQL",
			mutate: func(generated *GeneratedPlan) {
				generated.JDMappings = generated.JDMappings[:2]
			},
		},
		{
			name: "mapping without JD",
			jd:   "",
			mutate: func(generated *GeneratedPlan) {
				generated.JDMappings = []JDMapping{{
					Requirement: "Invented requirement",
					EvidenceIDs: []contracts.EvidenceID{},
					Gap:         "unknown",
				}}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			service := newScenarioService(
				t,
				newScenarioRepository(),
				generatorFunc(func(
					_ context.Context,
					input GenerationInput,
				) (GeneratedPlan, error) {
					generated := generatedPlan(input)
					test.mutate(&generated)
					return generated, nil
				}),
			)

			plan, err := service.Generate(
				context.Background(),
				scenarioRequest(confirmedProfile(), test.jd),
				nil,
				nil,
			)

			if !domainerr.IsCode(err, domainerr.CodeValidation) {
				t.Fatalf("Generate error = %v, plan=%#v", err, plan)
			}
			if plan.ID != "" {
				t.Fatalf("unsafe plan escaped validation: %#v", plan)
			}
		})
	}
}

func TestManualEditsAndRulesSurviveRegeneration(t *testing.T) {
	t.Parallel()

	profile := confirmedProfile()
	generation := 0
	service := newScenarioService(t, newScenarioRepository(), generatorFunc(func(
		_ context.Context,
		input GenerationInput,
	) (GeneratedPlan, error) {
		generation++
		result := generatedPlan(input)
		result.Scenario.Questions[0].Prompt =
			"provider refresh " + string(rune('0'+generation))
		return result, nil
	}))
	request := scenarioRequest(profile, "Backend Engineer with Go and SQL")
	first, err := service.Generate(
		context.Background(),
		request,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("initial Generate: %v", err)
	}
	replacement := first.Scenario.Questions[0]
	replacement.Prompt = "USER EDIT: explain my payment trade-off"

	edited, err := service.EditQuestion(first, replacement)
	if err != nil {
		t.Fatalf("EditQuestion: %v", err)
	}
	if edited.Revision != 2 ||
		!reflect.DeepEqual(edited.ManualQuestionIDs, []string{"Q1"}) {
		t.Fatalf("edited plan = %#v", edited)
	}
	rules, err := service.UpdateRules(
		edited,
		contracts.ScenarioCoach,
		900,
	)
	if err != nil {
		t.Fatalf("UpdateRules: %v", err)
	}
	if rules.Revision != 3 || !rules.ManualRules {
		t.Fatalf("rules plan = %#v", rules)
	}

	refreshed, err := service.Generate(
		context.Background(),
		request,
		&rules,
		nil,
	)
	if err != nil {
		t.Fatalf("refresh Generate: %v", err)
	}
	if refreshed.Revision != 4 ||
		refreshed.Scenario.Questions[0].Prompt != replacement.Prompt ||
		refreshed.Scenario.Mode != contracts.ScenarioCoach ||
		refreshed.Scenario.TimeBudgetSeconds != 900 {
		t.Fatalf("refresh overwrote manual changes: %#v", refreshed)
	}
}

func TestGenerationFailurePreservesPreviousPlanAndCanRetry(t *testing.T) {
	t.Parallel()

	profile := confirmedProfile()
	request := scenarioRequest(profile, "Backend Engineer with Go and SQL")
	goodService := newScenarioService(
		t,
		newScenarioRepository(),
		generatorFunc(func(
			_ context.Context,
			input GenerationInput,
		) (GeneratedPlan, error) {
			return generatedPlan(input), nil
		}),
	)
	previous, err := goodService.Generate(
		context.Background(),
		request,
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("initial Generate: %v", err)
	}
	failingService := newScenarioService(
		t,
		newScenarioRepository(),
		generatorFunc(func(
			context.Context,
			GenerationInput,
		) (GeneratedPlan, error) {
			return GeneratedPlan{}, errors.New("provider unavailable")
		}),
	)
	var states []async.State[Progress]

	preserved, err := failingService.Generate(
		context.Background(),
		request,
		&previous,
		func(state async.State[Progress]) {
			states = append(states, state)
		},
	)

	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("failed refresh error = %v", err)
	}
	if !reflect.DeepEqual(preserved, previous) {
		t.Fatalf("failed refresh changed plan: got=%#v want=%#v", preserved, previous)
	}
	assertPlannerLifecycle(t, states, async.Failed)

	retried, err := goodService.Generate(
		context.Background(),
		request,
		&preserved,
		nil,
	)
	if err != nil || retried.Revision != previous.Revision+1 {
		t.Fatalf("retry: plan=%#v err=%v", retried, err)
	}
}

func TestConfirmFailureDoesNotLockDraft(t *testing.T) {
	t.Parallel()

	profile := confirmedProfile()
	repository := newScenarioRepository()
	repository.saveErr = errors.New("database is read-only")
	service := newScenarioService(t, repository, generatorFunc(func(
		_ context.Context,
		input GenerationInput,
	) (GeneratedPlan, error) {
		return generatedPlan(input), nil
	}))
	plan, err := service.Generate(
		context.Background(),
		scenarioRequest(profile, "Backend Engineer with Go and SQL"),
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	result, err := service.Confirm(context.Background(), plan)

	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("Confirm error = %v", err)
	}
	if result.Locked || result.ConfirmedAt != nil ||
		!reflect.DeepEqual(result, plan) {
		t.Fatalf("failed Confirm mutated draft: %#v", result)
	}
}

func TestConfirmedProfileGeneratesPersistedScenarioVersion(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	profile := confirmedProfile()
	if err := store.SaveProfileAggregate(ctx, profile); err != nil {
		_ = store.Close()
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	service := newScenarioService(t, store, generatorFunc(func(
		_ context.Context,
		input GenerationInput,
	) (GeneratedPlan, error) {
		return generatedPlan(input), nil
	}))
	plan, err := service.Generate(
		ctx,
		scenarioRequest(profile, "Backend Engineer with Go and SQL"),
		nil,
		nil,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("Generate: %v", err)
	}
	confirmed, err := service.Confirm(ctx, plan)
	if err != nil {
		_ = store.Close()
		t.Fatalf("Confirm: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restored, found, err := reopened.GetScenario(ctx, confirmed.ID)

	if err != nil || !found {
		t.Fatalf("GetScenario: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(restored, confirmed.Scenario) ||
		restored.PromptVersion != "scenario-planner-v1.r1" {
		t.Fatalf("restored scenario = %#v", restored)
	}
}

func TestControlsKeepBackAvailableDuringGenerationAndBlockEmptyStart(t *testing.T) {
	t.Parallel()

	for _, state := range []async.State[Progress]{
		async.NewPending[Progress](),
		async.NewStreaming(&Progress{Stage: "generating"}),
	} {
		controls := ControlsFor(state, nil)
		if controls.GenerateEnabled ||
			controls.StartEnabled ||
			!controls.BackEnabled {
			t.Fatalf("loading controls = %#v", controls)
		}
	}
	empty := ControlsFor(async.NewSucceeded(Progress{Stage: "empty"}), nil)
	if !empty.GenerateEnabled || empty.StartEnabled || !empty.BackEnabled {
		t.Fatalf("empty controls = %#v", empty)
	}
}

func scenarioRequest(profile coreprofile.Aggregate, jd string) Request {
	return Request{
		PlanID:            "scenario-main",
		Profile:           profile,
		TemplateID:        "project_deep_dive",
		Mode:              contracts.ScenarioStandard,
		TimeBudgetSeconds: 1200,
		JD:                jd,
	}
}

func generatedPlan(input GenerationInput) GeneratedPlan {
	mappings := []JDMapping{}
	if strings.TrimSpace(input.JD) != "" {
		mappings = []JDMapping{
			{
				Requirement: "Build backend services",
				EvidenceIDs: []contracts.EvidenceID{"fact-payment"},
				Gap:         "",
			},
			{
				Requirement: "Use Go",
				EvidenceIDs: []contracts.EvidenceID{"fact-payment"},
				Gap:         "",
			},
			{
				Requirement: "Distributed systems",
				EvidenceIDs: []contracts.EvidenceID{},
				Gap:         "No confirmed distributed systems evidence",
			},
		}
	}
	return GeneratedPlan{
		Scenario: contracts.Scenario{
			Template:          input.Template.ID,
			Mode:              input.Mode,
			TimeBudgetSeconds: input.TimeBudget,
			PromptVersion:     input.PromptVersion,
			Questions: []contracts.ScenarioQuestion{
				{
					ID:               "Q1",
					Prompt:           "Explain the payment service trade-offs.",
					Intent:           "Assess confirmed project depth",
					EstimatedSeconds: 360,
					Rubric: []string{
						"Explains personal contribution",
						"Names one trade-off",
					},
					EvidenceIDs:  []contracts.EvidenceID{"fact-payment"},
					Generic:      false,
					MaxFollowUps: 2,
					EndCondition: "One evidence-backed trade-off is explained",
				},
				{
					ID:               "Q2",
					Prompt:           "Clarify how you approach an unfamiliar outage.",
					Intent:           "Assess general failure reasoning",
					EstimatedSeconds: 360,
					Rubric:           []string{"Clarifies impact and evidence"},
					EvidenceIDs:      []contracts.EvidenceID{},
					Generic:          true,
					MaxFollowUps:     2,
					EndCondition:     "A general diagnostic sequence is explained",
				},
			},
		},
		JDMappings: mappings,
	}
}

func confirmedProfile() coreprofile.Aggregate {
	source := "Built payment service with Go.\n" +
		"Used PostgreSQL for data.\n" +
		"Improved latency by 30%."
	now := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	confirmedAt := now.Add(time.Minute)
	return coreprofile.Aggregate{
		ID: "profile-confirmed",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{
				scenarioFact(
					source,
					"fact-payment",
					"project",
					"payment service",
					"Built payment service with Go.",
				),
				scenarioFact(
					source,
					"fact-postgres",
					"skill",
					"PostgreSQL",
					"Used PostgreSQL for data.",
				),
				scenarioFact(
					source,
					"fact-latency",
					"achievement",
					"30%",
					"Improved latency by 30%.",
				),
			},
			Inferences: []contracts.ProfileInference{{
				ID:                "inference-lead",
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
			UpdatedAt:          now,
		},
		ConfirmedAt: &confirmedAt,
	}
}

func scenarioFact(
	source string,
	id contracts.EvidenceID,
	field string,
	value string,
	spanText string,
) contracts.ProfileFact {
	start := strings.Index(source, spanText)
	return contracts.ProfileFact{
		ID:    id,
		Field: field,
		Value: value,
		SourceSpan: contracts.SourceSpan{
			Start: start,
			End:   start + len(spanText),
			Text:  spanText,
		},
	}
}

type generatorFunc func(
	context.Context,
	GenerationInput,
) (GeneratedPlan, error)

func (function generatorFunc) Generate(
	ctx context.Context,
	input GenerationInput,
) (GeneratedPlan, error) {
	return function(ctx, input)
}

type scenarioSave struct {
	id          string
	profileID   string
	scenario    contracts.Scenario
	confirmedAt time.Time
}

type scenarioRepository struct {
	saves   []scenarioSave
	items   map[string]contracts.Scenario
	profile coreprofile.Aggregate
	saveErr error
}

func newScenarioRepository() *scenarioRepository {
	return &scenarioRepository{
		items:   make(map[string]contracts.Scenario),
		profile: confirmedProfile(),
	}
}

func (repository *scenarioRepository) SaveScenario(
	_ context.Context,
	id string,
	profileID string,
	scenario contracts.Scenario,
	confirmedAt time.Time,
) error {
	if repository.saveErr != nil {
		return repository.saveErr
	}
	value := cloneScenario(scenario)
	repository.saves = append(repository.saves, scenarioSave{
		id:          id,
		profileID:   profileID,
		scenario:    value,
		confirmedAt: confirmedAt,
	})
	repository.items[id] = value
	return nil
}

func (repository *scenarioRepository) GetScenario(
	_ context.Context,
	id string,
) (contracts.Scenario, bool, error) {
	value, found := repository.items[id]
	return cloneScenario(value), found, nil
}

func (repository *scenarioRepository) GetProfileAggregate(
	_ context.Context,
	id string,
) (coreprofile.Aggregate, bool, error) {
	if repository.profile.ID != id {
		return coreprofile.Aggregate{}, false, nil
	}
	return cloneConfirmedProfile(repository.profile), true, nil
}

func newScenarioService(
	t *testing.T,
	repository Repository,
	generator Generator,
) *Service {
	t.Helper()
	service, err := NewService(
		repository,
		generator,
		func() time.Time {
			return time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
		},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func assertPlannerLifecycle(
	t *testing.T,
	states []async.State[Progress],
	terminal async.Phase,
) {
	t.Helper()
	if len(states) < 2 ||
		states[0].Phase != async.Pending ||
		states[len(states)-1].Phase != terminal {
		t.Fatalf("planner lifecycle = %#v", states)
	}
	for index, state := range states {
		if err := state.Validate(); err != nil {
			t.Fatalf("state %d invalid: %v", index, err)
		}
	}
}

func cloneConfirmedProfile(
	value coreprofile.Aggregate,
) coreprofile.Aggregate {
	value.Candidate.Facts = append(
		[]contracts.ProfileFact(nil),
		value.Candidate.Facts...,
	)
	value.Candidate.Inferences = append(
		[]contracts.ProfileInference(nil),
		value.Candidate.Inferences...,
	)
	value.Candidate.Projects = append([]string(nil), value.Candidate.Projects...)
	value.Candidate.Skills = append([]string(nil), value.Candidate.Skills...)
	value.Metadata.LockedFactIDs = append(
		[]contracts.EvidenceID(nil),
		value.Metadata.LockedFactIDs...,
	)
	value.Metadata.LockedInferenceIDs = append(
		[]string(nil),
		value.Metadata.LockedInferenceIDs...,
	)
	if value.ConfirmedAt != nil {
		confirmedAt := *value.ConfirmedAt
		value.ConfirmedAt = &confirmedAt
	}
	return value
}
