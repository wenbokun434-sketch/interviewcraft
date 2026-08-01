package components

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// EvidenceLinkState distinguishes a resolvable report source from an explicit
// unavailable state.
type EvidenceLinkState string

const (
	EvidenceNormal  EvidenceLinkState = "normal"
	EvidenceMissing EvidenceLinkState = "missing"
)

// EvidenceLink renders the report's signature conclusion-to-source affordance.
// The arrow and text make the state usable without color.
type EvidenceLink struct {
	ID         contracts.EvidenceID
	Label      string
	QuestionID string
	Timestamp  string
	State      EvidenceLinkState
	Focused    bool
}

// Render returns one clipped, keyboard-focusable evidence row.
func (link EvidenceLink) Render(current theme.Theme, width int) string {
	if width <= 0 {
		return ""
	}
	if link.State == EvidenceMissing || strings.TrimSpace(string(link.ID)) == "" {
		line := current.Glyphs.Warning + " evidence unavailable"
		if link.Focused {
			line = current.Glyphs.Cursor + " " + line
			return current.Paint(theme.Focus, layout.ClipRight(line, width))
		}
		return current.Paint(theme.Warning, layout.ClipRight(line, width))
	}
	arrow := "→"
	if current.UseASCII {
		arrow = "->"
	}
	parts := []string{arrow, nonBlank(link.Label, string(link.ID))}
	if strings.TrimSpace(link.QuestionID) != "" {
		parts = append(parts, link.QuestionID)
	}
	if strings.TrimSpace(link.Timestamp) != "" {
		parts = append(parts, link.Timestamp)
	}
	line := strings.Join(parts, " · ")
	role := theme.Info
	if link.Focused {
		line = current.Glyphs.Cursor + " " + line
		role = theme.Focus
	}
	return current.Paint(role, layout.ClipRight(line, width))
}

// LearningGapState is a text-backed topic priority, never a person label.
type LearningGapState string

const (
	LearningGapHigh     LearningGapState = "high"
	LearningGapMedium   LearningGapState = "medium"
	LearningGapLow      LearningGapState = "low"
	LearningGapResolved LearningGapState = "resolved"
)

// LearningGapRow renders one aggregated learning topic.
type LearningGapRow struct {
	Topic        string
	AskCount     int
	MaxHelpLevel contracts.HelpLevel
	QuestionIDs  []string
	State        LearningGapState
	Focused      bool
}

// Render returns a concise topic row with priority, ask count, help level, and
// related questions expressed in text.
func (row LearningGapRow) Render(current theme.Theme, width int) string {
	if width <= 0 {
		return ""
	}
	state := row.State
	if state == "" {
		state = LearningGapLow
	}
	questions := "no question"
	if len(row.QuestionIDs) > 0 {
		questions = strings.Join(row.QuestionIDs, ",")
	}
	line := fmt.Sprintf(
		"[%s] %s · %d asks · %s · %s",
		state,
		nonBlank(row.Topic, "未分类主题"),
		max(0, row.AskCount),
		nonBlank(string(row.MaxHelpLevel), "no hint"),
		questions,
	)
	role := learningGapRole(state)
	if row.Focused {
		line = current.Glyphs.Cursor + " " + line
		role = theme.Focus
	}
	return current.Paint(role, layout.ClipRight(line, width))
}

// LearningGapStateFor derives a calm priority from aggregate outcomes.
func LearningGapStateFor(gap corereport.LearningGap) LearningGapState {
	if gap.AskCount > 0 && gap.UnderstoodCount == gap.AskCount {
		return LearningGapResolved
	}
	if gap.ConfusedCount > 0 || gap.ReviewCount > 0 ||
		gap.MaxHelpLevel == contracts.HelpL3 ||
		gap.MaxHelpLevel == contracts.HelpL4 {
		return LearningGapHigh
	}
	if gap.AskCount >= 2 || gap.MaxHelpLevel == contracts.HelpL2 {
		return LearningGapMedium
	}
	return LearningGapLow
}

func learningGapRole(state LearningGapState) theme.Role {
	switch state {
	case LearningGapHigh:
		return theme.Warning
	case LearningGapMedium:
		return theme.Info
	case LearningGapResolved:
		return theme.Success
	default:
		return theme.Coach
	}
}
