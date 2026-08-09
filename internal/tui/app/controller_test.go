package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestControllerFourStates(t *testing.T) {
	tests := []struct {
		name      string
		frame     string
		loadErr   error
		wantError bool
		contains  string
	}{
		{name: "main", frame: "TRAINING READY", contains: "TRAINING READY"},
		{name: "empty", frame: "", contains: "暂无数据"},
		{name: "dependency error", frame: "TRAINING", loadErr: errors.New("provider unavailable"), wantError: true, contains: "provider unavailable"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			screen := &fakeScreen{frame: test.frame, loadErr: test.loadErr}
			model, err := New(fakeFactory{screen: screen}, Route{Page: PageTraining}, 80, 24)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			err = model.RunOnce(context.Background())
			if (err != nil) != test.wantError {
				t.Fatalf("RunOnce error=%v", err)
			}
			if !strings.Contains(model.View().Content, test.contains) {
				t.Fatalf("view=%q, want %q", model.View().Content, test.contains)
			}
		})
	}

	model, err := New(fakeFactory{screen: &fakeScreen{frame: "ready"}}, Route{Page: PageTraining}, 80, 24)
	if err != nil {
		t.Fatalf("New loading: %v", err)
	}
	_ = model.Init()
	if !strings.Contains(model.View().Content, "正在加载") {
		t.Fatalf("loading view=%q", model.View().Content)
	}
}

func TestControllerDropsStaleTaskAfterCancel(t *testing.T) {
	model, err := New(fakeFactory{screen: &fakeScreen{frame: "preserved"}}, Route{Page: PageTraining}, 80, 24)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	cmd := model.startTask(func(context.Context) (Route, error) {
		return Route{Page: PageSettings}, errors.New("stale failure")
	})
	stale := cmd()
	model.stopTask()
	model.Update(stale)
	if model.Route().Page != PageTraining || model.err != nil {
		t.Fatalf("stale result applied: route=%#v err=%v", model.Route(), model.err)
	}
}

func TestControllerResizePasteBackspaceAndTick(t *testing.T) {
	screen := &fakeScreen{frame: "editor", concurrent: true}
	model, err := New(fakeFactory{screen: screen}, Route{Page: PageCoding}, 80, 24)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	model.Update(tea.WindowSizeMsg{Width: 132, Height: 41})
	model.Update(tea.PasteMsg{Content: "粘贴 text"})
	model.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyBackspace}))
	model.Update(tickMsg(time.Now()))
	if screen.width != 132 || screen.height != 41 {
		t.Fatalf("resize=%dx%d", screen.width, screen.height)
	}
	if screen.text != "粘贴 tex" {
		t.Fatalf("text=%q", screen.text)
	}
	if screen.tick.IsZero() {
		t.Fatal("tick not delivered")
	}
}

func TestControllerDefersUnsafeResizeUntilTaskCompletes(t *testing.T) {
	screen := &fakeScreen{frame: "loading", concurrent: false}
	model, err := New(fakeFactory{screen: screen}, Route{Page: PageProfile}, 80, 24)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	command := model.startTask(func(context.Context) (Route, error) {
		return Route{}, nil
	})
	model.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	if screen.width != 0 || screen.height != 0 {
		t.Fatalf("unsafe resize mutated busy screen: %dx%d", screen.width, screen.height)
	}
	model.Update(command())
	if screen.width != 100 || screen.height != 30 {
		t.Fatalf("deferred resize=%dx%d", screen.width, screen.height)
	}
}

type fakeFactory struct{ screen Screen }

func (factory fakeFactory) Open(Route, int, int) (Screen, error) {
	return factory.screen, nil
}

type fakeScreen struct {
	frame      string
	loadErr    error
	concurrent bool
	width      int
	height     int
	text       string
	tick       time.Time
}

func (screen *fakeScreen) Render() (string, error)  { return screen.frame, nil }
func (screen *fakeScreen) Resize(width, height int) { screen.width, screen.height = width, height }
func (screen *fakeScreen) HandleKey(string) Action  { return Action{} }
func (screen *fakeScreen) InsertText(value string) error {
	screen.text += value
	return nil
}
func (screen *fakeScreen) Backspace() error {
	runes := []rune(screen.text)
	if len(runes) != 0 {
		screen.text = string(runes[:len(runes)-1])
	}
	return nil
}
func (screen *fakeScreen) Tick(now time.Time)         { screen.tick = now }
func (screen *fakeScreen) Load(context.Context) error { return screen.loadErr }
func (screen *fakeScreen) ConcurrentSafe() bool       { return screen.concurrent }
