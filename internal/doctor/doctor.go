// Package doctor performs ordered, actionable Lite runtime diagnostics.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

// Status is a semantic diagnostic result.
type Status string

const (
	Ready   Status = "ready"
	Warning Status = "warning"
	Error   Status = "error"
)

// Check is one human-readable diagnostic result.
type Check struct {
	Name           string
	Status         Status
	Message        string
	RecoveryAction string
}

// Report contains all ordered runtime checks.
type Report struct {
	Checks []Check
}

// Blocking reports whether any check prevents the training workflow.
func (r Report) Blocking() bool {
	for _, check := range r.Checks {
		if check.Status == Error {
			return true
		}
	}
	return false
}

// Progress identifies the check currently running.
type Progress struct {
	Current int
	Total   int
	Check   string
}

// Observer receives typed diagnostic lifecycle updates.
type Observer func(async.State[Progress])

// TerminalProbe reports a known terminal size or known=false.
type TerminalProbe interface {
	Size() (width int, height int, known bool, err error)
}

// ModelProbe checks configured model connectivity without returning secrets.
type ModelProbe interface {
	Check(context.Context, config.LLM) error
}

// RunnerProbe checks the optional Docker runtime.
type RunnerProbe interface {
	Check(context.Context) error
}

// Options replaces external probes in deterministic tests.
type Options struct {
	Terminal TerminalProbe
	Model    ModelProbe
	Runner   RunnerProbe
	Observer Observer
}

// DefaultOptions selects operating-system probes.
func DefaultOptions() Options {
	return Options{
		Terminal: EnvironmentTerminalProbe{LookupEnv: os.LookupEnv},
		Model: HTTPModelProbe{
			LookupEnv: os.LookupEnv,
		},
		Runner: DockerRunnerProbe{},
	}
}

// Run performs all checks in stable order and returns a typed blocking error
// after preserving the complete report.
func Run(
	ctx context.Context,
	runtime config.Runtime,
	options Options,
) (Report, error) {
	options = fillDefaults(options)
	notify(options.Observer, async.NewPending[Progress]())

	type diagnostic struct {
		name string
		run  func(context.Context) Check
	}
	checks := []diagnostic{
		{name: "terminal", run: func(context.Context) Check {
			return checkTerminal(options.Terminal)
		}},
		{name: "data", run: func(context.Context) Check {
			return checkDataDirectory(runtime.DataDir)
		}},
		{name: "sqlite", run: func(ctx context.Context) Check {
			return checkSQLite(ctx, runtime)
		}},
		{name: "model", run: func(ctx context.Context) Check {
			return checkModel(ctx, runtime.LLM, options.Model)
		}},
		{name: "runner", run: func(ctx context.Context) Check {
			return checkRunner(ctx, runtime.RunnerMode, options.Runner)
		}},
	}

	report := Report{Checks: make([]Check, 0, len(checks))}
	for index, item := range checks {
		if err := ctx.Err(); err != nil {
			typed := domainerr.Wrap(
				domainerr.CodeOperationCancelled,
				"run diagnostics",
				"",
				"运行环境检查已取消。",
				"重新运行 doctor。",
				true,
				err,
			)
			notify(options.Observer, async.NewFailed[Progress](typed))
			return report, typed
		}
		progress := Progress{
			Current: index + 1,
			Total:   len(checks),
			Check:   item.name,
		}
		notify(options.Observer, async.NewStreaming(&progress))
		report.Checks = append(report.Checks, item.run(ctx))
	}

	final := Progress{Current: len(checks), Total: len(checks), Check: "complete"}
	if report.Blocking() {
		typed := domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"run diagnostics",
			"运行环境存在阻塞问题。",
			"按各检查项的修复说明处理后重新运行 doctor。",
			true,
		)
		notify(options.Observer, async.NewFailed[Progress](typed))
		return report, typed
	}
	notify(options.Observer, async.NewSucceeded(final))
	return report, nil
}

func checkTerminal(probe TerminalProbe) Check {
	width, height, known, err := probe.Size()
	if err != nil {
		return Check{
			Name:           "terminal",
			Status:         Warning,
			Message:        "无法读取终端尺寸。",
			RecoveryAction: "在交互式终端中运行，或设置 COLUMNS 和 LINES。",
		}
	}
	if !known {
		return Check{
			Name:           "terminal",
			Status:         Warning,
			Message:        "终端尺寸未知，最低要求为 80×24。",
			RecoveryAction: "设置 COLUMNS 和 LINES，或在交互式终端中重试。",
		}
	}
	if width < 80 || height < 24 {
		return Check{
			Name:           "terminal",
			Status:         Error,
			Message:        fmt.Sprintf("终端为 %d×%d，InterviewCraft 最低需要 80×24。", width, height),
			RecoveryAction: "调整终端尺寸后重新运行 doctor。",
		}
	}
	return Check{
		Name:    "terminal",
		Status:  Ready,
		Message: fmt.Sprintf("终端尺寸 %d×%d 可用。", width, height),
	}
}

func checkDataDirectory(path string) Check {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return Check{
			Name:           "data",
			Status:         Error,
			Message:        "本地数据目录不存在：" + path + "。",
			RecoveryAction: "运行 `interviewcraft init`。",
		}
	}
	if err != nil {
		return Check{
			Name:           "data",
			Status:         Error,
			Message:        "无法读取本地数据目录：" + path + "。",
			RecoveryAction: "检查路径和访问权限后重试。",
		}
	}
	if !info.IsDir() {
		return Check{
			Name:           "data",
			Status:         Error,
			Message:        "本地数据路径不是目录：" + path + "。",
			RecoveryAction: "改用可写目录后重试。",
		}
	}

	probe, err := os.CreateTemp(path, ".doctor-write-*")
	if err != nil {
		return Check{
			Name:           "data",
			Status:         Error,
			Message:        "本地数据目录不可写：" + path + "。",
			RecoveryAction: "授予当前用户写入权限后重试。",
		}
	}
	probePath := probe.Name()
	closeErr := probe.Close()
	removeErr := os.Remove(probePath)
	if closeErr != nil || removeErr != nil {
		return Check{
			Name:           "data",
			Status:         Error,
			Message:        "无法完成本地数据目录写入检查：" + path + "。",
			RecoveryAction: "检查文件锁和删除权限后重试。",
		}
	}
	return Check{
		Name:    "data",
		Status:  Ready,
		Message: "本地数据目录可写：" + path + "。",
	}
}

func checkSQLite(ctx context.Context, runtime config.Runtime) Check {
	if info, err := os.Stat(runtime.DataDir); err != nil || !info.IsDir() {
		return Check{
			Name:           "sqlite",
			Status:         Error,
			Message:        "尚未检查 SQLite，因为本地数据目录不可用。",
			RecoveryAction: "运行 `interviewcraft init` 后重试。",
		}
	}
	store, err := db.Open(ctx, db.Config{
		DataDir:      runtime.DataDir,
		DatabaseName: runtime.DatabaseName,
	}, nil)
	if err != nil {
		return Check{
			Name:           "sqlite",
			Status:         Error,
			Message:        "SQLite 无法读写：" + filepath.Join(runtime.DataDir, runtime.DatabaseName) + "。",
			RecoveryAction: "检查数据库文件和目录权限后重试。",
		}
	}
	version, versionErr := store.SchemaVersion(ctx)
	closeErr := store.Close()
	if versionErr != nil || closeErr != nil {
		return Check{
			Name:           "sqlite",
			Status:         Error,
			Message:        "无法确认 SQLite 迁移状态。",
			RecoveryAction: "保留数据库文件并查看日志后重试。",
		}
	}
	return Check{
		Name:    "sqlite",
		Status:  Ready,
		Message: fmt.Sprintf("SQLite 可写，迁移版本为 %d。", version),
	}
}

func checkModel(ctx context.Context, llm config.LLM, probe ModelProbe) Check {
	if llm.Provider == "" {
		return Check{
			Name:           "model",
			Status:         Error,
			Message:        "尚未配置 LLM Provider。",
			RecoveryAction: "设置 Provider、endpoint 和 model 后重试。",
		}
	}
	if err := probe.Check(ctx, llm); err != nil {
		return Check{
			Name:           "model",
			Status:         Error,
			Message:        "LLM Provider 无法连接：" + llm.Endpoint + "。",
			RecoveryAction: "检查 endpoint、认证、模型和服务状态后重试。",
		}
	}
	return Check{
		Name:    "model",
		Status:  Ready,
		Message: "LLM Provider 可连接：" + llm.Provider + "。",
	}
}

func checkRunner(ctx context.Context, mode string, probe RunnerProbe) Check {
	if mode == config.RunnerDisabled {
		return Check{
			Name:           "runner",
			Status:         Warning,
			Message:        "Docker Runner 已禁用；文字面试仍可使用。",
			RecoveryAction: "需要运行代码时执行 `interviewcraft setup --profile full --restart`。",
		}
	}
	if err := probe.Check(ctx); err != nil {
		return Check{
			Name:           "runner",
			Status:         Warning,
			Message:        "Docker Runner 当前不可用；文字面试仍可使用。",
			RecoveryAction: "启动 Docker 后重新运行 `interviewcraft setup --profile full --restart`。",
		}
	}
	return Check{
		Name:    "runner",
		Status:  Ready,
		Message: "Docker Runner 可连接。",
	}
}

func fillDefaults(options Options) Options {
	defaults := DefaultOptions()
	if options.Terminal == nil {
		options.Terminal = defaults.Terminal
	}
	if options.Model == nil {
		options.Model = defaults.Model
	}
	if options.Runner == nil {
		options.Runner = defaults.Runner
	}
	return options
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}
