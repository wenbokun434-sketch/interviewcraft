package components

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// CodeEditorState identifies the visible editor lifecycle without coupling the
// pure renderer to storage or the optional Runner.
type CodeEditorState string

const (
	CodeEditorEditing        CodeEditorState = "editing"
	CodeEditorDraftRestored  CodeEditorState = "draft-restored"
	CodeEditorReadonly       CodeEditorState = "readonly"
	CodeEditorRunnerDisabled CodeEditorState = "runner-disabled"
)

// CodeEditor renders a keyboard-first, three-language source buffer. CursorRune
// is a rune offset so CJK edits and focus restoration never split UTF-8 bytes.
type CodeEditor struct {
	Language   corecoding.Language
	Source     string
	CursorRune int
	State      CodeEditorState
	Focused    bool
}

// Validate rejects state that would make focus or source selection ambiguous.
func (editor CodeEditor) Validate() error {
	supported := false
	for _, language := range corecoding.Languages() {
		if editor.Language == language {
			supported = true
			break
		}
	}
	if !supported {
		return fmt.Errorf("unsupported editor language %q", editor.Language)
	}
	switch editor.State {
	case "", CodeEditorEditing, CodeEditorDraftRestored,
		CodeEditorReadonly, CodeEditorRunnerDisabled:
	default:
		return fmt.Errorf("unsupported editor state %q", editor.State)
	}
	count := utf8.RuneCountInString(editor.Source)
	if editor.CursorRune < 0 || editor.CursorRune > count {
		return fmt.Errorf("editor cursor %d outside source rune range 0..%d", editor.CursorRune, count)
	}
	return nil
}

// Render returns a fixed-size editor body with language tabs, line numbers,
// selected-line focus, and a text state that never relies on color alone.
func (editor CodeEditor) Render(
	current theme.Theme,
	width int,
	height int,
) ([]string, error) {
	if err := editor.Validate(); err != nil {
		return nil, err
	}
	if width < 16 || height < 4 {
		return nil, fmt.Errorf("CodeEditor requires at least 16x4 cells")
	}

	tabs := make([]string, 0, len(corecoding.Languages()))
	for index, language := range corecoding.Languages() {
		label := fmt.Sprintf("[%d %s]", index+1, languageLabel(language))
		if language == editor.Language {
			label = current.Glyphs.Cursor + label
			label = current.Paint(theme.Focus, label)
		} else {
			label = current.Paint(theme.Muted, label)
		}
		tabs = append(tabs, label)
	}
	lines := []string{layout.ClipRight(strings.Join(tabs, " "), width)}

	sourceLines := strings.Split(editor.Source, "\n")
	if len(sourceLines) > 1 && sourceLines[len(sourceLines)-1] == "" {
		sourceLines = sourceLines[:len(sourceLines)-1]
	}
	if len(sourceLines) == 0 {
		sourceLines = []string{""}
	}
	cursorLine, cursorColumn := codeCursor(editor.Source, editor.CursorRune)
	bodyHeight := max(1, height-2)
	start := max(0, cursorLine-bodyHeight/2)
	if start+bodyHeight > len(sourceLines) {
		start = max(0, len(sourceLines)-bodyHeight)
	}
	numberWidth := len(fmt.Sprintf("%d", len(sourceLines)))
	for row := 0; row < bodyHeight; row++ {
		lineIndex := start + row
		if lineIndex >= len(sourceLines) {
			lines = append(lines, "")
			continue
		}
		marker := " "
		role := theme.Primary
		if lineIndex == cursorLine {
			marker = current.Glyphs.Cursor
			if editor.Focused {
				role = theme.Focus
			}
		}
		prefix := fmt.Sprintf("%s %*d ", marker, numberWidth, lineIndex+1)
		available := max(0, width-layout.VisibleWidth(prefix))
		body := strings.ReplaceAll(sourceLines[lineIndex], "\t", "    ")
		rowText := prefix + layout.TruncateRight(body, available, current.UseASCII)
		lines = append(lines, current.Paint(role, layout.Fit(rowText, width)))
	}

	state := "editing"
	switch editor.State {
	case CodeEditorDraftRestored:
		state = "draft restored"
	case CodeEditorReadonly:
		state = "read only"
	case CodeEditorRunnerDisabled:
		state = "runner disabled · editor writable"
	}
	footer := fmt.Sprintf(
		"%s · Ln %d, Col %d · %s",
		languageLabel(editor.Language),
		cursorLine+1,
		cursorColumn+1,
		state,
	)
	lines = append(lines, current.Paint(
		theme.Muted,
		layout.TruncateRight(footer, width, current.UseASCII),
	))
	return fitLines(lines, width, height), nil
}

// RunSummaryState identifies every P-05 run-panel state required by DESIGN.
type RunSummaryState string

const (
	RunSummaryNotRun      RunSummaryState = "not-run"
	RunSummaryRunning     RunSummaryState = "running"
	RunSummaryPassed      RunSummaryState = "passed"
	RunSummaryFailed      RunSummaryState = "failed"
	RunSummaryTimeout     RunSummaryState = "timeout"
	RunSummaryOutOfMemory RunSummaryState = "out-of-memory"
	RunSummaryDisabled    RunSummaryState = "runner-disabled"
	RunSummaryError       RunSummaryState = "error"
)

// RunSummary is deliberately limited to the evidence-safe coding domain
// result. Typed Error causes are never rendered.
type RunSummary struct {
	State       RunSummaryState
	PublicTests []corecoding.PublicTestResult
	HiddenTests corecoding.HiddenTestSummary
	HasResult   bool
	Runtime     corecoding.RuntimeStats
	Elapsed     time.Duration
	Notice      *domainerr.Error
	CoachNote   string
	Focused     bool
}

// Validate rejects unknown display states and malformed safe counters.
func (summary RunSummary) Validate() error {
	switch summary.State {
	case "", RunSummaryNotRun, RunSummaryRunning, RunSummaryPassed,
		RunSummaryFailed, RunSummaryTimeout, RunSummaryOutOfMemory,
		RunSummaryDisabled, RunSummaryError:
	default:
		return fmt.Errorf("unsupported run summary state %q", summary.State)
	}
	if summary.HiddenTests.Passed < 0 || summary.HiddenTests.Failed < 0 {
		return fmt.Errorf("hidden test counts cannot be negative")
	}
	if summary.Runtime.DurationMilliseconds < 0 || summary.Runtime.PeakMemoryKB < 0 {
		return fmt.Errorf("runtime statistics cannot be negative")
	}
	return nil
}

// Render returns only public test names/statuses, hidden counts, safe resource
// statistics, typed recovery copy, and policy-validated Coach guidance.
func (summary RunSummary) Render(
	current theme.Theme,
	width int,
	height int,
) ([]string, error) {
	if err := summary.Validate(); err != nil {
		return nil, err
	}
	if width < 20 || height < 3 {
		return nil, fmt.Errorf("RunSummary requires at least 20x3 cells")
	}
	state := summary.State
	if state == "" {
		state = RunSummaryNotRun
	}
	lines := []string{summaryHeadline(summary, state, current)}

	if summary.HasResult {
		for _, result := range summary.PublicTests {
			marker, role := publicTestMarker(result.Status, current)
			line := fmt.Sprintf("%s %s · %s", marker, result.Name, result.Status)
			lines = append(lines, current.Paint(
				role,
				layout.TruncateRight(line, width, current.UseASCII),
			))
		}
		hidden := fmt.Sprintf(
			"hidden tests: %d passed · %d failed (counts only)",
			summary.HiddenTests.Passed,
			summary.HiddenTests.Failed,
		)
		lines = append(lines, current.Paint(
			theme.Muted,
			layout.TruncateRight(hidden, width, current.UseASCII),
		))
	}
	if summary.Notice != nil {
		if message := strings.TrimSpace(summary.Notice.Message); message != "" {
			lines = append(lines, current.Paint(
				theme.Error,
				current.Glyphs.Error+" "+message,
			))
		}
		if recovery := strings.TrimSpace(summary.Notice.RecoveryAction); recovery != "" {
			lines = append(lines, current.Paint(theme.Warning, recovery))
		}
	}
	if note := strings.TrimSpace(summary.CoachNote); note != "" {
		lines = append(lines, current.Paint(theme.Coach, "Coach · "+note))
	}
	return fitLines(lines, width, height), nil
}

func summaryHeadline(
	summary RunSummary,
	state RunSummaryState,
	current theme.Theme,
) string {
	passed := 0
	for _, result := range summary.PublicTests {
		if result.Status == corecoding.TestPassed {
			passed++
		}
	}
	total := len(summary.PublicTests)
	runtime := runtimeLabel(summary.Runtime)
	var text string
	role := theme.Muted
	switch state {
	case RunSummaryRunning:
		text = fmt.Sprintf("running public tests · %.1fs", max(0, summary.Elapsed.Seconds()))
		role = theme.Info
	case RunSummaryPassed:
		text = fmt.Sprintf("%s %d/%d public tests passed%s", current.Glyphs.Success, passed, total, runtime)
		role = theme.Success
	case RunSummaryFailed:
		text = fmt.Sprintf("%s %d/%d public tests passed%s", current.Glyphs.Error, passed, total, runtime)
		role = theme.Error
	case RunSummaryTimeout:
		text = current.Glyphs.Error + " Test stopped — execution time limit reached" + runtime
		role = theme.Error
	case RunSummaryOutOfMemory:
		text = current.Glyphs.Error + " Test stopped — memory limit reached" + runtime
		role = theme.Error
	case RunSummaryDisabled:
		text = current.Glyphs.Warning + " Public tests are unavailable — Docker runner is disabled"
		role = theme.Warning
	case RunSummaryError:
		text = current.Glyphs.Error + " Public tests could not be completed safely"
		role = theme.Error
	default:
		text = "-- public tests not run --"
	}
	if summary.Focused {
		text = current.Glyphs.Cursor + " " + text
	}
	return current.Paint(role, text)
}

func runtimeLabel(runtime corecoding.RuntimeStats) string {
	if runtime.DurationMilliseconds <= 0 && runtime.PeakMemoryKB <= 0 {
		return ""
	}
	parts := make([]string, 0, 2)
	if runtime.DurationMilliseconds > 0 {
		parts = append(parts, fmt.Sprintf("%dms", runtime.DurationMilliseconds))
	}
	if runtime.PeakMemoryKB > 0 {
		parts = append(parts, fmt.Sprintf("%.1fMB", float64(runtime.PeakMemoryKB)/1024))
	}
	return " · " + strings.Join(parts, " · ")
}

func publicTestMarker(
	status corecoding.TestStatus,
	current theme.Theme,
) (string, theme.Role) {
	switch status {
	case corecoding.TestPassed:
		return current.Glyphs.Success, theme.Success
	case corecoding.TestFailed:
		return current.Glyphs.Error, theme.Error
	default:
		return current.Glyphs.Warning, theme.Warning
	}
}

func languageLabel(language corecoding.Language) string {
	switch language {
	case corecoding.LanguagePython:
		return "Python"
	case corecoding.LanguageJavaScript:
		return "JavaScript"
	case corecoding.LanguageJava:
		return "Java"
	default:
		return string(language)
	}
}

func codeCursor(source string, cursorRune int) (int, int) {
	line := 0
	column := 0
	for index, current := range []rune(source) {
		if index == cursorRune {
			break
		}
		if current == '\n' {
			line++
			column = 0
		} else {
			column++
		}
	}
	return line, column
}
