package components

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// NoticeKind identifies recoverable guidance.
type NoticeKind string

const (
	NoticeInfo    NoticeKind = "info"
	NoticeWarning NoticeKind = "warning"
	NoticeError   NoticeKind = "error"
	NoticeSuccess NoticeKind = "success"
)

// InlineNotice holds one message, recovery sentence, and at most one action.
type InlineNotice struct {
	Kind     NoticeKind
	Message  string
	Recovery string
	Action   *KeyHint
	Failure  *domainerr.Error
}

// ErrorNotice creates a renderer-safe notice from a typed domain error.
func ErrorNotice(failure *domainerr.Error, action *KeyHint) InlineNotice {
	return InlineNotice{
		Kind:    NoticeError,
		Action:  action,
		Failure: failure,
	}
}

// Render returns the state grammar: what happened, then what to do next.
func (notice InlineNotice) Render(current theme.Theme, width int) ([]string, error) {
	if width <= 0 {
		return nil, fmt.Errorf("notice width must be positive")
	}
	message := strings.TrimSpace(notice.Message)
	recovery := strings.TrimSpace(notice.Recovery)
	if notice.Kind == NoticeError {
		if notice.Failure == nil {
			return nil, fmt.Errorf("error notice requires a typed domain error")
		}
		message = notice.Failure.Message
		recovery = notice.Failure.RecoveryAction
	}
	if message == "" {
		return nil, fmt.Errorf("notice message cannot be blank")
	}

	symbol, role := noticeStyle(notice.Kind, current)
	lines := layout.Wrap(symbol+" "+message, width)
	if recovery != "" {
		recoveryLine := "  " + recovery
		if notice.Action != nil {
			recoveryLine += " " + notice.Action.plain()
		}
		lines = append(lines, layout.Wrap(recoveryLine, width)...)
	} else if notice.Action != nil {
		lines = append(lines, layout.Wrap("  "+notice.Action.plain(), width)...)
	}
	for index := range lines {
		if index == 0 {
			lines[index] = current.Paint(role, lines[index])
		}
	}
	return lines, nil
}

func noticeStyle(kind NoticeKind, current theme.Theme) (string, theme.Role) {
	switch kind {
	case NoticeSuccess:
		return current.Glyphs.Success, theme.Success
	case NoticeWarning:
		return current.Glyphs.Warning, theme.Warning
	case NoticeError:
		return current.Glyphs.Error, theme.Error
	default:
		return current.Glyphs.Info, theme.Info
	}
}

// ProgressState identifies determinate work.
type ProgressState string

const (
	ProgressRunning  ProgressState = "running"
	ProgressComplete ProgressState = "complete"
	ProgressFailed   ProgressState = "failed"
)

// ProgressLine renders measurable progress only.
type ProgressLine struct {
	Current int
	Total   int
	Label   string
	State   ProgressState
	Failure *domainerr.Error
}

// Render returns an eight-cell progress bar and concrete state text.
func (progress ProgressLine) Render(current theme.Theme, width int) (string, error) {
	if width <= 0 {
		return "", fmt.Errorf("progress width must be positive")
	}
	if progress.Total <= 0 || progress.Current < 0 || progress.Current > progress.Total {
		return "", fmt.Errorf("invalid progress %d/%d", progress.Current, progress.Total)
	}
	if progress.State == ProgressFailed && progress.Failure == nil {
		return "", fmt.Errorf("failed progress requires a typed domain error")
	}
	if progress.State == ProgressComplete && progress.Current != progress.Total {
		return "", fmt.Errorf("complete progress requires current to equal total")
	}

	percent := progress.Current * 100 / progress.Total
	filled := progress.Current * 8 / progress.Total
	fullGlyph := "█"
	emptyGlyph := "░"
	if current.UseASCII {
		fullGlyph = "#"
		emptyGlyph = "-"
	}
	bar := "[" + strings.Repeat(fullGlyph, filled) +
		strings.Repeat(emptyGlyph, 8-filled) + "]"
	label := nonBlank(progress.Label, "处理中")
	role := theme.Info
	line := fmt.Sprintf("%s %3d%% %s", bar, percent, label)
	switch progress.State {
	case ProgressComplete:
		role = theme.Success
		line = current.Glyphs.Success + " " + line
	case ProgressFailed:
		role = theme.Error
		line = current.Glyphs.Error + " " + label + " — " + progress.Failure.Message
	case "", ProgressRunning:
	default:
		return "", fmt.Errorf("unsupported progress state %q", progress.State)
	}
	return current.Paint(role, layout.ClipRight(line, width)), nil
}

// ActivityLine renders non-determinate typed async work.
type ActivityLine struct {
	State async.State[string]
	Label string
	Frame int
}

// Render uses motion only to communicate pending work and becomes static when
// reduced motion is enabled.
func (activity ActivityLine) Render(current theme.Theme, width int) (string, error) {
	if err := activity.State.Validate(); err != nil {
		return "", err
	}
	if width <= 0 {
		return "", fmt.Errorf("activity width must be positive")
	}
	if activity.Frame < 0 {
		return "", fmt.Errorf("activity frame cannot be negative")
	}
	label := nonBlank(activity.Label, "处理中")
	role := theme.Info
	var line string
	switch activity.State.Phase {
	case async.Pending:
		count := activity.Frame%3 + 1
		if current.ReduceMotion {
			count = 1
		}
		line = strings.Repeat(current.Glyphs.Activity, count) + " " + label
	case async.Streaming:
		if activity.State.Value != nil && strings.TrimSpace(*activity.State.Value) != "" {
			label = strings.TrimSpace(*activity.State.Value)
		}
		line = label + " " + current.Glyphs.Stream
	case async.Succeeded:
		role = theme.Success
		line = current.Glyphs.Success + " " + label
	case async.Failed:
		role = theme.Error
		line = current.Glyphs.Error + " " + activity.State.Err.Message
	}
	return current.Paint(role, layout.ClipRight(line, width)), nil
}
