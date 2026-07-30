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
	corescenario "github.com/interviewcraft/interviewcraft/internal/core/scenario"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestGenerateEditDeleteRefreshAndStartLocksVersion(t *testing.T) {
	t.Parallel()

	profile := screenProfile()
	repository := newMemoryScenarioRepository(profile)
	generator := &screenGenerator{}
	planner := newCorePlanner(t, repository, generator)
	sessions := &memorySessionStore{}
	model := newScenarioModel(t, planner, sessions, profile, 160, 48, false)

	if err := model.SelectTemplate("project_deep_dive"); err != nil {
		t.Fatalf("SelectTemplate: %v", err)
	}
	if err := model.Generate(context.Background(), nil); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if err := model.UpdateRules(
		DifficultyStretch,
		contracts.ScenarioCoach,
		30*60,
	); err != nil {
		t.Fatalf("UpdateRules: %v", err)
	}
	plan, _ := model.Plan()
	replacement := plan.Scenario.Questions[0]
	replacement.Prompt = "Explain the manually selected consistency trade-off."
	replacement.Intent = "Assess an explicit manual trade-off"
	if err := model.ReplaceSelected(replacement); err != nil {
		t.Fatalf("ReplaceSelected: %v", err)
	}

	model.focusTarget(focusPlan)
	model.HandleKey("down")
	if action := model.HandleKey("d"); action.Intent != IntentDelete {
		t.Fatalf("delete action = %#v", action)
	}
	if err := model.DeleteSelected(); err != nil {
		t.Fatalf("DeleteSelected: %v", err)
	}
	if err := model.Generate(context.Background(), nil); err != nil {
		t.Fatalf("refresh Generate: %v", err)
	}

	plan, _ = model.Plan()
	if len(plan.Scenario.Questions) != 2 {
		t.Fatalf("questions after refresh = %#v", plan.Scenario.Questions)
	}
	if plan.Scenario.Questions[0].Prompt != replacement.Prompt {
		t.Fatalf("manual replacement lost: %#v", plan.Scenario.Questions[0])
	}
	for _, question := range plan.Scenario.Questions {
		if question.ID == "Q2" {
			t.Fatal("manually deleted Q2 returned after refresh")
		}
	}
	templateID, difficulty, mode, duration := model.SelectedSettings()
	if templateID != "project_deep_dive" ||
		difficulty != DifficultyStretch ||
		mode != contracts.ScenarioCoach ||
		duration != 30*60 {
		t.Fatalf(
			"settings = %q %q %q %d",
			templateID,
			difficulty,
			mode,
			duration,
		)
	}

	if action := model.HandleKey("ctrl+enter"); action.Intent != IntentStart {
		t.Fatalf("start action = %#v", action)
	}
	action, err := model.Start(context.Background(), nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if action.Destination != DestinationInterview ||
		action.SessionID != "session-screen" ||
		action.ScenarioID == "" {
		t.Fatalf("start navigation = %#v", action)
	}
	locked, _ := model.Plan()
	if !locked.Locked || locked.ConfirmedAt == nil {
		t.Fatalf("started plan is not locked: %#v", locked)
	}
	if sessions.session.ID != "session-screen" ||
		sessions.session.ScenarioID != locked.ID ||
		sessions.session.Status != db.SessionActive {
		t.Fatalf("created session = %#v", sessions.session)
	}
	if _, found := repository.scenarios[locked.ID]; !found {
		t.Fatalf("scenario %q was not persisted", locked.ID)
	}

	version := locked.ID
	if err := model.UpdateRules(
		DifficultyFoundation,
		contracts.ScenarioStrict,
		15*60,
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("locked UpdateRules error = %v", err)
	}
	if err := model.DeleteSelected(); !domainerr.IsCode(
		err,
		domainerr.CodePolicyDenied,
	) {
		t.Fatalf("locked DeleteSelected error = %v", err)
	}
	after, _ := model.Plan()
	if after.ID != version || !reflect.DeepEqual(after.Scenario, locked.Scenario) {
		t.Fatal("locked scenario version changed after rejected edits")
	}
}

func TestGenerationLoadingKeepsBackAvailableAndStartDisabled(t *testing.T) {
	t.Parallel()

	profile := screenProfile()
	planner := newCorePlanner(
		t,
		newMemoryScenarioRepository(profile),
		&screenGenerator{},
	)
	model := newScenarioModel(
		t,
		planner,
		&memorySessionStore{},
		profile,
		120,
		36,
		false,
	)
	var phases []async.Phase
	var loadingRender string
	var back Action
	var start Action

	err := model.Generate(
		context.Background(),
		func(state async.State[Progress]) {
			phases = append(phases, state.Phase)
			if state.Phase != async.Streaming {
				return
			}
			loadingRender, _ = model.Render()
			back = model.HandleKey("b")
			start = model.HandleKey("ctrl+enter")
		},
	)

	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(phases) < 3 ||
		phases[0] != async.Pending ||
		phases[1] != async.Pending ||
		phases[len(phases)-1] != async.Succeeded {
		t.Fatalf("phases = %#v", phases)
	}
	if !strings.Contains(loadingRender, "正在") ||
		!strings.Contains(loadingRender, "[b] 返回画像") {
		t.Fatalf("loading render = %q", loadingRender)
	}
	if back.Destination != DestinationProfile || start != (Action{}) {
		t.Fatalf("loading actions: back=%#v start=%#v", back, start)
	}
}

func TestEmptyPlanShowsActionAndBlocksDeleteAndStart(t *testing.T) {
	t.Parallel()

	profile := screenProfile()
	planner := newCorePlanner(
		t,
		newMemoryScenarioRepository(profile),
		&screenGenerator{},
	)
	model := newScenarioModel(
		t,
		planner,
		&memorySessionStore{},
		profile,
		80,
		24,
		false,
	)

	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "还没有场景计划") ||
		!strings.Contains(rendered, "[g] 生成 Run Plan") {
		t.Fatalf("empty render = %q", rendered)
	}
	if action := model.HandleKey("ctrl+enter"); action != (Action{}) {
		t.Fatalf("empty start key = %#v", action)
	}
	if err := model.DeleteSelected(); !domainerr.IsCode(
		err,
		domainerr.CodeValidation,
	) {
		t.Fatalf("empty DeleteSelected error = %v", err)
	}
	if _, err := model.Start(
		context.Background(),
		nil,
	); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("empty Start error = %v", err)
	}
}

func TestProviderAndSchemaFailuresAreActionableAndPreservePlan(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		invalid bool
		cause   error
	}{
		{
			name:  "provider unavailable",
			cause: errors.New("provider offline"),
		},
		{
			name:    "invalid model output",
			invalid: true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile := screenProfile()
			repository := newMemoryScenarioRepository(profile)
			generator := &screenGenerator{}
			planner := newCorePlanner(t, repository, generator)
			model := newScenarioModel(
				t,
				planner,
				&memorySessionStore{},
				profile,
				120,
				36,
				false,
			)
			if err := model.Generate(context.Background(), nil); err != nil {
				t.Fatalf("initial Generate: %v", err)
			}
			plan, _ := model.Plan()
			replacement := plan.Scenario.Questions[0]
			replacement.Prompt = "Preserved manual question"
			if err := model.ReplaceSelected(replacement); err != nil {
				t.Fatalf("ReplaceSelected: %v", err)
			}
			before, _ := model.Plan()
			generator.err = test.cause
			generator.invalid = test.invalid

			err := model.Generate(context.Background(), nil)

			if err == nil {
				t.Fatal("Generate succeeded, want failure")
			}
			after, _ := model.Plan()
			if !reflect.DeepEqual(after, before) {
				t.Fatal("failed generation changed the current plan")
			}
			rendered, renderErr := model.Render()
			if renderErr != nil {
				t.Fatalf("Render: %v", renderErr)
			}
			if !strings.Contains(rendered, "[g] 重试") ||
				(!strings.Contains(rendered, "无法生成场景计划") &&
					!strings.Contains(rendered, "有效结构") &&
					!strings.Contains(
						rendered,
						"结构化数据不符合 Scenario 契约",
					)) {
				t.Fatalf("failure render = %q", rendered)
			}
		})
	}

	model, err := New(Options{
		Profile: screenProfile(),
		Width:   120,
		Height:  36,
		Theme:   noColorScenarioTheme(t, false),
	})
	if err != nil {
		t.Fatalf("New without Planner: %v", err)
	}
	if err := model.Generate(context.Background(), nil); !domainerr.IsCode(
		err,
		domainerr.CodeDependencyUnavailable,
	) {
		t.Fatalf("missing Planner error = %v", err)
	}
}

func TestFocusHelpResizeAndResponsiveSnapshots(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		width     int
		height    int
		ascii     bool
		required  []string
		forbidden []string
	}{
		{
			name: "wide_160x48", width: 160, height: 48,
			required: []string{
				"NEW SCENARIO",
				"PLAN DETAIL",
				"Assess confirmed project depth",
				"fact-go",
			},
		},
		{
			name: "split_120x36", width: 120, height: 36,
			required: []string{
				"NEW SCENARIO",
				"PLAN DETAIL",
				"项目深挖",
				"coach policy",
			},
		},
		{
			name: "narrow_80x24_ascii", width: 80, height: 24, ascii: true,
			required: []string{
				"+",
				"NEW SCENARIO",
				"selected intent",
				"fact-go",
			},
			forbidden: []string{"┌", "✓"},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			profile := screenProfile()
			planner := newCorePlanner(
				t,
				newMemoryScenarioRepository(profile),
				&screenGenerator{},
			)
			model := newScenarioModel(
				t,
				planner,
				&memorySessionStore{},
				profile,
				test.width,
				test.height,
				test.ascii,
			)
			if err := model.SelectTemplate("project_deep_dive"); err != nil {
				t.Fatalf("SelectTemplate: %v", err)
			}
			if err := model.Generate(context.Background(), nil); err != nil {
				t.Fatalf("Generate: %v", err)
			}
			for index := 0; index < 4; index++ {
				model.HandleKey("tab")
			}
			if model.focus.Active() != focusPlan {
				t.Fatalf("focus = %q, want Run Plan", model.focus.Active())
			}
			model.HandleKey("down")
			selectedBefore := model.selected
			model.HandleKey("?")
			model.Resize(test.width, test.height)
			model.HandleKey("esc")
			if model.focus.Active() != focusPlan ||
				model.selected != selectedBefore {
				t.Fatal("help/resize did not restore exact plan focus")
			}

			rendered, err := model.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertScenarioGeometry(
				t,
				rendered,
				test.width,
				test.height,
			)
			for _, expected := range test.required {
				if !strings.Contains(rendered, expected) {
					t.Errorf("snapshot missing %q", expected)
				}
			}
			for _, forbidden := range test.forbidden {
				if strings.Contains(rendered, forbidden) {
					t.Errorf("snapshot contains forbidden %q", forbidden)
				}
			}
		})
	}
}

func TestConfirmedProfileToScenarioToSQLiteSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	profile := screenProfile()
	if err := store.SaveProfileAggregate(ctx, profile); err != nil {
		_ = store.Close()
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	planner := newCorePlanner(t, store, &screenGenerator{})
	model := newScenarioModel(
		t,
		planner,
		store,
		profile,
		120,
		36,
		false,
	)
	if err := model.Generate(ctx, nil); err != nil {
		_ = store.Close()
		t.Fatalf("Generate: %v", err)
	}
	action, err := model.Start(ctx, nil)
	if err != nil {
		_ = store.Close()
		t.Fatalf("Start: %v", err)
	}
	if action.Destination != DestinationInterview {
		_ = store.Close()
		t.Fatalf("action = %#v", action)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	session, found, err := reopened.GetSession(ctx, action.SessionID)
	if err != nil || !found {
		t.Fatalf("GetSession: found=%v err=%v", found, err)
	}
	storedScenario, found, err := reopened.GetScenario(
		ctx,
		action.ScenarioID,
	)
	if err != nil || !found {
		t.Fatalf("GetScenario: found=%v err=%v", found, err)
	}
	if session.ScenarioID != action.ScenarioID ||
		session.Status != db.SessionActive ||
		storedScenario.PromptVersion == "" ||
		len(storedScenario.Questions) != 3 {
		t.Fatalf(
			"restored session=%#v scenario=%#v",
			session,
			storedScenario,
		)
	}
}

type screenGenerator struct {
	err     error
	invalid bool
}

func (generator *screenGenerator) Generate(
	_ context.Context,
	input corescenario.GenerationInput,
) (corescenario.GeneratedPlan, error) {
	if generator.err != nil {
		return corescenario.GeneratedPlan{}, generator.err
	}
	if generator.invalid {
		return corescenario.GeneratedPlan{
			Scenario:   contracts.Scenario{},
			JDMappings: []corescenario.JDMapping{},
		}, nil
	}
	return screenGeneratedPlan(input), nil
}

func screenGeneratedPlan(
	input corescenario.GenerationInput,
) corescenario.GeneratedPlan {
	questions := []contracts.ScenarioQuestion{
		{
			ID:               "Q1",
			Prompt:           "Explain the Go service architecture.",
			Intent:           "Assess confirmed project depth",
			EstimatedSeconds: 300,
			Rubric:           []string{"Explains one owned decision"},
			EvidenceIDs:      []contracts.EvidenceID{"fact-go"},
			MaxFollowUps:     2,
			EndCondition:     "One evidence-backed decision is explained",
		},
		{
			ID:               "Q2",
			Prompt:           "How did you handle a production failure?",
			Intent:           "Assess failure recovery",
			EstimatedSeconds: 300,
			Rubric:           []string{"Explains diagnosis and recovery"},
			EvidenceIDs:      []contracts.EvidenceID{"fact-go"},
			MaxFollowUps:     2,
			EndCondition:     "A recovery sequence is explained",
		},
		{
			ID:               "Q3",
			Prompt:           "Clarify an unfamiliar system constraint.",
			Intent:           "Assess general clarification",
			EstimatedSeconds: 300,
			Rubric:           []string{"Clarifies impact before solution"},
			EvidenceIDs:      []contracts.EvidenceID{},
			Generic:          true,
			MaxFollowUps:     2,
			EndCondition:     "A general clarification sequence is explained",
		},
	}
	return corescenario.GeneratedPlan{
		Scenario: contracts.Scenario{
			Template:          input.Template.ID,
			Mode:              input.Mode,
			TimeBudgetSeconds: input.TimeBudget,
			PromptVersion:     input.PromptVersion,
			Questions:         questions,
		},
		JDMappings: []corescenario.JDMapping{},
	}
}

type memoryScenarioRepository struct {
	profile   coreprofile.Aggregate
	scenarios map[string]contracts.Scenario
}

func newMemoryScenarioRepository(
	profile coreprofile.Aggregate,
) *memoryScenarioRepository {
	return &memoryScenarioRepository{
		profile:   cloneProfile(profile),
		scenarios: make(map[string]contracts.Scenario),
	}
}

func (repository *memoryScenarioRepository) SaveScenario(
	_ context.Context,
	id string,
	_ string,
	value contracts.Scenario,
	_ time.Time,
) error {
	repository.scenarios[id] = cloneScenarioContract(value)
	return nil
}

func (repository *memoryScenarioRepository) GetScenario(
	_ context.Context,
	id string,
) (contracts.Scenario, bool, error) {
	value, found := repository.scenarios[id]
	return cloneScenarioContract(value), found, nil
}

func (repository *memoryScenarioRepository) GetProfileAggregate(
	context.Context,
	string,
) (coreprofile.Aggregate, bool, error) {
	return cloneProfile(repository.profile), true, nil
}

type memorySessionStore struct {
	session db.Session
	err     error
}

func (store *memorySessionStore) CreateSession(
	_ context.Context,
	session db.Session,
) error {
	if store.err != nil {
		return store.err
	}
	store.session = session
	return nil
}

func newCorePlanner(
	t *testing.T,
	repository corescenario.Repository,
	generator corescenario.Generator,
) *corescenario.Service {
	t.Helper()
	service, err := corescenario.NewService(
		repository,
		generator,
		func() time.Time {
			return time.Date(2026, 7, 30, 15, 30, 0, 0, time.UTC)
		},
	)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return service
}

func newScenarioModel(
	t *testing.T,
	planner Planner,
	sessions SessionStore,
	profile coreprofile.Aggregate,
	width int,
	height int,
	ascii bool,
) *Model {
	t.Helper()
	model, err := New(Options{
		PlanID:   "scenario-screen",
		Profile:  profile,
		Planner:  planner,
		Sessions: sessions,
		Now: func() time.Time {
			return time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
		},
		NextSessionID: func() string { return "session-screen" },
		Width:         width,
		Height:        height,
		Theme:         noColorScenarioTheme(t, ascii),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func screenProfile() coreprofile.Aggregate {
	sourceText := "Built a Go payment service and improved latency."
	confirmedAt := time.Date(2026, 7, 30, 15, 0, 0, 0, time.UTC)
	return coreprofile.Aggregate{
		ID: "profile-screen",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID:    "fact-go",
				Field: "project",
				Value: "Go payment service",
				SourceSpan: contracts.SourceSpan{
					Start: 0,
					End:   len(sourceText),
					Text:  sourceText,
				},
			}},
			Inferences: []contracts.ProfileInference{},
			Projects:   []string{"Go payment service"},
			Skills:     []string{"Go"},
		},
		Metadata: coreprofile.Metadata{
			Source: coreprofile.Source{
				Kind: coreprofile.SourcePaste,
				Name: "pasted-resume.txt",
				Text: sourceText,
			},
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
			CreatedAt:          confirmedAt.Add(-time.Hour),
			UpdatedAt:          confirmedAt,
		},
		ConfirmedAt: &confirmedAt,
	}
}

func noColorScenarioTheme(t *testing.T, ascii bool) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode:         theme.Auto,
		ColorMode:    theme.NoColor,
		UseASCII:     ascii,
		ReduceMotion: true,
	})
	if err != nil {
		t.Fatalf("Resolve theme: %v", err)
	}
	return current
}

func assertScenarioGeometry(
	t *testing.T,
	rendered string,
	width int,
	height int,
) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("rows=%d, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("row %d width=%d, want %d", index, got, width)
		}
	}
}

func cloneScenarioContract(value contracts.Scenario) contracts.Scenario {
	questions := value.Questions
	value.Questions = make(
		[]contracts.ScenarioQuestion,
		len(questions),
	)
	for index, question := range questions {
		value.Questions[index] = cloneQuestion(question)
	}
	return value
}

func (model *Model) focusTarget(target string) {
	for index := 0; index < 5 && model.focus.Active() != target; index++ {
		model.focus.Next()
	}
}
