package components

import (
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestAnswerTracePreservesOrderFocusAndCollapsedSummary(t *testing.T) {
	t.Parallel()

	current := noColorInterviewTheme(t, false, false)
	base := time.Date(2026, 7, 30, 14, 3, 0, 0, time.Local)
	items := []TraceItem{
		{ID: "q1", At: base, Kind: TraceQuestion, Label: "Q1"},
		{ID: "a1", At: base.Add(time.Minute), Kind: TraceAnswer, Label: "answer"},
		{ID: "p1", At: base.Add(2 * time.Minute), Kind: TracePause, Label: "paused"},
	}
	lines := (AnswerTrace{
		Items:    items,
		Selected: 1,
		Focused:  true,
	}).Render(current, 20, 3)
	rendered := strings.Join(lines, "\n")
	for _, expected := range []string{"14:03 Q1", "› 14:04 answer", "14:05 paused"} {
		if !strings.Contains(rendered, expected) {
			t.Fatalf("trace missing %q:\n%s", expected, rendered)
		}
	}
	if strings.Index(rendered, "Q1") > strings.Index(rendered, "answer") {
		t.Fatalf("trace changed supplied event order:\n%s", rendered)
	}

	collapsed := (AnswerTrace{
		Items:     items,
		Collapsed: true,
	}).Render(current, 40, 1)
	if got := strings.TrimSpace(collapsed[0]); got != "14:05 · paused · 3 events" {
		t.Fatalf("collapsed trace=%q", got)
	}
}

func TestQuestionCardAndTimerExposeTextBackedStates(t *testing.T) {
	t.Parallel()

	current := noColorInterviewTheme(t, false, false)
	card := QuestionCard{
		Index:        2,
		Total:        3,
		Prompt:       "请说明缓存失效时你如何保证一致性？",
		Intent:       "评估项目深度",
		Evidence:     []string{"fact-redis"},
		FollowUps:    1,
		MaxFollowUps: 2,
		EndCondition: "解释一种权衡",
		Estimated:    6 * time.Minute,
		State:        QuestionTimed,
	}
	rendered := strings.Join(card.Render(current, 64, 6), "\n")
	for _, expected := range []string{
		"QUESTION 02/03",
		"请说明缓存失效时你如何保证一致性？",
		"intent: 评估项目深度",
		"source: fact-redis",
		"follow-ups: 1/2",
		"close when: 解释一种权衡",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("QuestionCard missing %q:\n%s", expected, rendered)
		}
	}

	states := []struct {
		timer Timer
		want  string
	}{
		{Timer{Remaining: 12*time.Minute + 14*time.Second}, "12:14 left"},
		{Timer{Remaining: 59 * time.Second, State: TimerWarning}, "00:59 left · warning"},
		{Timer{Remaining: 5 * time.Minute, State: TimerPaused}, "paused · 05:00 left"},
		{Timer{State: TimerExpired}, "time ended"},
	}
	for _, test := range states {
		if got := test.timer.Render(current); got != test.want {
			t.Errorf("Timer.Render()=%q, want %q", got, test.want)
		}
	}
}

func TestInterviewComponentsASCIIReduceMotion(t *testing.T) {
	t.Parallel()

	current := noColorInterviewTheme(t, true, true)
	item := TraceItem{
		ID:      "answer-1",
		At:      time.Date(2026, 7, 30, 14, 7, 0, 0, time.Local),
		Kind:    TraceAnswer,
		Label:   "answer",
		Summary: "一段很长的中文回答",
	}
	rendered := strings.Join((AnswerTrace{
		Items:          []TraceItem{item},
		Focused:        true,
		JustAppendedID: item.ID,
	}).Render(current, 40, 2), "\n")
	if !strings.Contains(rendered, "> 14:07 answer") ||
		strings.Contains(rendered, "›") {
		t.Fatalf("ASCII trace=%q", rendered)
	}
}

func noColorInterviewTheme(
	t *testing.T,
	ascii bool,
	reduceMotion bool,
) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode:         theme.Auto,
		ColorMode:    theme.NoColor,
		UseASCII:     ascii,
		ReduceMotion: reduceMotion,
	})
	if err != nil {
		t.Fatalf("Resolve theme: %v", err)
	}
	return current
}
