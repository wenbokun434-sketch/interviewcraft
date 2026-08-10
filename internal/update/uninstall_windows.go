//go:build windows

package update

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	pathBegin = "# >>> InterviewCraft PATH >>>"
	pathEnd   = "# <<< InterviewCraft PATH <<<"
)

func scheduleUninstallHelper(receipt Receipt, options UninstallOptions) error {
	root, err := os.MkdirTemp("", "interviewcraft-uninstall-*")
	if err != nil {
		return err
	}
	helper := filepath.Join(root, "interviewcraft-uninstall-helper.exe")
	if err := copyFile(receipt.BinaryPath, helper, 0o700); err != nil {
		_ = os.RemoveAll(root)
		return err
	}
	arguments := []string{"__uninstall-helper", "--parent", strconv.Itoa(os.Getpid()), "--receipt", options.ReceiptPath, "--data-dir", options.DataDir}
	if options.PurgeData {
		arguments = append(arguments, "--purge-data", "--confirm-purge", options.Confirmation)
	}
	command := exec.Command(helper, arguments...)
	if err := command.Start(); err != nil {
		_ = os.RemoveAll(root)
		return err
	}
	return nil
}

func removeManagedPaths(receipt Receipt) error {
	if receipt.PathTarget == "" {
		return nil
	}
	if receipt.PathTarget != `HKCU\Environment\Path` {
		return removeManagedBlock(receipt.PathTarget)
	}
	key, err := registry.OpenKey(registry.CURRENT_USER, `Environment`, registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	value, _, err := key.GetStringValue("Path")
	if err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	kept := make([]string, 0)
	for _, entry := range strings.Split(value, ";") {
		if strings.TrimSpace(entry) != "" && !strings.EqualFold(strings.TrimRight(entry, `\`), strings.TrimRight(receipt.InstallDir, `\`)) {
			kept = append(kept, entry)
		}
	}
	return key.SetStringValue("Path", strings.Join(kept, ";"))
}

func removeManagedBlock(path string) error {
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	cleaned, err := stripManagedBlock(string(payload))
	if err != nil {
		return err
	}
	return writeAtomic(path, []byte(cleaned), 0o600)
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

func CleanupUninstallHelper(path string) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err == nil {
		_ = windows.MoveFileEx(pointer, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		directory, directoryErr := windows.UTF16PtrFromString(filepath.Dir(path))
		if directoryErr == nil {
			_ = windows.MoveFileEx(directory, nil, windows.MOVEFILE_DELAY_UNTIL_REBOOT)
		}
	}
}
