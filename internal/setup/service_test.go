package setup

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/credentials"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/doctor"
)

func TestSetupProfilesMainEmptyAndProgress(t *testing.T) {
	tests := []struct {
		name     string
		profile  Profile
		provider string
		secret   string
	}{
		{name: "lite", profile: ProfileLite, provider: config.ProviderOpenAICompatible, secret: "top-secret-value"},
		{name: "private local", profile: ProfilePrivateLocal, provider: config.ProviderOllama},
		{name: "full without image", profile: ProfileFull, provider: config.ProviderOpenAICompatible, secret: "top-secret-value"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "data")
			store := &fakeCredentialStore{}
			var phases []async.Phase
			var seenStages []Stage
			result, err := Run(context.Background(), Request{
				Profile: test.profile, DataDir: dataDir, Provider: test.provider,
				APIKey: test.secret, NonInteractive: true,
			}, testDependencies(store), func(state async.State[Progress]) {
				phases = append(phases, state.Phase)
				if state.Value != nil {
					seenStages = append(seenStages, state.Value.Stage)
				}
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Runtime.RunnerMode != config.RunnerDisabled {
				t.Fatalf("runner mode=%q", result.Runtime.RunnerMode)
			}
			if len(seenStages) != len(stages)+1 || phases[0] != async.Pending || phases[len(phases)-1] != async.Succeeded {
				t.Fatalf("phases=%v stages=%v", phases, seenStages)
			}
			payload, err := os.ReadFile(result.ConfigPath)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if strings.Contains(string(payload), test.secret) && test.secret != "" {
				t.Fatal("configuration leaked API key")
			}
			if _, err := os.Stat(filepath.Join(dataDir, StateFileName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("setup state remains: %v", err)
			}
		})
	}
}

func TestSetupCancelResumesAtSafeCheckpoint(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	store := &fakeCredentialStore{}
	deps := testDependencies(store)
	deps.Diagnose = func(context.Context, config.Runtime, func(string) (string, bool)) (doctor.Report, error) {
		return doctor.Report{}, context.Canceled
	}
	request := Request{Profile: ProfileLite, DataDir: dataDir, APIKey: "resume-secret"}
	if _, err := Run(context.Background(), request, deps, nil); err == nil {
		t.Fatal("cancelled setup succeeded")
	}
	checkpoint, found, err := loadCheckpoint(filepath.Join(dataDir, StateFileName))
	if err != nil || !found || checkpoint.Stage != StageDiagnose {
		t.Fatalf("checkpoint=%#v found=%v err=%v", checkpoint, found, err)
	}
	statePayload, readErr := os.ReadFile(filepath.Join(dataDir, StateFileName))
	if readErr != nil || strings.Contains(string(statePayload), "resume-secret") {
		t.Fatalf("checkpoint secret leak or read error: payload=%q err=%v", statePayload, readErr)
	}
	deps.Diagnose = testDependencies(store).Diagnose
	var first Stage
	result, err := Run(context.Background(), request, deps, func(state async.State[Progress]) {
		if first == "" && state.Phase == async.Streaming && state.Value != nil {
			first = state.Value.Stage
		}
	})
	if err != nil || !result.Resumed || first != StageDiagnose {
		t.Fatalf("result=%#v first=%q err=%v", result, first, err)
	}
}

func TestSetupKeyringFailureRequiresEnvironment(t *testing.T) {
	store := &fakeCredentialStore{err: errors.New("keyring unavailable")}
	_, err := Run(context.Background(), Request{
		Profile: ProfileLite, DataDir: t.TempDir(), APIKey: "not-persisted",
	}, testDependencies(store), nil)
	if err == nil || !strings.Contains(err.Error(), "凭据库") {
		t.Fatalf("error=%v", err)
	}
}

func TestSetupConfigFailureRollsBackCredential(t *testing.T) {
	store := &fakeCredentialStore{secret: "previous-secret"}
	deps := testDependencies(store)
	deps.SaveConfig = func(string, config.Runtime) error { return errors.New("injected atomic failure") }
	_, err := Run(context.Background(), Request{
		Profile: ProfileLite, DataDir: t.TempDir(), APIKey: "replacement-secret",
	}, deps, nil)
	if err == nil {
		t.Fatal("setup unexpectedly succeeded")
	}
	if store.secret != "previous-secret" {
		t.Fatalf("credential=%q, want previous value", store.secret)
	}
}

func TestSetupRepeatIsIdempotent(t *testing.T) {
	dataDir := t.TempDir()
	store := &fakeCredentialStore{}
	deps := testDependencies(store)
	request := Request{Profile: ProfileLite, DataDir: dataDir, APIKey: "repeat-secret"}
	first, err := Run(context.Background(), request, deps, nil)
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	firstPayload, _ := os.ReadFile(first.ConfigPath)
	second, err := Run(context.Background(), request, deps, nil)
	if err != nil || second.Resumed {
		t.Fatalf("second=%#v err=%v", second, err)
	}
	secondPayload, _ := os.ReadFile(second.ConfigPath)
	if string(firstPayload) != string(secondPayload) {
		t.Fatalf("configuration changed across repeat\nfirst=%s\nsecond=%s", firstPayload, secondPayload)
	}
}

func TestSetupFullEnablesOnlyInjectedReadyLocalRunner(t *testing.T) {
	dataDir := t.TempDir()
	store := &fakeCredentialStore{}
	deps := testDependencies(store)
	deps.RunnerReady = func(context.Context) bool { return true }
	var diagnosedMode string
	deps.Diagnose = func(_ context.Context, runtime config.Runtime, resolve func(string) (string, bool)) (doctor.Report, error) {
		diagnosedMode = runtime.RunnerMode
		if _, ok := resolve(runtime.LLM.APIKeyEnv); !ok {
			return doctor.Report{}, errors.New("missing in-memory credential")
		}
		return doctor.Report{}, nil
	}
	result, err := Run(context.Background(), Request{
		Profile: ProfileFull, DataDir: dataDir, APIKey: "full-secret",
	}, deps, nil)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.RunnerReady || result.Runtime.RunnerMode != config.RunnerDocker || diagnosedMode != config.RunnerDocker {
		t.Fatalf("result=%#v diagnosedMode=%q", result, diagnosedMode)
	}
}

func TestSetupRejectsCheckpointWithTrailingJSON(t *testing.T) {
	dataDir := t.TempDir()
	statePath := filepath.Join(dataDir, StateFileName)
	payload := `{"version":1,"profile":"lite","runtime":{},"stage":"preflight","updated_at":"2026-01-01T00:00:00Z"} {}`
	if err := os.WriteFile(statePath, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, _, err := loadCheckpoint(statePath)
	if err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("error=%v", err)
	}
}

func TestSetupRestartDiscardsMismatchedCheckpoint(t *testing.T) {
	dataDir := t.TempDir()
	store := &fakeCredentialStore{}
	deps := testDependencies(store)
	deps.Diagnose = func(context.Context, config.Runtime, func(string) (string, bool)) (doctor.Report, error) {
		return doctor.Report{}, errors.New("provider unavailable")
	}
	_, _ = Run(context.Background(), Request{
		Profile: ProfileLite, DataDir: dataDir, APIKey: "first-secret", Model: "first-model",
	}, deps, nil)
	deps.Diagnose = testDependencies(store).Diagnose
	result, err := Run(context.Background(), Request{
		Profile: ProfileLite, DataDir: dataDir, APIKey: "second-secret", Model: "second-model", Restart: true,
	}, deps, nil)
	if err != nil || result.Resumed || result.Runtime.LLM.Model != "second-model" {
		t.Fatalf("result=%#v err=%v", result, err)
	}
}

type fakeCredentialStore struct {
	secret string
	err    error
}

func (store *fakeCredentialStore) Get(string, string) (string, error) {
	if store.err != nil {
		return "", store.err
	}
	if store.secret == "" {
		return "", credentials.ErrNotFound
	}
	return store.secret, nil
}
func (store *fakeCredentialStore) Set(_, _, secret string) error {
	if store.err != nil {
		return store.err
	}
	store.secret = secret
	return nil
}
func (store *fakeCredentialStore) Delete(string, string) error {
	if store.err != nil {
		return store.err
	}
	store.secret = ""
	return nil
}

type fakeDatabase struct{}

func (fakeDatabase) Close() error { return nil }

func testDependencies(store credentials.Store) Dependencies {
	return Dependencies{
		Credentials: store,
		LookupEnv:   func(string) (string, bool) { return "", false },
		OpenDatabase: func(context.Context, db.Config) (databaseCloser, error) {
			return fakeDatabase{}, nil
		},
		Diagnose: func(_ context.Context, runtime config.Runtime, resolve func(string) (string, bool)) (doctor.Report, error) {
			if runtime.LLM.Provider == config.ProviderOpenAICompatible {
				if secret, ok := resolve(runtime.LLM.APIKeyEnv); !ok || strings.TrimSpace(secret) == "" {
					return doctor.Report{}, errors.New("secret was not resolved in memory")
				}
			}
			return doctor.Report{}, nil
		},
		RunnerReady: func(context.Context) bool { return false },
		SaveConfig:  config.SaveAtomic,
		Now:         func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
	}
}
