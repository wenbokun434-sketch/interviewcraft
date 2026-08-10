package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
	buildversion "github.com/interviewcraft/interviewcraft/internal/version"
)

const totalStages = 8

func Run(ctx context.Context, request Request, options Options, observer Observer) (result Result, returnErr error) {
	options, err := fillOptions(options)
	if err != nil {
		return Result{}, updateFailure("configure update", "", err)
	}
	notify(observer, async.NewPending[Progress]())
	defer func() {
		if returnErr == nil {
			return
		}
		var typed *domainerr.Error
		if !errors.As(returnErr, &typed) {
			typed = updateFailure("run update", result.DiagnosticPath, returnErr)
		}
		notify(observer, async.NewFailed[Progress](typed))
	}()
	receipt, err := ReadReceipt(options.ReceiptPath)
	if err != nil {
		return result, updateFailure("read install receipt", "", err)
	}
	if err := validateInstalledIdentity(options, receipt); err != nil {
		return result, updateFailure("validate installed version", "", err)
	}
	result.CurrentVersion = options.Current.Version
	progress(observer, StageCheck, 1, "checking signed release channel")
	target := strings.TrimSpace(request.Version)
	if target == "" {
		target, err = resolveLatest(ctx, options.Client, options.LatestURL)
	}
	if err != nil || !versionPattern.MatchString(target) {
		return result, updateFailure("check latest release", "", err)
	}
	result.AvailableVersion = target
	comparison, err := compareVersions(target, options.Current.Version)
	if err != nil {
		return result, updateFailure("compare release versions", "", err)
	}
	if comparison <= 0 {
		completed := Progress{Stage: StageCheck, Current: 1, Total: totalStages, Message: "no update available"}
		notify(observer, async.NewSucceeded(completed))
		return result, nil
	}
	if request.CheckOnly {
		completed := Progress{Stage: StageCheck, Current: 1, Total: totalStages, Message: "update available"}
		notify(observer, async.NewSucceeded(completed))
		return result, nil
	}

	root := backupRootFor(options.DataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return result, updateFailure("create update workspace", root, err)
	}
	working, err := os.MkdirTemp(root, ".download-*.tmp")
	if err != nil {
		return result, updateFailure("create update download directory", root, err)
	}
	defer os.RemoveAll(working)
	base := strings.TrimRight(options.ReleaseBaseURL, "/") + "/v" + target
	manifestPath := filepath.Join(working, "release-manifest.txt")
	bundlePath := filepath.Join(working, "release-manifest.sigstore.json")
	progress(observer, StageDownload, 2, "downloading signed release metadata")
	if err := downloadFile(ctx, options.Client, base+"/release-manifest.txt", manifestPath, 1<<20); err != nil {
		return result, updateFailure("download release manifest", working, err)
	}
	if err := downloadFile(ctx, options.Client, base+"/release-manifest.sigstore.json", bundlePath, 4<<20); err != nil {
		return result, updateFailure("download release signature", working, err)
	}
	verifier := options.Verifier
	if verifier == nil {
		cosign := ""
		var err error
		if options.LocalVerifierPath != "" {
			cosign, err = prepareLocalVerifier(options.LocalVerifierPath, options.LocalVerifierSHA256)
		} else {
			cosign, err = prepareCosign(ctx, options.Client, options.GOOS, options.GOARCH, working)
		}
		if err != nil {
			return result, updateFailure("prepare pinned Cosign", working, err)
		}
		verifier = commandVerifier{commands: options.Commands, binary: cosign}
	}
	progress(observer, StageVerify, 3, "verifying publisher, manifest and archive")
	if err := verifier.VerifyBlob(ctx, manifestPath, bundlePath, identityPrefix+target, oidcIssuer); err != nil {
		return result, updateFailure("verify release signature", working, err)
	}
	manifestFile, err := os.Open(manifestPath)
	if err != nil {
		return result, updateFailure("open release manifest", working, err)
	}
	manifest, parseErr := ParseReleaseManifest(manifestFile)
	_ = manifestFile.Close()
	if parseErr != nil {
		return result, updateFailure("parse release manifest", working, parseErr)
	}
	if manifest.Version != target {
		return result, updateFailure("parse release manifest", working, errors.New("release manifest version does not match the requested release"))
	}
	asset, err := manifest.AssetFor(options.GOOS, options.GOARCH)
	if err != nil {
		return result, updateFailure("select release asset", working, err)
	}
	archivePath := filepath.Join(working, asset.Filename)
	if err := downloadFile(ctx, options.Client, base+"/"+asset.Filename, archivePath, asset.Size); err != nil {
		return result, updateFailure("download release archive", working, err)
	}
	archiveInfo, err := os.Stat(archivePath)
	if err != nil || archiveInfo.Size() != asset.Size {
		return result, updateFailure("validate release archive size", working, errors.New("archive size mismatch"))
	}
	hash, err := fileSHA256(archivePath)
	if err != nil || hash != asset.SHA256 {
		return result, updateFailure("validate release archive hash", working, errors.New("archive checksum mismatch"))
	}
	stagedDownload := filepath.Join(working, applicationName(options.GOOS))
	if err := extractReleaseBinary(archivePath, asset.Filename, stagedDownload, options.GOOS); err != nil {
		return result, updateFailure("extract verified release", working, err)
	}
	stagedInfo, err := inspectBinary(ctx, options.Commands, stagedDownload)
	if err != nil || stagedInfo.Version != target || stagedInfo.GitCommit != manifest.Commit || stagedInfo.GOOS != options.GOOS || stagedInfo.GOARCH != options.GOARCH {
		return result, updateFailure("validate staged binary", working, errors.New("staged binary metadata mismatch"))
	}
	if err := ctx.Err(); err != nil {
		return result, updateCancelled("cancel update before backup", err)
	}

	guard, err := db.AcquireMaintenance(ctx, options.DataDir)
	if err != nil {
		return result, updateFailure("acquire exclusive update lock", statePathFor(options.DataDir), err)
	}
	guardOwned := true
	defer func() {
		if guardOwned {
			_ = guard.Close()
		}
	}()
	progress(observer, StageBackup, 4, "creating and verifying complete backup")
	backupDir, err := options.CreateBackup(ctx, options.DataDir, receipt.BinaryPath, options.Current.Version, options.Now(), options.AvailableBytes)
	if err != nil {
		return result, updateFailure("create pre-update backup", root, err)
	}
	result.BackupDirectory = backupDir
	stableStaging := filepath.Join(root, "staging", filepath.Base(backupDir), applicationName(options.GOOS))
	if err := copyFile(stagedDownload, stableStaging, 0o700); err != nil {
		return result, updateFailure("stage verified update binary", root, err)
	}
	diagnostic := filepath.Join(root, "diagnostics", filepath.Base(backupDir)+"-update.log")
	state := State{
		Schema: StateSchema, Phase: PhasePrepared, FromVersion: options.Current.Version, ToVersion: target,
		DataDir: options.DataDir, BackupDirectory: backupDir, BinaryPath: receipt.BinaryPath,
		StagedBinary: stableStaging, ReceiptPath: options.ReceiptPath, DiagnosticPath: diagnostic,
		GuardToken: guard.Token(), UpdatedAt: options.Now().UTC(),
	}
	statePath := statePathFor(options.DataDir)
	if err := saveState(statePath, state); err != nil {
		return result, updateFailure("save recoverable update state", root, err)
	}
	result.DiagnosticPath = diagnostic
	if options.GOOS == "windows" && !options.ForceDirect {
		if err := options.ScheduleHelper(state, statePath); err != nil {
			return result, updateFailure("schedule Windows self-replacement", diagnostic, err)
		}
		guardOwned = false
		result.Scheduled = true
		completed := Progress{Stage: StageSwitch, Current: 5, Total: totalStages, Message: "Windows replacement helper scheduled"}
		notify(observer, async.NewSucceeded(completed))
		return result, nil
	}
	final, err := finalize(ctx, statePath, state, receipt, guard, options, observer)
	guardOwned = false
	if err != nil {
		return final, err
	}
	return final, nil
}

func Finalize(ctx context.Context, statePath string, options Options, observer Observer) (Result, error) {
	state, err := readState(statePath)
	if err != nil {
		return Result{}, updateFailure("read prepared update state", statePath, err)
	}
	guard, err := db.OpenMaintenanceGuard(state.DataDir, state.GuardToken)
	if err != nil {
		return Result{}, updateFailure("resume exclusive update guard", state.DiagnosticPath, err)
	}
	receipt, err := ReadReceipt(state.ReceiptPath)
	if err != nil {
		_ = guard.Close()
		return Result{}, updateFailure("read install receipt", state.DiagnosticPath, err)
	}
	options, err = fillFinalizeOptions(options, state)
	if err != nil {
		_ = guard.Close()
		return Result{}, updateFailure("configure update helper", state.DiagnosticPath, err)
	}
	return finalize(ctx, statePath, state, receipt, guard, options, observer)
}

func finalize(ctx context.Context, statePath string, state State, receipt Receipt, guard *db.MaintenanceGuard, options Options, observer Observer) (Result, error) {
	result := Result{CurrentVersion: state.FromVersion, AvailableVersion: state.ToVersion, BackupDirectory: state.BackupDirectory, DiagnosticPath: state.DiagnosticPath}
	rollback := func(cause error) (Result, error) {
		progress(observer, StageRollback, 8, "restoring previous binary and complete data directory")
		diagnosticDir := filepath.Join(backupRootFor(state.DataDir), "diagnostics", filepath.Base(state.BackupDirectory)+"-failed-data")
		restoreErr := options.RestoreBackup(context.Background(), state.BackupDirectory, state.DataDir, state.BinaryPath, state.GuardToken, diagnosticDir)
		if restoreErr == nil {
			receipt.Version = state.FromVersion
			receipt.DataDir = state.DataDir
			restoreErr = WriteReceipt(state.ReceiptPath, receipt)
		}
		closeErr := guard.Close()
		state.Phase = PhaseFailed
		if closeErr == nil {
			state.GuardToken = ""
		}
		state.UpdatedAt = options.Now().UTC()
		_ = saveState(statePath, state)
		if restoreErr != nil || closeErr != nil {
			return result, updateFailure("automatic rollback failed", state.DiagnosticPath, errors.Join(cause, restoreErr, closeErr))
		}
		return result, updateFailure("update failed and was rolled back", state.DiagnosticPath, cause)
	}
	progress(observer, StageSwitch, 5, "atomically switching application binary")
	if err := options.InstallBinary(state.StagedBinary, state.BinaryPath); err != nil {
		return rollback(err)
	}
	state.Phase = PhaseSwitched
	state.UpdatedAt = options.Now().UTC()
	if err := saveState(statePath, state); err != nil {
		return rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return rollback(err)
	}
	environment := []string{db.MaintenanceTokenEnv + "=" + state.GuardToken, "INTERVIEWCRAFT_DATA_DIR=" + state.DataDir}
	progress(observer, StageMigrate, 6, "running new-version SQLite migrations")
	migrate, err := options.Commands.Run(ctx, environment, state.BinaryPath, "__update-migrate", "--data-dir", state.DataDir, "--token", state.GuardToken)
	appendDiagnostic(state.DiagnosticPath, "migrate", migrate)
	if err != nil || migrate.ExitCode != 0 {
		return rollback(errors.New("new-version migration validation failed"))
	}
	progress(observer, StageDoctor, 7, "running new-version doctor and Runner validation")
	diagnose, err := options.Commands.Run(ctx, environment, state.BinaryPath, "__update-doctor", "--data-dir", state.DataDir, "--token", state.GuardToken)
	appendDiagnostic(state.DiagnosticPath, "doctor", diagnose)
	if err != nil || diagnose.ExitCode != 0 {
		return rollback(errors.New("new-version doctor validation failed"))
	}
	progress(observer, StageCommit, 8, "committing update receipt and rollback point")
	receipt.Version = state.ToVersion
	receipt.DataDir = state.DataDir
	if err := WriteReceipt(state.ReceiptPath, receipt); err != nil {
		return rollback(err)
	}
	state.Phase = PhaseCommitted
	state.UpdatedAt = options.Now().UTC()
	if err := saveState(statePath, state); err != nil {
		return rollback(err)
	}
	if err := guard.Close(); err != nil {
		return result, updateFailure("release update guard", state.DiagnosticPath, err)
	}
	state.GuardToken = ""
	state.UpdatedAt = options.Now().UTC()
	_ = saveState(statePath, state)
	result.CurrentVersion = state.ToVersion
	result.Updated = true
	completed := Progress{Stage: StageCommit, Current: totalStages, Total: totalStages, Message: "update complete"}
	notify(observer, async.NewSucceeded(completed))
	return result, nil
}

func Rollback(ctx context.Context, options Options, observer Observer) (Result, error) {
	options, err := fillOptions(options)
	if err != nil {
		return Result{}, updateFailure("configure rollback", "", err)
	}
	statePath := statePathFor(options.DataDir)
	state, err := readState(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return Result{CurrentVersion: options.Current.Version}, nil
	}
	if err != nil {
		return Result{}, updateFailure("read rollback state", statePath, err)
	}
	if (state.Phase == PhaseCommitted || state.Phase == PhaseRolledBack) && state.GuardToken != "" {
		if _, guardErr := os.Stat(db.MaintenanceGuardPath(state.DataDir)); guardErr == nil {
			guard, openErr := db.OpenMaintenanceGuard(state.DataDir, state.GuardToken)
			if openErr != nil {
				return Result{}, updateFailure("resume committed maintenance guard", state.DiagnosticPath, openErr)
			}
			if closeErr := guard.Close(); closeErr != nil {
				return Result{}, updateFailure("release committed maintenance guard", state.DiagnosticPath, closeErr)
			}
		} else if !errors.Is(guardErr, os.ErrNotExist) {
			return Result{}, updateFailure("inspect committed maintenance guard", state.DiagnosticPath, guardErr)
		}
		state.GuardToken = ""
		state.UpdatedAt = options.Now().UTC()
		if saveErr := saveState(statePath, state); saveErr != nil {
			return Result{}, updateFailure("clear committed maintenance guard", state.DiagnosticPath, saveErr)
		}
	}
	if state.Phase == PhasePrepared {
		guard, guardErr := db.OpenMaintenanceGuard(state.DataDir, state.GuardToken)
		if guardErr != nil {
			return Result{}, updateFailure("resume interrupted update", state.DiagnosticPath, guardErr)
		}
		if err := guard.Close(); err != nil {
			return Result{}, updateFailure("release interrupted update guard", state.DiagnosticPath, err)
		}
		state.Phase = PhaseFailed
		state.GuardToken = ""
		state.UpdatedAt = options.Now().UTC()
		_ = saveState(statePath, state)
		return Result{CurrentVersion: state.FromVersion, AvailableVersion: state.ToVersion, BackupDirectory: state.BackupDirectory, DiagnosticPath: state.DiagnosticPath}, nil
	}
	if state.Phase == PhaseSwitched {
		guard, guardErr := db.OpenMaintenanceGuard(state.DataDir, state.GuardToken)
		if guardErr != nil {
			return Result{}, updateFailure("resume interrupted rollback guard", state.DiagnosticPath, guardErr)
		}
		receipt, receiptErr := ReadReceipt(state.ReceiptPath)
		if receiptErr != nil {
			_ = guard.Close()
			return Result{}, updateFailure("read interrupted receipt", state.DiagnosticPath, receiptErr)
		}
		diagnosticDir := filepath.Join(backupRootFor(state.DataDir), "diagnostics", filepath.Base(state.BackupDirectory)+"-interrupted-data")
		if restoreErr := options.RestoreBackup(ctx, state.BackupDirectory, state.DataDir, state.BinaryPath, state.GuardToken, diagnosticDir); restoreErr != nil {
			_ = guard.Close()
			return Result{}, updateFailure("restore interrupted update", state.DiagnosticPath, restoreErr)
		}
		receipt.Version = state.FromVersion
		receipt.DataDir = state.DataDir
		if receiptErr = WriteReceipt(state.ReceiptPath, receipt); receiptErr != nil {
			_ = guard.Close()
			return Result{}, updateFailure("restore interrupted receipt", state.DiagnosticPath, receiptErr)
		}
		if closeErr := guard.Close(); closeErr != nil {
			return Result{}, updateFailure("release interrupted update guard", state.DiagnosticPath, closeErr)
		}
		state.Phase = PhaseFailed
		state.GuardToken = ""
		state.UpdatedAt = options.Now().UTC()
		_ = saveState(statePath, state)
		return Result{CurrentVersion: state.FromVersion, AvailableVersion: state.ToVersion, BackupDirectory: state.BackupDirectory, DiagnosticPath: state.DiagnosticPath, RolledBack: true}, nil
	}
	if state.Phase == PhaseFailed {
		if state.GuardToken != "" {
			guard, guardErr := db.OpenMaintenanceGuard(state.DataDir, state.GuardToken)
			if guardErr != nil {
				return Result{}, updateFailure("resume failed update guard", state.DiagnosticPath, guardErr)
			}
			receipt, receiptErr := ReadReceipt(state.ReceiptPath)
			if receiptErr == nil {
				diagnosticDir := filepath.Join(backupRootFor(state.DataDir), "diagnostics", filepath.Base(state.BackupDirectory)+"-recovery-"+strconv.FormatInt(options.Now().UnixNano(), 10))
				receiptErr = options.RestoreBackup(ctx, state.BackupDirectory, state.DataDir, state.BinaryPath, state.GuardToken, diagnosticDir)
			}
			if receiptErr == nil {
				receipt.Version = state.FromVersion
				receipt.DataDir = state.DataDir
				receiptErr = WriteReceipt(state.ReceiptPath, receipt)
			}
			if receiptErr != nil {
				_ = guard.Close()
				return Result{}, updateFailure("recover failed update", state.DiagnosticPath, receiptErr)
			}
			if closeErr := guard.Close(); closeErr != nil {
				return Result{}, updateFailure("release failed update guard", state.DiagnosticPath, closeErr)
			}
			state.GuardToken = ""
			state.UpdatedAt = options.Now().UTC()
			if saveErr := saveState(statePath, state); saveErr != nil {
				return Result{}, updateFailure("commit failed update recovery", state.DiagnosticPath, saveErr)
			}
		}
		return Result{CurrentVersion: options.Current.Version, AvailableVersion: state.ToVersion, BackupDirectory: state.BackupDirectory, DiagnosticPath: state.DiagnosticPath}, nil
	}
	if state.Phase != PhaseCommitted {
		return Result{CurrentVersion: options.Current.Version}, nil
	}
	receipt, err := ReadReceipt(state.ReceiptPath)
	if err != nil || receipt.Version != state.ToVersion || options.Current.Version != state.ToVersion {
		return Result{}, updateFailure("validate rollback installation", state.DiagnosticPath, errors.New("installed version does not match rollback point"))
	}
	if err := validateInstalledIdentity(options, receipt); err != nil {
		return Result{}, updateFailure("validate rollback binary", state.DiagnosticPath, err)
	}
	guard, err := db.AcquireMaintenance(ctx, state.DataDir)
	if err != nil {
		return Result{}, updateFailure("acquire rollback lock", state.DiagnosticPath, err)
	}
	defer guard.Close()
	progress(observer, StageBackup, 1, "backing up current version before rollback")
	forward, err := options.CreateBackup(ctx, state.DataDir, state.BinaryPath, state.ToVersion, options.Now(), options.AvailableBytes)
	if err != nil {
		return Result{}, updateFailure("create forward recovery backup", state.DiagnosticPath, err)
	}
	diagnosticDir := filepath.Join(backupRootFor(state.DataDir), "diagnostics", filepath.Base(forward)+"-rollback-data")
	progress(observer, StageRollback, 2, "restoring previous binary and complete data directory")
	if err := options.RestoreBackup(ctx, state.BackupDirectory, state.DataDir, state.BinaryPath, guard.Token(), diagnosticDir); err != nil {
		return Result{}, updateFailure("restore rollback point", state.DiagnosticPath, err)
	}
	restoreForward := func(cause error) (Result, error) {
		failedDir := filepath.Join(backupRootFor(state.DataDir), "diagnostics", filepath.Base(forward)+"-failed-rollback-"+strconv.FormatInt(options.Now().UnixNano(), 10))
		restoreErr := options.RestoreBackup(context.Background(), forward, state.DataDir, state.BinaryPath, guard.Token(), failedDir)
		receipt.Version = state.ToVersion
		receipt.DataDir = state.DataDir
		receiptErr := WriteReceipt(state.ReceiptPath, receipt)
		return Result{}, updateFailure("rollback failed; current version restored", state.DiagnosticPath, errors.Join(cause, restoreErr, receiptErr))
	}
	environment := []string{db.MaintenanceTokenEnv + "=" + guard.Token(), "INTERVIEWCRAFT_DATA_DIR=" + state.DataDir}
	validate, validateErr := options.Commands.Run(ctx, environment, state.BinaryPath, "__update-migrate", "--data-dir", state.DataDir, "--token", guard.Token())
	appendDiagnostic(state.DiagnosticPath, "rollback-migrate", validate)
	if validateErr == nil && validate.ExitCode == 0 {
		validate, validateErr = options.Commands.Run(ctx, environment, state.BinaryPath, "__update-doctor", "--data-dir", state.DataDir, "--token", guard.Token())
		appendDiagnostic(state.DiagnosticPath, "rollback-doctor", validate)
	}
	if validateErr != nil || validate.ExitCode != 0 {
		return restoreForward(errors.New("rollback target failed validation"))
	}
	receipt.Version = state.FromVersion
	receipt.DataDir = state.DataDir
	if err := WriteReceipt(state.ReceiptPath, receipt); err != nil {
		return restoreForward(err)
	}
	state.Phase = PhaseRolledBack
	state.ForwardBackup = forward
	state.GuardToken = guard.Token()
	state.UpdatedAt = options.Now().UTC()
	if err := saveState(statePath, state); err != nil {
		return restoreForward(err)
	}
	if err := guard.Close(); err != nil {
		return Result{}, updateFailure("release rollback guard", state.DiagnosticPath, err)
	}
	state.GuardToken = ""
	state.UpdatedAt = options.Now().UTC()
	_ = saveState(statePath, state)
	completed := Progress{Stage: StageRollback, Current: 2, Total: 2, Message: "rollback complete"}
	notify(observer, async.NewSucceeded(completed))
	return Result{CurrentVersion: state.FromVersion, AvailableVersion: state.ToVersion, RolledBack: true, BackupDirectory: state.BackupDirectory, DiagnosticPath: state.DiagnosticPath}, nil
}

func fillOptions(options Options) (Options, error) {
	if options.Current.Version == "" {
		options.Current = buildversion.Current()
	}
	if options.Current.Version == "dev" || !versionPattern.MatchString(options.Current.Version) {
		return options, errors.New("update requires an installed release build")
	}
	if options.ExecutablePath == "" {
		value, err := os.Executable()
		if err != nil {
			return options, err
		}
		options.ExecutablePath, _ = filepath.Abs(value)
	}
	if options.DataDir == "" {
		return options, errors.New("update data directory is required")
	}
	value, err := filepath.Abs(options.DataDir)
	if err != nil {
		return options, err
	}
	options.DataDir = value
	if options.ReceiptPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return options, err
		}
		options.ReceiptPath = filepath.Join(home, ".interviewcraft", "install-receipt.txt")
	}
	options.ReceiptPath, err = filepath.Abs(options.ReceiptPath)
	if err != nil {
		return options, err
	}
	if options.LatestURL == "" {
		options.LatestURL = "https://api.github.com/repos/wenbokun434-sketch/interviewcraft/releases/latest"
	}
	if options.ReleaseBaseURL == "" {
		options.ReleaseBaseURL = "https://github.com/wenbokun434-sketch/interviewcraft/releases/download"
	}
	if options.Client == nil {
		options.Client = &http.Client{Timeout: 2 * time.Minute}
	}
	if options.GOOS == "" {
		options.GOOS = runtime.GOOS
	}
	if options.GOARCH == "" {
		options.GOARCH = runtime.GOARCH
	}
	if !validPlatform(options.GOOS, options.GOARCH) {
		return options, errors.New("update platform is unsupported")
	}
	if options.Commands == nil {
		options.Commands = osCommands{}
	}
	if options.AvailableBytes == nil {
		options.AvailableBytes = diskAvailable
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	if options.ScheduleHelper == nil {
		options.ScheduleHelper = scheduleHelper
	}
	if options.LocalVerifierPath != "" {
		options.LocalVerifierPath, err = filepath.Abs(options.LocalVerifierPath)
		if err != nil || !hashPattern.MatchString(options.LocalVerifierSHA256) || !pathWithin(filepath.Clean(os.TempDir()), options.LocalVerifierPath) {
			return options, errors.New("local update verifier fixture is invalid")
		}
	}
	if options.CreateBackup == nil {
		options.CreateBackup = createBackup
	}
	if options.RestoreBackup == nil {
		options.RestoreBackup = restoreBackup
	}
	if options.InstallBinary == nil {
		options.InstallBinary = installBinary
	}
	return options, nil
}

func fillFinalizeOptions(options Options, state State) (Options, error) {
	if options.Commands == nil {
		options.Commands = osCommands{}
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	if options.CreateBackup == nil {
		options.CreateBackup = createBackup
	}
	if options.RestoreBackup == nil {
		options.RestoreBackup = restoreBackup
	}
	if options.InstallBinary == nil {
		options.InstallBinary = installBinary
	}
	options.DataDir = state.DataDir
	options.ReceiptPath = state.ReceiptPath
	options.ExecutablePath = state.BinaryPath
	return options, nil
}

func validateInstalledIdentity(options Options, receipt Receipt) error {
	if receipt.Version != options.Current.Version {
		return errors.New("install receipt version does not match running binary")
	}
	left, err := os.Stat(receipt.BinaryPath)
	if err != nil {
		return err
	}
	right, err := os.Stat(options.ExecutablePath)
	if err != nil {
		return err
	}
	if !os.SameFile(left, right) {
		return errors.New("update must run from the receipt-owned binary")
	}
	if receipt.DataDir != "" && filepath.Clean(receipt.DataDir) != filepath.Clean(options.DataDir) {
		return errors.New("install receipt data directory does not match the active configuration")
	}
	return nil
}

func inspectBinary(ctx context.Context, commands CommandRunner, path string) (buildversion.Info, error) {
	result, err := commands.Run(ctx, nil, path, "version", "--json")
	if err != nil || result.ExitCode != 0 {
		return buildversion.Info{}, errors.New("staged version command failed")
	}
	decoder := json.NewDecoder(strings.NewReader(string(result.Stdout)))
	decoder.DisallowUnknownFields()
	var info buildversion.Info
	if err := decoder.Decode(&info); err != nil || info.SchemaVersion != buildversion.SchemaVersion {
		return buildversion.Info{}, errors.New("staged version output is invalid")
	}
	return info, nil
}

func applicationName(goos string) string {
	if goos == "windows" {
		return "interviewcraft.exe"
	}
	return "interviewcraft"
}

func appendDiagnostic(path, stage string, result CommandResult) {
	_ = os.MkdirAll(filepath.Dir(path), 0o700)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, "[%s] exit=%d\n", stage, result.ExitCode)
	_, _ = file.Write(result.Stdout)
	_, _ = file.Write(result.Stderr)
	_, _ = file.WriteString("\n")
}

func progress(observer Observer, stage Stage, current int, message string) {
	value := Progress{Stage: stage, Current: current, Total: totalStages, Message: message}
	notify(observer, async.NewStreaming(&value))
}
func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func updateFailure(operation, diagnostic string, cause error) *domainerr.Error {
	if cause == nil {
		cause = errors.New("update failed")
	}
	recovery := "检查网络、磁盘和安装收据后重试。"
	if diagnostic != "" {
		recovery = "诊断材料已保留在 " + diagnostic + "；修复后重试或运行 `interviewcraft rollback`。"
	}
	return domainerr.Wrap(domainerr.CodeInvalidState, operation, "update", "InterviewCraft 更新未能安全完成。", recovery, true, cause)
}
func updateCancelled(operation string, cause error) *domainerr.Error {
	return domainerr.Wrap(domainerr.CodeOperationCancelled, operation, "update", "更新已在安全切换前取消，当前版本和数据保持不变。", "可随时重新运行 `interviewcraft update`。", true, cause)
}

func parseParentPID(value string) (int, error) {
	pid, err := strconv.Atoi(value)
	if err != nil || pid <= 0 {
		return 0, errors.New("invalid parent PID")
	}
	return pid, nil
}
