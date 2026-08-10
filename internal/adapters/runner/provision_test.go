package runner

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
)

type provisionVerifierStub struct {
	blobErr  error
	imageErr error
	mu       sync.Mutex
	blobs    int
	images   int
}

func (stub *provisionVerifierStub) VerifyBlob(context.Context, string, string, string, string) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.blobs++
	return stub.blobErr
}
func (stub *provisionVerifierStub) VerifyImage(context.Context, string, string, string) error {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.images++
	return stub.imageErr
}

func TestProvisionVerifiedRunnerMainProgressAndCleanup(t *testing.T) {
	server := runnerReleaseServer(t, validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)))
	defer server.Close()
	command := newProvisionDocker("amd64", nil)
	verifier := &provisionVerifierStub{}
	var phases []async.Phase
	var stages []ProvisionStage
	result, err := Provision(context.Background(), ProvisionRequest{Version: "1.2.3"}, ProvisionOptions{
		Client: server.Client(), ReleaseURL: server.URL, GOOS: "linux", GOARCH: "amd64",
		Docker: command, Verifier: verifier,
		Smoke: func(context.Context, Config, CommandExecutor) error { return nil },
	}, func(state async.State[ProvisionProgress]) {
		phases = append(phases, state.Phase)
		if state.Value != nil {
			stages = append(stages, state.Value.Stage)
		}
	})
	if err != nil || result.Digest != "sha256:"+strings.Repeat("a", 64) || result.Architecture != "amd64" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	wantStages := []ProvisionStage{ProvisionResolve, ProvisionPull, ProvisionVerify, ProvisionInspect, ProvisionSmoke, ProvisionEnable, ProvisionEnable}
	if strings.Trim(strings.Join(provisionStageStrings(stages), ","), ",") != strings.Join(provisionStageStrings(wantStages), ",") ||
		phases[0] != async.Pending || phases[len(phases)-1] != async.Succeeded {
		t.Fatalf("phases=%v stages=%v", phases, stages)
	}
	if verifier.blobs != 1 || verifier.images != 1 || command.removed() {
		t.Fatalf("verifier=%#v calls=%#v", verifier, command.snapshot())
	}
}

func TestProvisionFailsClosedAndRemovesNewImage(t *testing.T) {
	tests := []struct {
		name       string
		manifest   string
		verifier   *provisionVerifierStub
		docker     *provisionDocker
		smokeError error
	}{
		{name: "manifest signature", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{blobErr: errors.New("bad signature")}},
		{name: "image signature", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{imageErr: errors.New("bad signature")}},
		{name: "digest", manifest: strings.Replace(validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("A", 64), 1), verifier: &provisionVerifierStub{}},
		{name: "architecture", manifest: strings.Replace(validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), "\tarm64\t", "\tamd64\t", 1), verifier: &provisionVerifierStub{}},
		{name: "label", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{}, docker: newProvisionDocker("amd64", func(value *imageInspection) { value.Config.Labels["io.interviewcraft.runner"] = "false" })},
		{name: "user", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{}, docker: newProvisionDocker("amd64", func(value *imageInspection) { value.Config.User = "0:0" })},
		{name: "protocol", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{}, docker: newProvisionDocker("amd64", func(value *imageInspection) { value.Config.Labels["io.interviewcraft.protocol"] = "runner-v0" })},
		{name: "daemon", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{}, docker: func() *provisionDocker {
			value := newProvisionDocker("amd64", nil)
			value.daemonErr = errors.New("daemon unavailable")
			return value
		}()},
		{name: "pull", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{}, docker: func() *provisionDocker {
			value := newProvisionDocker("amd64", nil)
			value.pullErr = errors.New("registry unavailable")
			return value
		}()},
		{name: "smoke", manifest: validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)), verifier: &provisionVerifierStub{}, smokeError: errors.New("smoke failed")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := runnerReleaseServer(t, test.manifest)
			defer server.Close()
			docker := test.docker
			if docker == nil {
				docker = newProvisionDocker("amd64", nil)
			}
			_, err := Provision(context.Background(), ProvisionRequest{Version: "1.2.3"}, ProvisionOptions{
				Client: server.Client(), ReleaseURL: server.URL, GOOS: "linux", GOARCH: "amd64",
				Docker: docker, Verifier: test.verifier,
				Smoke: func(context.Context, Config, CommandExecutor) error { return test.smokeError },
			}, nil)
			if err == nil {
				t.Fatal("invalid Runner was enabled")
			}
			if test.name != "manifest signature" && test.name != "digest" && test.name != "architecture" && !docker.removed() {
				t.Fatalf("new image was not cleaned: %#v", docker.snapshot())
			}
		})
	}
}

func TestProvisionCancellationCleansPartialPull(t *testing.T) {
	server := runnerReleaseServer(t, validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64)))
	defer server.Close()
	docker := newProvisionDocker("amd64", nil)
	docker.pullErr = context.Canceled
	_, err := Provision(context.Background(), ProvisionRequest{Version: "1.2.3"}, ProvisionOptions{
		Client: server.Client(), ReleaseURL: server.URL, GOOS: "linux", GOARCH: "amd64",
		Docker: docker, Verifier: &provisionVerifierStub{}, Smoke: func(context.Context, Config, CommandExecutor) error { return nil },
	}, nil)
	if err == nil || !docker.removed() {
		t.Fatalf("err=%v calls=%#v", err, docker.snapshot())
	}
}

func TestPersistCosignIsAtomicAndIdempotent(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source-cosign")
	target := filepath.Join(directory, "runner-tools", "v3.1.3", "cosign")
	if err := os.WriteFile(source, []byte("verified-cosign-binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := persistCosign(source, target); err != nil {
		t.Fatalf("first persist: %v", err)
	}
	first, err := os.ReadFile(target)
	if err != nil || string(first) != "verified-cosign-binary" {
		t.Fatalf("payload=%q err=%v", first, err)
	}
	if err := persistCosign(source, target); err != nil {
		t.Fatalf("idempotent persist: %v", err)
	}
	if matches, err := filepath.Glob(filepath.Join(filepath.Dir(target), ".cosign-*.tmp")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary verifier files=%v err=%v", matches, err)
	}
}

func runnerReleaseServer(t *testing.T, manifest string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/runner-manifest.txt":
			_, _ = response.Write([]byte(manifest))
		case "/runner-manifest.sigstore.json":
			_, _ = response.Write([]byte(`{"bundle":"fixture"}`))
		default:
			http.NotFound(response, request)
		}
	}))
}

type provisionDocker struct {
	*fakeCommand
	mu           sync.Mutex
	pulled       bool
	pullErr      error
	daemonErr    error
	remove       bool
	architecture string
	mutate       func(*imageInspection)
}

func newProvisionDocker(architecture string, mutate func(*imageInspection)) *provisionDocker {
	docker := &provisionDocker{architecture: architecture, mutate: mutate}
	docker.fakeCommand = &fakeCommand{}
	docker.fakeCommand.handler = docker.handle
	return docker
}

func (docker *provisionDocker) handle(_ context.Context, _ []byte, args []string) (CommandResult, error) {
	docker.mu.Lock()
	defer docker.mu.Unlock()
	if len(args) == 0 {
		return CommandResult{}, errors.New("missing command")
	}
	switch args[0] {
	case "pull":
		if docker.pullErr != nil {
			return CommandResult{ExitCode: 1}, docker.pullErr
		}
		docker.pulled = true
		return CommandResult{}, nil
	case "version":
		if docker.daemonErr != nil {
			return CommandResult{ExitCode: 1}, docker.daemonErr
		}
		return CommandResult{Stdout: []byte("29.1.3")}, nil
	case "image":
		if len(args) > 1 && args[1] == "rm" {
			docker.remove = true
			return CommandResult{}, nil
		}
		if !docker.pulled {
			return CommandResult{ExitCode: 1}, errors.New("not found")
		}
		inspection := imageInspection{RepoDigests: []string{OfficialRepository + "@sha256:" + strings.Repeat("a", 64)}, OS: "linux", Architecture: docker.architecture}
		inspection.Config.User = "65532:65532"
		inspection.Config.Labels = map[string]string{"io.interviewcraft.runner": "true", "io.interviewcraft.version": "1.2.3", "io.interviewcraft.protocol": responseVersion}
		if docker.mutate != nil {
			docker.mutate(&inspection)
		}
		payload := `[{"RepoDigests":["` + inspection.RepoDigests[0] + `"],"Os":"` + inspection.OS + `","Architecture":"` + inspection.Architecture + `","Config":{"User":"` + inspection.Config.User + `","Labels":{"io.interviewcraft.runner":"` + inspection.Config.Labels["io.interviewcraft.runner"] + `","io.interviewcraft.version":"` + inspection.Config.Labels["io.interviewcraft.version"] + `","io.interviewcraft.protocol":"` + inspection.Config.Labels["io.interviewcraft.protocol"] + `"}}}]`
		return CommandResult{Stdout: []byte(payload)}, nil
	default:
		return CommandResult{}, errors.New("unexpected command")
	}
}

func (docker *provisionDocker) removed() bool {
	docker.mu.Lock()
	defer docker.mu.Unlock()
	return docker.remove
}
func provisionStageStrings(values []ProvisionStage) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = string(value)
	}
	return result
}
