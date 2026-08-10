package update

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/db"
	buildversion "github.com/interviewcraft/interviewcraft/internal/version"
)

const (
	testFromVersion = "1.0.0"
	testToVersion   = "1.1.0"
	testCommit      = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

type fixtureVerifier struct{ err error }

func (verifier fixtureVerifier) VerifyBlob(_ context.Context, _, _, identity, issuer string) error {
	if identity != identityPrefix+testToVersion || issuer != oidcIssuer {
		return errors.New("unexpected signing identity")
	}
	return verifier.err
}

type fixtureCommands struct {
	mu           sync.Mutex
	dataDir      string
	failMigrate  bool
	failDoctor   bool
	migrateValue string
	calls        []string
}

func (commands *fixtureCommands) Run(_ context.Context, _ []string, _ string, arguments ...string) (CommandResult, error) {
	commands.mu.Lock()
	defer commands.mu.Unlock()
	if len(arguments) == 2 && arguments[0] == "version" && arguments[1] == "--json" {
		payload, _ := json.Marshal(buildversion.Info{
			SchemaVersion: buildversion.SchemaVersion,
			Version:       testToVersion,
			GitCommit:     testCommit,
			BuildTime:     "2026-08-11T00:00:00Z",
			GOOS:          "windows",
			GOARCH:        "amd64",
		})
		return CommandResult{Stdout: payload}, nil
	}
	if len(arguments) > 0 {
		commands.calls = append(commands.calls, arguments[0])
	}
	if len(arguments) > 0 && arguments[0] == "__update-migrate" {
		if commands.migrateValue != "" {
			_ = os.WriteFile(filepath.Join(commands.dataDir, "workspace.txt"), []byte(commands.migrateValue), 0o600)
		}
		if commands.failMigrate {
			return CommandResult{Stderr: []byte("private migration failure"), ExitCode: 1}, nil
		}
	}
	if len(arguments) > 0 && arguments[0] == "__update-doctor" && commands.failDoctor {
		return CommandResult{Stderr: []byte("private doctor failure"), ExitCode: 1}, nil
	}
	return CommandResult{}, nil
}

type updateFixture struct {
	options  Options
	commands *fixtureCommands
	server   *httptest.Server
	dataDir  string
	binary   string
	receipt  string
	archive  []byte
	manifest string
}

func newUpdateFixture(t *testing.T) *updateFixture {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, ".interviewcraft")
	installDir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(installDir, "interviewcraft.exe")
	if err := os.WriteFile(binary, []byte("old-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "workspace.txt"), []byte("old-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dataDir, "empty-exports"), 0o700); err != nil {
		t.Fatal(err)
	}
	receiptPath := filepath.Join(dataDir, "install-receipt.txt")
	if err := WriteReceipt(receiptPath, Receipt{Version: testFromVersion, InstallDir: installDir, BinaryPath: binary}); err != nil {
		t.Fatal(err)
	}
	archive := zipPayload(t, "interviewcraft.exe", []byte("new-binary"))
	manifest := fixtureManifest(testToVersion, testCommit, archive, false)
	fixture := &updateFixture{dataDir: dataDir, binary: binary, receipt: receiptPath, archive: archive, manifest: manifest}
	fixture.server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/latest":
			_, _ = io.WriteString(writer, `{"tag_name":"v`+testToVersion+`"}`)
		case "/v" + testToVersion + "/release-manifest.txt":
			_, _ = io.WriteString(writer, fixture.manifest)
		case "/v" + testToVersion + "/release-manifest.sigstore.json":
			_, _ = io.WriteString(writer, `{"verified":true}`)
		case "/v" + testToVersion + "/interviewcraft_" + testToVersion + "_windows_amd64.zip":
			_, _ = writer.Write(fixture.archive)
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(fixture.server.Close)
	fixture.commands = &fixtureCommands{dataDir: dataDir, migrateValue: "migrated-data"}
	fixture.options = Options{
		Client:         fixture.server.Client(),
		Current:        buildversion.Info{SchemaVersion: buildversion.SchemaVersion, Version: testFromVersion, GitCommit: "bbbbbbb", GOOS: "windows", GOARCH: "amd64"},
		ExecutablePath: binary,
		DataDir:        dataDir,
		ReceiptPath:    receiptPath,
		LatestURL:      fixture.server.URL + "/latest",
		ReleaseBaseURL: fixture.server.URL,
		GOOS:           "windows",
		GOARCH:         "amd64",
		Verifier:       fixtureVerifier{},
		Commands:       fixture.commands,
		AvailableBytes: func(string) (uint64, error) { return 1 << 40, nil },
		Now:            func() time.Time { return time.Date(2026, 8, 11, 1, 2, 3, 4, time.UTC) },
		ForceDirect:    true,
	}
	return fixture
}

func (fixture *updateFixture) read(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func TestRunUpdateAndRollbackCompleteData(t *testing.T) {
	fixture := newUpdateFixture(t)
	var states []async.State[Progress]
	result, err := Run(context.Background(), Request{}, fixture.options, func(state async.State[Progress]) {
		states = append(states, state)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Updated || result.CurrentVersion != testToVersion || fixture.read(t, fixture.binary) != "new-binary" || fixture.read(t, filepath.Join(fixture.dataDir, "workspace.txt")) != "migrated-data" {
		t.Fatalf("unexpected update result: %+v", result)
	}
	assertProgress(t, states, []Stage{StageCheck, StageDownload, StageVerify, StageBackup, StageSwitch, StageMigrate, StageDoctor, StageCommit})
	receipt, err := ReadReceipt(fixture.receipt)
	if err != nil || receipt.Version != testToVersion {
		t.Fatalf("receipt was not updated: %+v, %v", receipt, err)
	}

	fixture.options.Current.Version = testToVersion
	fixture.commands.migrateValue = ""
	rolledBack, err := Rollback(context.Background(), fixture.options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !rolledBack.RolledBack || rolledBack.CurrentVersion != testFromVersion || fixture.read(t, fixture.binary) != "old-binary" || fixture.read(t, filepath.Join(fixture.dataDir, "workspace.txt")) != "old-data" {
		t.Fatalf("rollback did not restore matching binary/data: %+v", rolledBack)
	}
	if info, err := os.Stat(filepath.Join(fixture.dataDir, "empty-exports")); err != nil || !info.IsDir() {
		t.Fatalf("rollback lost an empty data directory: %v", err)
	}
	receipt, err = ReadReceipt(fixture.receipt)
	if err != nil || receipt.Version != testFromVersion {
		t.Fatalf("rollback receipt mismatch: %+v, %v", receipt, err)
	}
}

func TestRunNoUpdateCreatesNoBackup(t *testing.T) {
	fixture := newUpdateFixture(t)
	result, err := Run(context.Background(), Request{Version: testFromVersion}, fixture.options, nil)
	if err != nil || result.Updated || result.BackupDirectory != "" {
		t.Fatalf("no-update result: %+v, %v", result, err)
	}
	if _, err := os.Stat(backupRootFor(fixture.dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("no-update check created backup workspace: %v", err)
	}
}

func TestRunCheckOnlyReportsAvailableWithoutBackup(t *testing.T) {
	fixture := newUpdateFixture(t)
	result, err := Run(context.Background(), Request{CheckOnly: true}, fixture.options, nil)
	if err != nil || result.AvailableVersion != testToVersion || result.Updated {
		t.Fatalf("check-only result: %+v, %v", result, err)
	}
	if _, err := os.Stat(backupRootFor(fixture.dataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check-only created backup workspace: %v", err)
	}
}

func TestRunPreSwitchFailuresPreserveInstallation(t *testing.T) {
	tests := []struct {
		name   string
		change func(*updateFixture)
	}{
		{name: "release api", change: func(f *updateFixture) { f.options.LatestURL = f.server.URL + "/missing" }},
		{name: "signature", change: func(f *updateFixture) { f.options.Verifier = fixtureVerifier{err: errors.New("bad signature")} }},
		{name: "checksum", change: func(f *updateFixture) { f.manifest = fixtureManifest(testToVersion, testCommit, f.archive, true) }},
		{name: "disk", change: func(f *updateFixture) { f.options.AvailableBytes = func(string) (uint64, error) { return 1, nil } }},
		{name: "backup", change: func(f *updateFixture) {
			f.options.CreateBackup = func(context.Context, string, string, string, time.Time, func(string) (uint64, error)) (string, error) {
				return "", errors.New("backup unavailable")
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUpdateFixture(t)
			test.change(fixture)
			_, err := Run(context.Background(), Request{}, fixture.options, nil)
			if err == nil {
				t.Fatal("expected update failure")
			}
			if fixture.read(t, fixture.binary) != "old-binary" || fixture.read(t, filepath.Join(fixture.dataDir, "workspace.txt")) != "old-data" {
				t.Fatal("pre-switch failure modified the installation")
			}
			if _, statErr := os.Stat(db.MaintenanceGuardPath(fixture.dataDir)); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("maintenance guard leaked: %v", statErr)
			}
		})
	}
}

func TestRunPostSwitchFailuresAutomaticallyRollback(t *testing.T) {
	tests := []struct {
		name   string
		change func(*updateFixture, context.CancelFunc)
	}{
		{name: "switch", change: func(f *updateFixture, _ context.CancelFunc) {
			f.options.InstallBinary = func(string, string) error { return errors.New("replace failed") }
		}},
		{name: "migration", change: func(f *updateFixture, _ context.CancelFunc) { f.commands.failMigrate = true }},
		{name: "doctor", change: func(f *updateFixture, _ context.CancelFunc) { f.commands.failDoctor = true }},
		{name: "cancel after switch", change: func(f *updateFixture, cancel context.CancelFunc) {
			f.options.InstallBinary = func(source, target string) error {
				if err := installBinary(source, target); err != nil {
					return err
				}
				cancel()
				return nil
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newUpdateFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			test.change(fixture, cancel)
			result, err := Run(ctx, Request{}, fixture.options, nil)
			if err == nil || result.DiagnosticPath == "" {
				t.Fatalf("expected diagnosed failure: %+v, %v", result, err)
			}
			if fixture.read(t, fixture.binary) != "old-binary" || fixture.read(t, filepath.Join(fixture.dataDir, "workspace.txt")) != "old-data" {
				t.Fatal("automatic rollback did not restore binary and data")
			}
			receipt, receiptErr := ReadReceipt(fixture.receipt)
			if receiptErr != nil || receipt.Version != testFromVersion {
				t.Fatalf("automatic rollback receipt mismatch: %+v, %v", receipt, receiptErr)
			}
		})
	}
}

func TestWindowsScheduledFinalize(t *testing.T) {
	fixture := newUpdateFixture(t)
	fixture.options.ForceDirect = false
	var scheduled State
	var statePath string
	fixture.options.ScheduleHelper = func(state State, path string) error {
		scheduled, statePath = state, path
		return nil
	}
	result, err := Run(context.Background(), Request{}, fixture.options, nil)
	if err != nil || !result.Scheduled || scheduled.Phase != PhasePrepared || fixture.read(t, fixture.binary) != "old-binary" {
		t.Fatalf("unexpected scheduled result: %+v, %+v, %v", result, scheduled, err)
	}
	final, err := Finalize(context.Background(), statePath, fixture.options, nil)
	if err != nil || !final.Updated || fixture.read(t, fixture.binary) != "new-binary" {
		t.Fatalf("helper finalize failed: %+v, %v", final, err)
	}
}

func TestRollbackRejectsCorruptedBackupAndPreservesCurrent(t *testing.T) {
	fixture := newUpdateFixture(t)
	result, err := Run(context.Background(), Request{}, fixture.options, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(result.BackupDirectory, "data", "workspace.txt"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture.options.Current.Version = testToVersion
	if _, err := Rollback(context.Background(), fixture.options, nil); err == nil {
		t.Fatal("corrupted backup was accepted")
	}
	if fixture.read(t, fixture.binary) != "new-binary" || fixture.read(t, filepath.Join(fixture.dataDir, "workspace.txt")) != "migrated-data" {
		t.Fatal("corrupt rollback point modified current installation")
	}
}

func TestInterruptedSwitchedStateRestoresPreviousVersion(t *testing.T) {
	fixture := newUpdateFixture(t)
	fixture.options.ForceDirect = false
	var state State
	var statePath string
	fixture.options.ScheduleHelper = func(value State, path string) error {
		state, statePath = value, path
		return nil
	}
	if _, err := Run(context.Background(), Request{}, fixture.options, nil); err != nil {
		t.Fatal(err)
	}
	if err := installBinary(state.StagedBinary, state.BinaryPath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.DataDir, "workspace.txt"), []byte("partially-migrated"), 0o600); err != nil {
		t.Fatal(err)
	}
	state.Phase = PhaseSwitched
	state.UpdatedAt = fixture.options.Now()
	if err := saveState(statePath, state); err != nil {
		t.Fatal(err)
	}
	fixture.options.Current.Version = testToVersion
	result, err := Rollback(context.Background(), fixture.options, nil)
	if err != nil || !result.RolledBack {
		t.Fatalf("interrupted rollback = %+v, %v", result, err)
	}
	if fixture.read(t, fixture.binary) != "old-binary" || fixture.read(t, filepath.Join(fixture.dataDir, "workspace.txt")) != "old-data" {
		t.Fatal("power-fault recovery did not restore matching binary/data")
	}
}

func TestRollbackValidationFailureRestoresForwardVersion(t *testing.T) {
	fixture := newUpdateFixture(t)
	if _, err := Run(context.Background(), Request{}, fixture.options, nil); err != nil {
		t.Fatal(err)
	}
	fixture.options.Current.Version = testToVersion
	fixture.commands.migrateValue = ""
	fixture.commands.failDoctor = true
	if _, err := Rollback(context.Background(), fixture.options, nil); err == nil {
		t.Fatal("rollback doctor failure was ignored")
	}
	if fixture.read(t, fixture.binary) != "new-binary" || fixture.read(t, filepath.Join(fixture.dataDir, "workspace.txt")) != "migrated-data" {
		t.Fatal("failed rollback did not restore the forward recovery snapshot")
	}
	receipt, err := ReadReceipt(fixture.receipt)
	if err != nil || receipt.Version != testToVersion {
		t.Fatalf("forward receipt mismatch: %+v, %v", receipt, err)
	}
}

func TestConcurrentStoreBlocksUpdateAndSecondUpdater(t *testing.T) {
	fixture := newUpdateFixture(t)
	store, err := db.Open(context.Background(), db.Config{DataDir: fixture.dataDir}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	if _, err := Run(ctx, Request{}, fixture.options, nil); err == nil {
		t.Fatal("update copied data while a Store was active")
	}
	if fixture.read(t, fixture.binary) != "old-binary" {
		t.Fatal("blocked update replaced the binary")
	}

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	guard, err := db.AcquireMaintenance(context.Background(), fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer guard.Close()
	if _, err := Run(context.Background(), Request{}, fixture.options, nil); err == nil {
		t.Fatal("second updater acquired an active maintenance guard")
	}
}

func TestCompareVersionsSemverPrerelease(t *testing.T) {
	tests := []struct {
		left, right string
		want        int
	}{
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-rc.10", "1.0.0-rc.2", 1},
		{"1.0.0-1", "1.0.0-alpha", -1},
		{"2.0.0", "10.0.0", -1},
	}
	for _, test := range tests {
		got, err := compareVersions(test.left, test.right)
		if err != nil || sign(got) != test.want {
			t.Fatalf("compareVersions(%q, %q) = %d, %v", test.left, test.right, got, err)
		}
	}
	if _, err := compareVersions("1.0.0-01", "1.0.0"); err == nil {
		t.Fatal("invalid numeric prerelease was accepted")
	}
}

func assertProgress(t *testing.T, states []async.State[Progress], want []Stage) {
	t.Helper()
	var got []Stage
	for _, state := range states {
		if err := state.Validate(); err != nil {
			t.Fatal(err)
		}
		if state.Phase == async.Streaming && state.Value != nil {
			got = append(got, state.Value.Stage)
		}
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("progress stages = %v, want %v", got, want)
	}
}

func fixtureManifest(versionValue, commit string, archive []byte, badHash bool) string {
	hash := sha256.Sum256(archive)
	archiveHash := hex.EncodeToString(hash[:])
	if badHash {
		archiveHash = strings.Repeat("f", 64)
	}
	rows := []string{
		releaseManifestHeader,
		"meta\t" + versionValue + "\t" + commit + "\t2026-08-11T00:00:00Z",
	}
	for _, platform := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
		extension := ".tar.gz"
		size := 1
		assetHash := strings.Repeat("a", 64)
		if platform[0] == "windows" {
			extension = ".zip"
		}
		if platform[0] == "windows" && platform[1] == "amd64" {
			size = len(archive)
			assetHash = archiveHash
		}
		filename := "interviewcraft_" + versionValue + "_" + platform[0] + "_" + platform[1] + extension
		rows = append(rows, fmt.Sprintf("asset\t%s\t%s\t%s\t%s\t%d", platform[0], platform[1], filename, assetHash, size))
	}
	rows = append(rows,
		"checksum\t-\t-\tchecksums.txt\t"+strings.Repeat("b", 64)+"\t1",
		"sbom\t-\t-\tinterviewcraft.spdx.json\t"+strings.Repeat("c", 64)+"\t1",
	)
	return strings.Join(rows, "\n") + "\n"
}

func zipPayload(t *testing.T, name string, payload []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	archive := zip.NewWriter(&buffer)
	file, err := archive.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func sign(value int) int {
	if value < 0 {
		return -1
	}
	if value > 0 {
		return 1
	}
	return 0
}
