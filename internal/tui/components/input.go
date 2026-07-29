package components

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// ListItem is one keyboard-selectable row.
type ListItem struct {
	ID             string
	Label          string
	Meta           string
	Disabled       bool
	DisabledReason string
}

// SelectableList renders a single-select keyboard list or actionable empty
// state.
type SelectableList struct {
	Items        []ListItem
	Selected     int
	Focused      bool
	EmptyMessage string
	EmptyAction  *KeyHint
}

// Render returns list rows clipped to width and height.
func (list SelectableList) Render(
	current theme.Theme,
	width int,
	height int,
) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	if len(list.Items) == 0 {
		message := "-- " + nonBlank(list.EmptyMessage, "还没有可选项目") + " --"
		lines := []string{layout.ClipRight(message, width)}
		if list.EmptyAction != nil {
			lines = append(lines, layout.ClipRight(list.EmptyAction.Render(current), width))
		}
		return fitLines(lines, width, height)
	}

	lines := make([]string, 0, min(height, len(list.Items)))
	for index, item := range list.Items {
		marker := " "
		role := theme.Primary
		if index == list.Selected {
			marker = current.Glyphs.Cursor
			if list.Focused {
				role = theme.Focus
			}
		}
		label := nonBlank(item.Label, item.ID)
		row := marker + " " + label
		meta := strings.TrimSpace(item.Meta)
		if item.Disabled {
			role = theme.Muted
			reason := nonBlank(item.DisabledReason, "当前不可用")
			meta = "unavailable — " + reason
		}
		if meta != "" {
			remaining := width - layout.VisibleWidth(row) - 2
			if remaining > 0 {
				meta = layout.TruncateLeft(meta, remaining, current.UseASCII)
				row += strings.Repeat(" ", max(1, width-layout.VisibleWidth(row)-layout.VisibleWidth(meta))) + meta
			}
		}
		row = layout.Fit(row, width)
		lines = append(lines, current.Paint(role, row))
		if len(lines) == height {
			break
		}
	}
	return fitLines(lines, width, height)
}

// ComposerState identifies TextComposer behavior.
type ComposerState string

const (
	ComposerEmpty         ComposerState = "empty"
	ComposerTyping        ComposerState = "typing"
	ComposerValidationErr ComposerState = "validation-error"
	ComposerDisabled      ComposerState = "disabled"
	ComposerDraftRestored ComposerState = "draft-restored"
)

// TextComposer renders a preserved multi-line local draft.
type TextComposer struct {
	Text           string
	State          ComposerState
	ValidationErr  *domainerr.Error
	DisabledReason string
	Focused        bool
}

// Validate checks state-specific data without mutating the draft.
func (composer TextComposer) Validate() error {
	switch composer.State {
	case "", ComposerEmpty, ComposerTyping, ComposerDraftRestored:
		return nil
	case ComposerValidationErr:
		if composer.ValidationErr == nil {
			return fmt.Errorf("validation-error composer requires a typed error")
		}
	case ComposerDisabled:
		if strings.TrimSpace(composer.DisabledReason) == "" {
			return fmt.Errorf("disabled composer requires a reason")
		}
	default:
		return fmt.Errorf("unsupported composer state %q", composer.State)
	}
	return nil
}

// Render returns content, state guidance, line count, and submit shortcut.
func (composer TextComposer) Render(
	current theme.Theme,
	width int,
	height int,
) ([]string, error) {
	if err := composer.Validate(); err != nil {
		return nil, err
	}
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("composer dimensions must be positive")
	}

	lines := make([]string, 0, height)
	text := strings.TrimSuffix(composer.Text, "\n")
	if strings.TrimSpace(text) == "" {
		lines = append(lines, current.Paint(theme.Muted, "在这里输入回答…"))
	} else {
		lines = appendWrapped(lines, text, width)
	}

	switch composer.State {
	case ComposerValidationErr:
		lines = append(lines, current.Paint(
			theme.Error,
			current.Glyphs.Error+" "+composer.ValidationErr.Message,
		))
		if composer.ValidationErr.RecoveryAction != "" {
			lines = appendWrapped(lines, composer.ValidationErr.RecoveryAction, width)
		}
	case ComposerDisabled:
		lines = append(lines, current.Paint(
			theme.Muted,
			"回答暂不可用 — "+strings.TrimSpace(composer.DisabledReason),
		))
	case ComposerDraftRestored:
		lines = append(lines, current.Paint(
			theme.Success,
			current.Glyphs.Success+" 已恢复本地草稿",
		))
	}

	lineCount := 1
	if text != "" {
		lineCount = strings.Count(text, "\n") + 1
	}
	status := fmt.Sprintf("%d 行", lineCount)
	if composer.State != ComposerDisabled {
		status += " · [Ctrl+Enter] 提交"
	}
	if composer.Focused {
		status = current.Glyphs.Cursor + " " + status
	}
	if len(lines) >= height {
		lines = lines[:height-1]
	}
	lines = append(lines, current.Paint(theme.Muted, layout.ClipRight(status, width)))
	return fitLines(lines, width, height), nil
}

// ConfirmChoice is the current safe confirmation selection.
type ConfirmChoice string

const (
	ConfirmAwaiting ConfirmChoice = "awaiting"
	ConfirmSelected ConfirmChoice = "confirm"
	CancelSelected  ConfirmChoice = "cancel"
)

// ConfirmPrompt is a keyboard-only destructive/session-ending confirmation.
type ConfirmPrompt struct {
	Message string
	Confirm KeyHint
	Cancel  KeyHint
	Choice  ConfirmChoice
}

// Render defaults to cancel and exposes both explicit keys.
func (prompt ConfirmPrompt) Render(current theme.Theme, width int) string {
	choice := prompt.Choice
	if choice == "" || choice == ConfirmAwaiting {
		choice = CancelSelected
	}
	confirm := prompt.Confirm
	cancel := prompt.Cancel
	confirm.Enabled = true
	cancel.Enabled = true
	confirmText := confirm.plain()
	cancelText := cancel.plain() + " (默认)"
	if choice == ConfirmSelected {
		confirmText = current.Glyphs.Cursor + " " + confirmText
	} else {
		cancelText = current.Glyphs.Cursor + " " + cancelText
	}
	controls := confirmText + " · " + cancelText
	prefix := current.Glyphs.Warning + " "
	messageWidth := width -
		layout.VisibleWidth(prefix) -
		layout.VisibleWidth(controls) -
		1
	message := layout.TruncateRight(
		nonBlank(prompt.Message, "确认当前操作？"),
		max(0, messageWidth),
		current.UseASCII,
	)
	line := prefix + message + " " + controls
	line = layout.ClipRight(line, width)
	if choice == ConfirmSelected {
		return current.Paint(theme.Error, line)
	}
	return current.Paint(theme.Warning, line)
}
