package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// TraceKind identifies one immutable training-evidence event.
type TraceKind string

const (
	TraceQuestion TraceKind = "question"
	TraceAnswer   TraceKind = "answer"
	TraceCoach    TraceKind = "coach"
	TraceCode     TraceKind = "code"
	TracePause    TraceKind = "pause"
	TraceReport   TraceKind = "report"
	TraceStatus   TraceKind = "status"
)

// TraceItem is one already-persisted event shown in storage order.
type TraceItem struct {
	ID      string
	At      time.Time
	Kind    TraceKind
	Label   string
	Summary string
}

// AnswerTrace renders the immutable, timestamped evidence rail.
type AnswerTrace struct {
	Items          []TraceItem
	Selected       int
	Focused        bool
	Collapsed      bool
	JustAppendedID string
}

// Render keeps the supplied event order and exposes a non-color focus marker.
func (trace AnswerTrace) Render(
	current theme.Theme,
	width int,
	height int,
) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	if len(trace.Items) == 0 {
		return fitLines([]string{
			current.Paint(theme.Muted, "-- 还没有面试事件 --"),
		}, width, height)
	}
	selected := min(max(trace.Selected, 0), len(trace.Items)-1)
	if trace.Collapsed {
		item := trace.Items[len(trace.Items)-1]
		line := fmt.Sprintf(
			"%s · %s · %d events",
			traceTime(item.At),
			nonBlank(item.Label, string(item.Kind)),
			len(trace.Items),
		)
		return fitLines([]string{
			current.Paint(
				traceRole(item.Kind),
				layout.TruncateRight(line, width, current.UseASCII),
			),
		}, width, height)
	}

	start := 0
	if selected >= height {
		start = selected - height + 1
	}
	if len(trace.Items)-start < height {
		start = max(0, len(trace.Items)-height)
	}
	end := min(len(trace.Items), start+height)
	lines := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		item := trace.Items[index]
		marker := " "
		role := traceRole(item.Kind)
		if index == selected {
			marker = current.Glyphs.Cursor
			if trace.Focused {
				role = theme.Focus
			}
		}
		label := nonBlank(item.Label, string(item.Kind))
		line := fmt.Sprintf(
			"%s %s %s",
			marker,
			traceTime(item.At),
			label,
		)
		if strings.TrimSpace(item.Summary) != "" && width >= 28 {
			line += " · " + oneLine(item.Summary)
		}
		if item.ID == trace.JustAppendedID &&
			!current.ReduceMotion &&
			!trace.Focused {
			role = theme.Info
		}
		lines = append(lines, current.Paint(
			role,
			layout.TruncateRight(line, width, current.UseASCII),
		))
	}
	return fitLines(lines, width, height)
}

// QuestionCardState identifies the single visible question treatment.
type QuestionCardState string

const (
	QuestionActive QuestionCardState = "active"
	QuestionTimed  QuestionCardState = "timed"
	QuestionCode   QuestionCardState = "code"
	QuestionClosed QuestionCardState = "closed"
)

// QuestionCard renders one current question and its confirmed constraints.
type QuestionCard struct {
	Index        int
	Total        int
	Prompt       string
	Intent       string
	Evidence     []string
	Generic      bool
	FollowUps    int
	MaxFollowUps int
	EndCondition string
	Estimated    time.Duration
	State        QuestionCardState
}

// Render returns a compact card without creating a nested border.
func (card QuestionCard) Render(
	current theme.Theme,
	width int,
	height int,
) []string {
	if width <= 0 || height <= 0 {
		return nil
	}
	index := max(1, card.Index)
	total := max(index, card.Total)
	label := fmt.Sprintf("QUESTION %02d/%02d", index, total)
	if card.State == QuestionClosed {
		label += " · closed"
	} else if card.State == QuestionCode {
		label += " · code"
	}
	lines := []string{
		SectionLabel{Text: label, Kind: LabelInfo}.Render(current),
	}
	prompt := strings.TrimSpace(card.Prompt)
	if prompt == "" {
		prompt = "当前会话没有可用题目。"
	}
	lines = append(lines, layout.Wrap(prompt, width)...)

	meta := make([]string, 0, 3)
	if strings.TrimSpace(card.Intent) != "" {
		meta = append(meta, "intent: "+oneLine(card.Intent))
	}
	source := "generic"
	if !card.Generic && len(card.Evidence) > 0 {
		source = strings.Join(card.Evidence, ",")
	}
	meta = append(meta, "source: "+source)
	meta = append(meta, fmt.Sprintf(
		"follow-ups: %d/%d",
		max(0, card.FollowUps),
		max(0, card.MaxFollowUps),
	))
	if card.Estimated > 0 {
		meta = append(meta, fmt.Sprintf(
			"planned: %dm",
			max(1, int(card.Estimated.Round(time.Minute)/time.Minute)),
		))
	}
	lines = append(lines, current.Paint(
		theme.Muted,
		layout.TruncateRight(
			strings.Join(meta, " · "),
			width,
			current.UseASCII,
		),
	))
	if strings.TrimSpace(card.EndCondition) != "" && height >= 6 {
		lines = append(lines, current.Paint(
			theme.Muted,
			layout.TruncateRight(
				"close when: "+oneLine(card.EndCondition),
				width,
				current.UseASCII,
			),
		))
	}
	return fitLines(lines, width, height)
}

// TimerState is an explicit text-backed timing state.
type TimerState string

const (
	TimerNormal  TimerState = "normal"
	TimerWarning TimerState = "warning"
	TimerPaused  TimerState = "paused"
	TimerExpired TimerState = "expired"
)

// Timer renders remaining time without relying on color.
type Timer struct {
	Remaining time.Duration
	State     TimerState
}

// Render returns normal, warning, paused, or ended text.
func (timer Timer) Render(current theme.Theme) string {
	remaining := max(timer.Remaining, 0)
	value := formatClock(remaining)
	switch timer.State {
	case TimerWarning:
		return current.Paint(
			theme.Warning,
			value+" left · warning",
		)
	case TimerPaused:
		return current.Paint(
			theme.Warning,
			"paused · "+value+" left",
		)
	case TimerExpired:
		return current.Paint(theme.Error, "time ended")
	default:
		return current.Paint(theme.Info, value+" left")
	}
}

func traceTime(value time.Time) string {
	if value.IsZero() {
		return "--:--"
	}
	return value.Local().Format("15:04")
}

func traceRole(kind TraceKind) theme.Role {
	switch kind {
	case TraceQuestion:
		return theme.Info
	case TraceCoach:
		return theme.Coach
	case TracePause:
		return theme.Warning
	case TraceReport:
		return theme.Success
	default:
		return theme.Primary
	}
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func formatClock(value time.Duration) string {
	seconds := int(value.Round(time.Second) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}
