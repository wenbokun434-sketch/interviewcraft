package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestLoadMissingConfigReturnsLiteDefaults(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtime, metadata, err := Load(testSource(home, nil))

	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if metadata.Exists {
		t.Fatal("missing configuration reported as existing")
	}
	wantDir := filepath.Join(home, ".interviewcraft")
	if runtime.DataDir != wantDir ||
		runtime.DatabaseName != DefaultDatabaseName ||
		runtime.RunnerMode != RunnerDisabled ||
		runtime.AudioProvider != AudioBrowser ||
		runtime.LLM.Provider != "" ||
		runtime.LLM.APIKeyEnv != "OPENAI_API_KEY" {
		t.Fatalf("Lite defaults = %#v", runtime)
	}
	if metadata.Path != filepath.Join(wantDir, ConfigFileName) {
		t.Fatalf("config path = %q", metadata.Path)
	}
}

func TestWriteInitialIsIdempotentAndStoresNoSecret(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	runtime, metadata, err := Load(testSource(home, nil))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	created, err := WriteInitial(metadata.Path, runtime)
	if err != nil || !created {
		t.Fatalf("WriteInitial first: created=%v err=%v", created, err)
	}
	created, err = WriteInitial(metadata.Path, runtime)
	if err != nil || created {
		t.Fatalf("WriteInitial second: created=%v err=%v", created, err)
	}

	payload, err := os.ReadFile(metadata.Path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if strings.Contains(string(payload), "actual-secret-value") ||
		!strings.Contains(string(payload), `"api_key_env": "OPENAI_API_KEY"`) {
		t.Fatalf("persisted configuration has unexpected secret representation: %s", payload)
	}

	loaded, loadedMetadata, err := Load(testSource(home, nil))
	if err != nil {
		t.Fatalf("Load existing: %v", err)
	}
	if !loadedMetadata.Exists || !reflect.DeepEqual(loaded, runtime) {
		t.Fatalf("loaded=%#v metadata=%#v, want %#v", loaded, loadedMetadata, runtime)
	}
}

func TestSaveAtomicReplacesConfigurationWithoutSecret(t *testing.T) {
	home := t.TempDir()
	runtime := defaults(filepath.Join(home, "data"))
	path := filepath.Join(home, "config.json")
	if err := SaveAtomic(path, runtime); err != nil {
		t.Fatalf("SaveAtomic first: %v", err)
	}
	runtime.LLM = LLM{
		Provider: ProviderOllama, Endpoint: "http://localhost:11434",
		Model: "local-model", APIKeyEnv: "OPENAI_API_KEY",
	}
	if err := SaveAtomic(path, runtime); err != nil {
		t.Fatalf("SaveAtomic replace: %v", err)
	}
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(payload), "local-model") ||
		strings.Contains(string(payload), "actual-secret-value") {
		t.Fatalf("payload=%s", payload)
	}
	if matches, err := filepath.Glob(filepath.Join(home, ".config-*.tmp")); err != nil || len(matches) != 0 {
		t.Fatalf("temporary files=%v err=%v", matches, err)
	}
}

func TestLoadAtIgnoresProcessOverridesAndReadsRequestedDirectory(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "workspace")
	runtime := defaults(dataDir)
	runtime.LLM = LLM{
		Provider: ProviderOllama, Endpoint: "http://127.0.0.1:11434",
		Model: "local-preserved", APIKeyEnv: "OPENAI_API_KEY",
	}
	if err := SaveAtomic(filepath.Join(dataDir, ConfigFileName), runtime); err != nil {
		t.Fatalf("SaveAtomic: %v", err)
	}
	t.Setenv(envLLMProvider, ProviderOpenAICompatible)
	loaded, metadata, err := LoadAt(dataDir)
	if err != nil {
		t.Fatalf("LoadAt: %v", err)
	}
	if !metadata.Exists || loaded.LLM.Provider != ProviderOllama || loaded.LLM.Model != "local-preserved" {
		t.Fatalf("loaded=%#v metadata=%#v", loaded, metadata)
	}
}

func TestEnvironmentOverridesStrictFile(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dataDir := filepath.Join(home, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	fileRuntime := defaults(dataDir)
	fileRuntime.LLM = LLM{
		Provider:  ProviderOllama,
		Endpoint:  "http://localhost:11434",
		Model:     "file-model",
		APIKeyEnv: "OPENAI_API_KEY",
	}
	fileRuntime.Runner = validRunnerMetadata()
	payload, err := json.Marshal(fileRuntime)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, ConfigFileName), payload, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	environment := map[string]string{
		envDataDir:     dataDir,
		envLLMProvider: ProviderOpenAICompatible,
		envLLMEndpoint: "https://example.test/v1",
		envLLMModel:    "environment-model",
		envLLMAPIKey:   "INTERVIEWCRAFT_TEST_KEY",
		envRunnerMode:  RunnerDocker,
	}
	runtime, metadata, err := Load(testSource(home, environment))

	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !metadata.Exists {
		t.Fatal("existing configuration reported missing")
	}
	if runtime.LLM.Provider != ProviderOpenAICompatible ||
		runtime.LLM.Endpoint != "https://example.test/v1" ||
		runtime.LLM.Model != "environment-model" ||
		runtime.LLM.APIKeyEnv != "INTERVIEWCRAFT_TEST_KEY" ||
		runtime.RunnerMode != RunnerDocker {
		t.Fatalf("environment overrides = %#v", runtime)
	}
}

func TestLoadRejectsUnknownFileField(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dataDir := filepath.Join(home, ".interviewcraft")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dataDir, ConfigFileName)
	payload := `{
		"version":1,
		"data_dir":"` + escapedJSONPath(dataDir) + `",
		"database_name":"interviewcraft.db",
		"llm":{"provider":"","endpoint":"","model":"","api_key_env":"OPENAI_API_KEY"},
		"runner_mode":"disabled",
		"audio_provider":"browser",
		"secret":"must-not-be-accepted"
	}`
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	_, metadata, err := Load(testSource(home, nil))

	if err == nil {
		t.Fatal("configuration with unknown field unexpectedly loaded")
	}
	if !metadata.Exists || !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("metadata=%#v err=%v, want existing validation error", metadata, err)
	}
	var typed *domainerr.Error
	if !errors.As(err, &typed) || typed.RecoveryAction == "" ||
		!strings.Contains(typed.Message, path) {
		t.Fatalf("configuration error is not actionable: %#v", typed)
	}
}

func TestValidateRejectsUnsupportedOptionalModes(t *testing.T) {
	t.Parallel()

	runtime := defaults(t.TempDir())
	runtime.RunnerMode = "required"
	runtime.AudioProvider = "whisper"

	err := runtime.Validate()

	if err == nil || !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("Validate error = %v, want validation code", err)
	}
}

func TestValidateRequiresCompleteVerifiedRunnerMetadata(t *testing.T) {
	runtime := defaults(t.TempDir())
	runtime.RunnerMode = RunnerDocker
	if err := runtime.Validate(); err == nil {
		t.Fatal("docker mode was accepted without verified metadata")
	}
	runtime.Runner = validRunnerMetadata()
	if err := runtime.Validate(); err != nil {
		t.Fatalf("valid released Runner metadata: %v", err)
	}
	mutations := []func(*Runner){
		func(value *Runner) { value.Image = "ghcr.io/attacker/runner" },
		func(value *Runner) { value.Digest = "sha256:" + strings.Repeat("A", 64) },
		func(value *Runner) { value.Version = "dev" },
		func(value *Runner) { value.Protocol = "runner-v0" },
		func(value *Runner) { value.Architecture = "386" },
	}
	for _, mutate := range mutations {
		candidate := runtime
		mutate(&candidate.Runner)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("invalid Runner metadata was accepted: %#v", candidate.Runner)
		}
	}
}

func TestValidateRejectsCredentialsInProviderEndpoint(t *testing.T) {
	t.Parallel()

	runtime := defaults(t.TempDir())
	runtime.LLM = LLM{
		Provider:  ProviderOpenAICompatible,
		Endpoint:  "https://user:secret@example.test/v1?api_key=secret",
		Model:     "test-model",
		APIKeyEnv: "OPENAI_API_KEY",
	}

	err := runtime.Validate()

	if err == nil || !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("Validate error = %v, want validation code", err)
	}
}

func testSource(home string, environment map[string]string) Source {
	return Source{
		UserHomeDir: func() (string, error) {
			return home, nil
		},
		LookupEnv: func(name string) (string, bool) {
			value, ok := environment[name]
			return value, ok
		},
	}
}

func escapedJSONPath(value string) string {
	payload, _ := json.Marshal(value)
	return strings.Trim(string(payload), `"`)
}

func validRunnerMetadata() Runner {
	return Runner{
		Image: RunnerRepository, Digest: "sha256:" + strings.Repeat("a", 64),
		Version: "1.2.3", Protocol: RunnerProtocol, Architecture: "amd64",
	}
}
