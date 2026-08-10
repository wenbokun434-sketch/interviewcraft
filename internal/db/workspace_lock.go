package db

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

const (
	workspaceLockName    = ".interviewcraft.workspace.lock"
	maintenanceGuardName = ".interviewcraft.update.lock"
	MaintenanceTokenEnv  = "INTERVIEWCRAFT_MAINTENANCE_TOKEN"
)

// MaintenanceGuardPath returns the exact update guard path for restore code.
func MaintenanceGuardPath(dataDir string) string { return filepath.Join(dataDir, maintenanceGuardName) }

// WorkspaceLockPath returns the stable cross-process lock-file path.
func WorkspaceLockPath(dataDir string) string { return filepath.Join(dataDir, workspaceLockName) }

type workspaceLock struct{ file *os.File }

func acquireWorkspaceLock(ctx context.Context, dataDir string) (*workspaceLock, *domainerr.Error) {
	guardPath := filepath.Join(dataDir, maintenanceGuardName)
	if !maintenanceAllowed(guardPath) {
		return nil, workspaceBusy("open SQLite during maintenance", dataDir, nil)
	}
	file, err := os.OpenFile(filepath.Join(dataDir, workspaceLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, workspaceBusy("open workspace lock", dataDir, err)
	}
	if err := waitFileLock(ctx, file, false); err != nil {
		_ = file.Close()
		return nil, workspaceBusy("acquire workspace lock", dataDir, err)
	}
	if !maintenanceAllowed(guardPath) {
		_ = unlockFile(file)
		_ = file.Close()
		return nil, workspaceBusy("open SQLite during maintenance", dataDir, nil)
	}
	return &workspaceLock{file: file}, nil
}

func (lock *workspaceLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	unlockErr := unlockFile(lock.file)
	closeErr := lock.file.Close()
	lock.file = nil
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

// MaintenanceGuard prevents normal Store opens while update/rollback copies,
// migrates and validates a complete data directory.
type MaintenanceGuard struct {
	path   string
	token  string
	closed bool
}

// AcquireMaintenance waits for every open Store to close, then creates a
// process-independent guard. A second updater fails rather than sharing it.
func AcquireMaintenance(ctx context.Context, dataDir string) (*MaintenanceGuard, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(dataDir))
	if err != nil || strings.TrimSpace(dataDir) == "" {
		return nil, workspaceBusy("resolve maintenance directory", dataDir, err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, workspaceBusy("create maintenance directory", absolute, err)
	}
	guardPath := filepath.Join(absolute, maintenanceGuardName)
	if _, err := os.Stat(guardPath); err == nil {
		return nil, workspaceBusy("acquire maintenance guard", absolute, errors.New("another update is active"))
	}
	file, err := os.OpenFile(filepath.Join(absolute, workspaceLockName), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, workspaceBusy("open maintenance lock", absolute, err)
	}
	if err := waitFileLock(ctx, file, true); err != nil {
		_ = file.Close()
		return nil, workspaceBusy("wait for active database writers", absolute, err)
	}
	defer func() { _ = unlockFile(file); _ = file.Close() }()
	if _, err := os.Stat(guardPath); err == nil {
		return nil, workspaceBusy("acquire maintenance guard", absolute, errors.New("another update is active"))
	}
	token, err := randomLockToken()
	if err != nil {
		return nil, workspaceBusy("create maintenance token", absolute, err)
	}
	guardFile, err := os.OpenFile(guardPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, workspaceBusy("create maintenance guard", absolute, err)
	}
	if _, err = guardFile.WriteString(token + "\n"); err == nil {
		err = guardFile.Sync()
	}
	closeErr := guardFile.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(guardPath)
		if err == nil {
			err = closeErr
		}
		return nil, workspaceBusy("write maintenance guard", absolute, err)
	}
	return &MaintenanceGuard{path: guardPath, token: token}, nil
}

func (guard *MaintenanceGuard) Token() string {
	if guard == nil {
		return ""
	}
	return guard.token
}

func (guard *MaintenanceGuard) Close() error {
	if guard == nil || guard.closed {
		return nil
	}
	payload, err := os.ReadFile(guard.path)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(payload)) != guard.token {
		return errors.New("maintenance guard ownership changed")
	}
	if err := os.Remove(guard.path); err != nil {
		return err
	}
	guard.closed = true
	return nil
}

// OpenMaintenanceGuard resumes a guarded update helper after the parent exits.
func OpenMaintenanceGuard(dataDir, token string) (*MaintenanceGuard, error) {
	path := filepath.Join(dataDir, maintenanceGuardName)
	payload, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(payload)) != strings.TrimSpace(token) || strings.TrimSpace(token) == "" {
		return nil, workspaceBusy("resume maintenance guard", dataDir, err)
	}
	return &MaintenanceGuard{path: path, token: strings.TrimSpace(token)}, nil
}

func maintenanceAllowed(path string) bool {
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return true
	}
	if err != nil {
		return false
	}
	want := strings.TrimSpace(string(payload))
	got := strings.TrimSpace(os.Getenv(MaintenanceTokenEnv))
	return want != "" && got != "" && subtleTokenEqual(want, got)
}

func subtleTokenEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	difference := byte(0)
	for index := range left {
		difference |= left[index] ^ right[index]
	}
	return difference == 0
}

func waitFileLock(ctx context.Context, file *os.File, exclusive bool) error {
	for {
		locked, err := tryLockFile(file, exclusive)
		if err != nil {
			return err
		}
		if locked {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func randomLockToken() (string, error) {
	payload := make([]byte, 32)
	if _, err := rand.Read(payload); err != nil {
		return "", err
	}
	return hex.EncodeToString(payload), nil
}

func workspaceBusy(operation, path string, cause error) *domainerr.Error {
	if cause == nil {
		cause = errors.New("workspace is in maintenance")
	}
	return domainerr.Wrap(domainerr.CodeInvalidState, operation, "SQLite", "InterviewCraft 数据目录正在被其他进程使用或维护。", "关闭其他 InterviewCraft 进程；若更新曾中断，请运行 `interviewcraft rollback`。", true, cause)
}
