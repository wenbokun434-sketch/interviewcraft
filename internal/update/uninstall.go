package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/credentials"
)

type UninstallOptions struct {
	ReceiptPath      string
	ExecutablePath   string
	DataDir          string
	GOOS             string
	PurgeData        bool
	Confirmation     string
	ForceDirect      bool
	RemoveCredential func(string) error
	RemovePaths      func(Receipt) error
	RemoveTree       func(string) error
	ScheduleHelper   func(Receipt, UninstallOptions) error
}

type UninstallResult struct {
	Version   string
	Scheduled bool
	Purged    bool
}

// Uninstall removes only the receipt-owned binary and managed PATH entries.
// Data and credentials are preserved unless an exact, double-confirmed purge
// is requested.
func Uninstall(ctx context.Context, options UninstallOptions) (UninstallResult, error) {
	options, receipt, err := prepareUninstall(options, true)
	if err != nil {
		return UninstallResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return UninstallResult{}, err
	}
	if options.GOOS == "windows" && !options.ForceDirect {
		if err := options.ScheduleHelper(receipt, options); err != nil {
			return UninstallResult{}, err
		}
		return UninstallResult{Version: receipt.Version, Scheduled: true, Purged: options.PurgeData}, nil
	}
	return finalizeUninstall(ctx, options, receipt)
}

// FinalizeUninstall is the narrow entry used by the Windows helper after the
// installed executable exits. It revalidates the receipt and exact purge target.
func FinalizeUninstall(ctx context.Context, options UninstallOptions) (UninstallResult, error) {
	options.ForceDirect = true
	options, receipt, err := prepareUninstall(options, false)
	if err != nil {
		return UninstallResult{}, err
	}
	return finalizeUninstall(ctx, options, receipt)
}

func prepareUninstall(options UninstallOptions, validateExecutable bool) (UninstallOptions, Receipt, error) {
	var err error
	if options.ReceiptPath == "" {
		home, homeErr := os.UserHomeDir()
		if homeErr != nil {
			return options, Receipt{}, homeErr
		}
		options.ReceiptPath = filepath.Join(home, ".interviewcraft", "install-receipt.txt")
	}
	options.ReceiptPath, err = filepath.Abs(options.ReceiptPath)
	if err != nil {
		return options, Receipt{}, err
	}
	receipt, err := ReadReceipt(options.ReceiptPath)
	if err != nil {
		return options, Receipt{}, err
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.ExecutablePath == "" {
		options.ExecutablePath, err = os.Executable()
		if err != nil {
			return options, Receipt{}, err
		}
	}
	options.ExecutablePath, err = filepath.Abs(options.ExecutablePath)
	if err != nil {
		return options, Receipt{}, err
	}
	if validateExecutable {
		installed, statErr := os.Stat(receipt.BinaryPath)
		running, runningErr := os.Stat(options.ExecutablePath)
		if statErr != nil || runningErr != nil || !os.SameFile(installed, running) {
			return options, Receipt{}, errors.New("uninstall must run from the receipt-owned binary")
		}
	}
	if options.DataDir == "" {
		options.DataDir = receipt.DataDir
	}
	if options.DataDir != "" {
		options.DataDir, err = filepath.Abs(options.DataDir)
		if err != nil {
			return options, Receipt{}, err
		}
	}
	if options.PurgeData {
		if receipt.DataDir == "" || !samePath(receipt.DataDir, options.DataDir, options.GOOS) {
			return options, Receipt{}, errors.New("purge requires a receipt-bound data directory")
		}
		if err := validatePurgeTarget(options.DataDir, receipt.InstallDir, options.Confirmation, options.GOOS); err != nil {
			return options, Receipt{}, err
		}
	}
	if options.RemoveCredential == nil {
		options.RemoveCredential = removeSystemCredential
	}
	if options.RemovePaths == nil {
		options.RemovePaths = removeManagedPaths
	}
	if options.RemoveTree == nil {
		options.RemoveTree = os.RemoveAll
	}
	if options.ScheduleHelper == nil {
		options.ScheduleHelper = scheduleUninstallHelper
	}
	return options, receipt, nil
}

func finalizeUninstall(ctx context.Context, options UninstallOptions, receipt Receipt) (UninstallResult, error) {
	if err := ctx.Err(); err != nil {
		return UninstallResult{}, err
	}
	if options.PurgeData {
		if err := options.RemoveCredential(options.DataDir); err != nil {
			return UninstallResult{}, errors.New("remove system credential before purge: " + err.Error())
		}
	}
	if err := options.RemovePaths(receipt); err != nil {
		return UninstallResult{}, err
	}
	if err := os.Remove(receipt.BinaryPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return UninstallResult{}, err
	}
	if options.PurgeData {
		if err := options.RemoveTree(options.DataDir); err != nil {
			return UninstallResult{}, err
		}
		if err := options.RemoveTree(backupRootFor(options.DataDir)); err != nil {
			return UninstallResult{}, err
		}
	}
	if err := os.Remove(options.ReceiptPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return UninstallResult{}, err
	}
	_ = os.Remove(receipt.InstallDir)
	return UninstallResult{Version: receipt.Version, Purged: options.PurgeData}, nil
}

func validatePurgeTarget(dataDir, installDir, confirmation, goos string) error {
	if err := validateScopedDirectory(dataDir); err != nil {
		return err
	}
	if strings.TrimSpace(confirmation) == "" || !samePath(dataDir, strings.TrimSpace(confirmation), goos) {
		return errors.New("purge requires --confirm-purge with the exact canonical data directory")
	}
	info, err := os.Lstat(dataDir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("purge refuses a symbolic-link data directory")
	}
	for _, broad := range broadDirectories() {
		if broad != "" && samePath(dataDir, broad, goos) {
			return errors.New("purge target is too broad")
		}
	}
	if samePath(dataDir, installDir, goos) || pathWithin(dataDir, installDir) || pathWithin(installDir, dataDir) {
		return errors.New("purge target overlaps the application installation")
	}
	return nil
}

func broadDirectories() []string {
	home, _ := os.UserHomeDir()
	working, _ := os.Getwd()
	return []string{filepath.VolumeName(home) + string(filepath.Separator), home, os.TempDir(), working}
}

func samePath(left, right, goos string) bool {
	leftAbsolute, leftErr := filepath.Abs(left)
	rightAbsolute, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return false
	}
	leftAbsolute, rightAbsolute = filepath.Clean(leftAbsolute), filepath.Clean(rightAbsolute)
	if goos == "windows" {
		return strings.EqualFold(leftAbsolute, rightAbsolute)
	}
	return leftAbsolute == rightAbsolute
}

func removeSystemCredential(dataDir string) error {
	account, err := credentials.Account(dataDir)
	if err != nil {
		return err
	}
	err = (credentials.SystemStore{}).Delete(credentials.Service, account)
	if errors.Is(err, credentials.ErrNotFound) {
		return nil
	}
	return err
}
