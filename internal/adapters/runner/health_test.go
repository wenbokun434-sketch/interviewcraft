package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestDiagnoseReportsOnlySafePolicyAndHealth(t *testing.T) {
	command := &fakeCommand{}
	command.handler = func(_ context.Context, _ []byte, args []string) (CommandResult, error) {
		switch first(args) {
		case "version":
			return CommandResult{Stdout: []byte("29.1.3\n")}, nil
		case "image":
			return CommandResult{Stdout: localInspectionPayload("true")}, nil
		default:
			return CommandResult{}, errors.New("unexpected command")
		}
	}
	runner := newTestRunner(t, DefaultConfig(), command, nil)
	diagnostic, err := runner.Diagnose(context.Background())
	if err != nil {
		t.Fatalf("Diagnose: %v", err)
	}
	if diagnostic.DockerVersion != "29.1.3" || !diagnostic.ImageReady ||
		!diagnostic.NetworkDisabled || !diagnostic.ReadOnlyRoot || !diagnostic.NonRootUser ||
		!diagnostic.CapabilitiesOff || !diagnostic.NoNewPrivileges || diagnostic.MemoryMB != 256 {
		t.Fatalf("diagnostic=%#v", diagnostic)
	}
	if len(command.snapshot()) != 2 {
		t.Fatalf("calls=%#v", command.snapshot())
	}
}

func TestDiagnoseFailsClosedForDockerAndImage(t *testing.T) {
	for _, test := range []struct {
		name    string
		handler func(context.Context, []byte, []string) (CommandResult, error)
	}{
		{
			name: "daemon unavailable",
			handler: func(context.Context, []byte, []string) (CommandResult, error) {
				return CommandResult{Stderr: []byte(`C:\secret\docker.sock`)}, errors.New(`C:\secret\docker.sock`)
			},
		},
		{
			name: "image label invalid",
			handler: func(_ context.Context, _ []byte, args []string) (CommandResult, error) {
				if first(args) == "version" {
					return CommandResult{Stdout: []byte("29.1.3")}, nil
				}
				return CommandResult{Stdout: localInspectionPayload("false")}, nil
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := newTestRunner(t, DefaultConfig(), &fakeCommand{handler: test.handler}, nil)
			_, err := runner.Diagnose(context.Background())
			if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDiagnoseReleasedImageVerifiesCompletePolicy(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	image := ManifestImage{OS: "linux", Architecture: "amd64", Repository: OfficialRepository, Digest: digest, Protocol: responseVersion}
	config := ReleaseConfig(image, "1.2.3", CertificateIdentityURL+"1.2.3")
	command := &fakeCommand{handler: func(_ context.Context, _ []byte, args []string) (CommandResult, error) {
		if first(args) == "version" {
			return CommandResult{Stdout: []byte("29.1.3")}, nil
		}
		return CommandResult{Stdout: []byte(`[{"RepoDigests":["` + OfficialRepository + `@` + digest + `"],"Os":"linux","Architecture":"amd64","Config":{"User":"65532:65532","Labels":{"io.interviewcraft.runner":"true","io.interviewcraft.version":"1.2.3","io.interviewcraft.protocol":"` + responseVersion + `"}}}]`)}, nil
	}}
	verifier := &provisionVerifierStub{}
	adapter, err := New(config, Options{Command: command, SignatureVerifier: verifier})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	diagnostic, err := adapter.Diagnose(context.Background())
	if err != nil || !diagnostic.SignatureVerified || diagnostic.Digest != digest ||
		diagnostic.Version != "1.2.3" || diagnostic.Protocol != responseVersion || diagnostic.Architecture != "amd64" || verifier.images != 1 {
		t.Fatalf("diagnostic=%#v verifier=%#v err=%v", diagnostic, verifier, err)
	}
}

func localInspectionPayload(label string) []byte {
	return []byte(`[{"RepoDigests":[],"Os":"linux","Architecture":"amd64","Config":{"User":"65532:65532","Labels":{"io.interviewcraft.runner":"` + label + `"}}}]`)
}

func TestConfigDefaultsAndRejectsWeakenedIsolation(t *testing.T) {
	runner, err := New(Config{}, Options{Command: &fakeCommand{}})
	if err != nil {
		t.Fatalf("New defaults: %v", err)
	}
	if runner.config.Image != defaultImage || runner.config.Limits != DefaultLimits() {
		t.Fatalf("config=%#v", runner.config)
	}
	for _, mutate := range []func(*Config){
		func(config *Config) { config.Limits.CPUs = 0.01 },
		func(config *Config) { config.Limits.MemoryMB = 32 },
		func(config *Config) { config.Limits.PIDs = 4 },
		func(config *Config) { config.Limits.WallTime = 31_000_000_000 },
		func(config *Config) { config.Image = "--privileged" },
	} {
		config := DefaultConfig()
		mutate(&config)
		if _, err := New(config, Options{Command: &fakeCommand{}}); !domainerr.IsCode(err, domainerr.CodeValidation) {
			t.Fatalf("weak config passed: %#v err=%v", config, err)
		}
	}
}
