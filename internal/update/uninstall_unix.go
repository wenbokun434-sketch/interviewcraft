//go:build !windows

package update

import (
	"errors"
	"os"
	"strings"
)

const (
	pathBegin = "# >>> InterviewCraft PATH >>>"
	pathEnd   = "# <<< InterviewCraft PATH <<<"
)

func scheduleUninstallHelper(Receipt, UninstallOptions) error {
	return errors.New("uninstall helper is only required on Windows")
}

func removeManagedPaths(receipt Receipt) error {
	for _, path := range receipt.PathFiles {
		payload, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		cleaned, err := stripManagedBlock(string(payload))
		if err != nil {
			return err
		}
		if err := writeAtomic(path, []byte(cleaned), 0o600); err != nil {
			return err
		}
	}
	return nil
}

func stripManagedBlock(value string) (string, error) {
	lines := strings.SplitAfter(value, "\n")
	inside, found := false, false
	var output strings.Builder
	for _, line := range lines {
		trimmed := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		switch trimmed {
		case pathBegin:
			if inside || found {
				return "", errors.New("managed PATH block is duplicated or nested")
			}
			inside, found = true, true
		case pathEnd:
			if !inside {
				return "", errors.New("managed PATH block is malformed")
			}
			inside = false
		default:
			if !inside {
				output.WriteString(line)
			}
		}
	}
	if inside {
		return "", errors.New("managed PATH block is incomplete")
	}
	return output.String(), nil
}

func CleanupUninstallHelper(string) {}
