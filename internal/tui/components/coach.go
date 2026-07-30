package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// HintMeter renders the current question's immutable Coach allowance.
type HintMeter struct {
	Mode      contracts.ScenarioMode
	Used      int
	Limit     int
	Unlimited bool
	MaxLevel  contracts.HelpLevel
}

// Render uses a compact quota scale plus explicit text, never color alone.
func (meter HintMeter) Render(current theme.Theme, width int) []string {
	if width <= 0 {
		return nil
	}
	mode := strings.ToUpper(strings.TrimSpace(string(meter.Mode)))
	if mode == "" {
		mode = "POLICY"
	}
	level := strings.TrimSpace(string(meter.MaxLevel))
	if level == "" {
		level = "—"
		if current.UseASCII {
			level = "-"
		}
	}
	used := max(0, meter.Used)
	if meter.Unlimited {
		quota := "∞"
		if current.UseASCII {
			quota = "unlimited"
		}
		return []string{
			current.Paint(
				theme.Coach,
				layout.TruncateRight(
					fmt.Sprintf("%s · hints %s · max %s", mode, quota, level),
					width,
					current.UseASCII,
				),
			),
			current.Paint(
				theme.Muted,
				layout.TruncateRight(
					"quota "+quota+" · guided practice",
					width,
					current.UseASCII,
				),
			),
		}
	}

	limit := max(1, meter.Limit)
	used = min(used, limit)
	full, empty := "●", "○"
	if current.UseASCII {
		full, empty = "#", "-"
	}
	scale := strings.Repeat(full, used) + strings.Repeat(empty, limit-used)
	return []string{
		current.Paint(
			theme.Coach,
			layout.TruncateRight(
				fmt.Sprintf("%s · hints %d/%d · max %s", mode, used, limit, level),
				width,
				current.UseASCII,
			),
		),
		current.Paint(
			theme.Muted,
			layout.TruncateRight(
				"quota ["+scale+"] · current question",
				width,
				current.UseASCII,
			),
		),
	}
}

// CoachShortcut is one keyboard-first Coach intent.
type CoachShortcut struct {
	Key     string
	Label   string
	Enabled bool
	Reason  string
}

// CoachEntry is one persisted learning event, separate from main transcript.
type CoachEntry struct {
	ID         string
	At         time.Time
	Level      contracts.HelpLevel
	Tags       []string
	Content    string
	PolicyNote string
	Outcome    string
}

// CoachPane is the pure renderer for Coach policy, events, and local input.
type CoachPane struct {
	Meter       HintMeter
	Shortcuts   []CoachShortcut
	History     []CoachEntry
	Draft       string
	Focused     bool
	PauseOnAsk  bool
	Operation   async.State[string]
	Selected    int
	CurrentTime time.Time
}

// Render returns Coach lines clipped to the supplied content box.
func (pane CoachPane) Render(
	current theme.Theme,
	width int,
	height int,
) ([]string, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("CoachPane dimensions must be positive")
	}
	if err := pane.Operation.Validate(); err != nil {
		return nil, err
	}

	header := []string{
		SectionLabel{Text: "Hint policy", Kind: LabelCoach}.Render(current),
	}
	header = append(header, pane.Meter.Render(current, width)...)
	header = append(header, "")

	body := make([]string, 0)
	switch pane.Operation.Phase {
	case async.Pending:
		line, err := (ActivityLine{
			State: async.NewPending[string](),
			Label: "Coach 正在准备上下文",
		}).Render(current, width)
		if err != nil {
			return nil, err
		}
		body = append(body, line)
	case async.Streaming:
		message := "coach: thinking"
		if pane.Operation.Value != nil &&
			strings.TrimSpace(*pane.Operation.Value) != "" {
			message = strings.TrimSpace(*pane.Operation.Value)
		}
		line, err := (ActivityLine{
			State: async.NewStreaming(&message),
			Label: message,
		}).Render(current, width)
		if err != nil {
			return nil, err
		}
		body = append(body, line)
	case async.Failed:
		notice, err := ErrorNotice(
			pane.Operation.Err,
			&KeyHint{Key: "t", Action: "重试", Enabled: true},
		).Render(current, width)
		if err != nil {
			return nil, err
		}
		body = append(body, notice...)
	case async.Succeeded:
		if len(pane.History) == 0 {
			body = append(body,
				current.Paint(theme.Coach, "COACH READY"),
				current.Paint(theme.Muted, "选择一个起点；默认不暂停主计时。"),
			)
		} else {
			entry := pane.History[min(
				max(pane.Selected, 0),
				len(pane.History)-1,
			)]
			body = append(body, renderCoachEntry(current, entry, width)...)
		}
	}

	footer := []string{
		"",
		SectionLabel{Text: "Quick ask", Kind: LabelCoach}.Render(current),
	}
	visible := min(3, len(pane.Shortcuts))
	for _, shortcut := range pane.Shortcuts[:visible] {
		hint := KeyHint{
			Key:     shortcut.Key,
			Action:  shortcut.Label,
			Enabled: shortcut.Enabled,
			Reason:  shortcut.Reason,
		}
		footer = append(footer, hint.Render(current))
	}
	if len(pane.Shortcuts) > visible {
		more := make([]string, 0, len(pane.Shortcuts)-visible)
		role := theme.Focus
		for _, shortcut := range pane.Shortcuts[visible:] {
			hint := KeyHint{
				Key:     shortcut.Key,
				Action:  shortcut.Label,
				Enabled: shortcut.Enabled,
				Reason:  shortcut.Reason,
			}
			more = append(more, hint.plain())
			if !shortcut.Enabled {
				role = theme.Muted
			}
		}
		wrapped := layout.Wrap(
			"more: "+strings.Join(more, " · "),
			width,
		)
		for _, line := range wrapped {
			footer = append(footer, current.Paint(role, line))
		}
	}

	footer = append(footer,
		"",
		SectionLabel{Text: "Your question", Kind: LabelCoach}.Render(current),
	)
	draft := strings.TrimSpace(pane.Draft)
	if draft == "" {
		footer = append(footer, current.Paint(
			theme.Muted,
			"输入要问 Coach 的问题…",
		))
	} else {
		footer = append(footer, layout.Wrap(draft, width)...)
	}
	mode := "timer continues"
	if pane.PauseOnAsk {
		mode = "pause_reason=coach_help"
	}
	status := "[Ctrl+Enter] 提问 · [Ctrl+P] 暂停并求教"
	if pane.Focused {
		status = current.Glyphs.Cursor + " " + status
	}
	footer = append(footer,
		current.Paint(theme.Muted, layout.TruncateRight(mode, width, current.UseASCII)),
		current.Paint(theme.Focus, layout.TruncateRight(status, width, current.UseASCII)),
	)
	if len(header)+len(footer) >= height {
		priority := append([]string(nil), footer...)
		if len(priority) > height {
			priority = priority[len(priority)-height:]
		}
		return fitLines(priority, width, height), nil
	}
	bodyHeight := height - len(header) - len(footer)
	if len(body) > bodyHeight {
		body = body[:bodyHeight]
	}
	lines := append(header, body...)
	lines = append(lines, footer...)
	return fitLines(lines, width, height), nil
}

func renderCoachEntry(
	current theme.Theme,
	entry CoachEntry,
	width int,
) []string {
	timeLabel := "--:--"
	if !entry.At.IsZero() {
		timeLabel = entry.At.Local().Format("15:04")
	}
	header := fmt.Sprintf("COACH · %s · %s", entry.Level, timeLabel)
	lines := []string{
		current.Paint(
			theme.Coach,
			layout.TruncateRight(header, width, current.UseASCII),
		),
	}
	content := strings.TrimSpace(entry.Content)
	if content == "" {
		content = "-- Coach 回复为空 --"
	}
	lines = append(lines, layout.Wrap(content, width)...)
	if len(entry.Tags) > 0 {
		lines = append(lines, current.Paint(
			theme.Muted,
			layout.TruncateRight(
				"topics: "+strings.Join(entry.Tags, ", "),
				width,
				current.UseASCII,
			),
		))
	}
	if note := strings.TrimSpace(entry.PolicyNote); note != "" {
		lines = append(lines, current.Paint(
			theme.Muted,
			layout.TruncateRight(
				"policy: "+note,
				width,
				current.UseASCII,
			),
		))
	}
	outcome := strings.TrimSpace(entry.Outcome)
	if outcome == "" || outcome == "unmarked" {
		outcome = "未标记"
	}
	lines = append(lines,
		current.Paint(theme.Muted, "learning: "+outcome),
		current.Paint(theme.Focus, "[u] 已理解 · [d] 仍困惑 · [r] 加入复习"),
	)
	return lines
}
