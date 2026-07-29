package components

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// PaneState identifies a bordered region treatment.
type PaneState string

const (
	PaneInactive  PaneState = "inactive"
	PaneFocused   PaneState = "focused"
	PaneCollapsed PaneState = "collapsed"
	PaneOverlay   PaneState = "overlay"
)

// Pane is the only bordered content primitive in the foundation layer.
type Pane struct {
	Title  string
	Status string
	Lines  []string
	State  PaneState
	Depth  int
}

// Validate enforces the two-level pane nesting limit.
func (pane Pane) Validate() error {
	if pane.Depth < 0 || pane.Depth > 2 {
		return fmt.Errorf("pane depth %d exceeds the supported range 0..2", pane.Depth)
	}
	switch pane.State {
	case "", PaneInactive, PaneFocused, PaneCollapsed, PaneOverlay:
		return nil
	default:
		return fmt.Errorf("unsupported pane state %q", pane.State)
	}
}

// Render fills exactly width × height terminal cells.
func (pane Pane) Render(current theme.Theme, width, height int) ([]string, error) {
	if err := pane.Validate(); err != nil {
		return nil, err
	}
	if width < 4 || height < 2 {
		return nil, fmt.Errorf("pane requires at least 4x2 cells")
	}

	if pane.State == PaneCollapsed {
		label := " " + strings.ToUpper(nonBlank(pane.Title, "PANE")) + " — collapsed "
		if current.UseASCII {
			label = " " + strings.ToUpper(nonBlank(pane.Title, "PANE")) + " - collapsed "
		}
		line := framedRule(
			current.Glyphs.TopLeft,
			current.Glyphs.TopRight,
			current.Glyphs.Horizontal,
			label,
			width,
		)
		return fitLines([]string{line}, width, height), nil
	}

	title := strings.ToUpper(nonBlank(pane.Title, "PANE"))
	if pane.State == PaneFocused || pane.State == PaneOverlay {
		title = current.Glyphs.Cursor + " " + title
	}
	header := " " + title + " "
	status := strings.TrimSpace(pane.Status)
	if status != "" {
		available := width - 4 - layout.VisibleWidth(header)
		if available > 3 {
			status = layout.TruncateLeft(status, available, current.UseASCII)
			header += status + " "
		}
	}

	borderRole := theme.Rule
	if pane.State == PaneFocused {
		borderRole = theme.Focus
	} else if pane.State == PaneOverlay {
		borderRole = theme.Coach
	}
	top := framedRule(
		current.Glyphs.TopLeft,
		current.Glyphs.TopRight,
		current.Glyphs.Horizontal,
		header,
		width,
	)
	top = current.Paint(borderRole, top)

	innerWidth := width - 2
	content := make([]string, 0, len(pane.Lines))
	for _, line := range pane.Lines {
		content = appendWrapped(content, line, innerWidth)
	}
	content = fitLines(content, innerWidth, height-2)

	lines := make([]string, 0, height)
	lines = append(lines, top)
	for _, line := range content {
		left := current.Paint(borderRole, current.Glyphs.Vertical)
		right := current.Paint(borderRole, current.Glyphs.Vertical)
		lines = append(lines, left+line+right)
	}
	bottom := current.Glyphs.BottomLeft +
		strings.Repeat(current.Glyphs.Horizontal, width-2) +
		current.Glyphs.BottomRight
	lines = append(lines, current.Paint(borderRole, bottom))
	return lines, nil
}

func framedRule(left, right, rule, content string, width int) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return left
	}
	innerWidth := width - 2
	content = layout.ClipRight(content, innerWidth)
	fill := max(0, innerWidth-layout.VisibleWidth(content))
	return left + content + strings.Repeat(rule, fill) + right
}
