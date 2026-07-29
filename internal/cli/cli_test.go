package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
)

func TestRunHelpListsOrderedCommandPlaceholders(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"--help"}, &stdout, &stderr)

	if code != ExitOK {
		t.Fatalf("Run(--help) exit code = %d, want %d", code, ExitOK)
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run(--help) stderr = %q, want empty", stderr.String())
	}

	output := stdout.String()
	lastIndex := -1
	for _, name := range []string{"init", "run", "doctor", "export", "import"} {
		index := strings.Index(output, name)
		if index == -1 {
			t.Errorf("help output does not contain %q", name)
			continue
		}
		if index <= lastIndex {
			t.Errorf("help command %q is out of order", name)
		}
		lastIndex = index
	}
}

func TestRunWithoutConfigurationShowsHelp(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run(nil, &stdout, &stderr)

	if code != ExitOK {
		t.Fatalf("Run(nil) exit code = %d, want %d", code, ExitOK)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("Run(nil) output = %q, want usage", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run(nil) stderr = %q, want empty", stderr.String())
	}
}

func TestRunKnownPlaceholderIsActionable(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"export"}, &stdout, &stderr)

	if code != ExitUnavailable {
		t.Fatalf("Run(export) exit code = %d, want %d", code, ExitUnavailable)
	}
	if stdout.Len() != 0 {
		t.Fatalf("Run(export) stdout = %q, want empty", stdout.String())
	}
	for _, expected := range []string{"尚未实现", "T-018", "interviewcraft --help"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("Run(export) stderr = %q, want %q", stderr.String(), expected)
		}
	}
}

func TestRunTrainingHomeAfterInit(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("INTERVIEWCRAFT_DATA_DIR", dataDir)
	t.Setenv("COLUMNS", "80")
	t.Setenv("LINES", "24")

	var initStdout bytes.Buffer
	var initStderr bytes.Buffer
	if code := Run([]string{"init"}, &initStdout, &initStderr); code != ExitOK {
		t.Fatalf("init exit=%d stderr=%q", code, initStderr.String())
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run(
		[]string{"run", "--ascii", "--reduce-motion", "--no-color"},
		&stdout,
		&stderr,
	)
	if code != ExitOK {
		t.Fatalf("run exit=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	for _, expected := range []string{
		"InterviewCraft",
		"TRAINING",
		"还没有训练记录",
		"[n] 创建第一场模拟",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("run stdout missing %q", expected)
		}
	}
	if strings.Contains(stdout.String(), "┌") {
		t.Errorf("run --ascii output contains Unicode border")
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != 24 {
		t.Fatalf("run rows = %d, want 24", len(lines))
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != 80 {
			t.Fatalf("run row %d width=%d, want 80", index, got)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("run stderr = %q, want empty", stderr.String())
	}
}

func TestRunTrainingHomeWithoutInitShowsRecovery(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv("INTERVIEWCRAFT_DATA_DIR", dataDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"run", "--no-color"}, &stdout, &stderr)

	if code != ExitFailure {
		t.Fatalf("run exit=%d, want %d", code, ExitFailure)
	}
	if stdout.Len() != 0 {
		t.Fatalf("run stdout=%q, want empty", stdout.String())
	}
	for _, expected := range []string{"尚未初始化", "interviewcraft init"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("run stderr missing %q", expected)
		}
	}
}

func TestRunTrainingHomeRejectsUnknownOption(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"run", "--unknown"}, &stdout, &stderr)

	if code != ExitUsage {
		t.Fatalf("run exit=%d, want %d", code, ExitUsage)
	}
	if !strings.Contains(stderr.String(), "interviewcraft run --help") {
		t.Fatalf("run stderr=%q", stderr.String())
	}
}

func TestRunUnknownCommandReturnsUsageError(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"unknown"}, &stdout, &stderr)

	if code != ExitUsage {
		t.Fatalf("Run(unknown) exit code = %d, want %d", code, ExitUsage)
	}
	if stdout.Len() != 0 {
		t.Fatalf("Run(unknown) stdout = %q, want empty", stdout.String())
	}
	for _, expected := range []string{"未知命令", "unknown", "interviewcraft --help"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("Run(unknown) stderr = %q, want %q", stderr.String(), expected)
		}
	}
}

func TestRunCommandHelpDoesNotExecutePlaceholder(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	var stderr bytes.Buffer

	code := Run([]string{"doctor", "--help"}, &stdout, &stderr)

	if code != ExitOK {
		t.Fatalf("Run(doctor --help) exit code = %d, want %d", code, ExitOK)
	}
	for _, expected := range []string{"interviewcraft doctor", "Status: available."} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("Run(doctor --help) stdout = %q, want %q", stdout.String(), expected)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run(doctor --help) stderr = %q, want empty", stderr.String())
	}
}

func TestRunInitIsIdempotent(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("INTERVIEWCRAFT_DATA_DIR", dataDir)

	var firstStdout bytes.Buffer
	var firstStderr bytes.Buffer
	firstCode := Run([]string{"init"}, &firstStdout, &firstStderr)

	if firstCode != ExitOK {
		t.Fatalf(
			"first init exit=%d stdout=%q stderr=%q",
			firstCode,
			firstStdout.String(),
			firstStderr.String(),
		)
	}
	for _, path := range []string{
		filepath.Join(dataDir, "config.json"),
		filepath.Join(dataDir, "interviewcraft.db"),
		filepath.Join(dataDir, "uploads"),
		filepath.Join(dataDir, "exports"),
		filepath.Join(dataDir, "logs"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("init path %q: %v", path, err)
		}
	}

	var secondStdout bytes.Buffer
	var secondStderr bytes.Buffer
	secondCode := Run([]string{"init"}, &secondStdout, &secondStderr)

	if secondCode != ExitOK || !strings.Contains(secondStdout.String(), "已保留现有 Lite 配置") {
		t.Fatalf(
			"second init exit=%d stdout=%q stderr=%q",
			secondCode,
			secondStdout.String(),
			secondStderr.String(),
		)
	}
}

func TestRunDoctorWithoutInitShowsRecovery(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "missing")
	t.Setenv("INTERVIEWCRAFT_DATA_DIR", dataDir)

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"doctor"}, &stdout, &stderr)

	if code != ExitFailure {
		t.Fatalf("doctor exit code = %d, want %d", code, ExitFailure)
	}
	if stdout.Len() != 0 {
		t.Fatalf("doctor stdout = %q, want empty", stdout.String())
	}
	for _, expected := range []string{"尚未初始化", "interviewcraft init"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("doctor stderr = %q, want %q", stderr.String(), expected)
		}
	}
}

func TestRunDoctorHealthyLiteConfiguration(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	dataDir := filepath.Join(t.TempDir(), "data")
	t.Setenv("INTERVIEWCRAFT_DATA_DIR", dataDir)
	t.Setenv("INTERVIEWCRAFT_LLM_PROVIDER", "ollama")
	t.Setenv("INTERVIEWCRAFT_LLM_ENDPOINT", server.URL)
	t.Setenv("INTERVIEWCRAFT_LLM_MODEL", "test-model")
	t.Setenv("RUNNER_MODE", "disabled")
	t.Setenv("COLUMNS", "120")
	t.Setenv("LINES", "36")

	var initStdout bytes.Buffer
	var initStderr bytes.Buffer
	if code := Run([]string{"init"}, &initStdout, &initStderr); code != ExitOK {
		t.Fatalf("init exit=%d stderr=%q", code, initStderr.String())
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	code := Run([]string{"doctor"}, &stdout, &stderr)

	if code != ExitOK {
		t.Fatalf(
			"doctor exit=%d stdout=%q stderr=%q",
			code,
			stdout.String(),
			stderr.String(),
		)
	}
	for _, expected := range []string{
		"terminal",
		"sqlite",
		"model",
		"Runner 已禁用",
		"Lite 运行环境检查通过",
	} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("doctor stdout = %q, want %q", stdout.String(), expected)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("doctor stderr = %q, want empty", stderr.String())
	}
}
