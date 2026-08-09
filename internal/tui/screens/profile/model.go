// Package profile implements the P-02 resume and target-role workbench.
package profile

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/interviewcraft/interviewcraft/internal/adapters/resume"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

const (
	focusFile     = "file"
	focusPaste    = "paste"
	focusRole     = "role"
	focusLevel    = "level"
	focusJD       = "jd"
	focusLanguage = "language"
	focusProfile  = "profile"
	focusHelp     = "help"
)

var (
	levelChoices    = []string{"Junior", "Mid", "Senior", "Staff"}
	languageChoices = []string{"中文", "English", "双语"}
)

// SourceMode selects the current file or pasted-text input.
type SourceMode string

const (
	SourceFile  SourceMode = "file"
	SourcePaste SourceMode = "paste"
)

// Stage identifies one workbench operation stage.
type Stage string

const (
	StageIdle        Stage = "idle"
	StageLoading     Stage = "loading"
	StageExtracting  Stage = "extracting"
	StageStructuring Stage = "structuring"
	StageSaving      Stage = "saving"
	StageReady       Stage = "ready"
)

// Progress is the typed screen lifecycle payload.
type Progress struct {
	Stage      Stage
	Message    string
	Current    int64
	Total      int64
	SourceName string
}

// Observer receives workbench loading, parsing, and saving states.
type Observer func(async.State[Progress])

// ResumeExtractor is the local resume input boundary.
type ResumeExtractor interface {
	Extract(
		context.Context,
		resume.Input,
		resume.Observer,
	) (coreprofile.Source, error)
}

// ProfileCommands is the query/command boundary used by P-02.
type ProfileCommands interface {
	Create(
		context.Context,
		string,
		coreprofile.Source,
		string,
		coreprofile.Observer,
	) (coreprofile.Aggregate, error)
	Load(context.Context, string) (coreprofile.Aggregate, bool, error)
	Confirm(context.Context, string) (coreprofile.Aggregate, error)
	EditFact(
		context.Context,
		string,
		contracts.ProfileFact,
	) (coreprofile.Aggregate, error)
	EditInference(
		context.Context,
		string,
		contracts.ProfileInference,
	) (coreprofile.Aggregate, error)
	DeleteItem(context.Context, string, string) (coreprofile.Aggregate, error)
	SetLocked(
		context.Context,
		string,
		string,
		bool,
	) (coreprofile.Aggregate, error)
}

// Form is the complete local target and resume draft.
type Form struct {
	FilePath string
	Paste    string
	Role     string
	Level    string
	JD       string
	Language string
}

// Destination is one global workbench navigation target.
type Destination string

const (
	DestinationNone     Destination = ""
	DestinationTraining Destination = "training"
	DestinationProfile  Destination = "profile"
	DestinationScenario Destination = "scenario"
	DestinationSettings Destination = "settings"
	DestinationQuit     Destination = "quit"
)

// Intent tells the application controller which async command to execute.
type Intent string

const (
	IntentNone       Intent = ""
	IntentParse      Intent = "parse"
	IntentCancel     Intent = "cancel"
	IntentSave       Intent = "save"
	IntentEdit       Intent = "edit"
	IntentApplyEdit  Intent = "apply-edit"
	IntentToggleLock Intent = "toggle-lock"
	IntentDelete     Intent = "delete"
)

// Action combines an in-screen command with optional navigation.
type Action struct {
	Intent      Intent
	Destination Destination
}

// Options constructs a Profile workbench.
type Options struct {
	ProfileID string
	Extractor ResumeExtractor
	Profiles  ProfileCommands
	Width     int
	Height    int
	Theme     theme.Theme
}

// Model owns P-02 form, operation, selection, and edit state.
type Model struct {
	profileID        string
	extractor        ResumeExtractor
	profiles         ProfileCommands
	form             Form
	sourceMode       SourceMode
	loadedSourceName string
	aggregate        *coreprofile.Aggregate
	operation        async.State[Progress]
	focus            *layout.FocusModel
	selected         int
	editID           string
	editBuffer       string
	cancel           context.CancelFunc
	helpOpen         bool

	Width  int
	Height int
	Theme  theme.Theme
}

// New creates an empty workbench without starting I/O.
func New(options Options) (*Model, error) {
	focus, err := layout.NewFocusModel(
		focusFile,
		focusPaste,
		focusRole,
		focusLevel,
		focusJD,
		focusLanguage,
		focusProfile,
	)
	if err != nil {
		return nil, err
	}
	profileID := strings.TrimSpace(options.ProfileID)
	if profileID == "" {
		profileID = "default"
	}
	return &Model{
		profileID:  profileID,
		extractor:  options.Extractor,
		profiles:   options.Profiles,
		sourceMode: SourceFile,
		form: Form{
			Level:    levelChoices[0],
			Language: languageChoices[0],
		},
		operation: async.NewSucceeded(Progress{
			Stage:   StageIdle,
			Message: "还没有加载简历",
		}),
		focus:  focus,
		Width:  options.Width,
		Height: options.Height,
		Theme:  options.Theme,
	}, nil
}

// Form returns a defensive copy of the current input draft.
func (model *Model) Form() Form {
	if model == nil {
		return Form{}
	}
	return model.form
}

// State returns the current typed operation lifecycle.
func (model *Model) State() async.State[Progress] {
	if model == nil {
		return async.State[Progress]{}
	}
	return model.operation
}

// Aggregate returns a defensive copy of the current profile.
func (model *Model) Aggregate() (coreprofile.Aggregate, bool) {
	if model == nil || model.aggregate == nil {
		return coreprofile.Aggregate{}, false
	}
	return cloneAggregate(*model.aggregate), true
}

// SelectSource changes which preserved resume input Parse will use.
func (model *Model) SelectSource(mode SourceMode) error {
	if model == nil {
		return errors.New("profile model is nil")
	}
	if mode != SourceFile && mode != SourcePaste {
		return fmt.Errorf("unsupported source mode %q", mode)
	}
	model.sourceMode = mode
	return nil
}

// UpdateActive updates the focused form field or active inline edit buffer.
func (model *Model) UpdateActive(value string) error {
	if model == nil {
		return errors.New("profile model is nil")
	}
	if model.editID != "" {
		model.editBuffer = value
		return nil
	}
	switch model.focus.Active() {
	case focusFile:
		model.form.FilePath = value
		model.sourceMode = SourceFile
	case focusPaste:
		model.form.Paste = value
		model.sourceMode = SourcePaste
	case focusRole:
		model.form.Role = value
	case focusLevel:
		if !slices.Contains(levelChoices, value) {
			return fmt.Errorf("unsupported level %q", value)
		}
		model.form.Level = value
	case focusJD:
		model.form.JD = value
	case focusLanguage:
		if !slices.Contains(languageChoices, value) {
			return fmt.Errorf("unsupported language %q", value)
		}
		model.form.Language = value
	default:
		return fmt.Errorf("focused profile list is not a text field")
	}
	return nil
}

// InsertText appends printable or pasted text to the active editable field.
// Choice fields continue to be changed with their existing navigation keys.
func (model *Model) InsertText(value string) error {
	if model == nil || value == "" {
		return nil
	}
	current, editable := model.activeText()
	if !editable {
		return nil
	}
	return model.UpdateActive(current + value)
}

// Backspace removes one complete UTF-8 rune from the active editable field.
func (model *Model) Backspace() error {
	if model == nil {
		return nil
	}
	current, editable := model.activeText()
	if !editable || current == "" {
		return nil
	}
	_, size := utf8.DecodeLastRuneInString(current)
	return model.UpdateActive(current[:len(current)-size])
}

func (model *Model) activeText() (string, bool) {
	if model.editID != "" {
		return model.editBuffer, true
	}
	switch model.focus.Active() {
	case focusFile:
		return model.form.FilePath, true
	case focusPaste:
		return model.form.Paste, true
	case focusRole:
		return model.form.Role, true
	case focusJD:
		return model.form.JD, true
	default:
		return "", false
	}
}

// Resize changes geometry without clearing the form, focus, profile, or edit.
func (model *Model) Resize(width, height int) {
	if model == nil {
		return
	}
	model.Width = width
	model.Height = height
}

// Load restores a persisted profile without discarding a current form on
// failure.
func (model *Model) Load(ctx context.Context, observer Observer) error {
	if model == nil {
		return errors.New("profile model is nil")
	}
	operationCtx, cancel := context.WithCancel(ctx)
	model.cancel = cancel
	defer func() {
		cancel()
		model.cancel = nil
	}()
	model.setState(async.NewPending[Progress](), observer)
	if model.profiles == nil {
		return model.fail(
			domainerr.New(
				domainerr.CodeDependencyUnavailable,
				"load Profile workbench",
				"画像查询接口不可用。",
				"重新打开画像页后重试。",
				true,
			),
			observer,
		)
	}
	loading := Progress{Stage: StageLoading, Message: "正在恢复已保存画像"}
	model.setState(async.NewStreaming(&loading), observer)
	aggregate, found, err := model.profiles.Load(operationCtx, model.profileID)
	if err != nil {
		return model.fail(workbenchFailure(
			err,
			"load Profile workbench",
			"无法恢复已保存画像。",
			"检查本地数据库后重试。",
		), observer)
	}
	if found {
		value := cloneAggregate(aggregate)
		model.aggregate = &value
		model.form.Role = aggregate.Candidate.TargetRole
		model.loadedSourceName = aggregate.Metadata.Source.Name
		switch aggregate.Metadata.Source.Kind {
		case coreprofile.SourcePaste:
			model.sourceMode = SourcePaste
			model.form.Paste = aggregate.Metadata.Source.Text
		default:
			model.sourceMode = SourceFile
		}
		model.selected = clampSelection(model.selected, model.itemCount())
		message := "画像已从本地恢复"
		model.setState(async.NewSucceeded(Progress{
			Stage:      StageReady,
			Message:    message,
			SourceName: aggregate.Metadata.Source.Name,
		}), observer)
		return nil
	}
	model.setState(async.NewSucceeded(Progress{
		Stage:   StageIdle,
		Message: "还没有加载简历",
	}), observer)
	return nil
}

// Parse extracts the selected input, generates a traceable profile, and saves
// an unconfirmed aggregate. The form remains intact on every failure.
func (model *Model) Parse(ctx context.Context, observer Observer) error {
	if model == nil {
		return errors.New("profile model is nil")
	}
	if strings.TrimSpace(model.form.Role) == "" {
		return model.fail(formError(
			"目标岗位不能为空。",
			"切换到 role 字段并填写目标岗位。",
		), observer)
	}
	input, err := model.resumeInput()
	if err != nil {
		return model.fail(err, observer)
	}
	if model.extractor == nil || model.profiles == nil {
		return model.fail(
			domainerr.New(
				domainerr.CodeDependencyUnavailable,
				"parse Profile workbench",
				"简历解析或画像服务不可用。",
				"检查 Provider 设置后重新打开画像页。",
				true,
			),
			observer,
		)
	}

	operationCtx, cancel := context.WithCancel(ctx)
	model.cancel = cancel
	defer func() {
		cancel()
		model.cancel = nil
	}()
	model.setState(async.NewPending[Progress](), observer)
	source, err := model.extractor.Extract(
		operationCtx,
		input,
		func(state async.State[resume.Progress]) {
			if state.Phase != async.Streaming || state.Value == nil {
				return
			}
			current := state.Value
			progress := Progress{
				Stage:      StageExtracting,
				Message:    current.Stage,
				Current:    current.Current,
				Total:      current.Total,
				SourceName: current.SourceName,
			}
			model.setState(async.NewStreaming(&progress), observer)
		},
	)
	if err != nil {
		return model.fail(err, observer)
	}

	aggregate, err := model.profiles.Create(
		operationCtx,
		model.profileID,
		source,
		strings.TrimSpace(model.form.Role),
		func(state async.State[coreprofile.Progress]) {
			if state.Phase != async.Streaming || state.Value == nil {
				return
			}
			stage := StageStructuring
			if strings.Contains(state.Value.Stage, "保存") {
				stage = StageSaving
			}
			progress := Progress{
				Stage:      stage,
				Message:    state.Value.Stage,
				SourceName: source.Name,
			}
			model.setState(async.NewStreaming(&progress), observer)
		},
	)
	if err != nil {
		return model.fail(err, observer)
	}
	value := cloneAggregate(aggregate)
	model.aggregate = &value
	model.loadedSourceName = source.Name
	model.selected = 0
	model.editID = ""
	model.editBuffer = ""
	model.setState(async.NewSucceeded(Progress{
		Stage:      StageReady,
		Message:    "画像已生成，请确认事实与待验证推断",
		SourceName: source.Name,
	}), observer)
	return nil
}

// Cancel stops the current Load/Parse operation. It never clears input.
func (model *Model) Cancel() {
	if model != nil && model.cancel != nil {
		model.cancel()
	}
}

// SaveAndContinue confirms the reviewed profile and returns the P-03 target.
func (model *Model) SaveAndContinue(
	ctx context.Context,
	observer Observer,
) (Destination, error) {
	if model == nil {
		return DestinationNone, errors.New("profile model is nil")
	}
	if model.aggregate == nil ||
		strings.TrimSpace(model.aggregate.Metadata.Source.Text) == "" {
		err := formError(
			"还没有加载简历。",
			"输入文件路径或粘贴文本并按 [x] 解析。",
		)
		return DestinationNone, model.fail(err, observer)
	}
	if strings.TrimSpace(model.form.Role) !=
		model.aggregate.Candidate.TargetRole {
		err := formError(
			"目标岗位已修改，当前画像需要重新解析。",
			"按 [x] 重新解析后再保存。",
		)
		return DestinationNone, model.fail(err, observer)
	}
	if model.profiles == nil {
		err := domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"confirm CandidateProfile",
			"画像保存接口不可用。",
			"当前输入和画像已保留；重新打开页面后重试。",
			true,
		)
		return DestinationNone, model.fail(err, observer)
	}

	model.setState(async.NewPending[Progress](), observer)
	saving := Progress{
		Stage:      StageSaving,
		Message:    "正在确认并保存画像",
		SourceName: model.aggregate.Metadata.Source.Name,
	}
	model.setState(async.NewStreaming(&saving), observer)
	aggregate, err := model.profiles.Confirm(ctx, model.profileID)
	if err != nil {
		return DestinationNone, model.fail(workbenchFailure(
			err,
			"confirm CandidateProfile",
			"无法保存已确认画像。",
			"当前输入和画像已保留；检查数据库后重试。",
		), observer)
	}
	value := cloneAggregate(aggregate)
	model.aggregate = &value
	model.setState(async.NewSucceeded(Progress{
		Stage:      StageReady,
		Message:    "画像已确认并保存",
		SourceName: aggregate.Metadata.Source.Name,
	}), observer)
	return DestinationScenario, nil
}

// BeginEdit starts inline editing for the selected fact or inference.
func (model *Model) BeginEdit() error {
	if model == nil {
		return errors.New("profile model is nil")
	}
	item, ok := model.selectedItem()
	if !ok {
		return formError("还没有可编辑的画像字段。", "先解析或恢复画像。")
	}
	if item.locked {
		return domainerr.New(
			domainerr.CodePolicyDenied,
			"edit profile item",
			"该画像字段已锁定。",
			"按 [l] 解锁后再编辑。",
			false,
		)
	}
	model.editID = item.id
	model.editBuffer = item.value
	return nil
}

// ApplyEdit validates and saves the current inline edit.
func (model *Model) ApplyEdit(ctx context.Context) error {
	if model == nil || model.aggregate == nil {
		return formError("还没有可编辑的画像字段。", "先解析或恢复画像。")
	}
	value := strings.TrimSpace(model.editBuffer)
	if model.editID == "" || value == "" {
		return model.recordFailure(formError(
			"编辑内容不能为空。",
			"填写有原文支持的内容，或按 [Esc] 取消。",
		))
	}
	for _, fact := range model.aggregate.Candidate.Facts {
		if string(fact.ID) != model.editID {
			continue
		}
		replacement := fact
		replacement.Value = value
		aggregate, err := model.profiles.EditFact(
			ctx,
			model.profileID,
			replacement,
		)
		if err != nil {
			return model.recordFailure(workbenchFailure(
				err,
				"edit profile fact",
				"无法保存事实编辑。",
				"当前编辑仍保留；使用原文支持的内容后重试。",
			))
		}
		model.finishMutation(aggregate)
		return nil
	}
	for _, inference := range model.aggregate.Candidate.Inferences {
		if inference.ID != model.editID {
			continue
		}
		replacement := inference
		replacement.Value = value
		aggregate, err := model.profiles.EditInference(
			ctx,
			model.profileID,
			replacement,
		)
		if err != nil {
			return model.recordFailure(workbenchFailure(
				err,
				"edit profile inference",
				"无法保存推断编辑。",
				"当前编辑仍保留；修正后重试。",
			))
		}
		model.finishMutation(aggregate)
		return nil
	}
	return model.recordFailure(formError(
		"找不到正在编辑的画像字段。",
		"取消编辑并重新选择字段。",
	))
}

// CancelEdit exits inline editing without changing the persisted aggregate.
func (model *Model) CancelEdit() {
	if model == nil {
		return
	}
	model.editID = ""
	model.editBuffer = ""
}

// ToggleSelectedLock persists the opposite lock state.
func (model *Model) ToggleSelectedLock(ctx context.Context) error {
	if model == nil || model.aggregate == nil {
		return formError("还没有可锁定的画像字段。", "先解析或恢复画像。")
	}
	item, ok := model.selectedItem()
	if !ok {
		return formError("还没有可锁定的画像字段。", "先解析或恢复画像。")
	}
	aggregate, err := model.profiles.SetLocked(
		ctx,
		model.profileID,
		item.id,
		!item.locked,
	)
	if err != nil {
		return model.recordFailure(workbenchFailure(
			err,
			"lock profile item",
			"无法更新字段锁定状态。",
			"当前画像未改变；检查数据库后重试。",
		))
	}
	model.finishMutation(aggregate)
	return nil
}

// DeleteSelected removes one unlocked fact or inference.
func (model *Model) DeleteSelected(ctx context.Context) error {
	if model == nil || model.aggregate == nil {
		return formError("还没有可删除的画像字段。", "先解析或恢复画像。")
	}
	item, ok := model.selectedItem()
	if !ok {
		return formError("还没有可删除的画像字段。", "先解析或恢复画像。")
	}
	aggregate, err := model.profiles.DeleteItem(
		ctx,
		model.profileID,
		item.id,
	)
	if err != nil {
		return model.recordFailure(workbenchFailure(
			err,
			"delete profile item",
			"无法删除画像字段。",
			"当前画像未改变；解锁或检查数据库后重试。",
		))
	}
	model.finishMutation(aggregate)
	model.selected = clampSelection(model.selected, model.itemCount())
	return nil
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
	if model.editID != "" {
		switch key {
		case "escape", "esc":
			model.CancelEdit()
		case "enter":
			return Action{Intent: IntentApplyEdit}
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
	case "enter":
		if model.focus.Active() == focusProfile && !model.isBusy() {
			if err := model.BeginEdit(); err != nil {
				model.recordFailure(err)
			} else {
				return Action{Intent: IntentEdit}
			}
		}
	case "x":
		if !model.isBusy() {
			return Action{Intent: IntentParse}
		}
	case "c":
		if model.isBusy() {
			model.Cancel()
			return Action{Intent: IntentCancel}
		}
	case "ctrl+enter", "w":
		if !model.isBusy() {
			return Action{Intent: IntentSave}
		}
	case "e":
		if model.focus.Active() == focusProfile && !model.isBusy() {
			if err := model.BeginEdit(); err != nil {
				model.recordFailure(err)
			} else {
				return Action{Intent: IntentEdit}
			}
		}
	case "l":
		if model.focus.Active() == focusProfile && !model.isBusy() {
			return Action{Intent: IntentToggleLock}
		}
	case "d":
		if model.focus.Active() == focusProfile && !model.isBusy() {
			return Action{Intent: IntentDelete}
		}
	case "h":
		return Action{Destination: DestinationTraining}
	case "p":
		model.sourceMode = SourcePaste
		model.focusTarget(focusPaste)
	case "s":
		return Action{Destination: DestinationSettings}
	case "q":
		return Action{Destination: DestinationQuit}
	}
	return Action{}
}

func (model *Model) resumeInput() (resume.Input, error) {
	if model.sourceMode == SourcePaste {
		if strings.TrimSpace(model.form.Paste) == "" {
			return resume.Input{}, formError(
				"还没有加载简历。",
				"在 paste 字段粘贴简历文本，或选择 file 输入。",
			)
		}
		return resume.Input{
			Kind: coreprofile.SourcePaste,
			Text: model.form.Paste,
		}, nil
	}
	if strings.TrimSpace(model.form.FilePath) == "" {
		return resume.Input{}, formError(
			"还没有加载简历。",
			"在 file 字段输入 PDF、DOCX、TXT 路径，或切换到 paste。",
		)
	}
	return resume.Input{Path: model.form.FilePath}, nil
}

func (model *Model) move(delta int) {
	switch model.focus.Active() {
	case focusLevel:
		model.form.Level = cycleChoice(model.form.Level, levelChoices, delta)
	case focusLanguage:
		model.form.Language = cycleChoice(
			model.form.Language,
			languageChoices,
			delta,
		)
	case focusProfile:
		model.selected = wrapSelection(
			model.selected,
			delta,
			model.itemCount(),
		)
	}
}

func (model *Model) focusTarget(target string) {
	if model == nil || model.focus == nil {
		return
	}
	for index := 0; index < 7 && model.focus.Active() != target; index++ {
		model.focus.Next()
	}
}

func (model *Model) itemCount() int {
	if model == nil || model.aggregate == nil {
		return 0
	}
	return len(model.aggregate.Candidate.Facts) +
		len(model.aggregate.Candidate.Inferences)
}

type profileItem struct {
	id        string
	value     string
	inference bool
	locked    bool
}

func (model *Model) selectedItem() (profileItem, bool) {
	if model == nil || model.aggregate == nil || model.itemCount() == 0 {
		return profileItem{}, false
	}
	index := clampSelection(model.selected, model.itemCount())
	if index < len(model.aggregate.Candidate.Facts) {
		fact := model.aggregate.Candidate.Facts[index]
		return profileItem{
			id:    string(fact.ID),
			value: fact.Value,
			locked: slices.Contains(
				model.aggregate.Metadata.LockedFactIDs,
				fact.ID,
			),
		}, true
	}
	inferenceIndex := index - len(model.aggregate.Candidate.Facts)
	inference := model.aggregate.Candidate.Inferences[inferenceIndex]
	return profileItem{
		id:        inference.ID,
		value:     inference.Value,
		inference: true,
		locked: slices.Contains(
			model.aggregate.Metadata.LockedInferenceIDs,
			inference.ID,
		),
	}, true
}

func (model *Model) finishMutation(aggregate coreprofile.Aggregate) {
	value := cloneAggregate(aggregate)
	model.aggregate = &value
	model.editID = ""
	model.editBuffer = ""
	model.operation = async.NewSucceeded(Progress{
		Stage:      StageReady,
		Message:    "画像修改已保存，请确认后继续",
		SourceName: aggregate.Metadata.Source.Name,
	})
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
	typed := workbenchFailure(
		err,
		"update Profile workbench",
		"无法完成画像操作。",
		"当前输入已保留；修正后重试。",
	)
	model.setState(async.NewFailed[Progress](typed), observer)
	return typed
}

func (model *Model) recordFailure(err error) error {
	typed := workbenchFailure(
		err,
		"update Profile workbench",
		"无法更新画像。",
		"当前输入和编辑已保留；修正后重试。",
	)
	model.operation = async.NewFailed[Progress](typed)
	return typed
}

func (model *Model) isBusy() bool {
	return model != nil &&
		(model.operation.Phase == async.Pending ||
			model.operation.Phase == async.Streaming)
}

func formError(message, recovery string) *domainerr.Error {
	return domainerr.New(
		domainerr.CodeValidation,
		"validate Profile workbench",
		message,
		recovery,
		false,
	)
}

func workbenchFailure(
	err error,
	operation string,
	message string,
	recovery string,
) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		operation,
		"Profile workbench",
		message,
		recovery,
		true,
		err,
	)
}

func cloneAggregate(value coreprofile.Aggregate) coreprofile.Aggregate {
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

func cycleChoice(current string, choices []string, delta int) string {
	index := slices.Index(choices, current)
	if index < 0 {
		index = 0
	}
	return choices[(index+delta%len(choices)+len(choices))%len(choices)]
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
