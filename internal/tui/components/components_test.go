package components

import (
	"errors"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestMainFlowComponentsExposeKeyboardAndNonColorFocus(t *testing.T) {
	t.Parallel()

	current := testTheme(t, false, false)
	label := SectionLabel{
		Text: "interview evidence ledger",
		Kind: LabelInfo,
	}.Render(current)
	if layout.VisibleWidth(label) > 18 || label != "INTERVIEW EVIDENCE" {
		t.Fatalf("SectionLabel=%q width=%d", label, layout.VisibleWidth(label))
	}

	hint := KeyHint{Key: "Enter", Action: "开始", Enabled: true}
	if got := hint.Render(current); got != "[Enter] 开始" {
		t.Fatalf("KeyHint=%q", got)
	}
	badge := StatusBadge{State: BadgeReady, Text: "model ready"}.Render(current)
	if !strings.Contains(badge, "✓") || !strings.Contains(badge, "ready") {
		t.Fatalf("StatusBadge=%q", badge)
	}

	list := SelectableList{
		Items: []ListItem{
			{ID: "resume", Label: "简历项目深挖", Meta: "technical 3/5"},
			{
				ID:             "code",
				Label:          "缓存实现",
				Disabled:       true,
				DisabledReason: "Docker Runner 已禁用",
			},
		},
		Selected: 0,
		Focused:  true,
	}
	listLines := list.Render(current, 48, 3)
	if !strings.Contains(listLines[0], "›") ||
		!strings.Contains(listLines[1], "Docker Runner 已禁用") {
		t.Fatalf("SelectableList=%q", listLines)
	}

	pane := Pane{
		Title: "practice queue",
		State: PaneFocused,
		Lines: listLines,
	}
	paneLines, err := pane.Render(current, 52, 8)
	if err != nil {
		t.Fatalf("Pane.Render: %v", err)
	}
	assertLinesSize(t, paneLines, 52, 8)
	if !strings.Contains(paneLines[0], "› PRACTICE QUEUE") {
		t.Fatalf("focused Pane header=%q", paneLines[0])
	}
}

func TestPaneStatesAndDepthBoundary(t *testing.T) {
	t.Parallel()

	current := testTheme(t, false, false)
	for _, state := range []PaneState{
		PaneInactive,
		PaneFocused,
		PaneCollapsed,
		PaneOverlay,
	} {
		lines, err := (Pane{
			Title: "state",
			State: state,
			Depth: 2,
			Lines: []string{"content"},
		}).Render(current, 24, 4)
		if err != nil {
			t.Fatalf("Render(%s): %v", state, err)
		}
		assertLinesSize(t, lines, 24, 4)
	}
	if err := (Pane{State: PaneFocused, Depth: 3}).Validate(); err == nil {
		t.Fatal("third-level Pane unexpectedly accepted")
	}
}

func TestEmptyListAndComposerStatesRemainActionable(t *testing.T) {
	t.Parallel()

	current := testTheme(t, false, false)
	action := KeyHint{Key: "n", Action: "创建第一场模拟", Enabled: true}
	empty := SelectableList{
		EmptyMessage: "还没有训练记录",
		EmptyAction:  &action,
	}.Render(current, 48, 3)
	if !strings.Contains(empty[0], "-- 还没有训练记录 --") ||
		!strings.Contains(empty[1], "[n] 创建第一场模拟") {
		t.Fatalf("empty list=%q", empty)
	}

	composer := TextComposer{
		State:   ComposerEmpty,
		Focused: true,
	}
	emptyComposer, err := composer.Render(current, 48, 4)
	if err != nil {
		t.Fatalf("empty composer: %v", err)
	}
	if !strings.Contains(emptyComposer[0], "在这里输入回答") ||
		!strings.Contains(strings.Join(emptyComposer, "\n"), "[Ctrl+Enter] 提交") {
		t.Fatalf("empty composer=%q", emptyComposer)
	}

	composer.Text = "第一行回答\n第二行包含 CJK"
	composer.State = ComposerDraftRestored
	before := composer.Text
	focus, err := layout.NewFocusModel("composer", "coach")
	if err != nil {
		t.Fatalf("NewFocusModel: %v", err)
	}
	if err := focus.OpenOverlay("coach-overlay"); err != nil {
		t.Fatalf("OpenOverlay: %v", err)
	}
	focus.CloseOverlay()
	restored, err := composer.Render(current, 48, 6)
	if err != nil {
		t.Fatalf("draft restored composer: %v", err)
	}
	if composer.Text != before || focus.Active() != "composer" ||
		!strings.Contains(strings.Join(restored, "\n"), "已恢复本地草稿") {
		t.Fatalf("draft/focus not preserved: text=%q focus=%q lines=%q", composer.Text, focus.Active(), restored)
	}
}

func TestLoadingProgressAndActivityStates(t *testing.T) {
	t.Parallel()

	current := testTheme(t, false, false)
	reduced := testTheme(t, false, true)
	progress := ProgressLine{
		Current: 2,
		Total:   4,
		Label:   "正在提取项目与技能",
		State:   ProgressRunning,
	}
	line, err := progress.Render(current, 48)
	if err != nil {
		t.Fatalf("ProgressLine running: %v", err)
	}
	if !strings.Contains(line, "50%") || !strings.Contains(line, "████") {
		t.Fatalf("running progress=%q", line)
	}

	progress.Current = 4
	progress.State = ProgressComplete
	line, err = progress.Render(current, 48)
	if err != nil || !strings.Contains(line, "✓") || !strings.Contains(line, "100%") {
		t.Fatalf("complete progress=%q err=%v", line, err)
	}

	pending := ActivityLine{
		State: async.NewPending[string](),
		Label: "正在生成项目深挖问题",
		Frame: 2,
	}
	moving, err := pending.Render(current, 48)
	if err != nil {
		t.Fatalf("pending ActivityLine: %v", err)
	}
	static, err := pending.Render(reduced, 48)
	if err != nil {
		t.Fatalf("reduced ActivityLine: %v", err)
	}
	if !strings.HasPrefix(moving, "··· ") || !strings.HasPrefix(static, "· ") {
		t.Fatalf("activity moving=%q static=%q", moving, static)
	}

	partial := "interviewer: 正在整理追问"
	streaming := ActivityLine{
		State: async.NewStreaming(&partial),
		Label: "正在生成",
	}
	line, err = streaming.Render(current, 48)
	if err != nil || !strings.Contains(line, partial) || !strings.Contains(line, "▌") {
		t.Fatalf("streaming activity=%q err=%v", line, err)
	}
}

func TestErrorComponentsRenderTypedSafeRecovery(t *testing.T) {
	t.Parallel()

	current := testTheme(t, false, false)
	failure := domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"connect model",
		"model",
		"无法连接 Ollama。",
		"检查服务是否运行后重试。",
		true,
		errors.New("Authorization: Bearer actual-secret"),
	)
	action := KeyHint{Key: "t", Action: "重试", Enabled: true}
	lines, err := ErrorNotice(failure, &action).Render(current, 48)
	if err != nil {
		t.Fatalf("InlineNotice: %v", err)
	}
	rendered := strings.Join(lines, "\n")
	for _, expected := range []string{"! 无法连接 Ollama", "检查服务", "[t] 重试"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("notice=%q, want %q", rendered, expected)
		}
	}
	if strings.Contains(rendered, "actual-secret") || strings.Contains(rendered, "Bearer") {
		t.Fatalf("notice leaked cause: %q", rendered)
	}

	failedProgress := ProgressLine{
		Current: 1,
		Total:   4,
		Label:   "简历解析失败",
		State:   ProgressFailed,
		Failure: failure,
	}
	if line, err := failedProgress.Render(current, 48); err != nil ||
		!strings.Contains(line, "无法连接 Ollama") {
		t.Fatalf("failed ProgressLine=%q err=%v", line, err)
	}

	failedActivity := ActivityLine{
		State: async.NewFailed[string](failure),
		Label: "连接模型",
	}
	if line, err := failedActivity.Render(current, 48); err != nil ||
		!strings.Contains(line, "无法连接 Ollama") ||
		strings.Contains(line, "actual-secret") {
		t.Fatalf("failed ActivityLine=%q err=%v", line, err)
	}
}

func TestConfirmPromptDefaultsToCancelAndComposerValidatesErrors(t *testing.T) {
	t.Parallel()

	current := testTheme(t, false, false)
	prompt := ConfirmPrompt{
		Message: "结束并保存当前练习？",
		Confirm: KeyHint{Key: "y", Action: "确认"},
		Cancel:  KeyHint{Key: "Esc", Action: "取消"},
	}
	line := prompt.Render(current, 72)
	if !strings.Contains(line, "› [Esc] 取消 (默认)") {
		t.Fatalf("ConfirmPrompt=%q", line)
	}
	narrow := prompt
	narrow.Message = "结束并保存这一场包含很长说明文字的当前练习？"
	line = narrow.Render(current, 40)
	for _, expected := range []string{"[y] 确认", "[Esc] 取消"} {
		if !strings.Contains(line, expected) {
			t.Fatalf("narrow ConfirmPrompt=%q, missing %q", line, expected)
		}
	}

	composer := TextComposer{State: ComposerValidationErr}
	if _, err := composer.Render(current, 40, 4); err == nil {
		t.Fatal("validation composer without typed error unexpectedly rendered")
	}
}

func testTheme(t *testing.T, ascii, reduceMotion bool) theme.Theme {
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

func assertLinesSize(t *testing.T, lines []string, width, height int) {
	t.Helper()
	if len(lines) != height {
		t.Fatalf("lines=%d, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("line %d width=%d, want=%d: %q", index, got, width, line)
		}
	}
}
