// Package scenario implements the P-03 scenario factory.
package scenario

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
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

const (
	focusTemplate   = "template"
	focusDifficulty = "difficulty"
	focusMode       = "mode"
	focusDuration   = "duration"
	focusPlan       = "run-plan"
	focusHelp       = "help"
)

var (
	difficultyChoices = []Difficulty{
		DifficultyFoundation,
		DifficultyIntermediate,
		DifficultyStretch,
	}
	modeChoices = []contracts.ScenarioMode{
		contracts.ScenarioStrict,
		contracts.ScenarioStandard,
		contracts.ScenarioCoach,
	}
	durationChoices = []int{15 * 60, 20 * 60, 30 * 60, 45 * 60}
)

// Difficulty is a user-controlled P-03 planning preference. The current core
// Scenario contract does not persist a separate difficulty field, so P-03
// keeps it with the editable factory controls and locks it with the session.
type Difficulty string

const (
	DifficultyFoundation   Difficulty = "foundation"
	DifficultyIntermediate Difficulty = "intermediate"
	DifficultyStretch      Difficulty = "stretch"
)

// Stage identifies one visible scenario-factory operation.
type Stage string

const (
	StageIdle       Stage = "idle"
	StageGenerating Stage = "generating"
	StageConfirming Stage = "confirming"
	StageStarting   Stage = "starting"
	StageReady      Stage = "ready"
)

// Progress is the typed screen lifecycle payload.
type Progress struct {
	Stage   Stage
	Message string
}

// Observer receives generation, confirmation, and session-start states.
type Observer func(async.State[Progress])

// Planner is the P-03 boundary over the core Scenario Planner.
type Planner interface {
	Templates() []corescenario.Template
	Generate(
		context.Context,
		corescenario.Request,
		*corescenario.Plan,
		corescenario.Observer,
	) (corescenario.Plan, error)
	EditQuestion(
		corescenario.Plan,
		contracts.ScenarioQuestion,
	) (corescenario.Plan, error)
	UpdateRules(
		corescenario.Plan,
		contracts.ScenarioMode,
		int,
	) (corescenario.Plan, error)
	Confirm(context.Context, corescenario.Plan) (corescenario.Plan, error)
}

// SessionStore is the minimal command boundary needed to begin a session.
type SessionStore interface {
	CreateSession(context.Context, db.Session) error
}

// Destination is one global navigation target.
type Destination string

const (
	DestinationNone      Destination = ""
	DestinationTraining  Destination = "training"
	DestinationProfile   Destination = "profile"
	DestinationInterview Destination = "interview"
	DestinationSettings  Destination = "settings"
	DestinationQuit      Destination = "quit"
)

// Intent tells the application controller which command to execute.
type Intent string

const (
	IntentNone     Intent = ""
	IntentGenerate Intent = "generate"
	IntentReplace  Intent = "replace"
	IntentDelete   Intent = "delete"
	IntentStart    Intent = "start"
)

// Action combines an in-screen command with optional navigation.
type Action struct {
	Intent      Intent
	Destination Destination
	SessionID   string
	ScenarioID  string
}

// Options constructs a Scenario factory from a confirmed profile.
type Options struct {
	PlanID        string
	Profile       coreprofile.Aggregate
	JD            string
	Planner       Planner
	Sessions      SessionStore
	Now           func() time.Time
	NextSessionID func() string
	Width         int
	Height        int
	Theme         theme.Theme
}

// Model owns P-03 controls, plan edits, focus, and start state.
type Model struct {
	planID        string
	profile       coreprofile.Aggregate
	jd            string
	planner       Planner
	sessions      SessionStore
	templates     []corescenario.Template
	templateIndex int
	difficulty    Difficulty
	mode          contracts.ScenarioMode
	duration      int
	plan          *corescenario.Plan
	deletedIDs    map[string]struct{}
	operation     async.State[Progress]
	focus         *layout.FocusModel
	selected      int
	helpOpen      bool
	now           func() time.Time
	nextSessionID func() string
	pendingID     string
	startedID     string

	Width  int
	Height int
	Theme  theme.Theme
}

// New creates an idle P-03 screen without starting Provider or storage work.
func New(options Options) (*Model, error) {
	focus, err := layout.NewFocusModel(
		focusTemplate,
		focusDifficulty,
		focusMode,
		focusDuration,
		focusPlan,
	)
	if err != nil {
		return nil, err
	}
	planID := strings.TrimSpace(options.PlanID)
	if planID == "" {
		planID = "scenario-main"
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	nextSessionID := options.NextSessionID
	if nextSessionID == nil {
		nextSessionID = randomSessionID
	}
	templates := []corescenario.Template{}
	if options.Planner != nil {
		templates = options.Planner.Templates()
	}
	model := &Model{
		planID:     planID,
		profile:    cloneProfile(options.Profile),
		jd:         options.JD,
		planner:    options.Planner,
		sessions:   options.Sessions,
		templates:  cloneTemplates(templates),
		difficulty: DifficultyIntermediate,
		mode:       contracts.ScenarioStandard,
		duration:   20 * 60,
		deletedIDs: make(map[string]struct{}),
		operation: async.NewSucceeded(Progress{
			Stage:   StageIdle,
			Message: "还没有场景计划",
		}),
		focus:         focus,
		now:           now,
		nextSessionID: nextSessionID,
		Width:         options.Width,
		Height:        options.Height,
		Theme:         options.Theme,
	}
	if len(model.templates) > 0 {
		model.mode = model.templates[0].DefaultMode
		model.duration = nearestDuration(
			model.templates[0].DefaultTimeBudgetSeconds,
		)
	}
	return model, nil
}

// State returns the current typed operation lifecycle.
func (model *Model) State() async.State[Progress] {
	if model == nil {
		return async.State[Progress]{}
	}
	return model.operation
}

// Plan returns a defensive copy of the current editable or locked plan.
func (model *Model) Plan() (corescenario.Plan, bool) {
	if model == nil || model.plan == nil {
		return corescenario.Plan{}, false
	}
	return clonePlan(*model.plan), true
}

// SelectedSettings returns the factory controls currently shown in the
// persistent summary row.
func (model *Model) SelectedSettings() (
	string,
	Difficulty,
	contracts.ScenarioMode,
	int,
) {
	if model == nil {
		return "", "", "", 0
	}
	return model.templateID(), model.difficulty, model.mode, model.duration
}

// Resize changes geometry without clearing plan edits, selection, or focus.
func (model *Model) Resize(width, height int) {
	if model == nil {
		return
	}
	model.Width = width
	model.Height = height
}

// SelectTemplate chooses one embedded template before a plan is generated.
func (model *Model) SelectTemplate(id string) error {
	if model == nil {
		return errors.New("scenario model is nil")
	}
	if model.plan != nil {
		return model.recordFailure(factoryError(
			domainerr.CodePolicyDenied,
			"当前计划已包含手工编辑，不能直接切换模板。",
			"返回画像页创建另一份场景，或继续编辑当前计划。",
		))
	}
	index := slices.IndexFunc(
		model.templates,
		func(template corescenario.Template) bool {
			return template.ID == id
		},
	)
	if index < 0 {
		return model.recordFailure(factoryError(
			domainerr.CodeValidation,
			"选择的场景模板不存在。",
			"选择内置的六类场景模板。",
		))
	}
	model.templateIndex = index
	model.mode = model.templates[index].DefaultMode
	model.duration = nearestDuration(
		model.templates[index].DefaultTimeBudgetSeconds,
	)
	return nil
}

// UpdateRules edits difficulty, Coach mode, and duration without changing
// manually replaced questions.
func (model *Model) UpdateRules(
	difficulty Difficulty,
	mode contracts.ScenarioMode,
	duration int,
) error {
	if model == nil {
		return errors.New("scenario model is nil")
	}
	if !slices.Contains(difficultyChoices, difficulty) ||
		!slices.Contains(modeChoices, mode) ||
		duration <= 0 {
		return model.recordFailure(factoryError(
			domainerr.CodeValidation,
			"场景难度、模式或时长无效。",
			"选择有效难度、strict/standard/coach 和正数时长。",
		))
	}
	if model.plan != nil && model.plan.Locked {
		return model.recordFailure(factoryError(
			domainerr.CodePolicyDenied,
			"已开始场景的策略与版本不可修改。",
			"完成当前训练，或从画像页创建新场景。",
		))
	}
	if model.plan == nil {
		model.difficulty = difficulty
		model.mode = mode
		model.duration = duration
		return nil
	}
	if model.planner == nil {
		return model.recordFailure(unavailablePlanner())
	}
	updated, err := model.planner.UpdateRules(*model.plan, mode, duration)
	if err != nil {
		return model.recordFailure(factoryFailure(err))
	}
	value := clonePlan(updated)
	model.plan = &value
	model.difficulty = difficulty
	model.mode = updated.Scenario.Mode
	model.duration = updated.Scenario.TimeBudgetSeconds
	model.operation = async.NewSucceeded(Progress{
		Stage:   StageReady,
		Message: "场景难度、模式与时长已更新",
	})
	return nil
}

// Generate creates or refreshes the Run Plan. Core manual question/rule
// markers and the screen deletion set keep user edits across refreshes.
func (model *Model) Generate(ctx context.Context, observer Observer) error {
	if model == nil {
		return errors.New("scenario model is nil")
	}
	model.setState(async.NewPending[Progress](), observer)
	if model.planner == nil || len(model.templates) == 0 {
		return model.fail(unavailablePlanner(), observer)
	}
	if model.plan != nil && model.plan.Locked {
		return model.fail(factoryError(
			domainerr.CodePolicyDenied,
			"已确认场景不能重新生成。",
			"直接开始训练，或从画像页创建新场景。",
		), observer)
	}

	request := corescenario.Request{
		PlanID:            model.planID,
		Profile:           cloneProfile(model.profile),
		TemplateID:        model.templateID(),
		Mode:              model.mode,
		TimeBudgetSeconds: model.duration,
		JD:                model.jd,
	}
	var previous *corescenario.Plan
	if model.plan != nil {
		value := clonePlan(*model.plan)
		previous = &value
	}
	plan, err := model.planner.Generate(
		ctx,
		request,
		previous,
		func(state async.State[corescenario.Progress]) {
			switch state.Phase {
			case async.Pending:
				model.setState(async.NewPending[Progress](), observer)
			case async.Streaming:
				message := "正在创建场景计划"
				if state.Value != nil &&
					strings.TrimSpace(state.Value.Stage) != "" {
					message = state.Value.Stage
				}
				progress := Progress{
					Stage:   StageGenerating,
					Message: message,
				}
				model.setState(async.NewStreaming(&progress), observer)
			}
		},
	)
	if err != nil {
		return model.fail(factoryFailure(err), observer)
	}
	filtered, err := model.applyDeletions(plan)
	if err != nil {
		return model.fail(err, observer)
	}
	value := clonePlan(filtered)
	model.plan = &value
	model.mode = value.Scenario.Mode
	model.duration = value.Scenario.TimeBudgetSeconds
	model.selected = clampSelection(
		model.selected,
		len(value.Scenario.Questions),
	)
	model.setState(async.NewSucceeded(Progress{
		Stage:   StageReady,
		Message: fmt.Sprintf("场景计划 v%d 已生成", value.Revision),
	}), observer)
	return nil
}

// ReplaceSelected replaces the selected question while preserving its stable
// ID, allowing the core Planner to retain the manual edit on refresh.
func (model *Model) ReplaceSelected(
	replacement contracts.ScenarioQuestion,
) error {
	if model == nil {
		return errors.New("scenario model is nil")
	}
	if model.plan == nil || len(model.plan.Scenario.Questions) == 0 {
		return model.recordFailure(factoryError(
			domainerr.CodeValidation,
			"还没有可替换的场景题目。",
			"先按 [g] 生成场景计划。",
		))
	}
	if model.plan.Locked {
		return model.recordFailure(factoryError(
			domainerr.CodePolicyDenied,
			"已开始场景的题目不可替换。",
			"完成当前训练，或创建新场景。",
		))
	}
	if model.planner == nil {
		return model.recordFailure(unavailablePlanner())
	}
	current := model.plan.Scenario.Questions[model.selected]
	replacement.ID = current.ID
	updated, err := model.planner.EditQuestion(*model.plan, replacement)
	if err != nil {
		return model.recordFailure(factoryFailure(err))
	}
	delete(model.deletedIDs, replacement.ID)
	value := clonePlan(updated)
	model.plan = &value
	model.operation = async.NewSucceeded(Progress{
		Stage:   StageReady,
		Message: "替换题目已保留为手工编辑",
	})
	return nil
}

// DeleteSelected removes the selected question and remembers that deletion
// across subsequent Provider refreshes.
func (model *Model) DeleteSelected() error {
	if model == nil {
		return errors.New("scenario model is nil")
	}
	if model.plan == nil || len(model.plan.Scenario.Questions) == 0 {
		return model.recordFailure(factoryError(
			domainerr.CodeValidation,
			"还没有可删除的场景题目。",
			"先按 [g] 生成场景计划。",
		))
	}
	if model.plan.Locked {
		return model.recordFailure(factoryError(
			domainerr.CodePolicyDenied,
			"已开始场景的题目不可删除。",
			"完成当前训练，或创建新场景。",
		))
	}
	if len(model.plan.Scenario.Questions) == 1 {
		return model.recordFailure(factoryError(
			domainerr.CodeValidation,
			"场景至少需要保留一道题。",
			"先替换当前题目，再开始训练。",
		))
	}

	value := clonePlan(*model.plan)
	deleted := value.Scenario.Questions[model.selected].ID
	value.Scenario.Questions = append(
		value.Scenario.Questions[:model.selected],
		value.Scenario.Questions[model.selected+1:]...,
	)
	value.ManualQuestionIDs = slices.DeleteFunc(
		value.ManualQuestionIDs,
		func(id string) bool { return id == deleted },
	)
	bumpVersion(&value)
	model.deletedIDs[deleted] = struct{}{}
	model.plan = &value
	model.selected = clampSelection(
		model.selected,
		len(value.Scenario.Questions),
	)
	model.operation = async.NewSucceeded(Progress{
		Stage:   StageReady,
		Message: "题目已从本轮计划删除",
	})
	return nil
}

// Start confirms the immutable Scenario version and creates one active
// Session. A failed session write keeps the locked plan and retry ID.
func (model *Model) Start(
	ctx context.Context,
	observer Observer,
) (Action, error) {
	if model == nil {
		return Action{}, errors.New("scenario model is nil")
	}
	if model.startedID != "" && model.plan != nil {
		return Action{
			Intent:      IntentStart,
			Destination: DestinationInterview,
			SessionID:   model.startedID,
			ScenarioID:  model.plan.ID,
		}, nil
	}
	if model.plan == nil {
		err := factoryError(
			domainerr.CodeValidation,
			"还没有场景计划。",
			"按 [g] 生成并检查 Run Plan 后再开始。",
		)
		return Action{}, model.fail(err, observer)
	}
	if model.planner == nil {
		return Action{}, model.fail(unavailablePlanner(), observer)
	}
	model.setState(async.NewPending[Progress](), observer)
	confirmed := clonePlan(*model.plan)
	if !confirmed.Locked {
		progress := Progress{
			Stage:   StageConfirming,
			Message: "正在确认并锁定场景版本",
		}
		model.setState(async.NewStreaming(&progress), observer)
		value, err := model.planner.Confirm(ctx, confirmed)
		if err != nil {
			return Action{}, model.fail(factoryFailure(err), observer)
		}
		confirmed = value
		value = clonePlan(value)
		model.plan = &value
	}
	if model.sessions == nil {
		return Action{}, model.fail(domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"create Scenario session",
			"会话存储接口不可用。",
			"已确认场景仍保留；重新打开场景页后重试。",
			true,
		), observer)
	}
	if model.pendingID == "" {
		model.pendingID = strings.TrimSpace(model.nextSessionID())
	}
	if model.pendingID == "" {
		return Action{}, model.fail(factoryError(
			domainerr.CodeInvalidState,
			"无法生成会话标识。",
			"重新打开场景页后重试。",
		), observer)
	}
	startedAt := model.now().UTC()
	progress := Progress{
		Stage:   StageStarting,
		Message: "正在创建训练会话",
	}
	model.setState(async.NewStreaming(&progress), observer)
	err := model.sessions.CreateSession(ctx, db.Session{
		ID:         model.pendingID,
		ScenarioID: confirmed.ID,
		Status:     db.SessionActive,
		StartedAt:  startedAt,
		UpdatedAt:  startedAt,
	})
	if err != nil {
		return Action{}, model.fail(factoryFailure(err), observer)
	}
	model.startedID = model.pendingID
	model.setState(async.NewSucceeded(Progress{
		Stage:   StageReady,
		Message: "训练会话已创建",
	}), observer)
	return Action{
		Intent:      IntentStart,
		Destination: DestinationInterview,
		SessionID:   model.startedID,
		ScenarioID:  confirmed.ID,
	}, nil
}

// HandleKey applies focus, selection, editing, and global navigation keys.
func (model *Model) HandleKey(key string) Action {
	if model == nil {
		return Action{}
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if model.helpOpen {
		if key == "escape" || key == "esc" || key == "?" {
			model.helpOpen = false
			model.focus.CloseOverlay()
		}
		return Action{}
	}
	switch key {
	case "?":
		if model.focus.OpenOverlay(focusHelp) == nil {
			model.helpOpen = true
		}
	case "tab":
		model.focus.Handle(layout.KeyTab)
	case "shift+tab":
		model.focus.Handle(layout.KeyShiftTab)
	case "up", "k":
		model.move(-1)
	case "down", "j":
		model.move(1)
	case "g":
		if !model.isBusy() && !model.isLocked() {
			return Action{Intent: IntentGenerate}
		}
	case "e", "r":
		if model.focus.Active() == focusPlan &&
			model.canMutatePlan() {
			return Action{Intent: IntentReplace}
		}
	case "d":
		if model.focus.Active() == focusPlan &&
			model.canMutatePlan() {
			return Action{Intent: IntentDelete}
		}
	case "ctrl+enter":
		if !model.isBusy() && model.plan != nil {
			return Action{Intent: IntentStart}
		}
	case "b", "p":
		return Action{Destination: DestinationProfile}
	case "h":
		return Action{Destination: DestinationTraining}
	case "s":
		return Action{Destination: DestinationSettings}
	case "q":
		return Action{Destination: DestinationQuit}
	}
	return Action{}
}

func (model *Model) move(delta int) {
	if model.isBusy() || model.isLocked() {
		return
	}
	switch model.focus.Active() {
	case focusTemplate:
		if model.plan == nil && len(model.templates) > 0 {
			model.templateIndex = cycleIndex(
				model.templateIndex,
				delta,
				len(model.templates),
			)
			template := model.templates[model.templateIndex]
			model.mode = template.DefaultMode
			model.duration = nearestDuration(
				template.DefaultTimeBudgetSeconds,
			)
		}
	case focusDifficulty:
		difficulty := cycleValue(
			model.difficulty,
			difficultyChoices,
			delta,
		)
		_ = model.UpdateRules(difficulty, model.mode, model.duration)
	case focusMode:
		mode := cycleValue(model.mode, modeChoices, delta)
		_ = model.UpdateRules(model.difficulty, mode, model.duration)
	case focusDuration:
		duration := cycleValue(model.duration, durationChoices, delta)
		_ = model.UpdateRules(model.difficulty, model.mode, duration)
	case focusPlan:
		count := 0
		if model.plan != nil {
			count = len(model.plan.Scenario.Questions)
		}
		model.selected = wrapSelection(model.selected, delta, count)
	}
}

func (model *Model) applyDeletions(
	plan corescenario.Plan,
) (corescenario.Plan, error) {
	if len(model.deletedIDs) == 0 {
		return plan, nil
	}
	value := clonePlan(plan)
	value.Scenario.Questions = slices.DeleteFunc(
		value.Scenario.Questions,
		func(question contracts.ScenarioQuestion) bool {
			_, deleted := model.deletedIDs[question.ID]
			return deleted
		},
	)
	value.ManualQuestionIDs = slices.DeleteFunc(
		value.ManualQuestionIDs,
		func(id string) bool {
			_, deleted := model.deletedIDs[id]
			return deleted
		},
	)
	if len(value.Scenario.Questions) == 0 {
		return corescenario.Plan{}, factoryError(
			domainerr.CodeValidation,
			"刷新结果只包含已删除题目。",
			"当前计划已保留；替换题目或稍后重新生成。",
		)
	}
	if err := value.Scenario.Validate(); err != nil {
		return corescenario.Plan{}, factoryFailure(err)
	}
	return value, nil
}

func (model *Model) setState(
	state async.State[Progress],
	observer Observer,
) {
	model.operation = state
	if observer != nil {
		observer(state)
	}
}

func (model *Model) fail(err error, observer Observer) error {
	typed := factoryFailure(err)
	model.setState(async.NewFailed[Progress](typed), observer)
	return typed
}

func (model *Model) recordFailure(err error) error {
	typed := factoryFailure(err)
	model.operation = async.NewFailed[Progress](typed)
	return typed
}

func (model *Model) isBusy() bool {
	return model != nil &&
		(model.operation.Phase == async.Pending ||
			model.operation.Phase == async.Streaming)
}

func (model *Model) isLocked() bool {
	return model != nil && model.plan != nil && model.plan.Locked
}

func (model *Model) canMutatePlan() bool {
	return model != nil &&
		!model.isBusy() &&
		model.plan != nil &&
		!model.plan.Locked &&
		len(model.plan.Scenario.Questions) > 0
}

func (model *Model) templateID() string {
	if model == nil ||
		model.templateIndex < 0 ||
		model.templateIndex >= len(model.templates) {
		return ""
	}
	return model.templates[model.templateIndex].ID
}

func (model *Model) selectedQuestion() (
	contracts.ScenarioQuestion,
	bool,
) {
	if model == nil || model.plan == nil ||
		len(model.plan.Scenario.Questions) == 0 {
		return contracts.ScenarioQuestion{}, false
	}
	index := clampSelection(
		model.selected,
		len(model.plan.Scenario.Questions),
	)
	return cloneQuestion(model.plan.Scenario.Questions[index]), true
}

func bumpVersion(plan *corescenario.Plan) {
	plan.Revision++
	plan.ID = fmt.Sprintf("%s-v%03d", plan.BaseID, plan.Revision)
	plan.Scenario.PromptVersion = fmt.Sprintf(
		"%s.r%d",
		corescenario.PromptVersionBase,
		plan.Revision,
	)
	plan.Locked = false
	plan.ConfirmedAt = nil
}

func randomSessionID() string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err == nil {
		return "session-" + hex.EncodeToString(value[:])
	}
	return fmt.Sprintf("session-%d", time.Now().UTC().UnixNano())
}

func unavailablePlanner() *domainerr.Error {
	return domainerr.New(
		domainerr.CodeDependencyUnavailable,
		"generate Scenario factory",
		"Scenario Planner Provider 不可用。",
		"按 [s] 打开设置，测试模型连接后按 [g] 重试。",
		true,
	)
}

func factoryError(
	code domainerr.Code,
	message string,
	recovery string,
) *domainerr.Error {
	return domainerr.New(
		code,
		"update Scenario factory",
		message,
		recovery,
		false,
	)
}

func factoryFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"update Scenario factory",
		"Scenario factory",
		"无法完成场景操作。",
		"当前计划和手工编辑已保留；检查 Provider 或数据库后重试。",
		true,
		err,
	)
}

func cloneTemplates(values []corescenario.Template) []corescenario.Template {
	result := make([]corescenario.Template, len(values))
	for index, value := range values {
		value.QuestionGuidance = slices.Clone(value.QuestionGuidance)
		value.RubricGuidance = slices.Clone(value.RubricGuidance)
		result[index] = value
	}
	return result
}

func cloneProfile(value coreprofile.Aggregate) coreprofile.Aggregate {
	value.Candidate.Facts = slices.Clone(value.Candidate.Facts)
	value.Candidate.Inferences = slices.Clone(value.Candidate.Inferences)
	value.Candidate.Projects = slices.Clone(value.Candidate.Projects)
	value.Candidate.Skills = slices.Clone(value.Candidate.Skills)
	value.Metadata.LockedFactIDs = slices.Clone(value.Metadata.LockedFactIDs)
	value.Metadata.LockedInferenceIDs = slices.Clone(
		value.Metadata.LockedInferenceIDs,
	)
	if value.ConfirmedAt != nil {
		confirmedAt := *value.ConfirmedAt
		value.ConfirmedAt = &confirmedAt
	}
	return value
}

func clonePlan(value corescenario.Plan) corescenario.Plan {
	questions := value.Scenario.Questions
	value.Scenario.Questions = make(
		[]contracts.ScenarioQuestion,
		len(questions),
	)
	for index, question := range questions {
		value.Scenario.Questions[index] = cloneQuestion(question)
	}
	mappings := value.JDMappings
	value.JDMappings = make(
		[]corescenario.JDMapping,
		len(mappings),
	)
	for index, mapping := range mappings {
		mapping.EvidenceIDs = slices.Clone(mapping.EvidenceIDs)
		value.JDMappings[index] = mapping
	}
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	value.ManualQuestionIDs = slices.Clone(value.ManualQuestionIDs)
	if value.ConfirmedAt != nil {
		confirmedAt := *value.ConfirmedAt
		value.ConfirmedAt = &confirmedAt
	}
	return value
}

func cloneQuestion(
	value contracts.ScenarioQuestion,
) contracts.ScenarioQuestion {
	value.Rubric = slices.Clone(value.Rubric)
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	return value
}

func nearestDuration(seconds int) int {
	if seconds <= 0 {
		return durationChoices[1]
	}
	best := durationChoices[0]
	distance := abs(seconds - best)
	for _, candidate := range durationChoices[1:] {
		if current := abs(seconds - candidate); current < distance {
			best = candidate
			distance = current
		}
	}
	return best
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func cycleIndex(current, delta, count int) int {
	if count <= 0 {
		return 0
	}
	return (current + delta%count + count) % count
}

func cycleValue[T comparable](current T, choices []T, delta int) T {
	index := slices.Index(choices, current)
	if index < 0 {
		index = 0
	}
	return choices[cycleIndex(index, delta, len(choices))]
}

func clampSelection(selected, count int) int {
	if count <= 0 {
		return 0
	}
	if selected < 0 {
		return 0
	}
	if selected >= count {
		return count - 1
	}
	return selected
}

func wrapSelection(selected, delta, count int) int {
	if count <= 0 {
		return 0
	}
	return (selected + delta%count + count) % count
}
