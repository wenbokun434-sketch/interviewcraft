// Package components contains pure terminal rendering primitives. Components
// receive state and never perform storage, model, or runner operations.
package components

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// LabelKind selects a semantic SectionLabel role.
type LabelKind string

const (
	LabelDefault LabelKind = "default"
	LabelInfo    LabelKind = "info"
	LabelCoach   LabelKind = "coach"
	LabelWarning LabelKind = "warning"
)

// SectionLabel is a compact region heading.
type SectionLabel struct {
	Text string
	Kind LabelKind
}

// Render returns an uppercase label of at most 18 visible columns.
func (label SectionLabel) Render(current theme.Theme) string {
	text := strings.ToUpper(strings.TrimSpace(label.Text))
	text = layout.ClipRight(text, 18)
	switch label.Kind {
	case LabelInfo:
		return current.Paint(theme.Info, text)
	case LabelCoach:
		return current.Paint(theme.Coach, text)
	case LabelWarning:
		return current.Paint(theme.Warning, text)
	default:
		return current.Paint(theme.Muted, text)
	}
}

// KeyHint is a keyboard affordance with an optional disabled reason.
type KeyHint struct {
	Key     string
	Action  string
	Enabled bool
	Reason  string
}

// Render returns a discoverable keyboard action.
func (hint KeyHint) Render(current theme.Theme) string {
	text := hint.plain()
	if hint.Enabled {
		return current.Paint(theme.Focus, text)
	}
	return current.Paint(theme.Muted, text)
}

func (hint KeyHint) plain() string {
	key := strings.TrimSpace(hint.Key)
	action := strings.TrimSpace(hint.Action)
	text := fmt.Sprintf("[%s] %s", key, action)
	if !hint.Enabled && strings.TrimSpace(hint.Reason) != "" {
		text += " — " + strings.TrimSpace(hint.Reason)
	}
	return text
}

// BadgeState identifies a small text-backed status.
type BadgeState string

const (
	BadgeReady    BadgeState = "ready"
	BadgeWarning  BadgeState = "warning"
	BadgeError    BadgeState = "error"
	BadgeDisabled BadgeState = "disabled"
)

// StatusBadge never relies on color alone.
type StatusBadge struct {
	State BadgeState
	Text  string
}

// Render returns the badge symbol and explicit status text.
func (badge StatusBadge) Render(current theme.Theme) string {
	text, role := badge.plain(current)
	return current.Paint(role, text)
}

func (badge StatusBadge) plain(current theme.Theme) (string, theme.Role) {
	label := strings.TrimSpace(badge.Text)
	if label == "" {
		label = string(badge.State)
	}
	switch badge.State {
	case BadgeReady:
		return current.Glyphs.Success + " " + label, theme.Success
	case BadgeWarning:
		return current.Glyphs.Warning + " " + label, theme.Warning
	case BadgeError:
		return current.Glyphs.Error + " " + label, theme.Error
	default:
		symbol := "–"
		if current.UseASCII {
			symbol = "-"
		}
		return symbol + " " + label, theme.Muted
	}
}

func fitLines(lines []string, width, height int) []string {
	if height <= 0 {
		return nil
	}
	result := make([]string, height)
	for index := 0; index < height; index++ {
		if index < len(lines) {
			result[index] = layout.Fit(lines[index], width)
		} else {
			result[index] = strings.Repeat(" ", max(0, width))
		}
	}
	return result
}

func appendWrapped(target []string, value string, width int) []string {
	return append(target, layout.Wrap(value, width)...)
}

func nonBlank(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}
