// Package cli owns the dependency-free command entry point for InterviewCraft.
package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/transfer"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/doctor"
	"github.com/interviewcraft/interviewcraft/internal/tui/screens/training"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
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
	{name: "run", description: "Start the InterviewCraft terminal UI"},
	{name: "doctor", description: "Check local runtime dependencies"},
	{name: "export", description: "Export reports or local training data"},
	{name: "import", description: "Import a local transfer package"},
}

// Run handles one InterviewCraft command and returns a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || isHelp(args[0]) {
		writeHelp(stdout)
		return ExitOK
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
		case "run":
			return runTraining(args[1:], stdout, stderr)
		case "doctor":
			return runDoctor(stdout, stderr)
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

func runTraining(args []string, stdout, stderr io.Writer) int {
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
	report, runErr := doctor.Run(ctx, runtime, doctor.DefaultOptions())
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
