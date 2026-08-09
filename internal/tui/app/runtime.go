package app

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	llmadapter "github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/adapters/resume"
	runneradapter "github.com/interviewcraft/interviewcraft/internal/adapters/runner"
	"github.com/interviewcraft/interviewcraft/internal/config"
	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/evaluation"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	corescenario "github.com/interviewcraft/interviewcraft/internal/core/scenario"
	"github.com/interviewcraft/interviewcraft/internal/core/transfer"
	"github.com/interviewcraft/interviewcraft/internal/credentials"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/coding"
	interviewscreen "github.com/interviewcraft/interviewcraft/internal/tui/screens/interview"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/profile"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/report"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/scenario"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/settings"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/training"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// RuntimeFactory is the dependency container for the complete P-01 to P-07
// journey. Optional Provider and Runner services remain explicitly disabled.
type RuntimeFactory struct {
	runtime    config.Runtime
	configPath string
	store      *db.Store
	theme      theme.Theme
	profiles   *coreprofile.Service
	scenarios  *corescenario.Service
	interviews *coreinterview.Service
	coach      *corecoach.Service
	coding     *corecoding.Service
	reports    *corereport.Service
	evaluator  *evaluation.Service
	transfer   *transfer.Service
}

// NewRuntimeFactory assembles SQLite, LLM, Profile, Scenario, Interview,
// Coach, Coding, Report, Transfer and optional Runner services once.
func NewRuntimeFactory(
	runtime config.Runtime,
	configPath string,
	store *db.Store,
	current theme.Theme,
) (*RuntimeFactory, error) {
	if store == nil {
		return nil, errors.New("runtime store is nil")
	}
	secretResolver, err := credentials.NewResolver(
		runtime.DataDir, os.LookupEnv, credentials.SystemStore{},
	)
	if err != nil {
		return nil, err
	}
	var generator *llmadapter.Client
	if strings.TrimSpace(runtime.LLM.Provider) != "" {
		client, err := llmadapter.New(runtime.LLM, llmadapter.Options{
			ResolveSecret: secretResolver.Resolve,
		})
		if err != nil {
			return nil, err
		}
		generator = client
	}
	profiles := coreprofile.NewService(
		store,
		resume.NewProfileStructurer(generator),
		nil,
	)
	scenarios, err := corescenario.NewService(
		store,
		llmadapter.NewScenarioPlanner(generator),
		nil,
	)
	if err != nil {
		return nil, err
	}
	interviews := coreinterview.NewService(
		store,
		llmadapter.NewInterviewer(generator),
		coreinterview.Options{},
	)
	coach := corecoach.NewService(
		store,
		llmadapter.NewCoach(generator),
		corecoach.Options{},
	)
	var codeRunner corecoding.Runner
	if runtime.RunnerMode == config.RunnerDocker {
		value, runnerErr := runneradapter.New(
			runneradapter.DefaultConfig(), runneradapter.Options{},
		)
		if runnerErr != nil {
			return nil, runnerErr
		}
		codeRunner = value
	}
	codingService, err := corecoding.NewService(store, corecoding.Options{
		Runner: codeRunner,
	})
	if err != nil {
		return nil, err
	}
	reports := corereport.NewService(store, corereport.Options{})
	return &RuntimeFactory{
		runtime: runtime, configPath: configPath, store: store, theme: current,
		profiles: profiles, scenarios: scenarios, interviews: interviews,
		coach: coach, coding: codingService, reports: reports,
		evaluator: evaluation.NewService(
			store, llmadapter.NewEvaluator(generator), evaluation.Options{},
		),
		transfer: transfer.NewService(store.Paths().Database, transfer.Options{}),
	}, nil
}

// Open builds only the selected screen; shared services and persisted IDs are
// retained by the root controller.
func (factory *RuntimeFactory) Open(route Route, width, height int) (Screen, error) {
	switch route.Page {
	case PageTraining:
		model, err := training.New(factory.store, width, height, factory.theme)
		return &trainingAdapter{model: model}, err
	case PageProfile:
		model, err := profile.New(profile.Options{
			ProfileID: route.ProfileID, Extractor: resume.Extractor{},
			Profiles: factory.profiles, Width: width, Height: height, Theme: factory.theme,
		})
		return &profileAdapter{model: model}, err
	case PageScenario:
		aggregate, _, err := factory.profiles.Load(context.Background(), route.ProfileID)
		if err != nil {
			return nil, err
		}
		model, err := scenario.New(scenario.Options{
			PlanID: route.PracticeID, Profile: aggregate,
			JD: aggregate.Candidate.TargetRole, Planner: factory.scenarios,
			Sessions: factory.store, Width: width, Height: height, Theme: factory.theme,
		})
		return &scenarioAdapter{model: model}, err
	case PageInterview:
		model, err := interviewscreen.New(interviewscreen.Options{
			SessionID: route.SessionID, Room: factory.interviews,
			Coach: factory.coach, Width: width, Height: height, Theme: factory.theme,
		})
		return &interviewAdapter{model: model}, err
	case PageCoding:
		model, err := coding.New(coding.Options{
			SessionID: route.SessionID, QuestionID: route.QuestionID,
			Service: factory.coding, Coach: factory.coach,
			Width: width, Height: height, Theme: factory.theme,
		})
		return &codingAdapter{model: model}, err
	case PageReport:
		model, err := report.New(report.Options{
			SessionID: route.SessionID, ReportID: route.ReportID,
			Reports: factory.reports, Evaluator: factory.evaluator,
			Deleter: factory.store, Width: width, Height: height, Theme: factory.theme,
		})
		return &reportAdapter{model: model}, err
	case PageSettings:
		model, err := settings.New(settings.Options{
			Runtime: factory.runtime,
			TesterFactory: func(value config.LLM) (settings.ConnectionTester, error) {
				resolver, resolverErr := credentials.NewResolver(
					factory.runtime.DataDir, os.LookupEnv, credentials.SystemStore{},
				)
				if resolverErr != nil {
					return nil, resolverErr
				}
				return llmadapter.New(value, llmadapter.Options{ResolveSecret: resolver.Resolve})
			},
			SaveConfig: func(value config.Runtime) error {
				return config.SaveAtomic(factory.configPath, value)
			},
			Data: factory.transfer, Width: width, Height: height, Theme: factory.theme,
		})
		return &settingsAdapter{model: model}, err
	default:
		return nil, errors.New("unknown application page: " + string(route.Page))
	}
}

type trainingAdapter struct{ model *training.Model }

func (screen *trainingAdapter) Render() (string, error) { return screen.model.Render() }
func (screen *trainingAdapter) Resize(w, h int)         { screen.model.Resize(w, h) }
func (screen *trainingAdapter) InsertText(string) error { return nil }
func (screen *trainingAdapter) Backspace() error        { return nil }
func (screen *trainingAdapter) Tick(time.Time)          {}
func (screen *trainingAdapter) ConcurrentSafe() bool    { return false }
func (screen *trainingAdapter) Load(ctx context.Context) error {
	screen.model.Load(ctx, nil)
	return stateError(screen.model.State().Err)
}
func (screen *trainingAdapter) HandleKey(key string) Action {
	value := screen.model.HandleKey(key)
	switch value.Destination {
	case training.DestinationProfile:
		return Action{Route: Route{Page: PageProfile}}
	case training.DestinationScenario:
		return Action{Route: Route{Page: PageScenario, SessionID: value.SessionID, ReportID: value.ReportID, PracticeID: value.PracticeID}}
	case training.DestinationInterview:
		return Action{Route: Route{Page: PageInterview, SessionID: value.SessionID}}
	case training.DestinationReport:
		return Action{Route: Route{Page: PageReport, SessionID: value.SessionID, ReportID: value.ReportID}}
	case training.DestinationSettings:
		return Action{Route: Route{Page: PageSettings}}
	case training.DestinationQuit:
		return Action{Quit: true}
	case training.DestinationTraining:
		return Action{Task: loadScreen(screen)}
	default:
		return Action{}
	}
}

type profileAdapter struct{ model *profile.Model }

func (screen *profileAdapter) Render() (string, error)        { return screen.model.Render() }
func (screen *profileAdapter) Resize(w, h int)                { screen.model.Resize(w, h) }
func (screen *profileAdapter) InsertText(v string) error      { return screen.model.InsertText(v) }
func (screen *profileAdapter) Backspace() error               { return screen.model.Backspace() }
func (screen *profileAdapter) Tick(time.Time)                 {}
func (screen *profileAdapter) ConcurrentSafe() bool           { return false }
func (screen *profileAdapter) Load(ctx context.Context) error { return screen.model.Load(ctx, nil) }
func (screen *profileAdapter) HandleKey(key string) Action {
	value := screen.model.HandleKey(key)
	switch value.Destination {
	case profile.DestinationTraining:
		return Action{Route: Route{Page: PageTraining}}
	case profile.DestinationScenario:
		return Action{Route: Route{Page: PageScenario}}
	case profile.DestinationSettings:
		return Action{Route: Route{Page: PageSettings}}
	case profile.DestinationQuit:
		return Action{Quit: true}
	}
	var task Task
	switch value.Intent {
	case profile.IntentParse:
		task = simpleTask(func(ctx context.Context) error { return screen.model.Parse(ctx, nil) })
	case profile.IntentSave:
		task = func(ctx context.Context) (Route, error) {
			_, err := screen.model.SaveAndContinue(ctx, nil)
			return Route{Page: PageScenario}, err
		}
	case profile.IntentApplyEdit:
		task = simpleTask(screen.model.ApplyEdit)
	case profile.IntentToggleLock:
		task = simpleTask(screen.model.ToggleSelectedLock)
	case profile.IntentDelete:
		task = simpleTask(screen.model.DeleteSelected)
	}
	return Action{Task: task}
}

type scenarioAdapter struct{ model *scenario.Model }

func (screen *scenarioAdapter) Render() (string, error)    { return screen.model.Render() }
func (screen *scenarioAdapter) Resize(w, h int)            { screen.model.Resize(w, h) }
func (screen *scenarioAdapter) InsertText(string) error    { return nil }
func (screen *scenarioAdapter) Backspace() error           { return nil }
func (screen *scenarioAdapter) Tick(time.Time)             {}
func (screen *scenarioAdapter) ConcurrentSafe() bool       { return false }
func (screen *scenarioAdapter) Load(context.Context) error { return nil }
func (screen *scenarioAdapter) HandleKey(key string) Action {
	value := screen.model.HandleKey(key)
	switch value.Destination {
	case scenario.DestinationTraining:
		return Action{Route: Route{Page: PageTraining}}
	case scenario.DestinationProfile:
		return Action{Route: Route{Page: PageProfile}}
	case scenario.DestinationSettings:
		return Action{Route: Route{Page: PageSettings}}
	case scenario.DestinationInterview:
		return Action{Route: Route{Page: PageInterview, SessionID: value.SessionID, ScenarioID: value.ScenarioID}}
	case scenario.DestinationQuit:
		return Action{Quit: true}
	}
	var task Task
	switch value.Intent {
	case scenario.IntentGenerate:
		task = simpleTask(func(ctx context.Context) error { return screen.model.Generate(ctx, nil) })
	case scenario.IntentDelete:
		task = simpleTask(func(context.Context) error { return screen.model.DeleteSelected() })
	case scenario.IntentStart:
		task = func(ctx context.Context) (Route, error) {
			next, err := screen.model.Start(ctx, nil)
			return Route{Page: PageInterview, SessionID: next.SessionID, ScenarioID: next.ScenarioID}, err
		}
	}
	return Action{Task: task}
}

type interviewAdapter struct{ model *interviewscreen.Model }

func (screen *interviewAdapter) Render() (string, error)        { return screen.model.Render() }
func (screen *interviewAdapter) Resize(w, h int)                { screen.model.Resize(w, h) }
func (screen *interviewAdapter) Tick(now time.Time)             { screen.model.Tick(now) }
func (screen *interviewAdapter) ConcurrentSafe() bool           { return true }
func (screen *interviewAdapter) Load(ctx context.Context) error { return screen.model.Load(ctx, nil) }
func (screen *interviewAdapter) InsertText(value string) error {
	if screen.model.ActiveTextTarget() == interviewscreen.TextTargetCoach {
		return screen.model.UpdateCoachDraft(screen.model.CoachDraft() + value)
	}
	if screen.model.ActiveTextTarget() != interviewscreen.TextTargetComposer {
		return nil
	}
	draft := []rune(screen.model.Draft())
	cursor := min(screen.model.DraftCursor(), len(draft))
	updated := string(append(append(append([]rune{}, draft[:cursor]...), []rune(value)...), draft[cursor:]...))
	if err := screen.model.UpdateDraft(context.Background(), updated); err != nil {
		return err
	}
	return screen.model.SetDraftCursor(cursor + utf8.RuneCountInString(value))
}
func (screen *interviewAdapter) Backspace() error {
	if screen.model.ActiveTextTarget() == interviewscreen.TextTargetCoach {
		value := []rune(screen.model.CoachDraft())
		if len(value) == 0 {
			return nil
		}
		return screen.model.UpdateCoachDraft(string(value[:len(value)-1]))
	}
	if screen.model.ActiveTextTarget() != interviewscreen.TextTargetComposer {
		return nil
	}
	draft := []rune(screen.model.Draft())
	cursor := min(screen.model.DraftCursor(), len(draft))
	if cursor == 0 {
		return nil
	}
	updated := string(append(append([]rune{}, draft[:cursor-1]...), draft[cursor:]...))
	if err := screen.model.UpdateDraft(context.Background(), updated); err != nil {
		return err
	}
	return screen.model.SetDraftCursor(cursor - 1)
}
func (screen *interviewAdapter) HandleKey(key string) Action {
	if key == "r" {
		snapshot := screen.model.Snapshot()
		if snapshot.Phase == coreinterview.PhaseCompleted {
			return Action{Route: Route{Page: PageReport, SessionID: snapshot.Session.ID}}
		}
	}
	if key == "ctrl+k" {
		snapshot := screen.model.Snapshot()
		if snapshot.CurrentQuestion != nil {
			return Action{Route: Route{Page: PageCoding, SessionID: snapshot.Session.ID, QuestionID: snapshot.CurrentQuestion.ID}}
		}
	}
	value := screen.model.HandleKey(key)
	if value.Destination == interviewscreen.DestinationTraining {
		return Action{Route: Route{Page: PageTraining}}
	}
	var task Task
	switch value.Intent {
	case interviewscreen.IntentSubmit:
		task = simpleTask(func(ctx context.Context) error { return screen.model.Submit(ctx, nil) })
	case interviewscreen.IntentRetry:
		task = simpleTask(func(ctx context.Context) error { return screen.model.Retry(ctx, nil) })
	case interviewscreen.IntentCancelWait:
		screen.model.CancelWaiting()
	case interviewscreen.IntentPause, interviewscreen.IntentResume:
		task = simpleTask(screen.model.TogglePause)
	case interviewscreen.IntentRequestEndQuestion:
		task = simpleTask(func(ctx context.Context) error { return screen.model.RequestEnd(ctx, coreinterview.EndQuestion) })
	case interviewscreen.IntentRequestEndSession:
		task = simpleTask(func(ctx context.Context) error { return screen.model.RequestEnd(ctx, coreinterview.EndSession) })
	case interviewscreen.IntentCancelEnd:
		task = simpleTask(screen.model.CancelEnd)
	case interviewscreen.IntentConfirmEnd:
		task = simpleTask(screen.model.ConfirmEnd)
	case interviewscreen.IntentCoachAsk, interviewscreen.IntentCoachAskPaused:
		task = simpleTask(func(ctx context.Context) error {
			return screen.model.AskCoach(ctx, value.CoachIntent, value.Intent == interviewscreen.IntentCoachAskPaused)
		})
	case interviewscreen.IntentCoachRetry:
		task = simpleTask(screen.model.RetryCoach)
	case interviewscreen.IntentCoachMark:
		task = simpleTask(func(ctx context.Context) error { return screen.model.MarkCoachOutcome(ctx, value.CoachOutcome) })
	}
	return Action{Task: task}
}

type codingAdapter struct{ model *coding.Model }

func (screen *codingAdapter) Render() (string, error) { return screen.model.Render() }
func (screen *codingAdapter) Resize(w, h int)         { screen.model.Resize(w, h) }
func (screen *codingAdapter) InsertText(v string) error {
	if screen.model.ActiveFocus() != "editor" {
		return nil
	}
	return screen.model.InsertText(v)
}
func (screen *codingAdapter) Backspace() error {
	if screen.model.ActiveFocus() != "editor" {
		return nil
	}
	return screen.model.Backspace()
}
func (screen *codingAdapter) Tick(now time.Time)             { screen.model.Tick(now) }
func (screen *codingAdapter) ConcurrentSafe() bool           { return true }
func (screen *codingAdapter) Load(ctx context.Context) error { return screen.model.Load(ctx, nil) }
func (screen *codingAdapter) HandleKey(key string) Action {
	value := screen.model.HandleKey(key)
	if value.Destination == coding.DestinationInterview {
		return Action{Route: Route{Page: PageInterview}}
	}
	if value.Intent != coding.IntentNone {
		return Action{Task: simpleTask(func(ctx context.Context) error { return screen.model.Execute(ctx, value, nil) })}
	}
	return Action{}
}

type reportAdapter struct{ model *report.Model }

func (screen *reportAdapter) Render() (string, error) { return screen.model.Render() }
func (screen *reportAdapter) Resize(w, h int)         { screen.model.Resize(w, h) }
func (screen *reportAdapter) InsertText(string) error { return nil }
func (screen *reportAdapter) Backspace() error        { return nil }
func (screen *reportAdapter) Tick(time.Time)          {}
func (screen *reportAdapter) ConcurrentSafe() bool    { return false }
func (screen *reportAdapter) Load(ctx context.Context) error {
	screen.model.Load(ctx, nil)
	return stateError(screen.model.State().Err)
}
func (screen *reportAdapter) HandleKey(key string) Action {
	value := screen.model.HandleKey(key)
	switch value.Destination {
	case report.DestinationTraining:
		return Action{Route: Route{Page: PageTraining}}
	case report.DestinationScenario:
		return Action{Route: Route{Page: PageScenario, SessionID: value.SessionID, ReportID: value.ReportID}}
	case report.DestinationQuit:
		return Action{Quit: true}
	}
	if value.Intent == report.IntentDeleteReport {
		return Action{Task: simpleTask(func(ctx context.Context) error {
			screen.model.Delete(ctx, nil)
			return stateError(screen.model.State().Err)
		})}
	}
	return Action{}
}

type settingsAdapter struct{ model *settings.Model }

func (screen *settingsAdapter) Render() (string, error) { return screen.model.Render() }
func (screen *settingsAdapter) Resize(w, h int)         { screen.model.Resize(w, h) }
func (screen *settingsAdapter) InsertText(string) error { return nil }
func (screen *settingsAdapter) Backspace() error        { return nil }
func (screen *settingsAdapter) Tick(time.Time)          {}
func (screen *settingsAdapter) ConcurrentSafe() bool    { return false }
func (screen *settingsAdapter) Load(ctx context.Context) error {
	screen.model.LoadData(ctx, nil)
	return stateError(screen.model.DataState().Err)
}
func (screen *settingsAdapter) HandleKey(key string) Action {
	destination := screen.model.HandleKey(key)
	switch destination {
	case settings.DestinationTraining:
		return Action{Route: Route{Page: PageTraining}}
	case settings.DestinationProfile:
		return Action{Route: Route{Page: PageProfile}}
	case settings.DestinationReport:
		return Action{Route: Route{Page: PageReport}}
	case settings.DestinationQuit:
		return Action{Quit: true}
	case settings.DestinationSettings:
		return Action{Task: simpleTask(func(ctx context.Context) error {
			screen.model.TestConnection(ctx, nil)
			return stateError(screen.model.ConnectionState().Err)
		})}
	case settings.DestinationDataReload:
		return Action{Task: loadScreen(screen)}
	case settings.DestinationDataDelete:
		return Action{Task: simpleTask(func(ctx context.Context) error {
			_, err := screen.model.DeleteData(ctx, nil)
			return err
		})}
	}
	return Action{}
}

func simpleTask(run func(context.Context) error) Task {
	if run == nil {
		return nil
	}
	return func(ctx context.Context) (Route, error) { return Route{}, run(ctx) }
}

func stateError(errValue *domainerr.Error) error {
	if errValue == nil {
		return nil
	}
	return errValue
}
