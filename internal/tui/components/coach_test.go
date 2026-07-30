package components

import (
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestHintMeterAndCoachPaneExposePolicyAndFourStates(t *testing.T) {
	t.Parallel()

	current := noColorInterviewTheme(t, false, true)
	ready := "Coach ready"
	shortcuts := []CoachShortcut{
		{Key: "1", Label: "解释概念", Enabled: true},
		{Key: "2", Label: "给我提示", Enabled: true},
		{Key: "3", Label: "梳理回答结构", Enabled: true},
	}
	base := CoachPane{
		Meter: HintMeter{
			Mode:     contracts.ScenarioStrict,
			Limit:    1,
			MaxLevel: contracts.HelpL2,
		},
		Shortcuts: shortcuts,
		Operation: async.NewSucceeded(ready),
		Focused:   true,
	}
	lines, err := base.Render(current, 36, 20)
	if err != nil {
		t.Fatalf("Render empty: %v", err)
	}
	rendered := strings.Join(lines, "\n")
	for _, expected := range []string{
		"STRICT · hints 0/1 · max L2",
		"quota [○]",
		"COACH READY",
		"[1] 解释概念",
		"[2] 给我提示",
		"[3] 梳理回答结构",
		"默认不暂停主计时",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("empty CoachPane missing %q:\n%s", expected, rendered)
		}
	}

	thinking := base
	partial := "coach: thinking"
	thinking.Operation = async.NewStreaming(&partial)
	lines, err = thinking.Render(current, 36, 20)
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "coach: thinking") {
		t.Fatalf("thinking CoachPane=%q err=%v", lines, err)
	}

	failure := domainerr.New(
		domainerr.CodeDependencyUnavailable,
		"ask Coach",
		"Coach Provider 暂不可用。",
		"继续独立作答。",
		true,
	)
	failed := base
	failed.Operation = async.NewFailed[string](failure)
	lines, err = failed.Render(current, 36, 20)
	rendered = strings.Join(lines, "\n")
	if err != nil ||
		!strings.Contains(rendered, "Coach Provider 暂不可用") ||
		!strings.Contains(rendered, "[t] 重试") {
		t.Fatalf("failed CoachPane=%q err=%v", lines, err)
	}

	history := base
	history.Meter.Used = 1
	history.History = []CoachEntry{{
		ID:         "coach-1",
		At:         time.Date(2026, 7, 30, 14, 6, 0, 0, time.Local),
		Level:      contracts.HelpL2,
		Tags:       []string{"Redis", "trade-off"},
		Content:    "先明确读写路径，再说明一致性边界。",
		PolicyNote: "保持练习边界。",
		Outcome:    "still_confused",
	}}
	lines, err = history.Render(current, 42, 24)
	rendered = strings.Join(lines, "\n")
	for _, expected := range []string{
		"hints 1/1",
		"COACH · L2 · 14:06",
		"先明确读写路径",
		"topics: Redis, trade-off",
		"learning: still_confused",
		"[u] 已理解",
	} {
		if err != nil || !strings.Contains(rendered, expected) {
			t.Errorf("history CoachPane missing %q err=%v:\n%s", expected, err, rendered)
		}
	}
}

func TestHintMeterASCIIUnlimitedHasTextFallback(t *testing.T) {
	t.Parallel()

	current := noColorInterviewTheme(t, true, true)
	rendered := strings.Join((HintMeter{
		Mode:      contracts.ScenarioCoach,
		Used:      3,
		Unlimited: true,
		MaxLevel:  contracts.HelpL3,
	}).Render(current, 40), "\n")
	if !strings.Contains(rendered, "hints unlimited") ||
		!strings.Contains(rendered, "max L3") ||
		strings.Contains(rendered, "∞") {
		t.Fatalf("ASCII HintMeter=%q", rendered)
	}
}

func TestCoachPaneCompactOverlayKeepsInputAndAllSixShortcuts(t *testing.T) {
	t.Parallel()

	current := noColorInterviewTheme(t, false, true)
	ready := "Coach ready"
	shortcuts := []CoachShortcut{
		{Key: "1", Label: "解释概念", Enabled: false, Reason: "额度已用完"},
		{Key: "2", Label: "给我提示", Enabled: false, Reason: "额度已用完"},
		{Key: "3", Label: "梳理回答结构", Enabled: false, Reason: "额度已用完"},
		{Key: "4", Label: "检查我的思路", Enabled: false, Reason: "额度已用完"},
		{Key: "5", Label: "解释失败", Enabled: false, Reason: "额度已用完"},
		{Key: "6", Label: "加入复习", Enabled: false, Reason: "额度已用完"},
	}
	pane := CoachPane{
		Meter: HintMeter{
			Mode:     contracts.ScenarioStandard,
			Used:     9,
			Limit:    2,
			MaxLevel: contracts.HelpL2,
		},
		Shortcuts:  shortcuts,
		Draft:      "请检查这段中文思路，但不要代替我回答。",
		Focused:    true,
		PauseOnAsk: true,
		Operation:  async.NewSucceeded(ready),
		History: []CoachEntry{{
			Level:   contracts.HelpL2,
			Content: "先说一个约束，再比较两种选择。",
			Outcome: "understood",
		}},
	}
	lines, err := pane.Render(current, 78, 20)
	if err != nil {
		t.Fatalf("Render compact CoachPane: %v", err)
	}
	rendered := strings.Join(lines, "\n")
	for _, expected := range []string{
		"STANDARD · hints 2/2 · max L2",
		"[1] 解释概念 — 额度已用完",
		"more: [4] 检查我的思路",
		"[6] 加入复习",
		"YOUR QUESTION",
		"请检查这段中文思路",
		"pause_reason=coach_help",
		"[Ctrl+P] 暂停并求教",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("compact CoachPane missing %q:\n%s", expected, rendered)
		}
	}

	if _, err := pane.Render(current, 0, 20); err == nil {
		t.Fatal("zero-width CoachPane unexpectedly rendered")
	}
	invalid := pane
	invalid.Operation = async.State[string]{}
	if _, err := invalid.Render(current, 40, 20); err == nil {
		t.Fatal("invalid Coach operation unexpectedly rendered")
	}
	if lines := (HintMeter{}).Render(current, 0); lines != nil {
		t.Fatalf("zero-width HintMeter=%#v", lines)
	}
	unicodeUnlimited := (HintMeter{
		Mode:      contracts.ScenarioCoach,
		Unlimited: true,
		MaxLevel:  contracts.HelpL4,
	}).Render(current, 36)
	if !strings.Contains(strings.Join(unicodeUnlimited, "\n"), "∞") {
		t.Fatalf("Unicode HintMeter=%#v", unicodeUnlimited)
	}
}
