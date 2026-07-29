package doctor

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestRunHealthyLiteEnvironmentAllowsDisabledRunner(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runtime := healthyRuntime(dataDir)
	var states []async.State[Progress]
	runner := &stubRunnerProbe{}

	report, err := Run(context.Background(), runtime, Options{
		Terminal: stubTerminalProbe{width: 120, height: 36, known: true},
		Model:    stubModelProbe{},
		Runner:   runner,
		Observer: func(state async.State[Progress]) {
			states = append(states, state)
		},
	})

	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report.Blocking() {
		t.Fatalf("healthy report is blocking: %#v", report)
	}
	if len(report.Checks) != 5 {
		t.Fatalf("check count = %d, want 5", len(report.Checks))
	}
	if got := checkStatus(report, "runner"); got != Warning {
		t.Fatalf("disabled runner status = %q, want warning", got)
	}
	if runner.calls != 0 {
		t.Fatalf("disabled runner probe calls = %d, want 0", runner.calls)
	}

	wantPhases := []async.Phase{
		async.Pending,
		async.Streaming,
		async.Streaming,
		async.Streaming,
		async.Streaming,
		async.Streaming,
		async.Succeeded,
	}
	assertDoctorPhases(t, states, wantPhases)
}

func TestRunReturnsCompleteReportAndFailedStateForBlockingChecks(t *testing.T) {
	t.Parallel()

	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	runtime := healthyRuntime(dataDir)
	runtime.RunnerMode = config.RunnerDocker
	var states []async.State[Progress]

	report, err := Run(context.Background(), runtime, Options{
		Terminal: stubTerminalProbe{width: 72, height: 22, known: true},
		Model:    stubModelProbe{err: errors.New("provider unavailable")},
		Runner:   &stubRunnerProbe{err: errors.New("docker unavailable")},
		Observer: func(state async.State[Progress]) {
			states = append(states, state)
		},
	})

	if err == nil || !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("Run error = %v, want dependency unavailable", err)
	}
	if !report.Blocking() || len(report.Checks) != 5 {
		t.Fatalf("blocking report = %#v", report)
	}
	if checkStatus(report, "terminal") != Error ||
		checkStatus(report, "model") != Error ||
		checkStatus(report, "runner") != Warning {
		t.Fatalf("unexpected check statuses: %#v", report.Checks)
	}
	if states[len(states)-1].Phase != async.Failed {
		t.Fatalf("final phase = %q, want failed", states[len(states)-1].Phase)
	}
}

func TestRunMissingDataDirectoryReportsDataAndSQLiteErrors(t *testing.T) {
	t.Parallel()

	runtime := healthyRuntime(filepath.Join(t.TempDir(), "missing"))
	report, err := Run(context.Background(), runtime, Options{
		Terminal: stubTerminalProbe{width: 120, height: 36, known: true},
		Model:    stubModelProbe{},
		Runner:   &stubRunnerProbe{},
	})

	if err == nil {
		t.Fatal("Run with missing data directory unexpectedly succeeded")
	}
	if checkStatus(report, "data") != Error || checkStatus(report, "sqlite") != Error {
		t.Fatalf("missing data report = %#v", report.Checks)
	}
	if _, statErr := os.Stat(runtime.DataDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("doctor mutated missing data directory: %v", statErr)
	}
}

func TestHTTPModelProbeChecksOpenAIAuthenticationAndPath(t *testing.T) {
	t.Parallel()

	var gotPath string
	var gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		gotPath = request.URL.Path
		gotAuthorization = request.Header.Get("Authorization")
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	probe := HTTPModelProbe{
		Client: server.Client(),
		LookupEnv: func(name string) (string, bool) {
			if name == "TEST_OPENAI_KEY" {
				return "test-secret", true
			}
			return "", false
		},
	}
	err := probe.Check(context.Background(), config.LLM{
		Provider:  config.ProviderOpenAICompatible,
		Endpoint:  server.URL + "/v1",
		Model:     "test-model",
		APIKeyEnv: "TEST_OPENAI_KEY",
	})

	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if gotPath != "/v1/models" || gotAuthorization != "Bearer test-secret" {
		t.Fatalf("request path=%q authorization=%q", gotPath, gotAuthorization)
	}
}

func TestHTTPModelProbeRejectsMissingKeyWithoutRequest(t *testing.T) {
	t.Parallel()

	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		requests++
		response.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	probe := HTTPModelProbe{Client: server.Client()}
	err := probe.Check(context.Background(), config.LLM{
		Provider:  config.ProviderOpenAICompatible,
		Endpoint:  server.URL + "/v1",
		Model:     "test-model",
		APIKeyEnv: "MISSING_KEY",
	})

	if err == nil {
		t.Fatal("OpenAI-compatible probe without key unexpectedly succeeded")
	}
	if requests != 0 {
		t.Fatalf("requests with missing key = %d, want 0", requests)
	}
}

func TestEnvironmentTerminalProbeStates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		environment map[string]string
		width       int
		height      int
		known       bool
		wantError   bool
	}{
		{name: "unknown", environment: nil},
		{
			name:        "known",
			environment: map[string]string{"COLUMNS": "160", "LINES": "48"},
			width:       160,
			height:      48,
			known:       true,
		},
		{
			name:        "invalid",
			environment: map[string]string{"COLUMNS": "wide", "LINES": "48"},
			wantError:   true,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			probe := EnvironmentTerminalProbe{
				LookupEnv: func(name string) (string, bool) {
					value, ok := test.environment[name]
					return value, ok
				},
			}
			width, height, known, err := probe.Size()
			if (err != nil) != test.wantError {
				t.Fatalf("Size error = %v, wantError=%v", err, test.wantError)
			}
			if width != test.width || height != test.height || known != test.known {
				t.Fatalf(
					"Size = %dx%d known=%v, want %dx%d known=%v",
					width,
					height,
					known,
					test.width,
					test.height,
					test.known,
				)
			}
		})
	}
}

type stubTerminalProbe struct {
	width  int
	height int
	known  bool
	err    error
}

func (probe stubTerminalProbe) Size() (int, int, bool, error) {
	return probe.width, probe.height, probe.known, probe.err
}

type stubModelProbe struct {
	err error
}

func (probe stubModelProbe) Check(context.Context, config.LLM) error {
	return probe.err
}

type stubRunnerProbe struct {
	calls int
	err   error
}

func (probe *stubRunnerProbe) Check(context.Context) error {
	probe.calls++
	return probe.err
}

func healthyRuntime(dataDir string) config.Runtime {
	return config.Runtime{
		Version:      config.CurrentVersion,
		DataDir:      dataDir,
		DatabaseName: config.DefaultDatabaseName,
		LLM: config.LLM{
			Provider:  config.ProviderOllama,
			Endpoint:  "http://localhost:11434",
			Model:     "test-model",
			APIKeyEnv: "OPENAI_API_KEY",
		},
		RunnerMode:    config.RunnerDisabled,
		AudioProvider: config.AudioBrowser,
	}
}

func checkStatus(report Report, name string) Status {
	for _, check := range report.Checks {
		if check.Name == name {
			return check.Status
		}
	}
	return ""
}

func assertDoctorPhases(
	t *testing.T,
	states []async.State[Progress],
	want []async.Phase,
) {
	t.Helper()
	got := make([]async.Phase, len(states))
	for index, state := range states {
		if err := state.Validate(); err != nil {
			t.Fatalf("state %d invalid: %v", index, err)
		}
		got[index] = state.Phase
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("doctor phases = %v, want %v", got, want)
	}
}
