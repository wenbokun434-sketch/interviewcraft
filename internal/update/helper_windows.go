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
	if err := os.MkdirAll(filepath.Dir(state.DiagnosticPath), 0o700); err != nil {
		return err
	}
	log, err := os.OpenFile(state.DiagnosticPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	command := exec.Command(helper, "__update-helper", "--state", statePath, "--parent", strconv.Itoa(os.Getpid()))
	command.Stdout, command.Stderr = log, log
	return command.Start()
}

func scheduleRollbackHelper(statePath, dataDir, binaryPath, receiptPath string) error {
	helperRoot := filepath.Join(filepath.Dir(statePath), "staging", "rollback-helper-"+strconv.Itoa(os.Getpid()))
	helper := filepath.Join(helperRoot, "interviewcraft-rollback-helper.exe")
	if err := copyFile(binaryPath, helper, 0o700); err != nil {
		return err
	}
	state, err := readState(statePath)
	if err != nil {
		return err
	}
	log, err := os.OpenFile(state.DiagnosticPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer log.Close()
	command := exec.Command(helper, "__rollback-helper", "--state", statePath, "--data-dir", dataDir, "--binary", binaryPath, "--receipt", receiptPath, "--parent", strconv.Itoa(os.Getpid()))
	command.Stdout, command.Stderr = log, log
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
