package coding

import (
	"fmt"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// Render returns an exact terminal frame with a separate specification,
// editor, persistent RunSummary, and context command bar.
func (model *Model) Render() (string, error) {
	if model == nil {
		return "", fmt.Errorf("coding model is nil")
	}
	model.mu.RLock()
	width := model.Width
	height := model.Height
	current := model.Theme
	workspace := cloneWorkspace(model.workspace)
	loaded := model.loaded
	source := model.source
	language := model.language
	cursorRune := model.cursorRune
	draftRestored := model.draftRestored
	specOffset := model.specOffset
	helpOpen := model.helpOpen
	activeFocus := model.focus.Active()
	operation := model.operation
	operationErr := model.operationErr
	running := model.running
	runAttempted := model.runAttempted
	elapsed := model.elapsed
	coachNote := model.coachNote
	model.mu.RUnlock()

	if width < 4 || height < 4 {
		return "", fmt.Errorf("coding workbench requires at least 4x4 cells")
	}
	plan := layout.Calculate(width, height)
	if plan.Mode == layout.Blocked {
		shell := components.AppShell{
			Width: width, Height: height, Screen: "P-05 CODE INTERVIEW",
			Provider: components.StatusBadge{
				State: components.BadgeDisabled, Text: "Runner optional",
			},
			Trace: components.Pane{Title: "SPEC"},
			Main:  components.Pane{Title: "EDITOR"},
			Coach: components.Pane{Title: "RUN SUMMARY"},
			Theme: current,
		}
		return shell.Render()
	}

	status := model.RunnerStatus()
	statusBadge := components.StatusBadge{
		State: components.BadgeReady, Text: "Runner ready",
	}
	if !status.Enabled {
		statusBadge = components.StatusBadge{
			State: components.BadgeDisabled, Text: "Runner disabled",
		}
	}
	questionTitle := "coding question"
	if loaded && strings.TrimSpace(workspace.Question.Title) != "" {
		questionTitle = workspace.Question.Title
	}
	activity := activityLabel(operation)
	header := " P-05 · " + questionTitle + " · " + statusBadge.Render(current)
	if activity != "" {
		header += " · " + activity
	}
	top := current.Paint(theme.Rule, frameRule(
		current.Glyphs.TopLeft,
		current.Glyphs.TopRight,
		current.Glyphs.Horizontal,
		header+" ",
		width,
	))

	contentHeight := height - 2
	summaryHeight := 8
	if height == layout.MinimumHeight {
		summaryHeight = 7
	}
	topHeight := contentHeight - summaryHeight
	if topHeight < 8 {
		return "", fmt.Errorf("coding content height %d is too small", contentHeight)
	}

	editorState := components.CodeEditorEditing
	if draftRestored {
		editorState = components.CodeEditorDraftRestored
	} else if !status.Enabled {
		editorState = components.CodeEditorRunnerDisabled
	}
	editor := components.CodeEditor{
		Language: language, Source: source, CursorRune: cursorRune,
		State: editorState, Focused: activeFocus == focusEditor,
	}

	var upper []string
	if helpOpen {
		help := components.Pane{
			Title: "SHORTCUT HELP", State: components.PaneOverlay,
			Lines: helpLines(),
		}
		var err error
		upper, err = help.Render(current, width, topHeight)
		if err != nil {
			return "", fmt.Errorf("render coding help: %w", err)
		}
	} else if width >= 110 {
		specWidth := max(34, width*35/100)
		editorWidth := width - specWidth
		specPane, err := renderSpecPane(
			current, workspace.Question, loaded, specOffset,
			activeFocus == focusSpec, specWidth, topHeight,
		)
		if err != nil {
			return "", err
		}
		editorLines, err := editor.Render(current, editorWidth-2, topHeight-2)
		if err != nil {
			return "", fmt.Errorf("render CodeEditor: %w", err)
		}
		editorPane := components.Pane{
			Title: "EDITOR", Status: editorStatus(language, running),
			State: paneState(activeFocus == focusEditor), Lines: editorLines,
		}
		editorPaneLines, err := editorPane.Render(current, editorWidth, topHeight)
		if err != nil {
			return "", fmt.Errorf("render editor pane: %w", err)
		}
		upper = joinCodingColumns(specPane, editorPaneLines)
	} else {
		specHeight := 7
		editorHeight := topHeight - specHeight
		specPane, err := renderSpecPane(
			current, workspace.Question, loaded, specOffset,
			activeFocus == focusSpec, width, specHeight,
		)
		if err != nil {
			return "", err
		}
		editorLines, err := editor.Render(current, width-2, editorHeight-2)
		if err != nil {
			return "", fmt.Errorf("render narrow CodeEditor: %w", err)
		}
		editorPane := components.Pane{
			Title: "EDITOR", Status: editorStatus(language, running),
			State: paneState(activeFocus == focusEditor), Lines: editorLines,
		}
		editorPaneLines, err := editorPane.Render(current, width, editorHeight)
		if err != nil {
			return "", fmt.Errorf("render narrow editor pane: %w", err)
		}
		upper = append(specPane, editorPaneLines...)
	}

	summary := buildRunSummary(
		workspace.LatestRun,
		status,
		operationErr,
		running,
		runAttempted,
		elapsed,
		coachNote,
		activeFocus == focusSummary,
	)
	summaryLines, err := summary.Render(current, width-2, summaryHeight-2)
	if err != nil {
		return "", fmt.Errorf("render RunSummary: %w", err)
	}
	summaryPane := components.Pane{
		Title: "RUN SUMMARY", Status: runStatusLabel(summary.State),
		State: paneState(activeFocus == focusSummary), Lines: summaryLines,
	}
	summaryPaneLines, err := summaryPane.Render(current, width, summaryHeight)
	if err != nil {
		return "", fmt.Errorf("render run summary pane: %w", err)
	}

	commands := commandLine(current, status, running, workspace.LatestRun)
	bottom := current.Paint(theme.Rule, frameRule(
		current.Glyphs.BottomLeft,
		current.Glyphs.BottomRight,
		current.Glyphs.Horizontal,
		" "+commands+" ",
		width,
	))
	lines := make([]string, 0, height)
	lines = append(lines, top)
	lines = append(lines, upper...)
	lines = append(lines, summaryPaneLines...)
	lines = append(lines, bottom)
	if len(lines) != height {
		return "", fmt.Errorf("coding workbench rendered %d rows, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			return "", fmt.Errorf("coding row %d has width %d, want %d", index, got, width)
		}
	}
	return strings.Join(lines, "\n"), nil
}

func renderSpecPane(
	current theme.Theme,
	question corecoding.Question,
	loaded bool,
	offset int,
	focused bool,
	width int,
	height int,
) ([]string, error) {
	innerWidth := max(1, width-2)
	lines := problemLines(question, loaded, innerWidth)
	offset = min(max(0, offset), max(0, len(lines)-1))
	lines = lines[offset:]
	pane := components.Pane{
		Title: "SPEC / TRACE", Status: "[↑↓] scroll",
		State: paneState(focused), Lines: lines,
	}
	rendered, err := pane.Render(current, width, height)
	if err != nil {
		return nil, fmt.Errorf("render specification pane: %w", err)
	}
	return rendered, nil
}

func problemLines(
	question corecoding.Question,
	loaded bool,
	width int,
) []string {
	if !loaded {
		return []string{"正在加载代码题…", "规格与编辑器将保持独立。"}
	}
	result := []string{strings.TrimSpace(question.Description)}
	result = append(result, "", "INPUT · "+strings.TrimSpace(question.InputFormat))
	result = append(result, "OUTPUT · "+strings.TrimSpace(question.OutputFormat), "", "CONSTRAINTS")
	for _, constraint := range question.Constraints {
		result = append(result, "- "+strings.TrimSpace(constraint))
	}
	result = append(result,
		"",
		"TARGET · time "+question.TargetComplexity.Time+" · space "+question.TargetComplexity.Space,
		"",
		"PUBLIC EXAMPLES",
	)
	for index, example := range question.Examples {
		result = append(result,
			fmt.Sprintf("%d. input: %s", index+1, example.Input),
			"   output: "+example.Output,
			"   "+example.Explanation,
		)
	}
	wrapped := make([]string, 0, len(result))
	for _, line := range result {
		wrapped = append(wrapped, layout.Wrap(line, width)...)
	}
	return wrapped
}

func buildRunSummary(
	latest *corecoding.RunSnapshot,
	status corecoding.RunnerStatus,
	operationErr *domainerr.Error,
	running bool,
	runAttempted bool,
	elapsed time.Duration,
	coachNote string,
	focused bool,
) components.RunSummary {
	summary := components.RunSummary{
		State: components.RunSummaryNotRun, Elapsed: elapsed,
		Notice: operationErr, CoachNote: coachNote, Focused: focused,
	}
	if running {
		summary.State = components.RunSummaryRunning
		return summary
	}
	if latest != nil {
		summary.HasResult = true
		summary.PublicTests = append([]corecoding.PublicTestResult(nil), latest.Result.PublicTests...)
		summary.HiddenTests = latest.Result.HiddenTests
		summary.Runtime = latest.Runtime
		switch {
		case latest.Result.Status == corecoding.RunPassed:
			summary.State = components.RunSummaryPassed
		case latest.Result.ErrorKind == corecoding.ErrorTimeout:
			summary.State = components.RunSummaryTimeout
		case latest.Result.ErrorKind == corecoding.ErrorOutOfMemory:
			summary.State = components.RunSummaryOutOfMemory
		case latest.Result.Status == corecoding.RunFailed:
			summary.State = components.RunSummaryFailed
		default:
			summary.State = components.RunSummaryError
		}
		return summary
	}
	if runAttempted && operationErr != nil {
		if !status.Enabled || operationErr.Code == domainerr.CodeDependencyUnavailable {
			summary.State = components.RunSummaryDisabled
		} else {
			summary.State = components.RunSummaryError
		}
		return summary
	}
	if !status.Enabled {
		summary.Notice = domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"run public tests",
			"Public tests are unavailable — Docker runner is disabled.",
			"[s] Open settings；文字面试和 Coach 仍可继续。",
			true,
		)
	}
	return summary
}

func activityLabel(state async.State[Progress]) string {
	if (state.Phase == async.Streaming || state.Phase == async.Succeeded) && state.Value != nil {
		message := strings.TrimSpace(state.Value.Message)
		if message != "" {
			return message
		}
	}
	if state.Phase == async.Pending {
		return "working"
	}
	if state.Phase == async.Failed {
		return "action failed · draft preserved"
	}
	return ""
}

func editorStatus(language corecoding.Language, running bool) string {
	status := string(language)
	if running {
		status += " · editable while tests run"
	}
	return status
}

func runStatusLabel(state components.RunSummaryState) string {
	switch state {
	case components.RunSummaryRunning:
		return "running"
	case components.RunSummaryPassed:
		return "passed"
	case components.RunSummaryFailed:
		return "failed"
	case components.RunSummaryTimeout:
		return "timeout"
	case components.RunSummaryOutOfMemory:
		return "out of memory"
	case components.RunSummaryDisabled:
		return "disabled"
	case components.RunSummaryError:
		return "error"
	default:
		return "not run"
	}
}

func paneState(focused bool) components.PaneState {
	if focused {
		return components.PaneFocused
	}
	return components.PaneInactive
}

func commandLine(
	current theme.Theme,
	status corecoding.RunnerStatus,
	running bool,
	latest *corecoding.RunSnapshot,
) string {
	commands := []components.KeyHint{
		{Key: "Tab", Action: "焦点", Enabled: true},
		{Key: "Ctrl+1/2/3", Action: "语言", Enabled: !running},
		{Key: "Ctrl+S", Action: "保存", Enabled: !running},
		{Key: "Ctrl+F", Action: "格式化", Enabled: !running},
	}
	run := components.KeyHint{Key: "Ctrl+R", Action: "运行公开测试", Enabled: status.Enabled && !running}
	if running {
		run.Reason = "本次运行尚未结束"
	} else if !status.Enabled {
		run.Reason = "Runner disabled；在 Settings 启用"
	}
	commands = append(commands, run)
	if latest != nil && latest.Result.Status != corecoding.RunPassed {
		commands = append(commands, components.KeyHint{Key: "Ctrl+E", Action: "解释错误", Enabled: !running})
	}
	commands = append(commands,
		components.KeyHint{Key: "Ctrl+H", Action: "返回面试", Enabled: true},
		components.KeyHint{Key: "?", Action: "帮助", Enabled: true},
	)
	parts := make([]string, 0, len(commands))
	for _, command := range commands {
		if command.Enabled || command == run {
			parts = append(parts, command.Render(current))
		}
	}
	return strings.Join(parts, " · ")
}

func helpLines() []string {
	return []string{
		"P-05 全键盘操作",
		"Tab / Shift+Tab · 在编辑器、题目规格、运行摘要间移动焦点",
		"Ctrl+1 / Ctrl+2 / Ctrl+3 · Python / JavaScript / Java",
		"Ctrl+S · 保存草稿    Ctrl+F · 格式化    Ctrl+Z · 重置模板",
		"Ctrl+R · 运行公开测试（运行期间仍可编辑，但不可重复 Run）",
		"Ctrl+E · 解释最近一次已运行错误；Strict 模式不提供完整实现",
		"Ctrl+H · 返回文字面试    Esc / ? · 关闭帮助",
	}
}

func joinCodingColumns(left, right []string) []string {
	height := max(len(left), len(right))
	result := make([]string, height)
	for row := 0; row < height; row++ {
		if row < len(left) {
			result[row] += left[row]
		}
		if row < len(right) {
			result[row] += right[row]
		}
	}
	return result
}

func frameRule(left, right, rule, content string, width int) string {
	if width <= 0 {
		return ""
	}
	if width == 1 {
		return left
	}
	innerWidth := width - 2
	content = layout.ClipRight(content, innerWidth)
	return left + content + strings.Repeat(rule, max(0, innerWidth-layout.VisibleWidth(content))) + right
}
