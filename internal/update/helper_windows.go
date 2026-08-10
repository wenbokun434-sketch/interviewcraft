//go:build windows

package update

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
)

func scheduleHelper(state State, statePath string) error {
	helper := filepath.Join(filepath.Dir(state.StagedBinary), "interviewcraft-update-helper.exe")
	if err := copyFile(state.BinaryPath, helper, 0o700); err != nil {
		return err
	}
	command := exec.Command(helper, "__update-helper", "--state", statePath, "--parent", strconv.Itoa(os.Getpid()))
	command.Stdout, command.Stderr = nil, nil
	return command.Start()
}

func WaitForParent(ctx context.Context, pid int) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(handle)
	for {
		status, err := windows.WaitForSingleObject(handle, 250)
		if err != nil {
			return err
		}
		if status == windows.WAIT_OBJECT_0 {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
}

func helperDescription(statePath string) string {
	return fmt.Sprintf("Windows update helper for %s", statePath)
}
