package layout

import (
	"fmt"
	"strings"
)

// Key is a focus-level keyboard action.
type Key string

const (
	KeyTab      Key = "tab"
	KeyShiftTab Key = "shift+tab"
	KeyEscape   Key = "escape"
)

// FocusModel tracks visible pane order and restores the exact prior target
// after a modal or narrow Coach overlay closes.
type FocusModel struct {
	order    []string
	index    int
	overlay  string
	previous string
}

// NewFocusModel creates a focus order with the first target active.
func NewFocusModel(targets ...string) (*FocusModel, error) {
	if err := validateTargets(targets); err != nil {
		return nil, err
	}
	return &FocusModel{order: append([]string(nil), targets...)}, nil
}

// Active returns the currently focused target.
func (model *FocusModel) Active() string {
	if model == nil {
		return ""
	}
	if model.overlay != "" {
		return model.overlay
	}
	if len(model.order) == 0 {
		return ""
	}
	return model.order[model.index]
}

// Handle applies the global focus keys. Unrelated keys are ignored.
func (model *FocusModel) Handle(key Key) {
	if model == nil {
		return
	}
	switch key {
	case KeyTab:
		model.Next()
	case KeyShiftTab:
		model.Previous()
	case KeyEscape:
		model.CloseOverlay()
	}
}

// Next moves focus to the next visible target. An overlay is a deliberate
// focus trap until Escape closes it.
func (model *FocusModel) Next() {
	if model == nil || model.overlay != "" || len(model.order) == 0 {
		return
	}
	model.index = (model.index + 1) % len(model.order)
}

// Previous moves focus to the previous visible target.
func (model *FocusModel) Previous() {
	if model == nil || model.overlay != "" || len(model.order) == 0 {
		return
	}
	model.index = (model.index - 1 + len(model.order)) % len(model.order)
}

// SetVisible replaces the focus order while preserving the active target when
// it remains visible.
func (model *FocusModel) SetVisible(targets ...string) error {
	if model == nil {
		return fmt.Errorf("focus model is nil")
	}
	if err := validateTargets(targets); err != nil {
		return err
	}
	active := model.Active()
	model.order = append(model.order[:0], targets...)
	model.index = 0
	if model.overlay != "" {
		return nil
	}
	for index, target := range model.order {
		if target == active {
			model.index = index
			break
		}
	}
	return nil
}

// OpenOverlay focuses an overlay and remembers the precise prior target.
func (model *FocusModel) OpenOverlay(target string) error {
	if model == nil {
		return fmt.Errorf("focus model is nil")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return fmt.Errorf("overlay target cannot be blank")
	}
	if model.overlay != "" {
		return fmt.Errorf("overlay %q is already open", model.overlay)
	}
	model.previous = model.Active()
	model.overlay = target
	return nil
}

// CloseOverlay restores the target active immediately before the overlay.
func (model *FocusModel) CloseOverlay() {
	if model == nil || model.overlay == "" {
		return
	}
	prior := model.previous
	model.overlay = ""
	model.previous = ""
	for index, target := range model.order {
		if target == prior {
			model.index = index
			return
		}
	}
}

func validateTargets(targets []string) error {
	if len(targets) == 0 {
		return fmt.Errorf("focus order cannot be empty")
	}
	seen := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		if target == "" {
			return fmt.Errorf("focus target cannot be blank")
		}
		if _, exists := seen[target]; exists {
			return fmt.Errorf("duplicate focus target %q", target)
		}
		seen[target] = struct{}{}
	}
	return nil
}
