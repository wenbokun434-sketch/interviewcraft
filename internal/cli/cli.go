// Package cli owns the dependency-free command entry point for InterviewCraft.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	term "github.com/charmbracelet/x/term"
	runneradapter "github.com/interviewcraft/interviewcraft/internal/adapters/runner"
	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/transfer"
	"github.com/interviewcraft/interviewcraft/internal/credentials"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/doctor"
	setupservice "github.com/interviewcraft/interviewcraft/internal/setup"
	"github.com/interviewcraft/interviewcraft/internal/tui/app"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/training"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
	updateservice "github.com/interviewcraft/interviewcraft/internal/update"
	buildversion "github.com/interviewcraft/interviewcraft/internal/version"
)

const (
	// ExitOK reports successful command handling.
	ExitOK = 0
	// ExitFailure reports an implemented command that could not complete.
	ExitFailure = 1
	// ExitUnavailable reports a known command whose ordered TODO task is pending.
	ExitUnavailable = ExitFailure
	// ExitUsage reports invalid command-line input.
	ExitUsage = 2
)

type command struct {
	name        string
	description string
	task        string
}

var commands = []command{
	{name: "init", description: "Initialize a local Lite workspace"},
	{name: "setup", description: "Configure and validate a complete local deployment"},
	{name: "run", description: "Start the InterviewCraft terminal UI"},
	{name: "doctor", description: "Check local runtime dependencies"},
	{name: "version", description: "Print build and platform metadata"},
	{name: "update", description: "Check for or install a verified release update"},
	{name: "rollback", description: "Restore the previous binary and complete data backup"},
	{name: "uninstall", description: "Remove the receipt-owned installation safely"},
	{name: "export", description: "Export reports or local training data"},
	{name: "import", description: "Import a local transfer package"},
}

// Run handles one InterviewCraft command and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	return RunWithIO(args, TerminalIO{
		Stdin: strings.NewReader(""), Stdout: stdout, Stderr: stderr,
	})
}

// TerminalIO makes stdin and terminal capability deterministic for tests.
type TerminalIO struct {
	Stdin        io.Reader
	Stdout       io.Writer
	Stderr       io.Writer
	Interactive  bool
	ReadPassword func() ([]byte, error)
}

// RunOS uses real process streams and detects whether Bubble Tea can safely
// take ownership of the terminal.
func RunOS(args []string, stdin *os.File, stdout *os.File, stderr io.Writer) int {
	return RunWithIO(args, TerminalIO{
		Stdin: stdin, Stdout: stdout, Stderr: stderr,
		Interactive:  isTerminalFile(stdin) && isTerminalFile(stdout),
		ReadPassword: func() ([]byte, error) { return term.ReadPassword(stdin.Fd()) },
	})
}

// RunWithIO handles one command using injected process streams.
func RunWithIO(args []string, terminal TerminalIO) int {
	stdout := terminal.Stdout
	stderr := terminal.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	if terminal.Stdin == nil {
		terminal.Stdin = strings.NewReader("")
	}
	if len(args) == 0 || isHelp(args[0]) {
		writeHelp(stdout)
		return ExitOK
	}
	if strings.HasPrefix(args[0], "__") {
		terminal.Stdout, terminal.Stderr = stdout, stderr
		switch args[0] {
		case "__update-migrate":
			return runUpdateMigrate(args[1:], stdout, stderr)
		case "__update-doctor":
			return runUpdateDoctor(args[1:], stdout, stderr)
		case "__update-helper":
			return runUpdateHelper(args[1:], stdout, stderr)
		case "__uninstall-helper":
			return runUninstallHelper(args[1:], stdout, stderr)
		default:
			fmt.Fprintln(stderr, "! Unknown internal maintenance command.")
			return ExitUsage
		}
	}

	for _, candidate := range commands {
		if candidate.name != args[0] {
			continue
		}

		if len(args) > 1 && isHelp(args[1]) {
			writeCommandHelp(stdout, candidate)
			return ExitOK
		}
		if len(args) > 1 &&
			candidate.name != "run" &&
			candidate.name != "setup" &&
			candidate.name != "version" &&
			candidate.name != "update" &&
			candidate.name != "uninstall" &&
			candidate.name != "export" &&
			candidate.name != "import" {
			fmt.Fprintf(
				stderr,
				"命令 %q 不接受参数 %q。\n运行 `interviewcraft %s --help` 查看用法。\n",
				candidate.name,
				args[1],
				candidate.name,
			)
			return ExitUsage
		}

		switch candidate.name {
		case "init":
			return runInit(stdout, stderr)
		case "setup":
			terminal.Stdout = stdout
			terminal.Stderr = stderr
			return runSetup(args[1:], terminal)
		case "run":
			terminal.Stdout = stdout
			terminal.Stderr = stderr
			return runTraining(args[1:], terminal)
		case "doctor":
			return runDoctor(stdout, stderr)
		case "version":
			return runVersion(args[1:], stdout, stderr)
		case "update":
			return runUpdate(args[1:], stdout, stderr)
		case "rollback":
			return runRollback(stdout, stderr)
		case "uninstall":
			return runUninstall(args[1:], stdout, stderr)
		case "export":
			return runExport(args[1:], stdout, stderr)
		case "import":
			return runImport(args[1:], stdout, stderr)
		default:
			fmt.Fprintf(
				stderr,
				"命令 %q 尚未实现，将在 TODO %s 中完成。\n运行 `interviewcraft --help` 查看当前可用入口。\n",
				candidate.name,
				candidate.task,
			)
			return ExitUnavailable
		}
	}

	fmt.Fprintf(
		stderr,
		"未知命令 %q。\n运行 `interviewcraft --help` 查看支持的命令。\n",
		args[0],
	)
	return ExitUsage
}

func isHelp(arg string) bool {
	return arg == "--help" || arg == "-h" || arg == "help"
}

func writeHelp(output io.Writer) {
	fmt.Fprintln(output, "InterviewCraft — local-first interview practice")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Usage:")
	fmt.Fprintln(output, "  interviewcraft <command>")
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Commands:")
	for _, candidate := range commands {
		fmt.Fprintf(output, "  %-8s %s\n", candidate.name, candidate.description)
	}
	fmt.Fprintln(output)
	fmt.Fprintln(output, "Global options:")
	fmt.Fprintln(output, "  -h, --help  Show command help")
}

func writeCommandHelp(output io.Writer, target command) {
	fmt.Fprintf(output, "Usage:\n  interviewcraft %s\n\n", target.name)
	fmt.Fprintln(output, target.description+".")
	if target.name == "run" {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Options:")
		fmt.Fprintln(output, "  --theme auto|dark|light")
		fmt.Fprintln(output, "  --ascii")
		fmt.Fprintln(output, "  --reduce-motion")
		fmt.Fprintln(output, "  --ansi-16")
		fmt.Fprintln(output, "  --no-color")
		fmt.Fprintln(output, "  --once              Render one frame for CI or redirected output")
	}
	if target.name == "setup" {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Options:")
		fmt.Fprintln(output, "  --profile lite|private-local|full")
		fmt.Fprintln(output, "  --data-dir <path>")
		fmt.Fprintln(output, "  --provider openai-compatible|ollama")
		fmt.Fprintln(output, "  --endpoint <url>")
		fmt.Fprintln(output, "  --model <name>")
		fmt.Fprintln(output, "  --api-key-env <name>")
		fmt.Fprintln(output, "  --api-key-stdin")
		fmt.Fprintln(output, "  --non-interactive")
		fmt.Fprintln(output, "  --restart")
		fmt.Fprintln(output)
		fmt.Fprintln(output, "API key values are never accepted as command-line arguments.")
	}
	if target.name == "update" {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Options:")
		fmt.Fprintln(output, "  --check             Check without downloading or modifying the installation")
		fmt.Fprintln(output, "  --version <version> Install an explicit newer release")
	}
	if target.name == "uninstall" {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Options:")
		fmt.Fprintln(output, "  --purge-data                 Also remove the receipt-bound data and credential")
		fmt.Fprintln(output, "  --confirm-purge <exact-path> Required second confirmation for --purge-data")
	}
	if target.name == "export" {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Options:")
		fmt.Fprintln(output, "  --format package|json|markdown")
		fmt.Fprintln(output, "  --output <new-file>")
		fmt.Fprintln(output, "  --session <id>       Required for json/markdown")
		fmt.Fprintln(output, "  --include-coach      Explicitly include Coach transcript text")
	}
	if target.name == "import" {
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Options:")
		fmt.Fprintln(output, "  --input <transfer-package>")
		fmt.Fprintln(output)
		fmt.Fprintln(output, "Import requires an empty initialized Lite instance.")
	}
	if target.task == "" {
		fmt.Fprintln(output, "Status: available.")
		return
	}
	fmt.Fprintf(output, "Status: planned for TODO %s.\n", target.task)
}

func runUpdate(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("update", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	checkOnly := flags.Bool("check", false, "check for updates")
	versionValue := flags.String("version", "", "explicit release version")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "! Unable to parse update options.")
		fmt.Fprintln(stderr, "  Run `interviewcraft update --help` for usage.")
		return ExitUsage
	}
	runtimeConfig, _, err := config.LoadOS()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	updateOptions := updateservice.Options{
		Current:        buildversion.Current(),
		DataDir:        runtimeConfig.DataDir,
		ReceiptPath:    installReceiptPath(),
		ExecutablePath: executablePath(),
	}
	if err := applyUpdateFixture(&updateOptions); err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	result, err := updateservice.Run(ctx, updateservice.Request{
		CheckOnly: *checkOnly,
		Version:   strings.TrimSpace(*versionValue),
	}, updateOptions, updateProgress(stdout))
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if *checkOnly {
		if result.AvailableVersion == "" || result.AvailableVersion == result.CurrentVersion {
			fmt.Fprintf(stdout, "InterviewCraft %s is up to date.\n", result.CurrentVersion)
		} else {
			fmt.Fprintf(stdout, "InterviewCraft %s is available (current %s).\n", result.AvailableVersion, result.CurrentVersion)
		}
		return ExitOK
	}
	if result.Scheduled {
		fmt.Fprintf(stdout, "Verified update %s is prepared; the Windows replacement helper will finish after this process exits.\n", result.AvailableVersion)
		return ExitOK
	}
	if !result.Updated {
		fmt.Fprintf(stdout, "InterviewCraft %s is already current; no backup was created.\n", result.CurrentVersion)
		return ExitOK
	}
	fmt.Fprintf(stdout, "InterviewCraft was updated to %s. Rollback point: %s\n", result.CurrentVersion, result.BackupDirectory)
	return ExitOK
}

func runRollback(stdout, stderr io.Writer) int {
	runtimeConfig, _, err := config.LoadOS()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	result, err := updateservice.Rollback(ctx, updateservice.Options{
		Current:        buildversion.Current(),
		DataDir:        runtimeConfig.DataDir,
		ReceiptPath:    installReceiptPath(),
		ExecutablePath: executablePath(),
	}, updateProgress(stdout))
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if !result.RolledBack {
		fmt.Fprintln(stdout, "No committed rollback point is available; the current installation was not changed.")
		return ExitOK
	}
	fmt.Fprintf(stdout, "InterviewCraft was rolled back to %s with its matching complete data directory.\n", result.CurrentVersion)
	return ExitOK
}

func runUninstall(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("uninstall", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	purge := flags.Bool("purge-data", false, "remove receipt-bound data and credential")
	confirmation := flags.String("confirm-purge", "", "exact canonical purge path")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "! Unable to parse uninstall options.")
		fmt.Fprintln(stderr, "  Run `interviewcraft uninstall --help` for usage.")
		return ExitUsage
	}
	if !*purge && strings.TrimSpace(*confirmation) != "" {
		fmt.Fprintln(stderr, "! --confirm-purge is only valid with --purge-data.")
		return ExitUsage
	}
	dataDir := ""
	if *purge {
		runtimeConfig, _, err := config.LoadOS()
		if err != nil {
			writeCommandError(stderr, err)
			return ExitFailure
		}
		dataDir = runtimeConfig.DataDir
	}
	result, err := updateservice.Uninstall(context.Background(), updateservice.UninstallOptions{
		ReceiptPath:    installReceiptPath(),
		ExecutablePath: executablePath(),
		DataDir:        dataDir,
		PurgeData:      *purge,
		Confirmation:   strings.TrimSpace(*confirmation),
	})
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if result.Scheduled {
		fmt.Fprintf(stdout, "InterviewCraft %s uninstall is scheduled and will finish after this process exits.\n", result.Version)
		return ExitOK
	}
	writeUninstallResult(stdout, result)
	return ExitOK
}

func runUpdateMigrate(args []string, stdout, stderr io.Writer) int {
	dataDir, token, ok := parseMaintenanceFlags("__update-migrate", args, stderr)
	if !ok {
		return ExitUsage
	}
	if token != strings.TrimSpace(os.Getenv(db.MaintenanceTokenEnv)) {
		fmt.Fprintln(stderr, "! Update maintenance authorization is invalid.")
		return ExitFailure
	}
	runtimeConfig, _, err := config.LoadAt(dataDir)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	store, err := db.Open(context.Background(), db.Config{DataDir: dataDir, DatabaseName: runtimeConfig.DatabaseName}, nil)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if err := store.Close(); err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	fmt.Fprintln(stdout, "SQLite migrations completed under the exclusive update guard.")
	return ExitOK
}

func runUpdateDoctor(args []string, stdout, stderr io.Writer) int {
	dataDir, token, ok := parseMaintenanceFlags("__update-doctor", args, stderr)
	if !ok {
		return ExitUsage
	}
	if token != strings.TrimSpace(os.Getenv(db.MaintenanceTokenEnv)) {
		fmt.Fprintln(stderr, "! Update maintenance authorization is invalid.")
		return ExitFailure
	}
	active, _, err := config.LoadOS()
	if err != nil || filepath.Clean(active.DataDir) != filepath.Clean(dataDir) {
		fmt.Fprintln(stderr, "! Update doctor data directory does not match the guarded workspace.")
		return ExitFailure
	}
	return runDoctor(stdout, stderr)
}

func runUpdateHelper(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("__update-helper", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	statePath := flags.String("state", "", "prepared update state")
	parentValue := flags.String("parent", "", "parent PID")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *statePath == "" {
		fmt.Fprintln(stderr, "! Invalid update helper request.")
		return ExitUsage
	}
	parent, err := strconv.Atoi(*parentValue)
	if err != nil || parent <= 0 {
		fmt.Fprintln(stderr, "! Invalid update helper parent process.")
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := updateservice.WaitForParent(ctx, parent); err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if _, err := updateservice.Finalize(ctx, *statePath, updateservice.Options{}, updateProgress(stdout)); err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	return ExitOK
}

func runUninstallHelper(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("__uninstall-helper", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	parentValue := flags.String("parent", "", "parent PID")
	receiptPath := flags.String("receipt", "", "install receipt")
	dataDir := flags.String("data-dir", "", "receipt-bound data directory")
	purge := flags.Bool("purge-data", false, "purge data")
	confirmation := flags.String("confirm-purge", "", "exact purge confirmation")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *receiptPath == "" {
		fmt.Fprintln(stderr, "! Invalid uninstall helper request.")
		return ExitUsage
	}
	parent, err := strconv.Atoi(*parentValue)
	if err != nil || parent <= 0 {
		fmt.Fprintln(stderr, "! Invalid uninstall helper parent process.")
		return ExitUsage
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := updateservice.WaitForParent(ctx, parent); err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	result, err := updateservice.FinalizeUninstall(ctx, updateservice.UninstallOptions{
		ReceiptPath: *receiptPath, DataDir: *dataDir, PurgeData: *purge, Confirmation: *confirmation,
	})
	helperPath := executablePath()
	updateservice.CleanupUninstallHelper(helperPath)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	writeUninstallResult(stdout, result)
	return ExitOK
}

func parseMaintenanceFlags(name string, args []string, stderr io.Writer) (string, string, bool) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	dataDir := flags.String("data-dir", "", "guarded data directory")
	token := flags.String("token", "", "maintenance token")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || strings.TrimSpace(*dataDir) == "" || strings.TrimSpace(*token) == "" {
		fmt.Fprintln(stderr, "! Invalid update maintenance request.")
		return "", "", false
	}
	absolute, err := filepath.Abs(*dataDir)
	if err != nil {
		fmt.Fprintln(stderr, "! Invalid update maintenance data directory.")
		return "", "", false
	}
	return absolute, strings.TrimSpace(*token), true
}

func updateProgress(output io.Writer) updateservice.Observer {
	return func(state async.State[updateservice.Progress]) {
		if state.Phase == async.Streaming && state.Value != nil {
			fmt.Fprintf(output, "· [%d/%d] %s: %s\n", state.Value.Current, state.Value.Total, state.Value.Stage, state.Value.Message)
		}
	}
}

func installReceiptPath() string {
	if value := strings.TrimSpace(os.Getenv("INTERVIEWCRAFT_INSTALL_RECEIPT")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".interviewcraft", "install-receipt.txt")
}

func applyUpdateFixture(options *updateservice.Options) error {
	if strings.TrimSpace(os.Getenv("INTERVIEWCRAFT_UPDATE_TEST_MODE")) != "1" {
		return nil
	}
	latest := strings.TrimSpace(os.Getenv("INTERVIEWCRAFT_UPDATE_TEST_LATEST_URL"))
	base := strings.TrimSpace(os.Getenv("INTERVIEWCRAFT_UPDATE_TEST_RELEASE_BASE_URL"))
	verifier := strings.TrimSpace(os.Getenv("INTERVIEWCRAFT_UPDATE_TEST_COSIGN_PATH"))
	hash := strings.TrimSpace(os.Getenv("INTERVIEWCRAFT_UPDATE_TEST_COSIGN_SHA256"))
	for _, raw := range []string{latest, base} {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme != "http" || parsed.Hostname() == "" {
			return errors.New("update fixture URLs must use loopback HTTP")
		}
		host := parsed.Hostname()
		address := net.ParseIP(host)
		if host != "localhost" && (address == nil || !address.IsLoopback()) {
			return errors.New("update fixture URLs must use loopback HTTP")
		}
	}
	if verifier == "" || hash == "" {
		return errors.New("update fixture requires an explicitly hashed local Cosign verifier")
	}
	options.LatestURL = latest
	options.ReleaseBaseURL = base
	options.LocalVerifierPath = verifier
	options.LocalVerifierSHA256 = hash
	return nil
}

func executablePath() string {
	path, _ := os.Executable()
	path, _ = filepath.Abs(path)
	return path
}

func writeUninstallResult(output io.Writer, result updateservice.UninstallResult) {
	fmt.Fprintf(output, "InterviewCraft %s was uninstalled.\n", result.Version)
	if result.Purged {
		fmt.Fprintln(output, "The receipt-bound data directory, rollback backups, and system credential were purged.")
	} else {
		fmt.Fprintln(output, "Configuration, credentials, SQLite data, reports, and rollback backups were preserved.")
	}
}

func runExport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("export", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	formatValue := flags.String("format", string(transfer.FormatPackage), "export format")
	outputPath := flags.String("output", "", "new output file")
	sessionID := flags.String("session", "", "report session id")
	includeCoach := flags.Bool("include-coach", false, "include Coach transcript")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "! 无法解析 export 选项。")
		fmt.Fprintln(stderr, "  运行 `interviewcraft export --help` 查看用法。")
		return ExitUsage
	}
	format := transfer.Format(strings.ToLower(strings.TrimSpace(*formatValue)))
	service, paths, err := transferService()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	target := strings.TrimSpace(*outputPath)
	if target == "" {
		target = defaultExportPath(paths.Exports, format, *sessionID)
	}
	result, err := service.Export(context.Background(), transfer.ExportOptions{
		Format: format, OutputPath: target, SessionID: strings.TrimSpace(*sessionID),
		IncludeCoachContent: *includeCoach,
	}, transferProgress(stdout))
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	fmt.Fprintf(stdout, "✓ 已导出 %s（%d 条记录）。\n", result.Path, result.RecordCount)
	if !*includeCoach {
		fmt.Fprintln(stdout, "  Coach 原文未包含；使用 --include-coach 可显式选择包含。")
	}
	return ExitOK
}

func runImport(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	inputPath := flags.String("input", "", "transfer package")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "! 无法解析 import 选项。")
		fmt.Fprintln(stderr, "  运行 `interviewcraft import --help` 查看用法。")
		return ExitUsage
	}
	if strings.TrimSpace(*inputPath) == "" {
		fmt.Fprintln(stderr, "! import 需要 --input <transfer-package>。")
		fmt.Fprintln(stderr, "  运行 `interviewcraft import --help` 查看用法。")
		return ExitUsage
	}
	service, _, err := transferService()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	result, err := service.Import(
		context.Background(),
		*inputPath,
		transferProgress(stdout),
	)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	fmt.Fprintf(
		stdout,
		"✓ 已恢复 %d 个画像、%d 场会话、%d 份报告。\n",
		result.Profiles,
		result.Sessions,
		result.Reports,
	)
	return ExitOK
}

func transferService() (*transfer.Service, db.Paths, error) {
	runtime, metadata, err := config.LoadOS()
	if err != nil {
		return nil, db.Paths{}, err
	}
	if !metadata.Exists {
		return nil, db.Paths{}, domainerr.New(
			domainerr.CodeInvalidState,
			"open transfer command",
			"尚未初始化 Lite 配置。",
			"运行 `interviewcraft init` 后重试。",
			false,
		)
	}
	store, err := db.Open(context.Background(), db.Config{
		DataDir: runtime.DataDir, DatabaseName: runtime.DatabaseName,
	}, nil)
	if err != nil {
		return nil, db.Paths{}, err
	}
	paths := store.Paths()
	if err := store.Close(); err != nil {
		return nil, db.Paths{}, domainerr.Wrap(
			domainerr.CodePersistenceFailed,
			"prepare transfer command",
			"SQLite",
			"无法安全准备本地迁移存储。",
			"检查数据库文件后重试。",
			true,
			err,
		)
	}
	return transfer.NewService(paths.Database, transfer.Options{}), paths, nil
}

func transferProgress(output io.Writer) transfer.Observer {
	return func(state async.State[transfer.Progress]) {
		if state.Phase != async.Streaming || state.Value == nil {
			return
		}
		fmt.Fprintf(
			output,
			"· [%d/%d] %s\n",
			state.Value.Current,
			state.Value.Total,
			state.Value.Message,
		)
	}
}

func defaultExportPath(exportsDir string, format transfer.Format, sessionID string) string {
	name := "interviewcraft-transfer.json"
	if format == transfer.FormatJSON {
		name = "report-" + safeFilePart(sessionID) + ".json"
	}
	if format == transfer.FormatMarkdown {
		name = "report-" + safeFilePart(sessionID) + ".md"
	}
	return filepath.Join(exportsDir, name)
}

func safeFilePart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "session"
	}
	var result strings.Builder
	for _, char := range value {
		if unicode.IsLetter(char) || unicode.IsDigit(char) || char == '-' || char == '_' {
			result.WriteRune(char)
		} else {
			result.WriteRune('-')
		}
	}
	return result.String()
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "print JSON build metadata")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(stderr, "! 无法解析 version 选项。")
		fmt.Fprintln(stderr, "  使用 `interviewcraft version [--json]`。")
		return ExitUsage
	}
	info := buildversion.Current()
	if *jsonOutput {
		encoder := json.NewEncoder(stdout)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(info); err != nil {
			fmt.Fprintln(stderr, "! 无法写出版本信息。")
			return ExitFailure
		}
		return ExitOK
	}
	fmt.Fprintf(stdout, "InterviewCraft %s\n", info.Version)
	fmt.Fprintf(stdout, "schema: %s\n", info.SchemaVersion)
	fmt.Fprintf(stdout, "commit: %s\n", info.GitCommit)
	fmt.Fprintf(stdout, "built: %s\n", info.BuildTime)
	fmt.Fprintf(stdout, "platform: %s/%s\n", info.GOOS, info.GOARCH)
	return ExitOK
}

func runSetup(args []string, terminal TerminalIO) int {
	defaults, _, err := config.LoadOS()
	if err != nil {
		writeCommandError(terminal.Stderr, err)
		return ExitFailure
	}
	flags := flag.NewFlagSet("setup", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	profileValue := flags.String("profile", "", "deployment profile")
	dataDir := flags.String("data-dir", defaults.DataDir, "data directory")
	provider := flags.String("provider", "", "Provider")
	endpoint := flags.String("endpoint", "", "Provider endpoint")
	model := flags.String("model", "", "Provider model")
	apiKeyEnv := flags.String("api-key-env", "OPENAI_API_KEY", "API key environment variable")
	apiKeyStdin := flags.Bool("api-key-stdin", false, "read API key from stdin")
	nonInteractive := flags.Bool("non-interactive", false, "disable prompts")
	restart := flags.Bool("restart", false, "discard safe setup checkpoint")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		fmt.Fprintln(terminal.Stderr, "! 无法解析 setup 选项；API Key 值不能放在命令行参数中。")
		fmt.Fprintln(terminal.Stderr, "  运行 `interviewcraft setup --help` 查看用法。")
		return ExitUsage
	}
	explicit := make(map[string]bool)
	flags.Visit(func(item *flag.Flag) { explicit[item.Name] = true })
	existing, metadata, loadErr := config.LoadAt(*dataDir)
	if loadErr != nil {
		writeCommandError(terminal.Stderr, loadErr)
		return ExitFailure
	}
	if metadata.Exists {
		if !explicit["profile"] {
			switch {
			case existing.RunnerMode == config.RunnerDocker:
				*profileValue = string(setupservice.ProfileFull)
			case existing.LLM.Provider == config.ProviderOllama:
				*profileValue = string(setupservice.ProfilePrivateLocal)
			default:
				*profileValue = string(setupservice.ProfileLite)
			}
		}
		profileChangesProvider := explicit["profile"] && !explicit["provider"]
		if !explicit["provider"] && !profileChangesProvider {
			*provider = existing.LLM.Provider
		}
		resolvedExistingProvider := strings.TrimSpace(*provider)
		if resolvedExistingProvider == "" {
			resolvedExistingProvider = existing.LLM.Provider
		}
		if resolvedExistingProvider == existing.LLM.Provider {
			if !explicit["endpoint"] {
				*endpoint = existing.LLM.Endpoint
			}
			if !explicit["model"] {
				*model = existing.LLM.Model
			}
			if !explicit["api-key-env"] {
				*apiKeyEnv = existing.LLM.APIKeyEnv
			}
		}
	}
	if strings.TrimSpace(*profileValue) == "" {
		*profileValue = string(setupservice.ProfileLite)
	}
	profile := setupservice.Profile(strings.TrimSpace(*profileValue))
	resolvedProvider := strings.TrimSpace(*provider)
	if resolvedProvider == "" {
		if profile == setupservice.ProfilePrivateLocal {
			resolvedProvider = config.ProviderOllama
		} else {
			resolvedProvider = config.ProviderOpenAICompatible
		}
	}
	if !*nonInteractive && !terminal.Interactive {
		fmt.Fprintln(terminal.Stderr, "! 交互式 setup 需要终端输入。")
		fmt.Fprintln(terminal.Stderr, "  使用交互终端，或添加 --non-interactive 并通过环境变量/--api-key-stdin 提供凭据。")
		return ExitUsage
	}

	secret := ""
	if *apiKeyStdin {
		payload, readErr := io.ReadAll(io.LimitReader(terminal.Stdin, 64<<10))
		if readErr != nil || len(payload) >= 64<<10 {
			fmt.Fprintln(terminal.Stderr, "! 无法从 stdin 安全读取 API Key。")
			return ExitFailure
		}
		secret = strings.TrimRight(string(payload), "\r\n")
		if strings.TrimSpace(secret) == "" {
			fmt.Fprintln(terminal.Stderr, "! --api-key-stdin 收到空凭据。")
			return ExitUsage
		}
	}
	if resolvedProvider == config.ProviderOllama && *apiKeyStdin {
		fmt.Fprintln(terminal.Stderr, "! Ollama setup 不接受 --api-key-stdin。")
		return ExitUsage
	}
	if resolvedProvider == config.ProviderOpenAICompatible && secret == "" {
		resolver, resolverErr := credentials.NewResolver(*dataDir, os.LookupEnv, credentials.SystemStore{})
		if resolverErr != nil {
			writeCommandError(terminal.Stderr, resolverErr)
			return ExitFailure
		}
		resolved, _, credentialErr := resolver.ResolveDetailed(*apiKeyEnv)
		switch {
		case credentialErr != nil:
			if *nonInteractive {
				writeCommandError(terminal.Stderr, credentialErr)
				return ExitFailure
			}
			fmt.Fprintln(terminal.Stderr, "! 系统凭据库不可用；未读取或保存 API Key。")
			fmt.Fprintln(terminal.Stderr, "  设置 API Key 环境变量后重试。")
			return ExitFailure
		case strings.TrimSpace(resolved) != "":
		case *nonInteractive:
			fmt.Fprintf(terminal.Stderr, "! 非交互 setup 缺少 %s 或 --api-key-stdin。\n", *apiKeyEnv)
			return ExitUsage
		default:
			if terminal.ReadPassword == nil {
				fmt.Fprintln(terminal.Stderr, "! 当前终端不支持隐藏凭据输入。")
				return ExitFailure
			}
			fmt.Fprintf(terminal.Stdout, "API Key（隐藏输入，将保存到系统凭据库）：")
			payload, passwordErr := terminal.ReadPassword()
			fmt.Fprintln(terminal.Stdout)
			if passwordErr != nil || strings.TrimSpace(string(payload)) == "" {
				fmt.Fprintln(terminal.Stderr, "! 未读取到有效 API Key，setup 已取消且未保存凭据。")
				return ExitFailure
			}
			secret = string(payload)
		}
	}

	setupContext, stopSetup := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stopSetup()
	result, err := setupservice.Run(setupContext, setupservice.Request{
		Profile: profile, DataDir: *dataDir, Provider: resolvedProvider,
		Endpoint: *endpoint, Model: *model, APIKeyEnv: *apiKeyEnv,
		APIKey: secret, NonInteractive: *nonInteractive, Restart: *restart,
	}, setupservice.DefaultDependencies(), func(state async.State[setupservice.Progress]) {
		if state.Phase == async.Streaming && state.Value != nil {
			fmt.Fprintf(terminal.Stdout, "· [%d/%d] %s\n", state.Value.Current, state.Value.Total, state.Value.Message)
		}
	})
	if err != nil {
		writeCommandError(terminal.Stderr, err)
		return ExitFailure
	}
	fmt.Fprintf(terminal.Stdout, "✓ setup 完成：%s\n", result.ConfigPath)
	if profile == setupservice.ProfileFull && result.RunnerReady {
		fmt.Fprintln(terminal.Stdout, "✓ Full Practice Runner 已按签名与 digest 验证并启用。")
	}
	return ExitOK
}

func runTraining(args []string, terminal TerminalIO) int {
	stdout, stderr := terminal.Stdout, terminal.Stderr
	themeArgs, once, err := parseRunMode(args)
	if err != nil {
		fmt.Fprintf(stderr, "! 无法解析 run 选项：%s。\n", err)
		fmt.Fprintln(stderr, "  运行 `interviewcraft run --help` 查看可用选项。")
		return ExitUsage
	}
	options, err := theme.ParseOptions(themeArgs)
	if err != nil {
		fmt.Fprintf(stderr, "! 无法解析 run 选项：%s。\n", err)
		fmt.Fprintln(stderr, "  运行 `interviewcraft run --help` 查看可用选项。")
		return ExitUsage
	}
	if !once && !terminal.Interactive {
		fmt.Fprintln(stderr, "! `interviewcraft run` 需要可交互终端。")
		fmt.Fprintln(stderr, "  在终端中运行，或为 CI/重定向输出使用 `interviewcraft run --once`。")
		return ExitFailure
	}
	current, err := theme.Resolve(options)
	if err != nil {
		writeCommandError(stderr, domainerr.Wrap(
			domainerr.CodeValidation,
			"resolve TUI theme",
			"terminal",
			"无法应用终端主题。",
			"检查 run 选项后重试。",
			false,
			err,
		))
		return ExitFailure
	}

	runtimeConfig, metadata, err := config.LoadOS()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if !metadata.Exists {
		fmt.Fprintln(stderr, "! 尚未初始化 InterviewCraft 配置。")
		fmt.Fprintln(stderr, "  运行 `interviewcraft init` 或 `interviewcraft setup` 后重试。")
		return ExitFailure
	}
	store, err := db.Open(context.Background(), db.Config{
		DataDir: runtimeConfig.DataDir, DatabaseName: runtimeConfig.DatabaseName,
	}, nil)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	closeStore := func() int {
		if closeErr := store.Close(); closeErr != nil {
			writeCommandError(stderr, domainerr.Wrap(
				domainerr.CodePersistenceFailed,
				"close TUI storage",
				"SQLite",
				"无法确认本地数据已安全关闭。",
				"检查数据库文件后重试。",
				true,
				closeErr,
			))
			return ExitFailure
		}
		return ExitOK
	}
	factory, err := app.NewRuntimeFactory(runtimeConfig, metadata.Path, store, current)
	if err != nil {
		_ = store.Close()
		writeCommandError(stderr, err)
		return ExitFailure
	}
	model, err := app.New(factory, app.Route{
		Page: app.PageTraining, ProfileID: "default",
	}, terminalDimension("COLUMNS", 120), terminalDimension("LINES", 36))
	if err != nil {
		_ = store.Close()
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if once {
		if err := model.RunOnce(context.Background()); err != nil {
			_ = store.Close()
			writeCommandError(stderr, err)
			return ExitFailure
		}
		fmt.Fprintln(stdout, model.View().Content)
		return closeStore()
	}
	program := tea.NewProgram(
		model,
		tea.WithInput(terminal.Stdin),
		tea.WithOutput(stdout),
	)
	if _, err := program.Run(); err != nil {
		_ = store.Close()
		writeCommandError(stderr, domainerr.Wrap(
			domainerr.CodeDependencyUnavailable,
			"run interactive TUI",
			"terminal",
			"交互终端异常退出。",
			"确认终端支持交互输入后重试；自动化环境使用 `run --once`。",
			true,
			err,
		))
		return ExitFailure
	}
	return closeStore()
}

func parseRunMode(args []string) ([]string, bool, error) {
	result := make([]string, 0, len(args))
	once := false
	for _, arg := range args {
		if arg != "--once" {
			result = append(result, arg)
			continue
		}
		if once {
			return nil, false, errors.New("--once 不能重复")
		}
		once = true
	}
	return result, once, nil
}

func isTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runTrainingLegacy(args []string, stdout, stderr io.Writer) int {
	options, err := theme.ParseOptions(args)
	if err != nil {
		fmt.Fprintf(stderr, "! 无法解析 run 选项：%s。\n", err)
		fmt.Fprintln(stderr, "  运行 `interviewcraft run --help` 查看可用选项。")
		return ExitUsage
	}
	current, err := theme.Resolve(options)
	if err != nil {
		writeCommandError(stderr, domainerr.Wrap(
			domainerr.CodeValidation,
			"resolve TUI theme",
			"terminal",
			"无法应用终端主题。",
			"检查 run 选项后重试。",
			false,
			err,
		))
		return ExitFailure
	}

	runtime, metadata, err := config.LoadOS()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if !metadata.Exists {
		fmt.Fprintln(stderr, "! 尚未初始化 Lite 配置。")
		fmt.Fprintln(stderr, "  运行 `interviewcraft init` 后重试。")
		return ExitFailure
	}

	store, err := db.Open(context.Background(), db.Config{
		DataDir:      runtime.DataDir,
		DatabaseName: runtime.DatabaseName,
	}, nil)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}

	width := terminalDimension("COLUMNS", 120)
	height := terminalDimension("LINES", 36)
	model, err := training.New(store, width, height, current)
	if err != nil {
		_ = store.Close()
		writeCommandError(stderr, domainerr.Wrap(
			domainerr.CodeInvalidState,
			"initialize training home",
			"TUI",
			"无法初始化训练主页。",
			"运行 `interviewcraft doctor` 后重试。",
			true,
			err,
		))
		return ExitFailure
	}
	model.Load(context.Background(), nil)
	rendered, err := model.Render()
	if err != nil {
		_ = store.Close()
		writeCommandError(stderr, domainerr.Wrap(
			domainerr.CodeInvalidState,
			"render training home",
			"TUI",
			"无法渲染训练主页。",
			"检查终端尺寸后重试。",
			true,
			err,
		))
		return ExitFailure
	}
	if err := store.Close(); err != nil {
		writeCommandError(stderr, domainerr.Wrap(
			domainerr.CodePersistenceFailed,
			"close training storage",
			"SQLite",
			"无法确认训练数据已安全关闭。",
			"检查数据库文件后重试 run。",
			true,
			err,
		))
		return ExitFailure
	}
	fmt.Fprintln(stdout, rendered)
	return ExitOK
}

func terminalDimension(name string, fallback int) int {
	value, err := strconv.Atoi(os.Getenv(name))
	if err != nil || value <= 0 {
		return fallback
	}
	return value
}

func runInit(stdout, stderr io.Writer) int {
	runtime, metadata, err := config.LoadOS()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	created, err := config.WriteInitial(metadata.Path, runtime)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}

	store, err := db.Open(context.Background(), db.Config{
		DataDir:      runtime.DataDir,
		DatabaseName: runtime.DatabaseName,
	}, nil)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if err := store.Close(); err != nil {
		writeCommandError(stderr, domainerr.Wrap(
			domainerr.CodePersistenceFailed,
			"close SQLite",
			"SQLite",
			"无法确认本地数据库已安全关闭。",
			"检查数据库文件后重试 init。",
			true,
			err,
		))
		return ExitFailure
	}

	if created {
		fmt.Fprintf(stdout, "✓ 已创建 Lite 配置：%s\n", metadata.Path)
	} else {
		fmt.Fprintf(stdout, "✓ 已保留现有 Lite 配置：%s\n", metadata.Path)
	}
	fmt.Fprintf(stdout, "✓ 本地数据已就绪：%s\n", runtime.DataDir)
	return ExitOK
}

func runDoctor(stdout, stderr io.Writer) int {
	runtime, metadata, err := config.LoadOS()
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	if !metadata.Exists {
		fmt.Fprintln(stderr, "! 尚未初始化 Lite 配置。")
		fmt.Fprintln(stderr, "  运行 `interviewcraft init` 后重试。")
		return ExitFailure
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	resolver, err := credentials.NewResolver(
		runtime.DataDir, os.LookupEnv, credentials.SystemStore{},
	)
	if err != nil {
		writeCommandError(stderr, err)
		return ExitFailure
	}
	options := doctor.DefaultOptions()
	options.Model = doctor.HTTPModelProbe{LookupEnv: resolver.Resolve}
	if runtime.RunnerMode == config.RunnerDocker {
		probe, probeErr := runneradapter.New(runneradapter.ConfigForRuntime(runtime), runneradapter.Options{})
		if probeErr != nil {
			writeCommandError(stderr, probeErr)
			return ExitFailure
		}
		options.Runner = probe
	}
	report, runErr := doctor.Run(ctx, runtime, options)
	for _, check := range report.Checks {
		switch check.Status {
		case doctor.Ready:
			fmt.Fprintf(stdout, "✓ %-8s %s\n", check.Name, check.Message)
		case doctor.Warning:
			fmt.Fprintf(stdout, "! %-8s %s\n", check.Name, check.Message)
		case doctor.Error:
			fmt.Fprintf(stdout, "! %-8s %s\n", check.Name, check.Message)
		}
		if check.RecoveryAction != "" && check.Status != doctor.Ready {
			fmt.Fprintf(stdout, "           %s\n", check.RecoveryAction)
		}
	}
	if runErr != nil {
		writeCommandError(stderr, runErr)
		return ExitFailure
	}
	fmt.Fprintln(stdout, "✓ Lite 运行环境检查通过。")
	return ExitOK
}

func writeCommandError(output io.Writer, err error) {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		fmt.Fprintln(output, "! "+typed.Message)
		if typed.RecoveryAction != "" {
			fmt.Fprintln(output, "  "+typed.RecoveryAction)
		}
		return
	}
	fmt.Fprintln(output, "! 命令无法完成。")
	fmt.Fprintln(output, "  查看日志并重试。")
}
