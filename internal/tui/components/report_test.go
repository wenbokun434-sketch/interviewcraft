package components

import (
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
)

func TestEvidenceLinkNormalMissingAndASCII(t *testing.T) {
	t.Parallel()

	current := testTheme(t, false, false)
	line := (EvidenceLink{
		ID:         "answer-q2",
		Label:      "answer",
		QuestionID: "Q2",
		Timestamp:  "14:07",
		Focused:    true,
	}).Render(current, 40)
	for _, expected := range []string{"›", "→", "answer", "Q2", "14:07"} {
		if !strings.Contains(line, expected) {
			t.Errorf("normal link missing %q: %q", expected, line)
		}
	}
	missing := (EvidenceLink{State: EvidenceMissing}).Render(current, 30)
	if !strings.Contains(missing, "evidence unavailable") {
		t.Fatalf("missing link=%q", missing)
	}
	ascii := testTheme(t, true, true)
	asciiLine := (EvidenceLink{
		ID: "answer-q2", Label: "answer",
	}).Render(ascii, 30)
	if !strings.Contains(asciiLine, "->") || strings.Contains(asciiLine, "→") {
		t.Fatalf("ASCII link=%q", asciiLine)
	}
	if layout.VisibleWidth(asciiLine) > 30 {
		t.Fatalf("ASCII width=%d", layout.VisibleWidth(asciiLine))
	}
}

func TestLearningGapRowUsesTopicPriorityAndTextState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		gap  corereport.LearningGap
		want LearningGapState
	}{
		{
			name: "resolved",
			gap: corereport.LearningGap{
				AskCount: 2, UnderstoodCount: 2,
			},
			want: LearningGapResolved,
		},
		{
			name: "high",
			gap: corereport.LearningGap{
				AskCount: 1, ConfusedCount: 1,
			},
			want: LearningGapHigh,
		},
		{
			name: "medium",
			gap: corereport.LearningGap{
				AskCount: 1, MaxHelpLevel: contracts.HelpL2,
			},
			want: LearningGapMedium,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := LearningGapStateFor(test.gap); got != test.want {
				t.Fatalf("state=%s want=%s", got, test.want)
			}
		})
	}
	current := testTheme(t, false, false)
	line := (LearningGapRow{
		Topic:        "Redis consistency",
		AskCount:     2,
		MaxHelpLevel: contracts.HelpL2,
		QuestionIDs:  []string{"Q1", "Q2"},
		State:        LearningGapHigh,
		Focused:      true,
	}).Render(current, 60)
	for _, expected := range []string{
		"[high]", "Redis consistency", "2 asks", "L2", "Q1,Q2",
	} {
		if !strings.Contains(line, expected) {
			t.Errorf("row missing %q: %q", expected, line)
		}
	}
}
