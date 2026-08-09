// Package app owns InterviewCraft's persistent Bubble Tea event loop.
package app

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
)

// Page is one of the complete product journey destinations.
type Page string

const (
	PageTraining  Page = "training"
	PageProfile   Page = "profile"
	PageScenario  Page = "scenario"
	PageInterview Page = "interview"
	PageCoding    Page = "coding"
	PageReport    Page = "report"
	PageSettings  Page = "settings"
)

// Route keeps durable IDs separate from the screen models that render them.
type Route struct {
	Page       Page
	ProfileID  string
	SessionID  string
	ScenarioID string
	QuestionID string
	ReportID   string
	PracticeID string
}

// Task is work that must not block Bubble Tea's event loop.
type Task func(context.Context) (Route, error)

// Action is the result of one screen key. A task may optionally return a route.
type Action struct {
	Route Route
	Task  Task
	Quit  bool
}

// Screen adapts an existing pure screen model to the root event loop.
type Screen interface {
	Render() (string, error)
	Resize(width, height int)
	HandleKey(key string) Action
	InsertText(value string) error
	Backspace() error
	Tick(time.Time)
	Load(context.Context) error
	ConcurrentSafe() bool
}

// Factory constructs one screen using the shared dependency container.
type Factory interface {
	Open(Route, int, int) (Screen, error)
}

type tickMsg time.Time

type taskResultMsg struct {
	token uint64
	route Route
	err   error
}

// Controller is the single owner of routing, cancellation and render timing.
type Controller struct {
	factory Factory
	route   Route
	screen  Screen
	width   int
	height  int
	frame   string
	busy    bool
	err     error
	token   uint64
	cancel  context.CancelFunc
}

// New creates the root model. Work begins only when Init is called.
func New(factory Factory, initial Route, width, height int) (*Controller, error) {
	if factory == nil {
		return nil, errors.New("app screen factory is nil")
	}
	if initial.Page == "" {
		initial.Page = PageTraining
	}
	if strings.TrimSpace(initial.ProfileID) == "" {
		initial.ProfileID = "default"
	}
	if width <= 0 {
		width = 120
	}
	if height <= 0 {
		height = 36
	}
	screen, err := factory.Open(initial, width, height)
	if err != nil {
		return nil, err
	}
	return &Controller{
		factory: factory, route: initial, screen: screen,
		width: width, height: height, frame: "正在加载…",
	}, nil
}

// Init loads the initial screen and starts the visible timer.
func (model *Controller) Init() tea.Cmd {
	return tea.Batch(model.startTask(loadScreen(model.screen)), tickCommand())
}

// RunOnce loads and renders a single frame without starting a terminal event
// loop. It is the deterministic CI and redirected-output entrypoint.
func (model *Controller) RunOnce(ctx context.Context) error {
	if model == nil || model.screen == nil {
		return errors.New("app screen is nil")
	}
	model.busy = true
	err := model.screen.Load(ctx)
	model.busy = false
	model.err = err
	model.refreshFrame()
	return err
}

// Update applies terminal events without sharing non-thread-safe screen state
// across goroutines.
func (model *Controller) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	if model == nil {
		return model, tea.Quit
	}
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			model.width = msg.Width
		}
		if msg.Height > 0 {
			model.height = msg.Height
		}
		if !model.busy || model.screen.ConcurrentSafe() {
			model.screen.Resize(model.width, model.height)
			model.refreshFrame()
		}
		return model, nil
	case tickMsg:
		if !model.busy || model.screen.ConcurrentSafe() {
			model.screen.Tick(time.Time(msg))
			model.refreshFrame()
		}
		return model, tickCommand()
	case taskResultMsg:
		if msg.token != model.token {
			return model, nil
		}
		model.cancel = nil
		model.busy = false
		model.err = msg.err
		model.screen.Resize(model.width, model.height)
		model.refreshFrame()
		if msg.err == nil && msg.route.Page != "" {
			return model, model.navigate(msg.route)
		}
		return model, nil
	case tea.PasteMsg:
		if !model.busy || model.screen.ConcurrentSafe() {
			model.err = model.screen.InsertText(msg.Content)
			model.refreshFrame()
		}
		return model, nil
	case tea.KeyPressMsg:
		return model.handleKey(msg)
	default:
		return model, nil
	}
}

func (model *Controller) handleKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := strings.ToLower(message.Keystroke())
	if key == "ctrl+c" {
		model.stopTask()
		return model, tea.Quit
	}
	if model.busy && !model.screen.ConcurrentSafe() {
		if key == "escape" || key == "esc" {
			model.stopTask()
			model.err = context.Canceled
			model.refreshFrame()
		}
		return model, nil
	}
	if key == "backspace" {
		model.err = model.screen.Backspace()
		model.refreshFrame()
		return model, nil
	}
	action := model.screen.HandleKey(key)
	if action.Quit {
		model.stopTask()
		return model, tea.Quit
	}
	if action.Route.Page != "" {
		return model, model.navigate(action.Route)
	}
	if action.Task != nil {
		return model, model.startTask(action.Task)
	}
	if text := message.Key().Text; text != "" {
		model.err = model.screen.InsertText(text)
	}
	model.refreshFrame()
	return model, nil
}

func (model *Controller) navigate(next Route) tea.Cmd {
	model.stopTask()
	next = mergeRoute(model.route, next)
	screen, err := model.factory.Open(next, model.width, model.height)
	if err != nil {
		model.err = err
		model.refreshFrame()
		return nil
	}
	model.route = next
	model.screen = screen
	model.err = nil
	model.frame = "正在加载…"
	return model.startTask(loadScreen(screen))
}

func loadScreen(screen Screen) Task {
	return func(ctx context.Context) (Route, error) {
		return Route{}, screen.Load(ctx)
	}
}

func mergeRoute(current, next Route) Route {
	if next.ProfileID == "" {
		next.ProfileID = current.ProfileID
	}
	if next.SessionID == "" {
		next.SessionID = current.SessionID
	}
	if next.ScenarioID == "" {
		next.ScenarioID = current.ScenarioID
	}
	if next.QuestionID == "" {
		next.QuestionID = current.QuestionID
	}
	if next.ReportID == "" {
		next.ReportID = current.ReportID
	}
	if next.PracticeID == "" {
		next.PracticeID = current.PracticeID
	}
	return next
}

func (model *Controller) startTask(task Task) tea.Cmd {
	if task == nil {
		return nil
	}
	model.stopTask()
	model.token++
	token := model.token
	ctx, cancel := context.WithCancel(context.Background())
	model.cancel = cancel
	model.busy = true
	model.err = nil
	model.frame = "正在加载…\n\n[Esc] 取消  [Ctrl+C] 退出"
	return func() tea.Msg {
		route, err := task(ctx)
		return taskResultMsg{token: token, route: route, err: err}
	}
}

func (model *Controller) stopTask() {
	if model.cancel != nil {
		model.cancel()
		model.cancel = nil
	}
	model.token++
	model.busy = false
}

func (model *Controller) refreshFrame() {
	frame, err := model.screen.Render()
	if err != nil {
		model.err = err
	}
	if strings.TrimSpace(frame) == "" {
		frame = "暂无数据。"
	}
	if model.err != nil {
		frame += "\n\n! " + actionableError(model.err)
	}
	if model.busy {
		frame += "\n\n正在加载…  [Esc] 取消"
	}
	model.frame = frame
}

func actionableError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "操作已取消；可重试或返回上一页。"
	}
	return fmt.Sprintf("%v", err)
}

func tickCommand() tea.Cmd {
	return tea.Tick(time.Second, func(now time.Time) tea.Msg {
		return tickMsg(now)
	})
}

// View preserves the existing renderers and asks Bubble Tea only to manage
// terminal lifecycle and the alternate screen.
func (model *Controller) View() tea.View {
	content := "InterviewCraft"
	if model != nil && strings.TrimSpace(model.frame) != "" {
		content = model.frame
	}
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "InterviewCraft"
	return view
}

// Route returns the current durable navigation state for tests and shutdown.
func (model *Controller) Route() Route {
	if model == nil {
		return Route{}
	}
	return model.route
}
