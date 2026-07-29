// Package layout owns responsive terminal geometry and keyboard focus order.
package layout

import "fmt"

const (
	MinimumWidth  = 80
	MinimumHeight = 24
)

// Mode identifies the responsive pane arrangement.
type Mode string

const (
	Blocked Mode = "blocked"
	Wide    Mode = "trace-main-coach"
	Split   Mode = "main-coach"
	Narrow  Mode = "main-overlay"
)

// Plan is the stable AppShell geometry for one terminal size.
type Plan struct {
	Mode          Mode
	Width         int
	Height        int
	ContentHeight int
	TraceWidth    int
	MainWidth     int
	CoachWidth    int
}

// Calculate applies the DESIGN breakpoints and pane width priorities.
func Calculate(width, height int) Plan {
	plan := Plan{
		Mode:          Blocked,
		Width:         width,
		Height:        height,
		ContentHeight: max(0, height-2),
	}
	if width < MinimumWidth || height < MinimumHeight {
		return plan
	}

	switch {
	case width >= 160 && height >= 48:
		plan.Mode = Wide
		plan.TraceWidth = 20
		plan.CoachWidth = min(38, width*35/100)
		plan.MainWidth = width - plan.TraceWidth - plan.CoachWidth
	case width >= 110 && height >= 36:
		plan.Mode = Split
		plan.CoachWidth = min(38, width*35/100)
		plan.CoachWidth = max(30, plan.CoachWidth)
		plan.MainWidth = width - plan.CoachWidth
	default:
		plan.Mode = Narrow
		plan.MainWidth = width
	}
	return plan
}

// Validate confirms that a plan keeps the primary pane usable.
func (plan Plan) Validate() error {
	if plan.Width < 0 || plan.Height < 0 {
		return fmt.Errorf("terminal dimensions cannot be negative")
	}
	if plan.Mode == Blocked {
		return nil
	}
	if plan.MainWidth < 52 {
		return fmt.Errorf("main pane width %d is below 52", plan.MainWidth)
	}
	if plan.TraceWidth+plan.MainWidth+plan.CoachWidth != plan.Width {
		return fmt.Errorf("pane widths do not fill terminal width")
	}
	if plan.CoachWidth > 0 && plan.CoachWidth > plan.Width*35/100 {
		return fmt.Errorf("coach pane exceeds 35 percent")
	}
	return nil
}
