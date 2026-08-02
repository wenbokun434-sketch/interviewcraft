package runner

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

const testContainerName = "interviewcraft-runner-00112233445566778899aabbccddeeff"

type commandCall struct {
	Stdin []byte
	Args  []string
}

type fakeCommand struct {
	mu      sync.Mutex
	calls   []commandCall
	handler func(context.Context, []byte, []string) (CommandResult, error)
}

func (command *fakeCommand) Run(ctx context.Context, stdin []byte, args ...string) (CommandResult, error) {
	command.mu.Lock()
	command.calls = append(command.calls, commandCall{
		Stdin: slices.Clone(stdin), Args: slices.Clone(args),
	})
	command.mu.Unlock()
	if command.handler == nil {
		return CommandResult{}, nil
	}
	return command.handler(ctx, stdin, args)
}

func (command *fakeCommand) snapshot() []commandCall {
	command.mu.Lock()
	defer command.mu.Unlock()
	result := make([]commandCall, len(command.calls))
	for index, call := range command.calls {
		result[index] = commandCall{Stdin: slices.Clone(call.Stdin), Args: slices.Clone(call.Args)}
	}
	return result
}

func TestDockerRunnerPassesAllLanguagesWithMandatoryIsolation(t *testing.T) {
	for _, language := range coding.Languages() {
		t.Run(string(language), func(t *testing.T) {
			command := &fakeCommand{}
			command.handler = successfulHandler(t, passedResponse(t))
			runner := newTestRunner(t, DefaultConfig(), command, nil)
			result, err := runner.Run(context.Background(), coding.ExecutionRequest{
				QuestionID: "pair_sum", Language: language, Source: "source-do-not-return",
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Result.Status != coding.RunPassed || result.Result.ErrorKind != coding.ErrorNone ||
				result.Result.HiddenTests.Passed != 2 || result.Runtime.DurationMilliseconds != 17 {
				t.Fatalf("result=%#v", result)
			}

			calls := command.snapshot()
			if len(calls) != 2 || first(calls[0].Args) != "run" || first(calls[1].Args) != "rm" {
				t.Fatalf("calls=%#v", calls)
			}
			assertSecurityArguments(t, calls[0].Args)
			assertCleanup(t, calls[1].Args)
			var request requestEnvelope
			if err := json.Unmarshal(calls[0].Stdin, &request); err != nil {
				t.Fatalf("request JSON: %v", err)
			}
			if request.Version != requestVersion || request.Language != language ||
				len(request.Public) != 2 || len(request.Hidden) != 2 || request.Source != "source-do-not-return" {
				t.Fatalf("request=%#v", request)
			}
			publicPayload, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			for _, forbidden := range []string{"source-do-not-return", "hidden-duplicate-values", "[-3,4,3,90]", "/tmp"} {
				if strings.Contains(string(publicPayload), forbidden) {
					t.Fatalf("result leaked %q: %s", forbidden, publicPayload)
				}
			}
		})
	}
}

func TestDockerRunnerReturnsFailedTestsWithoutLeakingHiddenCases(t *testing.T) {
	command := &fakeCommand{handler: successfulHandler(t, failedResponse(t))}
	runner := newTestRunner(t, DefaultConfig(), command, nil)
	result, err := runner.Run(context.Background(), coding.ExecutionRequest{
		QuestionID: "pair_sum", Language: coding.LanguagePython, Source: "def pair_sum(a, b): return []",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Result.Status != coding.RunFailed || result.Result.ErrorKind != coding.ErrorNone ||
		result.Result.PublicTests[1].Status != coding.TestFailed ||
		result.Result.HiddenTests != (coding.HiddenTestSummary{Passed: 1, Failed: 1}) {
		t.Fatalf("result=%#v", result)
	}
	if len(command.snapshot()) != 2 {
		t.Fatalf("container was not cleaned: %#v", command.snapshot())
	}
}

func TestDockerRunnerStreamsElapsedTimeAndTimesOutWithoutOwningEditor(t *testing.T) {
	config := DefaultConfig()
	config.Limits.WallTime = 100 * time.Millisecond
	config.Limits.ProgressInterval = 10 * time.Millisecond
	command := &fakeCommand{}
	command.handler = func(ctx context.Context, _ []byte, args []string) (CommandResult, error) {
		if first(args) == "run" {
			<-ctx.Done()
			return CommandResult{ExitCode: -1}, ctx.Err()
		}
		return CommandResult{}, nil
	}
	states := make(chan async.State[Progress], 16)
	runner := newTestRunner(t, config, command, func(state async.State[Progress]) { states <- state })
	type outcome struct {
		result coding.ExecutionResult
		err    error
	}
	completed := make(chan outcome, 1)
	editor := "before"
	go func() {
		result, err := runner.Run(context.Background(), coding.ExecutionRequest{
			QuestionID: "pair_sum", Language: coding.LanguagePython, Source: "while True: pass",
		})
		completed <- outcome{result: result, err: err}
	}()
	streamed := false
	deadline := time.After(time.Second)
	for !streamed {
		select {
		case state := <-states:
			if err := state.Validate(); err != nil {
				t.Fatalf("state: %v", err)
			}
			streamed = state.Phase == async.Streaming && state.Value != nil && state.Value.Elapsed > 0
		case <-deadline:
			t.Fatal("no elapsed streaming state")
		}
	}
	editor = "still writable"
	value := <-completed
	if value.err != nil || value.result.Result.ErrorKind != coding.ErrorTimeout ||
		value.result.Result.Status != coding.RunError || editor != "still writable" {
		t.Fatalf("outcome=%#v editor=%q", value, editor)
	}
	assertLastCleanup(t, command.snapshot())
}

func TestDockerRunnerMapsOOMNetworkAndUnhealthyStatesSafely(t *testing.T) {
	t.Run("oom", func(t *testing.T) {
		command := &fakeCommand{}
		command.handler = func(_ context.Context, _ []byte, args []string) (CommandResult, error) {
			switch first(args) {
			case "run":
				return CommandResult{ExitCode: 137, Stderr: []byte("host-secret-path")}, errors.New("exit 137")
			case "inspect":
				return CommandResult{Stdout: []byte("true\n")}, nil
			default:
				return CommandResult{}, nil
			}
		}
		runner := newTestRunner(t, DefaultConfig(), command, nil)
		result, err := runner.Run(context.Background(), requestFor(coding.LanguagePython, "memory bomb"))
		if err != nil || result.Result.ErrorKind != coding.ErrorOutOfMemory {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		assertLastCleanup(t, command.snapshot())
	})

	t.Run("network denied by container", func(t *testing.T) {
		response := errorResponse(t, coding.ErrorRuntime)
		command := &fakeCommand{handler: successfulHandler(t, response)}
		runner := newTestRunner(t, DefaultConfig(), command, nil)
		result, err := runner.Run(context.Background(), requestFor(coding.LanguagePython, "network request"))
		if err != nil || result.Result.ErrorKind != coding.ErrorRuntime || result.Result.Status != coding.RunError {
			t.Fatalf("result=%#v err=%v", result, err)
		}
		assertLastCleanup(t, command.snapshot())
	})

	t.Run("runner unhealthy", func(t *testing.T) {
		command := &fakeCommand{}
		command.handler = func(_ context.Context, _ []byte, args []string) (CommandResult, error) {
			if first(args) == "run" {
				return CommandResult{ExitCode: -1, Stderr: []byte(`C:\host\secret\hidden.json`)}, errors.New(`C:\host\secret\hidden.json`)
			}
			return CommandResult{Stdout: []byte("false")}, nil
		}
		runner := newTestRunner(t, DefaultConfig(), command, nil)
		_, err := runner.Run(context.Background(), requestFor(coding.LanguagePython, "source"))
		if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) ||
			strings.Contains(err.Error(), "host") || strings.Contains(err.Error(), "hidden") {
			t.Fatalf("unsafe error=%v", err)
		}
		assertLastCleanup(t, command.snapshot())
	})
}

func TestDockerRunnerRejectsBadProtocolValidationAndCancellation(t *testing.T) {
	t.Run("invalid response", func(t *testing.T) {
		command := &fakeCommand{handler: successfulHandler(t, []byte(`{"secret":"hidden-input"}`))}
		runner := newTestRunner(t, DefaultConfig(), command, nil)
		_, err := runner.Run(context.Background(), requestFor(coding.LanguagePython, "source"))
		if !domainerr.IsCode(err, domainerr.CodeInvalidState) || strings.Contains(err.Error(), "hidden-input") {
			t.Fatalf("error=%v", err)
		}
		assertLastCleanup(t, command.snapshot())
	})

	t.Run("empty source never starts container", func(t *testing.T) {
		command := &fakeCommand{}
		runner := newTestRunner(t, DefaultConfig(), command, nil)
		_, err := runner.Run(context.Background(), requestFor(coding.LanguagePython, " "))
		if !domainerr.IsCode(err, domainerr.CodeValidation) || len(command.snapshot()) != 0 {
			t.Fatalf("error=%v calls=%#v", err, command.snapshot())
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		command := &fakeCommand{}
		command.handler = func(ctx context.Context, _ []byte, args []string) (CommandResult, error) {
			if first(args) == "run" {
				<-ctx.Done()
				return CommandResult{ExitCode: -1}, ctx.Err()
			}
			return CommandResult{}, nil
		}
		runner := newTestRunner(t, DefaultConfig(), command, nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := runner.Run(ctx, requestFor(coding.LanguagePython, "source"))
		if !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
			t.Fatalf("error=%v", err)
		}
		assertLastCleanup(t, command.snapshot())
	})
}

func TestResponseValidationIsStrict(t *testing.T) {
	suite, err := suiteFor("pair_sum")
	if err != nil {
		t.Fatal(err)
	}
	valid := responseEnvelope{
		Version: responseVersion,
		Result: coding.SafeResult{
			Version: coding.ResultVersion, Status: coding.RunPassed,
			PublicTests: []coding.PublicTestResult{
				{Name: "example-1", Status: coding.TestPassed},
				{Name: "example-2", Status: coding.TestPassed},
			},
			HiddenTests: coding.HiddenTestSummary{Passed: 2}, ErrorKind: coding.ErrorNone,
		},
	}
	if err := validateResponse(valid, suite); err != nil {
		t.Fatalf("valid response: %v", err)
	}
	invalid := valid
	invalid.Result.HiddenTests = coding.HiddenTestSummary{Passed: 1}
	if err := validateResponse(invalid, suite); err == nil {
		t.Fatal("invalid hidden aggregate passed")
	}
	invalid = valid
	invalid.Result.PublicTests[0].Name = "hidden-case-name"
	if err := validateResponse(invalid, suite); err == nil {
		t.Fatal("unexpected public test name passed")
	}
	if _, err := decodeResponse([]byte(`{"version":"x","unknown":true}`)); err == nil {
		t.Fatal("unknown response field passed")
	}
}

func newTestRunner(t *testing.T, config Config, command CommandExecutor, observer Observer) *DockerRunner {
	t.Helper()
	runner, err := New(config, Options{
		Command: command, Observer: observer,
		NewContainerName: func() string { return testContainerName },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return runner
}

func requestFor(language coding.Language, source string) coding.ExecutionRequest {
	return coding.ExecutionRequest{QuestionID: "pair_sum", Language: language, Source: source}
}

func successfulHandler(t *testing.T, response []byte) func(context.Context, []byte, []string) (CommandResult, error) {
	t.Helper()
	return func(_ context.Context, _ []byte, args []string) (CommandResult, error) {
		if first(args) == "run" {
			return CommandResult{Stdout: slices.Clone(response)}, nil
		}
		return CommandResult{}, nil
	}
}

func passedResponse(t *testing.T) []byte {
	t.Helper()
	return marshalResponse(t, responseEnvelope{
		Version: responseVersion,
		Result: coding.SafeResult{
			Version: coding.ResultVersion, Status: coding.RunPassed,
			PublicTests: []coding.PublicTestResult{
				{Name: "example-1", Status: coding.TestPassed},
				{Name: "example-2", Status: coding.TestPassed},
			},
			HiddenTests: coding.HiddenTestSummary{Passed: 2}, ErrorKind: coding.ErrorNone,
		},
		Runtime: coding.RuntimeStats{DurationMilliseconds: 17, PeakMemoryKB: 2048},
	})
}

func failedResponse(t *testing.T) []byte {
	t.Helper()
	return marshalResponse(t, responseEnvelope{
		Version: responseVersion,
		Result: coding.SafeResult{
			Version: coding.ResultVersion, Status: coding.RunFailed,
			PublicTests: []coding.PublicTestResult{
				{Name: "example-1", Status: coding.TestPassed},
				{Name: "example-2", Status: coding.TestFailed},
			},
			HiddenTests: coding.HiddenTestSummary{Passed: 1, Failed: 1}, ErrorKind: coding.ErrorNone,
		},
		Runtime: coding.RuntimeStats{DurationMilliseconds: 11, PeakMemoryKB: 1024},
	})
}

func errorResponse(t *testing.T, kind coding.ErrorKind) []byte {
	t.Helper()
	return marshalResponse(t, responseEnvelope{
		Version: responseVersion,
		Result: coding.SafeResult{
			Version: coding.ResultVersion, Status: coding.RunError,
			PublicTests: []coding.PublicTestResult{
				{Name: "example-1", Status: coding.TestError},
				{Name: "example-2", Status: coding.TestError},
			},
			HiddenTests: coding.HiddenTestSummary{Failed: 2}, ErrorKind: kind,
		},
	})
}

func marshalResponse(t *testing.T, response responseEnvelope) []byte {
	t.Helper()
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func assertSecurityArguments(t *testing.T, args []string) {
	t.Helper()
	for _, pair := range [][2]string{
		{"--name", testContainerName}, {"--label", "interviewcraft.runner=true"},
		{"--pull", "never"}, {"--network", "none"}, {"--user", "65532:65532"},
		{"--cap-drop", "ALL"}, {"--security-opt", "no-new-privileges=true"},
		{"--cpus", "0.50"}, {"--memory", "256m"}, {"--memory-swap", "256m"},
		{"--pids-limit", "64"}, {"--ipc", "none"}, {"--log-driver", "none"},
	} {
		if !hasPair(args, pair[0], pair[1]) {
			t.Fatalf("missing %q %q in %#v", pair[0], pair[1], args)
		}
	}
	for _, flag := range []string{"--read-only", "--init", "--interactive"} {
		if !slices.Contains(args, flag) {
			t.Fatalf("missing %q in %#v", flag, args)
		}
	}
	if !hasPair(args, "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m,mode=1777") {
		t.Fatalf("unsafe tmpfs: %#v", args)
	}
	for _, forbidden := range []string{"-v", "--volume", "--mount", "-e", "--env", "--privileged"} {
		if slices.Contains(args, forbidden) {
			t.Fatalf("forbidden Docker flag %q in %#v", forbidden, args)
		}
	}
	if args[len(args)-1] != defaultImage {
		t.Fatalf("image=%q", args[len(args)-1])
	}
}

func assertLastCleanup(t *testing.T, calls []commandCall) {
	t.Helper()
	if len(calls) == 0 {
		t.Fatal("no Docker calls")
	}
	assertCleanup(t, calls[len(calls)-1].Args)
}

func assertCleanup(t *testing.T, args []string) {
	t.Helper()
	want := []string{"rm", "--force", "--volumes", testContainerName}
	if !slices.Equal(args, want) {
		t.Fatalf("cleanup=%#v want=%#v", args, want)
	}
}

func hasPair(args []string, left, right string) bool {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == left && args[index+1] == right {
			return true
		}
	}
	return false
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}
