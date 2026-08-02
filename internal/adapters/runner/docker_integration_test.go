package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/coding"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

const integrationEnvironment = "INTERVIEWCRAFT_RUNNER_INTEGRATION"

func TestDockerIntegrationHealth(t *testing.T) {
	requireDockerIntegration(t)
	runner, err := New(DefaultConfig(), Options{})
	if err != nil {
		t.Fatal("create Docker Runner")
	}
	diagnostic, err := runner.Diagnose(context.Background())
	if err != nil {
		t.Fatal("diagnose Docker Runner")
	}
	if diagnostic.DockerVersion == "" || !diagnostic.ImageReady ||
		!diagnostic.NetworkDisabled || !diagnostic.ReadOnlyRoot ||
		!diagnostic.NonRootUser || !diagnostic.CapabilitiesOff ||
		!diagnostic.NoNewPrivileges {
		t.Fatalf("unsafe diagnostic: %#v", diagnostic)
	}
}

func TestDockerIntegrationLanguages(t *testing.T) {
	requireDockerIntegration(t)
	t.Setenv("INTERVIEWCRAFT_HOST_SECRET", "host-secret-must-not-cross-runner")
	tests := []struct {
		name     string
		language coding.Language
		correct  string
		wrong    string
	}{
		{
			name: "python", language: coding.LanguagePython,
			correct: `# source-token-python-must-not-leak
import os

def pair_sum(nums, target):
    if os.environ.get("INTERVIEWCRAFT_HOST_SECRET"):
        return [9, 9]
    seen = {}
    for index, value in enumerate(nums):
        other = target - value
        if other in seen:
            return [seen[other], index]
        seen[value] = index
    return []
`,
			wrong: `def pair_sum(nums, target):
    return [0, 0]
`,
		},
		{
			name: "javascript", language: coding.LanguageJavaScript,
			correct: `// source-token-javascript-must-not-leak
function pairSum(nums, target) {
  if (process.env.INTERVIEWCRAFT_HOST_SECRET) return [9, 9];
  const seen = new Map();
  for (let index = 0; index < nums.length; index++) {
    const other = target - nums[index];
    if (seen.has(other)) return [seen.get(other), index];
    seen.set(nums[index], index);
  }
  return [];
}
`,
			wrong: `function pairSum(nums, target) {
  return [0, 0];
}
`,
		},
		{
			name: "java", language: coding.LanguageJava,
			correct: `// source-token-java-must-not-leak
import java.util.HashMap;
import java.util.Map;

class Solution {
    public int[] pairSum(int[] nums, int target) {
        if (System.getenv("INTERVIEWCRAFT_HOST_SECRET") != null) return new int[]{9, 9};
        Map<Integer, Integer> seen = new HashMap<>();
        for (int index = 0; index < nums.length; index++) {
            int other = target - nums[index];
            if (seen.containsKey(other)) return new int[]{seen.get(other), index};
            seen.put(nums[index], index);
        }
        return new int[0];
    }
}
`,
			wrong: `class Solution {
    public int[] pairSum(int[] nums, int target) {
        return new int[]{0, 0};
    }
}
`,
		},
	}

	for _, test := range tests {
		t.Run(test.name+"/passed", func(t *testing.T) {
			name := integrationContainerName(test.name + "-passed")
			runner, states := newDockerIntegrationRunner(t, name, DefaultConfig())
			result, err := runner.Run(context.Background(), requestFor(test.language, test.correct))
			if err != nil {
				t.Fatal("run correct implementation")
			}
			assertPassedResult(t, result)
			assertCompletedLifecycle(t, states())
			assertSafeResult(t, result, test.correct)
			assertNoContainer(t, name)
		})

		t.Run(test.name+"/failed", func(t *testing.T) {
			name := integrationContainerName(test.name + "-failed")
			runner, states := newDockerIntegrationRunner(t, name, DefaultConfig())
			result, err := runner.Run(context.Background(), requestFor(test.language, test.wrong))
			if err != nil {
				t.Fatal("run incorrect implementation")
			}
			if result.Result.Status != coding.RunFailed || result.Result.ErrorKind != coding.ErrorNone {
				t.Fatalf("incorrect implementation result: %#v", result.Result)
			}
			assertCompletedLifecycle(t, states())
			assertSafeResult(t, result, test.wrong)
			assertNoContainer(t, name)
		})
	}
}

func TestDockerIntegrationIsolationAttacks(t *testing.T) {
	requireDockerIntegration(t)
	tests := []struct {
		name       string
		language   coding.Language
		source     string
		wantStatus coding.RunStatus
		wantError  coding.ErrorKind
	}{
		{
			name: "infinite-loop", language: coding.LanguagePython,
			source: `def pair_sum(nums, target):
    while True:
        pass
`,
			wantStatus: coding.RunError, wantError: coding.ErrorTimeout,
		},
		{
			name: "network-denied", language: coding.LanguagePython,
			source: `def pair_sum(nums, target):
    import socket
    try:
        connection = socket.create_connection(("1.1.1.1", 53), timeout=0.25)
        connection.close()
        return [9, 9]
    except OSError:
        seen = {}
        for index, value in enumerate(nums):
            other = target - value
            if other in seen:
                return [seen[other], index]
            seen[value] = index
        return []
`,
			wantStatus: coding.RunPassed, wantError: coding.ErrorNone,
		},
		{
			name: "memory-bomb", language: coding.LanguageJavaScript,
			source: `function pairSum(nums, target) {
  const values = [];
  while (true) values.push(new Array(1000000).fill(1));
}
`,
			wantStatus: coding.RunError, wantError: coding.ErrorOutOfMemory,
		},
		{
			name: "process-bomb", language: coding.LanguagePython,
			source: `def pair_sum(nums, target):
    import os
    import time
    while True:
        try:
            process_id = os.fork()
        except OSError:
            while True:
                pass
        if process_id == 0:
            time.sleep(60)
            os._exit(0)
`,
			wantStatus: coding.RunError, wantError: coding.ErrorTimeout,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			name := integrationContainerName(test.name)
			runner, states := newDockerIntegrationRunner(t, name, DefaultConfig())
			result, err := runner.Run(context.Background(), requestFor(test.language, test.source))
			if err != nil {
				t.Fatal("run isolation attack")
			}
			if result.Result.Status != test.wantStatus || result.Result.ErrorKind != test.wantError {
				t.Fatalf("attack result status=%q error=%q", result.Result.Status, result.Result.ErrorKind)
			}
			assertCompletedLifecycle(t, states())
			assertSafeResult(t, result, test.source)
			assertNoContainer(t, name)
		})
	}
}

func TestDockerIntegrationCancellationAndLoading(t *testing.T) {
	requireDockerIntegration(t)
	name := integrationContainerName("cancellation")
	config := DefaultConfig()
	config.Limits.ProgressInterval = 25 * time.Millisecond
	runner, states := newDockerIntegrationRunner(t, name, config)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result coding.ExecutionResult
		err    error
	}
	completed := make(chan outcome, 1)
	go func() {
		result, err := runner.Run(ctx, requestFor(coding.LanguagePython, `def pair_sum(nums, target):
    while True:
        pass
`))
		completed <- outcome{result: result, err: err}
	}()
	waitForContainer(t, name, 3*time.Second)
	editorState := "editable while runner is loading"
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case value := <-completed:
		if !domainerr.IsCode(value.err, domainerr.CodeOperationCancelled) {
			t.Fatal("cancellation did not return the typed cancellation error")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("cancelled Runner did not return within the cleanup bound")
	}
	if editorState != "editable while runner is loading" {
		t.Fatal("caller-owned editor state changed")
	}
	assertElapsedStreaming(t, states())
	assertNoContainer(t, name)
}

func TestDockerIntegrationDependencyAndProtocolErrors(t *testing.T) {
	requireDockerIntegration(t)

	t.Run("missing-image", func(t *testing.T) {
		name := integrationContainerName("missing-image")
		config := DefaultConfig()
		config.Image = "interviewcraft-runner:missing-t020"
		runner, _ := newDockerIntegrationRunner(t, name, config)
		_, err := runner.Run(context.Background(), requestFor(coding.LanguagePython, "def pair_sum(a, b): return []"))
		if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
			t.Fatal("missing image was not a typed dependency error")
		}
		assertSafeError(t, err)
		assertNoContainer(t, name)
	})

	t.Run("invalid-image-label", func(t *testing.T) {
		config := DefaultConfig()
		config.Image = "alpine:3.22"
		runner, err := New(config, Options{})
		if err != nil {
			t.Fatal("create invalid-label Runner")
		}
		if _, err := runner.Diagnose(context.Background()); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
			t.Fatal("invalid label was not rejected")
		}
	})

	t.Run("invalid-protocol", func(t *testing.T) {
		const image = "interviewcraft-runner-protocol-test:local"
		buildInvalidProtocolImage(t, image)
		defer removeImage(image)
		name := integrationContainerName("invalid-protocol")
		config := DefaultConfig()
		config.Image = image
		runner, _ := newDockerIntegrationRunner(t, name, config)
		_, err := runner.Run(context.Background(), requestFor(coding.LanguagePython, "def pair_sum(a, b): return []"))
		if !domainerr.IsCode(err, domainerr.CodeInvalidState) {
			t.Fatal("invalid protocol was not rejected")
		}
		assertSafeError(t, err)
		assertNoContainer(t, name)
	})
}

func requireDockerIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv(integrationEnvironment) != "1" {
		t.Skip("set INTERVIEWCRAFT_RUNNER_INTEGRATION=1 to run Docker isolation tests")
	}
}

func newDockerIntegrationRunner(
	t *testing.T,
	containerName string,
	config Config,
) (*DockerRunner, func() []async.State[Progress]) {
	t.Helper()
	var mutex sync.Mutex
	states := make([]async.State[Progress], 0, 8)
	runner, err := New(config, Options{
		NewContainerName: func() string { return containerName },
		Observer: func(state async.State[Progress]) {
			mutex.Lock()
			defer mutex.Unlock()
			states = append(states, state)
		},
	})
	if err != nil {
		t.Fatal("create integration Runner")
	}
	return runner, func() []async.State[Progress] {
		mutex.Lock()
		defer mutex.Unlock()
		return append([]async.State[Progress](nil), states...)
	}
}

func integrationContainerName(suffix string) string {
	return "interviewcraft-runner-integration-" + suffix
}

func assertPassedResult(t *testing.T, result coding.ExecutionResult) {
	t.Helper()
	if result.Result.Status != coding.RunPassed || result.Result.ErrorKind != coding.ErrorNone ||
		len(result.Result.PublicTests) != 2 || result.Result.HiddenTests.Passed != 2 ||
		result.Result.HiddenTests.Failed != 0 || result.Runtime.DurationMilliseconds <= 0 ||
		result.Runtime.PeakMemoryKB <= 0 {
		t.Fatalf("invalid passing result: %#v", result)
	}
	for _, public := range result.Result.PublicTests {
		if public.Status != coding.TestPassed {
			t.Fatalf("public test did not pass: %#v", public)
		}
	}
}

func assertCompletedLifecycle(t *testing.T, states []async.State[Progress]) {
	t.Helper()
	if len(states) < 3 || states[0].Phase != async.Pending || states[len(states)-1].Phase != async.Succeeded {
		t.Fatalf("invalid lifecycle phases: %#v", states)
	}
	for _, state := range states {
		if state.Phase == async.Streaming && state.Value != nil {
			return
		}
	}
	t.Fatal("lifecycle did not stream progress")
}

func assertElapsedStreaming(t *testing.T, states []async.State[Progress]) {
	t.Helper()
	for _, state := range states {
		if state.Phase == async.Streaming && state.Value != nil && state.Value.Elapsed > 0 {
			return
		}
	}
	t.Fatalf("no elapsed streaming state: %#v", states)
}

func assertSafeResult(t *testing.T, result coding.ExecutionResult, source string) {
	t.Helper()
	payload, err := json.Marshal(result)
	if err != nil {
		t.Fatal("marshal safe result")
	}
	for _, forbidden := range []string{
		source,
		"source-token-",
		"host-secret-must-not-cross-runner",
		"hidden-duplicate-values",
		"hidden-negative-values",
		"[-3,4,3,90]",
		"expected",
		"stderr",
		"/tmp/",
		"/src/",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("safe result contains forbidden runner detail %q", forbidden)
		}
	}
}

func assertSafeError(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("expected a typed error")
	}
	for _, forbidden := range []string{
		"/tmp/", "/src/", "docker_engine", "stderr", "host-secret", "hidden-",
	} {
		if strings.Contains(err.Error(), forbidden) {
			t.Fatalf("safe error contains forbidden detail %q", forbidden)
		}
	}
}

func waitForContainer(t *testing.T, name string, limit time.Duration) {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ids, err := containerIDs(name)
		if err != nil {
			t.Fatal("query Runner container while waiting")
		}
		if ids != "" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Runner container did not start within the bounded wait")
}

func assertNoContainer(t *testing.T, name string) {
	t.Helper()
	ids, err := containerIDs(name)
	if err != nil {
		t.Fatal("query Runner container after execution")
	}
	if ids == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	arguments := append([]string{"rm", "--force", "--volumes"}, strings.Fields(ids)...)
	_ = exec.CommandContext(ctx, "docker", arguments...).Run()
	t.Fatal("Runner container cleanup was incomplete")
}

func containerIDs(name string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	command := exec.CommandContext(
		ctx,
		"docker",
		"ps",
		"-aq",
		"--filter",
		"name=^/"+name+"$",
	)
	output, err := command.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func buildInvalidProtocolImage(t *testing.T, image string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "docker", "build", "--pull=false", "-t", image, "-")
	command.Stdin = strings.NewReader("FROM alpine:3.22\nUSER 65532:65532\nENTRYPOINT [\"/bin/echo\",\"invalid-runner-response\"]\n")
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatal("build invalid protocol fixture image")
	}
}

func removeImage(image string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = exec.CommandContext(ctx, "docker", "image", "rm", "--force", image).Run()
}
