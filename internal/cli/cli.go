// Package cli owns the dependency-free command entry point for InterviewCraft.
package cli

import (
	"fmt"
	"io"
)

const (
	// ExitOK reports successful command handling.
	ExitOK = 0
	// ExitUnavailable reports a known command whose ordered TODO task is pending.
	ExitUnavailable = 1
	// ExitUsage reports invalid command-line input.
	ExitUsage = 2
)

type command struct {
	name        string
	description string
	task        string
}

var commands = []command{
	{name: "init", description: "Initialize a local Lite workspace", task: "T-004"},
	{name: "run", description: "Start the InterviewCraft terminal UI", task: "T-006"},
	{name: "doctor", description: "Check local runtime dependencies", task: "T-004"},
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

		fmt.Fprintf(
			stderr,
			"命令 %q 尚未实现，将在 TODO %s 中完成。\n运行 `interviewcraft --help` 查看当前可用入口。\n",
			candidate.name,
			candidate.task,
		)
		return ExitUnavailable
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
	fmt.Fprintf(output, "Status: planned for TODO %s.\n", target.task)
}
