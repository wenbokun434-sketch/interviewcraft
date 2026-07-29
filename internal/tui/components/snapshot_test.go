package components

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
)

func TestAppShellResponsiveSnapshots(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
	}{
		{name: "wide_160x48", width: 160, height: 48},
		{name: "split_120x36", width: 120, height: 36},
		{name: "narrow_80x24", width: 80, height: 24},
		{name: "blocked_72x22", width: 72, height: 22},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			shell := snapshotShell(t, test.width, test.height, false)
			rendered, err := shell.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertSnapshotDimensions(t, rendered, test.width, test.height)
			snapshot := compactSnapshot(rendered)
			if os.Getenv("PRINT_SNAPSHOTS") == "1" {
				t.Logf("%s\n%s", test.name, snapshot)
				return
			}
			path := filepath.Join("testdata", test.name+".golden")
			expected, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read snapshot %s: %v", path, err)
			}
			if snapshot != string(expected) {
				t.Fatalf(
					"snapshot %s changed\n--- want ---\n%s\n--- got ---\n%s",
					test.name,
					expected,
					snapshot,
				)
			}
		})
	}
}

func TestAppShellASCIISmokeAndNarrowOverlay(t *testing.T) {
	t.Parallel()

	shell := snapshotShell(t, 80, 24, true)
	shell.Overlay = &Pane{
		Title: "COACH",
		Lines: []string{
			"-- Coach is ready when you need it --",
			"[1] 解释概念",
		},
	}
	rendered, err := shell.Render()
	if err != nil {
		t.Fatalf("Render ASCII overlay: %v", err)
	}
	assertSnapshotDimensions(t, rendered, 80, 24)
	if strings.ContainsAny(rendered, "┌┐└┘│─✓›▌") {
		t.Fatalf("ASCII render contains Unicode UI glyphs:\n%s", rendered)
	}
	for _, expected := range []string{"+", "|", "> COACH", "[Esc] 返回"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("ASCII overlay missing %q", expected)
		}
	}
}

func snapshotShell(t *testing.T, width, height int, ascii bool) AppShell {
	t.Helper()
	current := testTheme(t, ascii, true)
	return AppShell{
		Width:           width,
		Height:          height,
		Screen:          "Interview / 项目深挖",
		Provider:        StatusBadge{State: BadgeReady, Text: "ollama/qwen3-coder-very-long-model-name"},
		ActivitySummary: "14:04 coach · 14:06 已保存回答 · C:\\候选人\\very\\long\\resume-final.pdf",
		Trace: Pane{
			Title: "ANSWER TRACE",
			Lines: []string{
				"14:02 scene",
				"14:03 问题 Q1",
				"14:04 coach L2",
				"14:06 answer saved",
			},
		},
		Main: Pane{
			Title:  "QUESTION 01/03",
			Status: `C:\候选人\very\long\workspace\resume-final.pdf`,
			State:  PaneFocused,
			Lines: []string{
				"请说明你如何处理缓存失效，并给出项目中的具体取舍。",
				"",
				"candidate:",
				"我先确认一致性目标，再选择主动失效与 TTL 的组合。",
				"",
				"在这里继续输入回答…",
				"2 行 · [Ctrl+Enter] 提交",
			},
		},
		Coach: Pane{
			Title: "COACH",
			State: PaneInactive,
			Lines: []string{
				"-- Coach 随时可用 --",
				"[1] 澄清概念",
				"[2] 检查回答结构",
				"[3] 标记待复习主题",
			},
		},
		Commands: []KeyHint{
			{Key: "Ctrl+Enter", Action: "提交", Enabled: true},
			{Key: "c", Action: "Coach", Enabled: true},
			{Key: "?", Action: "快捷键", Enabled: true},
			{Key: "q", Action: "退出", Enabled: true},
		},
		Theme: current,
	}
}

func assertSnapshotDimensions(t *testing.T, rendered string, width, height int) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("snapshot rows=%d, want=%d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("snapshot row %d width=%d, want=%d", index, got, width)
		}
	}
}

func compactSnapshot(rendered string) string {
	lines := strings.Split(rendered, "\n")
	var output strings.Builder
	for index := 0; index < len(lines); {
		end := index + 1
		for end < len(lines) && lines[end] == lines[index] {
			end++
		}
		if end-index == 1 {
			fmt.Fprintf(&output, "%02d: %s\n", index, strings.TrimRight(lines[index], " "))
		} else {
			fmt.Fprintf(
				&output,
				"%02d-%02d: %s\n",
				index,
				end-1,
				strings.TrimRight(lines[index], " "),
			)
		}
		index = end
	}
	return output.String()
}
