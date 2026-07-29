// Package training implements the P-01 training home read model, navigation,
// keyboard behavior, and pure terminal rendering.
package training

import (
	"context"
	"errors"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

const (
	focusPrimary = "primary"
	focusRecent  = "recent"
	focusQueue   = "practice-queue"
	focusHelp    = "help"
)

// Query is the read-only storage boundary used by the home screen.
type Query interface {
	LoadTrainingHome(context.Context, int) (db.TrainingHomeData, error)
}

// StateObserver receives the screen loading lifecycle.
type StateObserver func(async.State[db.TrainingHomeData])

// Destination is a global navigation or row activation target.
type Destination string

const (
	DestinationNone      Destination = ""
	DestinationTraining  Destination = "training"
	DestinationProfile   Destination = "profile"
	DestinationScenario  Destination = "scenario"
	DestinationInterview Destination = "interview"
	DestinationReport    Destination = "report"
	DestinationSettings  Destination = "settings"
	DestinationQuit      Destination = "quit"
)

// Navigation identifies the destination plus the selected persisted object.
type Navigation struct {
	Destination Destination
	SessionID   string
	ReportID    string
	PracticeID  string
}

// Model owns P-01 state. Components remain pure renderers and never query it.
type Model struct {
	query          Query
	state          async.State[db.TrainingHomeData]
	focus          *layout.FocusModel
	selectedRecent int
	selectedQueue  int
	helpOpen       bool

	Width    int
	Height   int
	Theme    theme.Theme
	Provider components.StatusBadge
}

// New creates a pending home screen with the primary action focused.
func New(
	query Query,
	width int,
	height int,
	current theme.Theme,
) (*Model, error) {
	focus, err := layout.NewFocusModel(focusPrimary, focusRecent, focusQueue)
	if err != nil {
		return nil, err
	}
	return &Model{
		query:  query,
		state:  async.NewPending[db.TrainingHomeData](),
		focus:  focus,
		Width:  width,
		Height: height,
		Theme:  current,
		Provider: components.StatusBadge{
			State: components.BadgeReady,
			Text:  "local",
		},
	}, nil
}

// State returns the current typed loading lifecycle.
func (model *Model) State() async.State[db.TrainingHomeData] {
	if model == nil {
		return async.State[db.TrainingHomeData]{}
	}
	return model.state
}

// Load refreshes the read model and emits pending plus one terminal state.
func (model *Model) Load(ctx context.Context, observer StateObserver) {
	if model == nil {
		return
	}
	model.state = async.NewPending[db.TrainingHomeData]()
	notifyState(observer, model.state)

	if model.query == nil {
		failure := domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"load training home",
			"训练主页没有可用的数据查询接口。",
			"重新启动 InterviewCraft 后按 [t] 重试。",
			true,
		)
		model.state = async.NewFailed[db.TrainingHomeData](failure)
		notifyState(observer, model.state)
		return
	}
	data, err := model.query.LoadTrainingHome(ctx, 5)
	if err != nil {
		model.state = async.NewFailed[db.TrainingHomeData](trainingFailure(err))
		notifyState(observer, model.state)
		return
	}
	if data.Recent == nil {
		data.Recent = []db.RecentTraining{}
	}
	if data.PracticeQueue == nil {
		data.PracticeQueue = []db.PracticeItem{}
	}
	model.selectedRecent = clampSelection(model.selectedRecent, len(data.Recent))
	model.selectedQueue = clampSelection(model.selectedQueue, len(data.PracticeQueue))
	model.state = async.NewSucceeded(data)
	notifyState(observer, model.state)
}

// Resize changes geometry without clearing data, selection, or focus.
func (model *Model) Resize(width, height int) {
	if model == nil {
		return
	}
	model.Width = width
	model.Height = height
}

// HandleKey applies global navigation and focused list behavior.
func (model *Model) HandleKey(key string) Navigation {
	if model == nil {
		return Navigation{}
	}
	key = strings.ToLower(strings.TrimSpace(key))
	if model.helpOpen {
		if key == "escape" || key == "esc" || key == "?" {
			model.helpOpen = false
			model.focus.CloseOverlay()
		}
		return Navigation{}
	}

	switch key {
	case "?":
		if model.focus.OpenOverlay(focusHelp) == nil {
			model.helpOpen = true
		}
		return Navigation{}
	case "tab":
		model.focus.Handle(layout.KeyTab)
		return Navigation{}
	case "shift+tab":
		model.focus.Handle(layout.KeyShiftTab)
		return Navigation{}
	case "up", "k":
		model.moveSelection(-1)
		return Navigation{}
	case "down", "j":
		model.moveSelection(1)
		return Navigation{}
	case "t":
		return Navigation{Destination: DestinationTraining}
	case "p", "n":
		return Navigation{Destination: DestinationProfile}
	case "s":
		return Navigation{Destination: DestinationSettings}
	case "r", "v":
		return model.reportNavigation()
	case "q":
		return Navigation{Destination: DestinationQuit}
	case "enter":
		return model.activateFocused()
	default:
		return Navigation{}
	}
}

func (model *Model) activateFocused() Navigation {
	data := model.data()
	if data == nil {
		return Navigation{}
	}
	switch model.focus.Active() {
	case focusPrimary:
		if data.Resume != nil {
			return Navigation{
				Destination: DestinationInterview,
				SessionID:   data.Resume.Session.ID,
			}
		}
		return Navigation{Destination: DestinationProfile}
	case focusRecent:
		if len(data.Recent) == 0 {
			return Navigation{Destination: DestinationProfile}
		}
		item := data.Recent[model.selectedRecent]
		if data.Resume != nil && item.SessionID == data.Resume.Session.ID {
			return Navigation{
				Destination: DestinationInterview,
				SessionID:   item.SessionID,
			}
		}
		if item.ReportID != "" {
			return Navigation{
				Destination: DestinationReport,
				SessionID:   item.SessionID,
				ReportID:    item.ReportID,
			}
		}
	case focusQueue:
		if len(data.PracticeQueue) > 0 {
			item := data.PracticeQueue[model.selectedQueue]
			return Navigation{
				Destination: DestinationScenario,
				SessionID:   item.SessionID,
				ReportID:    item.ReportID,
				PracticeID:  item.ID,
			}
		}
	}
	return Navigation{}
}

func (model *Model) reportNavigation() Navigation {
	data := model.data()
	if data == nil {
		return Navigation{}
	}
	if model.focus.Active() == focusRecent && len(data.Recent) > 0 {
		item := data.Recent[model.selectedRecent]
		if item.ReportID != "" {
			return Navigation{
				Destination: DestinationReport,
				SessionID:   item.SessionID,
				ReportID:    item.ReportID,
			}
		}
	}
	for _, item := range data.Recent {
		if item.ReportID != "" {
			return Navigation{
				Destination: DestinationReport,
				SessionID:   item.SessionID,
				ReportID:    item.ReportID,
			}
		}
	}
	return Navigation{}
}

func (model *Model) moveSelection(delta int) {
	data := model.data()
	if data == nil {
		return
	}
	switch model.focus.Active() {
	case focusRecent:
		model.selectedRecent = wrapSelection(
			model.selectedRecent,
			delta,
			len(data.Recent),
		)
	case focusQueue:
		model.selectedQueue = wrapSelection(
			model.selectedQueue,
			delta,
			len(data.PracticeQueue),
		)
	}
}

func (model *Model) data() *db.TrainingHomeData {
	if model == nil ||
		(model.state.Phase != async.Succeeded &&
			model.state.Phase != async.Streaming) {
		return nil
	}
	return model.state.Value
}

func trainingFailure(err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"load training home",
		"SQLite",
		"无法读取训练主页数据。",
		"检查本地数据库后按 [t] 重试，或运行 `interviewcraft doctor`。",
		true,
		err,
	)
}

func notifyState(observer StateObserver, state async.State[db.TrainingHomeData]) {
	if observer != nil {
		observer(state)
	}
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
