package components

import (
	"errors"
	"strings"
	"testing"
	"time"

	corecoding "github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestCodeEditorRendersThreeLanguagesCJKCursorAndRestoredDraft(t *testing.T) {
	current := codingTheme(t, false)
	editor := CodeEditor{
		Language:   corecoding.LanguagePython,
		Source:     "def pair_sum(nums, target):\n    # 中文草稿\n    return []",
		CursorRune: 36,
		State:      CodeEditorDraftRestored,
		Focused:    true,
	}
	lines, err := editor.Render(current, 72, 10)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	rendered := strings.Join(lines, "\n")
	for _, want := range []string{
		"Python", "JavaScript", "Java", "中文草稿", "draft restored", "Ln 2",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("editor missing %q:\n%s", want, rendered)
		}
	}
	assertCodingLines(t, lines, 72, 10)
}

func TestCodeEditorRunnerDisabledStillSaysWritableAndValidatesCursor(t *testing.T) {
	current := codingTheme(t, true)
	editor := CodeEditor{
		Language:   corecoding.LanguageJava,
		Source:     "class Solution {}",
		CursorRune: 17,
		State:      CodeEditorRunnerDisabled,
	}
	lines, err := editor.Render(current, 64, 6)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if rendered := strings.Join(lines, "\n"); !strings.Contains(rendered, "runner disabled") ||
		!strings.Contains(rendered, "editor writable") {
		t.Fatalf("disabled editor copy missing:\n%s", rendered)
	}
	editor.CursorRune++
	if _, err := editor.Render(current, 64, 6); err == nil {
		t.Fatal("cursor outside rune range should fail")
	}
}

func TestRunSummaryCoversEmptyRunningPassFailureAndSafeTypedError(t *testing.T) {
	current := codingTheme(t, false)
	cases := []struct {
		name    string
		summary RunSummary
		want    string
	}{
		{name: "empty", summary: RunSummary{State: RunSummaryNotRun}, want: "public tests not run"},
		{name: "running", summary: RunSummary{State: RunSummaryRunning, Elapsed: 2400 * time.Millisecond}, want: "2.4s"},
		{
			name: "passed",
			summary: RunSummary{
				State: RunSummaryPassed, HasResult: true,
				PublicTests: []corecoding.PublicTestResult{
					{Name: "example_1", Status: corecoding.TestPassed},
					{Name: "example_2", Status: corecoding.TestPassed},
				},
				HiddenTests: corecoding.HiddenTestSummary{Passed: 3},
				Runtime: corecoding.RuntimeStats{
					DurationMilliseconds: 124,
					PeakMemoryKB:         32 * 1024,
				},
			},
			want: "2/2 public tests passed",
		},
		{name: "failed", summary: RunSummary{State: RunSummaryFailed}, want: "0/0 public tests passed"},
		{name: "timeout", summary: RunSummary{State: RunSummaryTimeout}, want: "execution time limit reached"},
		{name: "oom", summary: RunSummary{State: RunSummaryOutOfMemory}, want: "memory limit reached"},
		{name: "disabled", summary: RunSummary{State: RunSummaryDisabled}, want: "Docker runner is disabled"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			lines, err := test.summary.Render(current, 78, 7)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if rendered := strings.Join(lines, "\n"); !strings.Contains(rendered, test.want) {
				t.Fatalf("missing %q:\n%s", test.want, rendered)
			}
			assertCodingLines(t, lines, 78, 7)
		})
	}

	secret := `C:\host\secret.go /tmp/runner stderr hidden_input=42`
	notice := domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"run code",
		"docker",
		"代码执行器当前不可用。",
		"返回编辑器或检查 Runner 健康状态。",
		true,
		errors.New(secret),
	)
	lines, err := (RunSummary{
		State:  RunSummaryError,
		Notice: notice,
	}).Render(current, 78, 7)
	if err != nil {
		t.Fatalf("Render typed error: %v", err)
	}
	rendered := strings.Join(lines, "\n")
	if strings.Contains(rendered, secret) || strings.Contains(rendered, "/tmp/") || strings.Contains(rendered, "stderr") {
		t.Fatalf("typed cause leaked:\n%s", rendered)
	}
	if !strings.Contains(rendered, notice.Message) || !strings.Contains(rendered, notice.RecoveryAction) {
		t.Fatalf("safe recovery copy missing:\n%s", rendered)
	}
}

func TestRunSummaryShowsHiddenCountsWithoutHiddenCasesOrExpectedValues(t *testing.T) {
	current := codingTheme(t, true)
	summary := RunSummary{
		State: RunSummaryFailed, HasResult: true,
		PublicTests: []corecoding.PublicTestResult{
			{Name: "example_1", Status: corecoding.TestFailed},
		},
		HiddenTests: corecoding.HiddenTestSummary{Passed: 2, Failed: 1},
		CoachNote:   "检查补数映射更新顺序，不要直接索取完整实现。",
	}
	lines, err := summary.Render(current, 80, 8)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	rendered := strings.Join(lines, "\n")
	for _, want := range []string{"example_1", "2 passed", "1 failed", "counts only", "Coach"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q:\n%s", want, rendered)
		}
	}
	for _, forbidden := range []string{"hidden_case", "expected", "hidden input"} {
		if strings.Contains(strings.ToLower(rendered), forbidden) {
			t.Fatalf("leaked %q:\n%s", forbidden, rendered)
		}
	}
}

func codingTheme(t *testing.T, ascii bool) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode: theme.Dark, ColorMode: theme.NoColor, UseASCII: ascii,
	})
	if err != nil {
		t.Fatalf("theme.Resolve: %v", err)
	}
	return current
}

func assertCodingLines(t *testing.T, lines []string, width, height int) {
	t.Helper()
	if len(lines) != height {
		t.Fatalf("height=%d want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("line %d width=%d want %d: %q", index, got, width, line)
		}
	}
}
