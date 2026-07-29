package cli

import (
	"bytes"
	"strings"
	"testing"
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

	code := Run([]string{"init"}, &stdout, &stderr)

	if code != ExitUnavailable {
		t.Fatalf("Run(init) exit code = %d, want %d", code, ExitUnavailable)
	}
	if stdout.Len() != 0 {
		t.Fatalf("Run(init) stdout = %q, want empty", stdout.String())
	}
	for _, expected := range []string{"尚未实现", "T-004", "interviewcraft --help"} {
		if !strings.Contains(stderr.String(), expected) {
			t.Errorf("Run(init) stderr = %q, want %q", stderr.String(), expected)
		}
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
	for _, expected := range []string{"interviewcraft doctor", "T-004"} {
		if !strings.Contains(stdout.String(), expected) {
			t.Errorf("Run(doctor --help) stdout = %q, want %q", stdout.String(), expected)
		}
	}
	if stderr.Len() != 0 {
		t.Fatalf("Run(doctor --help) stderr = %q, want empty", stderr.String())
	}
}
