package e2e_test

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const deploymentOIDCIssuer = "https://token.actions.githubusercontent.com"

type deploymentBuild struct {
	version string
	commit  string
	binary  string
}

func TestCleanDeploymentInstallSetupUpdateRollbackUninstall(t *testing.T) {
	if os.Getenv("INTERVIEWCRAFT_DEPLOYMENT_E2E") != "1" {
		t.Skip("set INTERVIEWCRAFT_DEPLOYMENT_E2E=1 to run clean process deployment acceptance")
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("unsupported deployment platform")
	}
	repo := repositoryRoot(t)
	root := strings.TrimSpace(os.Getenv("INTERVIEWCRAFT_DEPLOYMENT_DEBUG_ROOT"))
	if root == "" {
		root = t.TempDir()
	} else {
		var err error
		root, err = filepath.Abs(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(root, 0o700); err != nil {
			t.Fatal(err)
		}
		t.Logf("preserving deployment fixture at %s", root)
	}
	goBinary := os.Getenv("GO_BINARY")
	if goBinary == "" {
		goBinary = "go"
	}
	fixtureBinary := filepath.Join(root, executableName("deployment-fixture"))
	buildGoCommand(t, repo, goBinary, fixtureBinary, "", "", "./scripts/deployment-fixture")
	verifier := filepath.Join(root, executableName("cosign-fixture"))
	copyFixtureFile(t, fixtureBinary, verifier, 0o700)
	verifierHash := fileHash(t, verifier)

	releaseRoot := filepath.Join(root, "release")
	first := deploymentBuild{version: "1.0.0", commit: strings.Repeat("1", 40), binary: filepath.Join(root, "build-v1", applicationName())}
	second := deploymentBuild{version: "1.1.0", commit: strings.Repeat("2", 40), binary: filepath.Join(root, "build-v2", applicationName())}
	for _, build := range []*deploymentBuild{&first, &second} {
		buildGoCommand(t, repo, goBinary, build.binary, build.version, build.commit, "./cmd/interviewcraft")
		writeRelease(t, releaseRoot, *build)
	}
	if err := os.WriteFile(filepath.Join(releaseRoot, "latest.txt"), []byte(second.version+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	readyFile := filepath.Join(root, "fixture-url.txt")
	server := exec.Command(fixtureBinary, "--root", releaseRoot, "--ready-file", readyFile)
	server.Dir = root
	var serverLog bytes.Buffer
	server.Stdout, server.Stderr = &serverLog, &serverLog
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = server.Process.Kill()
		_, _ = server.Process.Wait()
	})
	baseURL := waitReadyFile(t, readyFile, server, &serverLog)

	for _, profile := range []string{"lite", "private-local"} {
		t.Run(profile, func(t *testing.T) {
			runDeploymentTier(t, repo, root, baseURL, verifier, verifierHash, first, second, profile)
		})
	}
}

func runDeploymentTier(t *testing.T, repo, root, baseURL, verifier, verifierHash string, first, second deploymentBuild, profile string) {
	t.Helper()
	tierRoot := filepath.Join(root, profile)
	home := filepath.Join(tierRoot, "home")
	dataDir := filepath.Join(home, ".interviewcraft")
	installDir := filepath.Join(tierRoot, "bin")
	receiptPath := filepath.Join(dataDir, "install-receipt.txt")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{
		"HOME": home, "USERPROFILE": home, "INTERVIEWCRAFT_DATA_DIR": dataDir,
		"INTERVIEWCRAFT_INSTALL_RECEIPT":               receiptPath,
		"INTERVIEWCRAFT_INSTALL_TEST_RECEIPT":          receiptPath,
		"INTERVIEWCRAFT_INSTALL_TEST_PATH_FILE":        filepath.Join(home, "managed-user-path.txt"),
		"INTERVIEWCRAFT_INSTALL_TEST_MODE":             "1",
		"INTERVIEWCRAFT_INSTALL_TEST_RELEASE_BASE_URL": baseURL,
		"INTERVIEWCRAFT_INSTALL_TEST_COSIGN_PATH":      verifier,
		"INTERVIEWCRAFT_INSTALL_TEST_COSIGN_SHA256":    verifierHash,
		"INTERVIEWCRAFT_INSTALL_TEST_FREE_BYTES":       "9999999999",
		"INTERVIEWCRAFT_UPDATE_TEST_MODE":              "1",
		"INTERVIEWCRAFT_UPDATE_TEST_LATEST_URL":        baseURL + "/latest",
		"INTERVIEWCRAFT_UPDATE_TEST_RELEASE_BASE_URL":  baseURL,
		"INTERVIEWCRAFT_UPDATE_TEST_COSIGN_PATH":       verifier,
		"INTERVIEWCRAFT_UPDATE_TEST_COSIGN_SHA256":     verifierHash,
		"COLUMNS": "80", "LINES": "24", "RUNNER_MODE": "disabled",
		"OPENAI_API_KEY": "deployment-fixture-key",
	}
	provider := "openai-compatible"
	if profile == "private-local" {
		provider = "ollama"
	}
	installOutput := runInstaller(t, repo, tierRoot, environment, first.version, profile, installDir, provider, baseURL)
	for _, stage := range []string{"[1/7]", "[2/7]", "[3/7]", "[4/7]", "[5/7]", "[6/7]", "[7/7]"} {
		if !strings.Contains(installOutput, stage) {
			t.Fatalf("installer output lacks %s: %s", stage, installOutput)
		}
	}
	binary := filepath.Join(installDir, applicationName())
	assertVersion(t, tierRoot, environment, binary, first.version)
	runProcess(t, tierRoot, environment, true, binary, "doctor")
	frame := runProcess(t, tierRoot, environment, true, binary, "run", "--once", "--ascii", "--reduce-motion", "--no-color")
	if len(strings.Split(strings.TrimSuffix(frame, "\n"), "\n")) != 24 {
		t.Fatalf("fresh run frame is not 80x24: %q", frame)
	}
	// A second process exercises restart recovery rather than only first start.
	runProcess(t, tierRoot, environment, true, binary, "run", "--once", "--ascii", "--reduce-motion", "--no-color")
	// Re-running the same one-command install must be idempotent and retain the
	// configuration created by setup.
	runInstaller(t, repo, tierRoot, environment, first.version, profile, installDir, provider, baseURL)
	assertVersion(t, tierRoot, environment, binary, first.version)
	runProcess(t, tierRoot, environment, true, binary, "doctor")

	marker := filepath.Join(dataDir, "deployment-survival.txt")
	if err := os.WriteFile(marker, []byte("preserve-across-update-rollback-uninstall"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := runProcess(t, tierRoot, environment, true, binary, "update", "--check")
	if !strings.Contains(check, second.version) {
		t.Fatalf("update check did not report %s: %s", second.version, check)
	}
	backupRoot := filepath.Join(home, ".interviewcraft-backups")
	if _, err := os.Stat(backupRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("check-only created a backup root: %v", err)
	}

	bundle := filepath.Join(root, "release", "v"+second.version, "release-manifest.sigstore.json")
	validBundle, err := os.ReadFile(bundle)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bundle, []byte("INVALID\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runProcess(t, tierRoot, environment, false, binary, "update", "--version", second.version)
	assertVersion(t, tierRoot, environment, binary, first.version)
	assertFileText(t, marker, "preserve-across-update-rollback-uninstall")
	if err := os.WriteFile(bundle, validBundle, 0o600); err != nil {
		t.Fatal(err)
	}

	updateOutput := runInstaller(t, repo, tierRoot, environment, second.version, profile, installDir, provider, baseURL)
	if !strings.Contains(updateOutput, "update") && !strings.Contains(updateOutput, "Update") {
		t.Fatalf("installer did not delegate version change to updater: %s", updateOutput)
	}
	statePath := filepath.Join(home, ".interviewcraft-backups", "update-state.json")
	waitVersion(t, tierRoot, environment, binary, second.version, statePath)
	waitStatePhase(t, statePath, "committed")
	assertFileText(t, marker, "preserve-across-update-rollback-uninstall")
	runProcess(t, tierRoot, environment, true, binary, "doctor")
	current := runProcess(t, tierRoot, environment, true, binary, "update", "--check")
	if !strings.Contains(current, "up to date") {
		t.Fatalf("latest version did not produce the no-update empty state: %s", current)
	}

	rollbackOutput := runProcess(t, tierRoot, environment, true, binary, "rollback")
	if runtime.GOOS == "windows" && !strings.Contains(rollbackOutput, "helper") {
		t.Fatalf("Windows rollback did not schedule helper: %s", rollbackOutput)
	}
	waitVersion(t, tierRoot, environment, binary, first.version, statePath)
	waitStatePhase(t, statePath, "rolled_back")
	assertFileText(t, marker, "preserve-across-update-rollback-uninstall")

	uninstallOutput := runProcess(t, tierRoot, environment, true, binary, "uninstall")
	if runtime.GOOS == "windows" && !strings.Contains(uninstallOutput, "scheduled") {
		t.Fatalf("Windows uninstall did not schedule helper: %s", uninstallOutput)
	}
	waitAbsent(t, binary)
	if _, err := os.Stat(receiptPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("uninstall receipt remains: %v", err)
	}
	assertFileText(t, marker, "preserve-across-update-rollback-uninstall")
	if _, err := os.Stat(filepath.Join(dataDir, "config.json")); err != nil {
		t.Fatalf("uninstall removed configuration: %v", err)
	}
}

func runInstaller(t *testing.T, repo, working string, environment map[string]string, versionValue, profile, installDir, provider, endpoint string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		return runProcess(t, working, environment, true, "powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(repo, "scripts", "install.ps1"), "-Version", versionValue, "-Profile", profile, "-InstallDir", installDir, "-Provider", provider, "-Endpoint", endpoint, "-Model", "fixture-model", "-NonInteractive")
	}
	return runProcess(t, working, environment, true, "sh", filepath.Join(repo, "scripts", "install.sh"), "--version", versionValue, "--profile", profile, "--install-dir", installDir, "--provider", provider, "--endpoint", endpoint, "--model", "fixture-model", "--non-interactive")
}

func runProcess(t *testing.T, working string, environment map[string]string, wantSuccess bool, executable string, arguments ...string) string {
	t.Helper()
	ctx, cancel := contextWithTimeout(t, 2*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, arguments...)
	command.Dir = working
	command.Env = mergedEnvironment(environment)
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	if ctx.Err() != nil {
		t.Fatalf("command timed out: %s %v\n%s", executable, arguments, output.String())
	}
	if wantSuccess && err != nil {
		t.Fatalf("command failed: %s %v: %v\n%s", executable, arguments, err, output.String())
	}
	if !wantSuccess && err == nil {
		t.Fatalf("command unexpectedly succeeded: %s %v\n%s", executable, arguments, output.String())
	}
	return output.String()
}

func buildGoCommand(t *testing.T, repo, goBinary, output, versionValue, commit, target string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		t.Fatal(err)
	}
	arguments := []string{"build", "-buildvcs=false", "-trimpath", "-o", output}
	if versionValue != "" {
		ldflags := "-X github.com/interviewcraft/interviewcraft/internal/version.ApplicationVersion=" + versionValue +
			" -X github.com/interviewcraft/interviewcraft/internal/version.GitCommit=" + commit +
			" -X github.com/interviewcraft/interviewcraft/internal/version.BuildTime=2026-08-11T00:00:00Z"
		arguments = append(arguments, "-ldflags", ldflags)
	}
	arguments = append(arguments, target)
	command := exec.Command(goBinary, arguments...)
	command.Dir = repo
	command.Env = mergedEnvironment(map[string]string{"CGO_ENABLED": "0"})
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("go build %s: %v\n%s", target, err, output)
	}
}

func writeRelease(t *testing.T, root string, build deploymentBuild) {
	t.Helper()
	directory := filepath.Join(root, "v"+build.version)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	type asset struct {
		goos, goarch, filename, hash string
		size                         int64
	}
	assets := make([]asset, 0, 6)
	for _, platform := range [][2]string{{"windows", "amd64"}, {"windows", "arm64"}, {"linux", "amd64"}, {"linux", "arm64"}, {"darwin", "amd64"}, {"darwin", "arm64"}} {
		extension := ".tar.gz"
		if platform[0] == "windows" {
			extension = ".zip"
		}
		filename := "interviewcraft_" + build.version + "_" + platform[0] + "_" + platform[1] + extension
		path := filepath.Join(directory, filename)
		if platform[0] == runtime.GOOS && platform[1] == runtime.GOARCH {
			writeApplicationArchive(t, path, build.binary, platform[0])
		} else if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		assets = append(assets, asset{goos: platform[0], goarch: platform[1], filename: filename, hash: fileHash(t, path), size: info.Size()})
	}
	checksumPath := filepath.Join(directory, "checksums.txt")
	sbomName := "interviewcraft_" + build.version + ".spdx.json"
	sbomPath := filepath.Join(directory, sbomName)
	if err := os.WriteFile(checksumPath, []byte("fixture checksums\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sbomPath, []byte(`{"spdxVersion":"SPDX-2.3"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var manifest strings.Builder
	manifest.WriteString("interviewcraft-release-v1\n")
	fmt.Fprintf(&manifest, "meta\t%s\t%s\t2026-08-11T00:00:00Z\n", build.version, build.commit)
	for _, item := range assets {
		fmt.Fprintf(&manifest, "asset\t%s\t%s\t%s\t%s\t%d\n", item.goos, item.goarch, item.filename, item.hash, item.size)
	}
	checksumInfo, _ := os.Stat(checksumPath)
	fmt.Fprintf(&manifest, "checksum\t-\t-\tchecksums.txt\t%s\t%d\n", fileHash(t, checksumPath), checksumInfo.Size())
	sbomInfo, _ := os.Stat(sbomPath)
	fmt.Fprintf(&manifest, "sbom\t-\t-\t%s\t%s\t%d\n", sbomName, fileHash(t, sbomPath), sbomInfo.Size())
	if err := os.WriteFile(filepath.Join(directory, "release-manifest.txt"), []byte(manifest.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	identity := "https://github.com/wenbokun434-sketch/interviewcraft/.github/workflows/release.yml@refs/tags/v" + build.version
	bundle := "VALID\n" + identity + "\n" + deploymentOIDCIssuer + "\n"
	if err := os.WriteFile(filepath.Join(directory, "release-manifest.sigstore.json"), []byte(bundle), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeApplicationArchive(t *testing.T, path, binary, goos string) {
	t.Helper()
	payload, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	if goos == "windows" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		archive := zip.NewWriter(file)
		entry, err := archive.Create("interviewcraft.exe")
		if err == nil {
			_, err = entry.Write(payload)
		}
		if closeErr := archive.Close(); err == nil {
			err = closeErr
		}
		if closeErr := file.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzipWriter := gzip.NewWriter(file)
	archive := tar.NewWriter(gzipWriter)
	header := &tar.Header{Name: "interviewcraft", Mode: 0o755, Size: int64(len(payload)), Typeflag: tar.TypeReg}
	if err = archive.WriteHeader(header); err == nil {
		_, err = archive.Write(payload)
	}
	for _, closer := range []io.Closer{archive, gzipWriter, file} {
		if closeErr := closer.Close(); err == nil {
			err = closeErr
		}
	}
	if err != nil {
		t.Fatal(err)
	}
}

func assertVersion(t *testing.T, working string, environment map[string]string, binary, want string) {
	t.Helper()
	output := runProcess(t, working, environment, true, binary, "version", "--json")
	var value struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(output), &value); err != nil || value.Version != want {
		t.Fatalf("version = %q, want %q, err=%v, output=%s", value.Version, want, err, output)
	}
}

func waitVersion(t *testing.T, working string, environment map[string]string, binary, want string, statePaths ...string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		command := exec.Command(binary, "version", "--json")
		command.Dir, command.Env = working, mergedEnvironment(environment)
		payload, err := command.Output()
		if err == nil {
			var value struct {
				Version string `json:"version"`
			}
			if json.Unmarshal(payload, &value) == nil && value.Version == want {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	diagnostics := ""
	for _, statePath := range statePaths {
		payload, readErr := os.ReadFile(statePath)
		diagnostics += fmt.Sprintf("\nstate %s: err=%v payload=%s", statePath, readErr, payload)
		var state struct {
			DiagnosticPath string `json:"diagnostic_path"`
		}
		if json.Unmarshal(payload, &state) == nil && state.DiagnosticPath != "" {
			logPayload, logErr := os.ReadFile(state.DiagnosticPath)
			diagnostics += fmt.Sprintf("\ndiagnostic %s: err=%v payload=%s", state.DiagnosticPath, logErr, logPayload)
		}
	}
	current := runProcess(t, working, environment, true, binary, "version", "--json")
	t.Fatalf("binary did not reach version %s; current=%s%s", want, current, diagnostics)
}

func waitStatePhase(t *testing.T, path, want string) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil {
			var value struct {
				Phase string `json:"phase"`
			}
			if json.Unmarshal(payload, &value) == nil && value.Phase == want {
				return
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("update state did not reach phase %s: %s", want, path)
}

func waitAbsent(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("path was not removed: %s", path)
}

func waitReadyFile(t *testing.T, path string, server *exec.Cmd, log *bytes.Buffer) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		payload, err := os.ReadFile(path)
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(payload)), "http://127.0.0.1:") {
			return strings.TrimSpace(string(payload))
		}
		if server.ProcessState != nil && server.ProcessState.Exited() {
			t.Fatalf("fixture server exited: %s", log.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("fixture server did not become ready: %s", log.String())
	return ""
}

func mergedEnvironment(overrides map[string]string) []string {
	values := map[string]string{}
	for _, entry := range os.Environ() {
		if index := strings.IndexByte(entry, '='); index > 0 {
			values[strings.ToUpper(entry[:index])] = entry[index+1:]
		}
	}
	for name, value := range overrides {
		values[strings.ToUpper(name)] = value
	}
	result := make([]string, 0, len(values))
	for name, value := range values {
		result = append(result, name+"="+value)
	}
	return result
}

func fileHash(t *testing.T, path string) string {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func copyFixtureFile(t *testing.T, source, target string, mode os.FileMode) {
	t.Helper()
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, payload, mode); err != nil {
		t.Fatal(err)
	}
}

func assertFileText(t *testing.T, path, want string) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil || string(payload) != want {
		t.Fatalf("file %s = %q, want %q, err=%v", path, payload, want, err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	working, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs(filepath.Join(working, "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func applicationName() string { return executableName("interviewcraft") }
func executableName(base string) string {
	if runtime.GOOS == "windows" {
		return base + ".exe"
	}
	return base
}

func contextWithTimeout(t *testing.T, duration time.Duration) (context.Context, context.CancelFunc) {
	t.Helper()
	return context.WithTimeout(context.Background(), duration)
}
