package components

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// AppShell is the stable one-row status bar, responsive content, and one-row
// context command bar shared by every interactive screen.
type AppShell struct {
	Width           int
	Height          int
	Screen          string
	Provider        StatusBadge
	ActivitySummary string
	Trace           Pane
	Main            Pane
	Coach           Pane
	Overlay         *Pane
	Commands        []KeyHint
	Theme           theme.Theme
}

// Render returns exactly Width × Height visible terminal cells.
func (shell AppShell) Render() (string, error) {
	if shell.Width < 4 || shell.Height < 4 {
		return "", fmt.Errorf("AppShell requires at least 4x4 cells")
	}
	plan := layout.Calculate(shell.Width, shell.Height)
	if err := plan.Validate(); err != nil {
		return "", err
	}

	statusText, _ := shell.Provider.plain(shell.Theme)
	status := " InterviewCraft · " +
		nonBlank(shell.Screen, "Training") + " · " +
		statusText
	if plan.Mode == layout.Split && strings.TrimSpace(shell.ActivitySummary) != "" {
		remaining := shell.Width - layout.VisibleWidth(status) - 6
		if remaining > 4 {
			status += " · " + layout.TruncateLeft(
				shell.ActivitySummary,
				remaining,
				shell.Theme.UseASCII,
			)
		}
	}
	status = layout.TruncateRight(status+" ", shell.Width-2, shell.Theme.UseASCII)
	top := framedRule(
		shell.Theme.Glyphs.TopLeft,
		shell.Theme.Glyphs.TopRight,
		shell.Theme.Glyphs.Horizontal,
		status,
		shell.Width,
	)
	top = shell.Theme.Paint(theme.Rule, top)

	var content []string
	switch plan.Mode {
	case layout.Blocked:
		content = shell.renderBlocked(plan)
	case layout.Wide:
		trace, err := shell.Trace.Render(
			shell.Theme,
			plan.TraceWidth,
			plan.ContentHeight,
		)
		if err != nil {
			return "", fmt.Errorf("render Trace pane: %w", err)
		}
		main, err := shell.Main.Render(
			shell.Theme,
			plan.MainWidth,
			plan.ContentHeight,
		)
		if err != nil {
			return "", fmt.Errorf("render Main pane: %w", err)
		}
		coach, err := shell.Coach.Render(
			shell.Theme,
			plan.CoachWidth,
			plan.ContentHeight,
		)
		if err != nil {
			return "", fmt.Errorf("render Coach pane: %w", err)
		}
		content = joinColumns(trace, main, coach)
	case layout.Split:
		main, err := shell.Main.Render(
			shell.Theme,
			plan.MainWidth,
			plan.ContentHeight,
		)
		if err != nil {
			return "", fmt.Errorf("render Main pane: %w", err)
		}
		coach, err := shell.Coach.Render(
			shell.Theme,
			plan.CoachWidth,
			plan.ContentHeight,
		)
		if err != nil {
			return "", fmt.Errorf("render Coach pane: %w", err)
		}
		content = joinColumns(main, coach)
	case layout.Narrow:
		target := shell.Main
		if shell.Overlay != nil {
			target = *shell.Overlay
			target.State = PaneOverlay
		}
		var err error
		content, err = target.Render(shell.Theme, plan.MainWidth, plan.ContentHeight)
		if err != nil {
			return "", fmt.Errorf("render narrow content: %w", err)
		}
	}

	commandText := renderCommands(shell.Commands)
	if plan.Mode == layout.Blocked {
		commandText = "[r] Retry"
	} else if shell.Overlay != nil && plan.Mode == layout.Narrow {
		commandText = "[Esc] 返回 · " + commandText
	}
	commandText = layout.TruncateRight(
		" "+commandText+" ",
		shell.Width-2,
		shell.Theme.UseASCII,
	)
	bottom := framedRule(
		shell.Theme.Glyphs.BottomLeft,
		shell.Theme.Glyphs.BottomRight,
		shell.Theme.Glyphs.Horizontal,
		commandText,
		shell.Width,
	)
	bottom = shell.Theme.Paint(theme.Rule, bottom)

	lines := make([]string, 0, shell.Height)
	lines = append(lines, top)
	lines = append(lines, content...)
	lines = append(lines, bottom)
	if len(lines) != shell.Height {
		return "", fmt.Errorf(
			"AppShell rendered %d rows, want %d",
			len(lines),
			shell.Height,
		)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != shell.Width {
			return "", fmt.Errorf(
				"AppShell row %d has width %d, want %d",
				index,
				got,
				shell.Width,
			)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func (shell AppShell) renderBlocked(plan layout.Plan) []string {
	message := fmt.Sprintf(
		"Terminal is %d×%d. InterviewCraft needs at least %d×%d.",
		plan.Width,
		plan.Height,
		layout.MinimumWidth,
		layout.MinimumHeight,
	)
	pane := Pane{
		Title: "RESIZE TERMINAL",
		State: PaneFocused,
		Lines: []string{
			shell.Theme.Glyphs.Error + " " + message,
			"Resize terminal, then press [r] Retry.",
		},
	}
	lines, err := pane.Render(shell.Theme, plan.Width, plan.ContentHeight)
	if err == nil {
		return lines
	}
	return fitLines([]string{message}, plan.Width, plan.ContentHeight)
}

func joinColumns(columns ...[]string) []string {
	height := 0
	for _, column := range columns {
		height = max(height, len(column))
	}
	lines := make([]string, height)
	for row := 0; row < height; row++ {
		var line strings.Builder
		for _, column := range columns {
			if row < len(column) {
				line.WriteString(column[row])
			}
		}
		lines[row] = line.String()
	}
	return lines
}

func renderCommands(commands []KeyHint) string {
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		if !command.Enabled {
			continue
		}
		parts = append(parts, command.plain())
	}
	if len(parts) == 0 {
		return "[?] 快捷键"
	}
	return strings.Join(parts, " · ")
}
