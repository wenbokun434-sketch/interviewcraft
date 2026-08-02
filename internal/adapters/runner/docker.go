package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// DockerRunner implements coding.Runner with one disposable container per run.
type DockerRunner struct {
	config           Config
	command          CommandExecutor
	newContainerName func() string
	observer         Observer
	now              func() time.Time
}

// New creates an explicitly configured Docker adapter. It does not start
// Docker, pull an image, or change RUNNER_MODE.
func New(config Config, options Options) (*DockerRunner, error) {
	config = normalizeConfig(config)
	if err := validateConfig(config); err != nil {
		return nil, err
	}
	command := options.Command
	if command == nil {
		command = osCommand{
			binary: config.DockerBinary, maxOutputBytes: config.Limits.MaxOutputBytes,
		}
	}
	newName := options.NewContainerName
	if newName == nil {
		newName = randomContainerName
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &DockerRunner{
		config: config, command: command, newContainerName: newName,
		observer: options.Observer, now: now,
	}, nil
}

// Run executes a known question suite with no host mounts, environment
// forwarding, network, root privileges, mutable root filesystem, or ambient
// capabilities.
func (runner *DockerRunner) Run(
	ctx context.Context,
	request coding.ExecutionRequest,
) (coding.ExecutionResult, error) {
	if runner == nil || runner.command == nil {
		return coding.ExecutionResult{}, unavailableRunner("run isolated code", nil)
	}
	notify(runner.observer, async.NewPending[Progress]())
	suite, err := suiteFor(request.QuestionID)
	if err != nil {
		return coding.ExecutionResult{}, runner.fail(validationFailure(err))
	}
	if !supportedLanguage(request.Language) || strings.TrimSpace(request.Source) == "" {
		return coding.ExecutionResult{}, runner.fail(validationFailure(
			errors.New("language or source is invalid"),
		))
	}
	payload, err := json.Marshal(requestEnvelope{
		Version: requestVersion, QuestionID: request.QuestionID,
		Language: request.Language, Source: request.Source,
		Public: suite.Public, Hidden: suite.Hidden,
	})
	if err != nil {
		return coding.ExecutionResult{}, runner.fail(validationFailure(err))
	}
	containerName := runner.newContainerName()
	if err := validateContainerName(containerName); err != nil {
		return coding.ExecutionResult{}, runner.fail(validationFailure(err))
	}
	runContext, cancel := context.WithTimeout(ctx, runner.config.Limits.WallTime)
	defer cancel()
	defer runner.cleanup(containerName)

	commandResult, commandErr := runner.runWithProgress(
		runContext,
		payload,
		runner.runArguments(containerName)...,
	)
	if errors.Is(runContext.Err(), context.DeadlineExceeded) {
		result := safeExecutionError(coding.ErrorTimeout, suite)
		runner.succeed("timeout")
		return result, nil
	}
	if errors.Is(runContext.Err(), context.Canceled) {
		return coding.ExecutionResult{}, runner.fail(cancelledRunner())
	}
	if commandErr != nil || commandResult.ExitCode != 0 {
		if runner.wasOOMKilled(containerName) {
			result := safeExecutionError(coding.ErrorOutOfMemory, suite)
			runner.succeed("out_of_memory")
			return result, nil
		}
		return coding.ExecutionResult{}, runner.fail(unavailableRunner(
			"run isolated code",
			commandErr,
		))
	}
	response, err := decodeResponse(commandResult.Stdout)
	if err != nil {
		return coding.ExecutionResult{}, runner.fail(protocolFailure(err))
	}
	if err := validateResponse(response, suite); err != nil {
		return coding.ExecutionResult{}, runner.fail(protocolFailure(err))
	}
	runner.succeed("completed")
	return coding.ExecutionResult{Result: response.Result, Runtime: response.Runtime}, nil
}

func (runner *DockerRunner) runWithProgress(
	ctx context.Context,
	stdin []byte,
	args ...string,
) (CommandResult, error) {
	type outcome struct {
		result CommandResult
		err    error
	}
	started := runner.now()
	progress(runner.observer, "starting_container", 0)
	completed := make(chan outcome, 1)
	go func() {
		result, err := runner.command.Run(ctx, stdin, args...)
		completed <- outcome{result: result, err: err}
	}()
	ticker := time.NewTicker(runner.config.Limits.ProgressInterval)
	defer ticker.Stop()
	for {
		select {
		case value := <-completed:
			return value.result, value.err
		case timestamp := <-ticker.C:
			elapsed := timestamp.Sub(started)
			if elapsed < 0 {
				elapsed = 0
			}
			progress(runner.observer, "running_tests", elapsed)
		case <-ctx.Done():
			select {
			case value := <-completed:
				return value.result, value.err
			case <-time.After(250 * time.Millisecond):
				return CommandResult{ExitCode: -1}, ctx.Err()
			}
		}
	}
}

func (runner *DockerRunner) runArguments(containerName string) []string {
	limits := runner.config.Limits
	return []string{
		"run",
		"--name", containerName,
		"--label", "interviewcraft.runner=true",
		"--pull", "never",
		"--network", "none",
		"--read-only",
		"--user", "65532:65532",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges=true",
		"--cpus", strconv.FormatFloat(limits.CPUs, 'f', 2, 64),
		"--memory", fmt.Sprintf("%dm", limits.MemoryMB),
		"--memory-swap", fmt.Sprintf("%dm", limits.MemoryMB),
		"--pids-limit", strconv.Itoa(limits.PIDs),
		"--ulimit", fmt.Sprintf("nproc=%d:%d", limits.PIDs, limits.PIDs),
		"--ulimit", "nofile=64:64",
		"--tmpfs", fmt.Sprintf(
			"/tmp:rw,nosuid,nodev,noexec,size=%dm,mode=1777",
			limits.TmpfsMB,
		),
		"--workdir", "/tmp",
		"--hostname", "runner",
		"--ipc", "none",
		"--log-driver", "none",
		"--init",
		"--interactive",
		runner.config.Image,
	}
}

func (runner *DockerRunner) wasOOMKilled(containerName string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), runner.config.Limits.CleanupTimeout)
	defer cancel()
	result, err := runner.command.Run(
		ctx,
		nil,
		"inspect",
		"--format",
		"{{.State.OOMKilled}}",
		containerName,
	)
	return err == nil && strings.TrimSpace(string(result.Stdout)) == "true"
}

func (runner *DockerRunner) cleanup(containerName string) {
	ctx, cancel := context.WithTimeout(context.Background(), runner.config.Limits.CleanupTimeout)
	defer cancel()
	_, _ = runner.command.Run(ctx, nil, "rm", "--force", "--volumes", containerName)
}

func decodeResponse(payload []byte) (responseEnvelope, error) {
	var response responseEnvelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&response); err != nil {
		return responseEnvelope{}, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple response values")
		}
		return responseEnvelope{}, err
	}
	return response, nil
}

func validateResponse(response responseEnvelope, suite testSuite) error {
	if response.Version != responseVersion ||
		response.Result.Version != coding.ResultVersion ||
		len(response.Result.PublicTests) != len(suite.Public) ||
		response.Result.HiddenTests.Passed < 0 ||
		response.Result.HiddenTests.Failed < 0 ||
		response.Result.HiddenTests.Passed+response.Result.HiddenTests.Failed != len(suite.Hidden) ||
		response.Runtime.DurationMilliseconds < 0 ||
		response.Runtime.PeakMemoryKB < 0 {
		return errors.New("runner response metadata is invalid")
	}
	for index, value := range response.Result.PublicTests {
		if value.Name != suite.Public[index].Name ||
			(value.Status != coding.TestPassed &&
				value.Status != coding.TestFailed &&
				value.Status != coding.TestError) {
			return errors.New("runner public result is invalid")
		}
	}
	if !validAggregate(response.Result) {
		return errors.New("runner aggregate status is invalid")
	}
	return nil
}

func validAggregate(result coding.SafeResult) bool {
	failed := result.HiddenTests.Failed
	for _, value := range result.PublicTests {
		if value.Status != coding.TestPassed {
			failed++
		}
	}
	switch result.Status {
	case coding.RunPassed:
		return failed == 0 && result.ErrorKind == coding.ErrorNone
	case coding.RunFailed:
		return failed > 0 && result.ErrorKind == coding.ErrorNone
	case coding.RunError:
		return result.ErrorKind != coding.ErrorNone
	default:
		return false
	}
}

func safeExecutionError(kind coding.ErrorKind, suite testSuite) coding.ExecutionResult {
	public := make([]coding.PublicTestResult, len(suite.Public))
	for index, value := range suite.Public {
		public[index] = coding.PublicTestResult{Name: value.Name, Status: coding.TestError}
	}
	return coding.ExecutionResult{
		Result: coding.SafeResult{
			Version: coding.ResultVersion, Status: coding.RunError,
			PublicTests: public,
			HiddenTests: coding.HiddenTestSummary{Failed: len(suite.Hidden)},
			ErrorKind:   kind,
		},
		Runtime: coding.RuntimeStats{},
	}
}

func supportedLanguage(language coding.Language) bool {
	return language == coding.LanguagePython ||
		language == coding.LanguageJavaScript ||
		language == coding.LanguageJava
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func progress(observer Observer, stage string, elapsed time.Duration) {
	value := Progress{Stage: stage, Elapsed: elapsed}
	notify(observer, async.NewStreaming(&value))
}

func (runner *DockerRunner) succeed(stage string) {
	notify(runner.observer, async.NewSucceeded(Progress{Stage: stage}))
}

func (runner *DockerRunner) fail(err error) error {
	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		typed = unavailableRunner("run isolated code", err)
	}
	notify(runner.observer, async.NewFailed[Progress](typed))
	return typed
}

func validationFailure(err error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"prepare isolated code run",
		"Docker Runner",
		"代码运行请求无效。",
		"保留草稿并重新选择受支持的题目与语言。",
		false,
		err,
	)
}

func protocolFailure(err error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeInvalidState,
		"decode isolated code result",
		"Docker Runner",
		"代码执行器返回了无效或不安全的结果。",
		"重建 Runner 镜像并重新运行健康检查。",
		true,
		err,
	)
}

func unavailableRunner(operation string, err error) *domainerr.Error {
	if err == nil {
		err = errors.New("runner unavailable")
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		operation,
		"Docker Runner",
		"Docker Runner 当前不可用；文字面试和 Coach 仍可继续。",
		"启动 Docker、构建 Runner 镜像并重新运行健康检查。",
		true,
		err,
	)
}

func cancelledRunner() *domainerr.Error {
	return domainerr.New(
		domainerr.CodeOperationCancelled,
		"run isolated code",
		"代码执行已取消；当前草稿保持不变。",
		"可在准备好后重新运行。",
		true,
	)
}
