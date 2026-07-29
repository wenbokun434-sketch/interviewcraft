// Package cli owns the dependency-free command entry point for InterviewCraft.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
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
	{name: "export", description: "Export local training data", task: "T-018"},
	{name: "import", description: "Import a local transfer package", task: "T-018"},
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
		if len(args) > 1 && candidate.name != "run" {
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
	if target.task == "" {
		fmt.Fprintln(output, "Status: available.")
		return
	}
	fmt.Fprintf(output, "Status: planned for TODO %s.\n", target.task)
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
