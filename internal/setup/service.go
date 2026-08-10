// Package setup implements the resumable, idempotent deployment wizard.
package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	runneradapter "github.com/interviewcraft/interviewcraft/internal/adapters/runner"
	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/credentials"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/doctor"
	"github.com/interviewcraft/interviewcraft/internal/version"
)

const StateFileName = "setup-state.json"

// Profile selects the local deployment shape.
type Profile string

const (
	ProfileLite         Profile = "lite"
	ProfilePrivateLocal Profile = "private-local"
	ProfileFull         Profile = "full"
)

// Stage is a recoverable setup checkpoint.
type Stage string

const (
	StagePreflight  Stage = "preflight"
	StageProfile    Stage = "profile"
	StageProvider   Stage = "provider"
	StageCredential Stage = "credential"
	StageInitialize Stage = "initialize"
	StageRunner     Stage = "runner"
	StageDiagnose   Stage = "diagnose"
	StageComplete   Stage = "complete"
)

var stages = []Stage{
	StagePreflight, StageProfile, StageProvider, StageCredential,
	StageInitialize, StageRunner, StageDiagnose, StageComplete,
}

// Progress is safe to print and never contains credential values.
type Progress struct {
	Stage   Stage
	Current int
	Total   int
	Message string
}

// Observer receives deterministic setup lifecycle states.
type Observer func(async.State[Progress])

// Request is the complete in-memory setup input. APIKey is never serialized.
type Request struct {
	Profile        Profile
	DataDir        string
	Provider       string
	Endpoint       string
	Model          string
	APIKeyEnv      string
	APIKey         string
	NonInteractive bool
	Restart        bool
}

// Checkpoint contains only non-sensitive setup state.
type Checkpoint struct {
	Version   int            `json:"version"`
	Profile   Profile        `json:"profile"`
	Runtime   config.Runtime `json:"runtime"`
	Stage     Stage          `json:"stage"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// Result reports committed non-secret configuration.
type Result struct {
	Runtime     config.Runtime
	ConfigPath  string
	Resumed     bool
	RunnerReady bool
}

type databaseCloser interface{ Close() error }

// Dependencies injects fault and platform boundaries for deterministic tests.
type Dependencies struct {
	Credentials     credentials.Store
	LookupEnv       func(string) (string, bool)
	OpenDatabase    func(context.Context, db.Config) (databaseCloser, error)
	Diagnose        func(context.Context, config.Runtime, func(string) (string, bool)) (doctor.Report, error)
	ProvisionRunner func(context.Context, runneradapter.ProvisionRequest, runneradapter.ProvisionObserver) (runneradapter.Provisioned, error)
	SaveConfig      func(string, config.Runtime) error
	Now             func() time.Time
}

// DefaultDependencies uses the operating-system keyring and local probes.
func DefaultDependencies() Dependencies {
	return Dependencies{
		Credentials: credentials.SystemStore{},
		LookupEnv:   os.LookupEnv,
		OpenDatabase: func(ctx context.Context, value db.Config) (databaseCloser, error) {
			return db.Open(ctx, value, nil)
		},
		Diagnose: func(ctx context.Context, runtime config.Runtime, resolve func(string) (string, bool)) (doctor.Report, error) {
			options := doctor.DefaultOptions()
			options.Model = doctor.HTTPModelProbe{LookupEnv: resolve}
			if runtime.RunnerMode == config.RunnerDocker {
				probe, err := runneradapter.New(runneradapter.ConfigForRuntime(runtime), runneradapter.Options{})
				if err != nil {
					return doctor.Report{}, err
				}
				options.Runner = probe
			}
			return doctor.Run(ctx, runtime, options)
		},
		ProvisionRunner: func(ctx context.Context, request runneradapter.ProvisionRequest, observer runneradapter.ProvisionObserver) (runneradapter.Provisioned, error) {
			return runneradapter.Provision(ctx, request, runneradapter.ProvisionOptions{}, observer)
		},
		SaveConfig: config.SaveAtomic,
		Now:        time.Now,
	}
}

// Run validates in memory, initializes SQLite, executes doctor, then commits
// credential and configuration changes as the final operation.
func Run(ctx context.Context, request Request, dependencies Dependencies, observer Observer) (Result, error) {
	dependencies = fillDependencies(dependencies)
	if request.Profile == "" {
		request.Profile = ProfileLite
	}
	candidate, err := runtimeFromRequest(request)
	if err != nil {
		return Result{}, fail(observer, setupFailure("validate setup request", err))
	}
	statePath := filepath.Join(candidate.DataDir, StateFileName)
	if request.Restart {
		if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Result{}, fail(observer, setupFailure("restart setup", err))
		}
	}
	checkpoint, found, err := loadCheckpoint(statePath)
	if err != nil {
		return Result{}, fail(observer, setupFailure("load setup checkpoint", err))
	}
	resumed := found && !request.Restart
	start := 0
	if resumed {
		if !checkpointMatches(checkpoint, request.Profile, candidate) {
			return Result{}, fail(observer, domainerr.New(
				domainerr.CodeInvalidState,
				"resume setup",
				"现有 setup 检查点与本次参数不一致。",
				"使用原参数继续，或添加 --restart 明确重新开始。",
				false,
			))
		}
		start = stageIndex(checkpoint.Stage)
		if start > stageIndex(StageRunner) && checkpoint.Runtime.RunnerMode == config.RunnerDocker {
			if validateErr := checkpoint.Runtime.Validate(); validateErr != nil {
				return Result{}, fail(observer, setupFailure("validate resumed Runner metadata", validateErr))
			}
			candidate.RunnerMode = checkpoint.Runtime.RunnerMode
			candidate.Runner = checkpoint.Runtime.Runner
		}
	}
	if err := os.MkdirAll(candidate.DataDir, 0o700); err != nil {
		return Result{}, fail(observer, setupFailure("create setup data directory", err))
	}

	account, err := credentials.Account(candidate.DataDir)
	if err != nil {
		return Result{}, fail(observer, setupFailure("resolve credential account", err))
	}
	credential, err := prepareCredential(request, candidate, account, dependencies)
	if err != nil {
		return Result{}, fail(observer, err)
	}
	notify(observer, async.NewPending[Progress]())
	for index := start; index < len(stages); index++ {
		if err := ctx.Err(); err != nil {
			return Result{}, fail(observer, domainerr.Wrap(
				domainerr.CodeOperationCancelled,
				"run setup",
				"setup",
				"setup 已取消，安全检查点和已确认的非敏感输入已保留。",
				"使用相同参数重新运行 setup 继续。",
				true,
				err,
			))
		}
		stage := stages[index]
		checkpoint = Checkpoint{
			Version: 1, Profile: request.Profile, Runtime: candidate,
			Stage: stage, UpdatedAt: dependencies.Now().UTC(),
		}
		if err := saveCheckpoint(statePath, checkpoint); err != nil {
			return Result{}, fail(observer, setupFailure("save setup checkpoint", err))
		}
		progress := Progress{Stage: stage, Current: index + 1, Total: len(stages), Message: stageMessage(stage)}
		notify(observer, async.NewStreaming(&progress))
		switch stage {
		case StagePreflight, StageProfile, StageProvider, StageCredential:
			// Validation and credential availability were completed before work.
		case StageInitialize:
			store, openErr := dependencies.OpenDatabase(ctx, db.Config{
				DataDir: candidate.DataDir, DatabaseName: candidate.DatabaseName,
			})
			if openErr != nil {
				if errors.Is(openErr, context.Canceled) {
					return Result{}, fail(observer, setupCancelled(openErr))
				}
				return Result{}, fail(observer, setupFailure("initialize SQLite", openErr))
			}
			if closeErr := store.Close(); closeErr != nil {
				return Result{}, fail(observer, setupFailure("close initialized SQLite", closeErr))
			}
		case StageRunner:
			if request.Profile != ProfileFull {
				break
			}
			provisioned, provisionErr := dependencies.ProvisionRunner(ctx, runneradapter.ProvisionRequest{Version: version.Current().Version, DataDir: candidate.DataDir}, func(state async.State[runneradapter.ProvisionProgress]) {
				if state.Phase == async.Streaming && state.Value != nil {
					value := Progress{Stage: StageRunner, Current: index + 1, Total: len(stages), Message: "runner: " + string(state.Value.Stage)}
					notify(observer, async.NewStreaming(&value))
				}
			})
			if provisionErr != nil {
				if errors.Is(provisionErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
					return Result{}, fail(observer, setupCancelled(provisionErr))
				}
				return Result{}, fail(observer, setupFailure("provision Full Practice Runner", provisionErr))
			}
			candidate.Runner = config.Runner{
				Image: provisioned.Image, Digest: provisioned.Digest, Version: provisioned.Version,
				Protocol: provisioned.Protocol, Architecture: provisioned.Architecture,
			}
			candidate.RunnerMode = config.RunnerDocker
		case StageDiagnose:
			_, diagnoseErr := dependencies.Diagnose(ctx, candidate, credential.resolve)
			if diagnoseErr != nil {
				if errors.Is(diagnoseErr, context.Canceled) {
					return Result{}, fail(observer, setupCancelled(diagnoseErr))
				}
				return Result{}, fail(observer, setupFailure("diagnose setup runtime", diagnoseErr))
			}
		case StageComplete:
			if err := credential.commit(); err != nil {
				return Result{}, fail(observer, err)
			}
			if err := dependencies.SaveConfig(filepath.Join(candidate.DataDir, config.ConfigFileName), candidate); err != nil {
				credential.rollback()
				return Result{}, fail(observer, setupFailure("commit setup configuration", err))
			}
			if err := os.Remove(statePath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return Result{}, fail(observer, setupFailure("remove completed setup checkpoint", err))
			}
		}
	}
	completed := Progress{Stage: StageComplete, Current: len(stages), Total: len(stages), Message: "setup complete"}
	notify(observer, async.NewSucceeded(completed))
	return Result{
		Runtime: candidate, ConfigPath: filepath.Join(candidate.DataDir, config.ConfigFileName),
		Resumed: resumed, RunnerReady: candidate.RunnerMode == config.RunnerDocker,
	}, nil
}

type preparedCredential struct {
	resolve       func(string) (string, bool)
	store         credentials.Store
	account       string
	secret        string
	write         bool
	previous      string
	previousFound bool
}

func prepareCredential(request Request, runtime config.Runtime, account string, dependencies Dependencies) (*preparedCredential, error) {
	prepared := &preparedCredential{store: dependencies.Credentials, account: account}
	if runtime.LLM.Provider != config.ProviderOpenAICompatible {
		prepared.resolve = func(string) (string, bool) { return "", false }
		return prepared, nil
	}
	environmentSecret, environmentFound := dependencies.LookupEnv(runtime.LLM.APIKeyEnv)
	environmentFound = environmentFound && strings.TrimSpace(environmentSecret) != ""
	if environmentFound {
		prepared.secret = environmentSecret
		prepared.resolve = func(name string) (string, bool) {
			if name == runtime.LLM.APIKeyEnv {
				return environmentSecret, true
			}
			return dependencies.LookupEnv(name)
		}
		return prepared, nil
	}
	if strings.TrimSpace(request.APIKey) != "" {
		previous, err := dependencies.Credentials.Get(credentials.Service, account)
		switch {
		case err == nil:
			prepared.previous, prepared.previousFound = previous, true
		case errors.Is(err, credentials.ErrNotFound):
		default:
			return nil, credentialUnavailable(err)
		}
		prepared.secret = request.APIKey
		prepared.write = true
		prepared.resolve = func(name string) (string, bool) {
			return request.APIKey, name == runtime.LLM.APIKeyEnv
		}
		return prepared, nil
	}
	secret, err := dependencies.Credentials.Get(credentials.Service, account)
	if err != nil {
		if errors.Is(err, credentials.ErrNotFound) {
			return nil, domainerr.New(
				domainerr.CodeValidation,
				"resolve setup credential",
				"OpenAI-compatible Provider 缺少 API Key。",
				"设置指定环境变量，或通过 --api-key-stdin 安全输入。",
				false,
			)
		}
		return nil, credentialUnavailable(err)
	}
	prepared.secret = secret
	prepared.resolve = func(name string) (string, bool) {
		return secret, name == runtime.LLM.APIKeyEnv && strings.TrimSpace(secret) != ""
	}
	return prepared, nil
}

func (credential *preparedCredential) commit() error {
	if credential == nil || !credential.write {
		return nil
	}
	if err := credential.store.Set(credentials.Service, credential.account, credential.secret); err != nil {
		return credentialUnavailable(err)
	}
	return nil
}

func (credential *preparedCredential) rollback() {
	if credential == nil || !credential.write {
		return
	}
	if credential.previousFound {
		_ = credential.store.Set(credentials.Service, credential.account, credential.previous)
		return
	}
	_ = credential.store.Delete(credentials.Service, credential.account)
}

func credentialUnavailable(cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable,
		"access system credential manager",
		"system keyring",
		"系统凭据库不可用，API Key 未写入任何明文文件。",
		"改用已设置的 API Key 环境变量后重试。",
		true,
		cause,
	)
}

func runtimeFromRequest(request Request) (config.Runtime, error) {
	profile := request.Profile
	if profile == "" {
		profile = ProfileLite
	}
	if profile != ProfileLite && profile != ProfilePrivateLocal && profile != ProfileFull {
		return config.Runtime{}, fmt.Errorf("unsupported profile %q", profile)
	}
	dataDir, err := filepath.Abs(strings.TrimSpace(request.DataDir))
	if err != nil || strings.TrimSpace(request.DataDir) == "" {
		return config.Runtime{}, errors.New("data directory is required")
	}
	provider := strings.TrimSpace(request.Provider)
	if provider == "" {
		if profile == ProfilePrivateLocal {
			provider = config.ProviderOllama
		} else {
			provider = config.ProviderOpenAICompatible
		}
	}
	endpoint := strings.TrimSpace(request.Endpoint)
	model := strings.TrimSpace(request.Model)
	if provider == config.ProviderOllama {
		if endpoint == "" {
			endpoint = "http://127.0.0.1:11434"
		}
		if model == "" {
			model = "llama3.2"
		}
	} else {
		if endpoint == "" {
			endpoint = "https://api.openai.com/v1"
		}
		if model == "" {
			model = "gpt-4o-mini"
		}
	}
	apiKeyEnv := strings.TrimSpace(request.APIKeyEnv)
	if apiKeyEnv == "" {
		apiKeyEnv = "OPENAI_API_KEY"
	}
	runtime := config.Runtime{
		Version: config.CurrentVersion, DataDir: dataDir,
		DatabaseName: config.DefaultDatabaseName,
		LLM:          config.LLM{Provider: provider, Endpoint: endpoint, Model: model, APIKeyEnv: apiKeyEnv},
		RunnerMode:   config.RunnerDisabled, AudioProvider: config.AudioBrowser,
	}
	if err := runtime.Validate(); err != nil {
		return config.Runtime{}, err
	}
	return runtime, nil
}

func fillDependencies(value Dependencies) Dependencies {
	defaults := DefaultDependencies()
	if value.Credentials == nil {
		value.Credentials = defaults.Credentials
	}
	if value.LookupEnv == nil {
		value.LookupEnv = defaults.LookupEnv
	}
	if value.OpenDatabase == nil {
		value.OpenDatabase = defaults.OpenDatabase
	}
	if value.Diagnose == nil {
		value.Diagnose = defaults.Diagnose
	}
	if value.ProvisionRunner == nil {
		value.ProvisionRunner = defaults.ProvisionRunner
	}
	if value.SaveConfig == nil {
		value.SaveConfig = defaults.SaveConfig
	}
	if value.Now == nil {
		value.Now = defaults.Now
	}
	return value
}

func stageIndex(stage Stage) int {
	for index, candidate := range stages {
		if candidate == stage {
			return index
		}
	}
	return -1
}

func checkpointMatches(checkpoint Checkpoint, profile Profile, candidate config.Runtime) bool {
	if checkpoint.Profile != profile {
		return false
	}
	// Runner readiness is a fresh local probe and may legitimately change
	// between attempts; it is never an input that authorizes remote work.
	stored := checkpoint.Runtime
	stored.RunnerMode = config.RunnerDisabled
	stored.Runner = config.Runner{}
	candidate.RunnerMode = config.RunnerDisabled
	candidate.Runner = config.Runner{}
	return reflect.DeepEqual(stored, candidate)
}

func stageMessage(stage Stage) string {
	switch stage {
	case StagePreflight:
		return "checking local prerequisites"
	case StageProfile:
		return "selecting deployment profile"
	case StageProvider:
		return "validating Provider configuration"
	case StageCredential:
		return "resolving secure credential reference"
	case StageInitialize:
		return "initializing SQLite"
	case StageRunner:
		return "provisioning signed Runner image"
	case StageDiagnose:
		return "running doctor"
	case StageComplete:
		return "committing configuration"
	default:
		return string(stage)
	}
}

func saveCheckpoint(path string, checkpoint Checkpoint) error {
	payload, err := json.MarshalIndent(checkpoint, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	return os.WriteFile(path, payload, 0o600)
}

func loadCheckpoint(path string) (Checkpoint, bool, error) {
	payload, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Checkpoint{}, false, nil
	}
	if err != nil {
		return Checkpoint{}, false, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var checkpoint Checkpoint
	if err := decoder.Decode(&checkpoint); err != nil {
		return Checkpoint{}, false, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return Checkpoint{}, false, errors.New("setup checkpoint contains trailing JSON")
		}
		return Checkpoint{}, false, err
	}
	if checkpoint.Version != 1 || stageIndex(checkpoint.Stage) < 0 {
		return Checkpoint{}, false, errors.New("unsupported setup checkpoint")
	}
	return checkpoint, true, nil
}

func setupCancelled(cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeOperationCancelled,
		"run setup",
		"setup",
		"setup 已取消，安全检查点和已确认的非敏感输入已保留。",
		"使用相同参数重新运行 setup 继续。",
		true,
		cause,
	)
}

func setupFailure(operation string, cause error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(cause, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodeDependencyUnavailable, operation, "setup",
		"setup 无法完成，现有配置和数据未被替换。",
		"修复依赖后使用相同参数重试，或添加 --restart 重新开始。",
		true, cause,
	)
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func fail(observer Observer, err error) error {
	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		typed = setupFailure("run setup", err)
	}
	notify(observer, async.NewFailed[Progress](typed))
	return typed
}
