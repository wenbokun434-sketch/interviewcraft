// Package config loads non-secret local runtime configuration. API keys are
// referenced by environment variable name and are never persisted here.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

const (
	CurrentVersion      = 1
	DefaultDatabaseName = "interviewcraft.db"
	ConfigFileName      = "config.json"

	ProviderOpenAICompatible = "openai-compatible"
	ProviderOllama           = "ollama"

	RunnerDisabled   = "disabled"
	RunnerDocker     = "docker"
	RunnerProtocol   = "interviewcraft-runner-response-v1"
	RunnerRepository = "ghcr.io/wenbokun434-sketch/interviewcraft-runner"

	AudioBrowser = "browser"
)

const (
	envDataDir       = "INTERVIEWCRAFT_DATA_DIR"
	envLLMProvider   = "INTERVIEWCRAFT_LLM_PROVIDER"
	envLLMEndpoint   = "INTERVIEWCRAFT_LLM_ENDPOINT"
	envLLMModel      = "INTERVIEWCRAFT_LLM_MODEL"
	envLLMAPIKey     = "INTERVIEWCRAFT_LLM_API_KEY_ENV"
	envRunnerMode    = "RUNNER_MODE"
	envAudioProvider = "AUDIO_PROVIDER"
)

// LLM contains only non-secret Provider settings.
type LLM struct {
	Provider  string `json:"provider"`
	Endpoint  string `json:"endpoint"`
	Model     string `json:"model"`
	APIKeyEnv string `json:"api_key_env"`
}

// Runner contains only verified, non-secret image metadata. Image is the
// canonical repository and Digest is always an immutable sha256 reference.
type Runner struct {
	Image        string `json:"image"`
	Digest       string `json:"digest"`
	Version      string `json:"version"`
	Protocol     string `json:"protocol"`
	Architecture string `json:"architecture"`
}

// Reference returns the immutable image reference accepted by Docker/Cosign.
func (runner Runner) Reference() string {
	if strings.TrimSpace(runner.Image) == "" || strings.TrimSpace(runner.Digest) == "" {
		return ""
	}
	return runner.Image + "@" + runner.Digest
}

func (runner Runner) empty() bool {
	return runner == (Runner{})
}

// Runtime is the complete Lite runtime configuration.
type Runtime struct {
	Version       int    `json:"version"`
	DataDir       string `json:"data_dir"`
	DatabaseName  string `json:"database_name"`
	LLM           LLM    `json:"llm"`
	RunnerMode    string `json:"runner_mode"`
	Runner        Runner `json:"runner"`
	AudioProvider string `json:"audio_provider"`
}

var (
	runnerDigestPattern  = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	runnerVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
)

// Metadata describes where configuration was resolved.
type Metadata struct {
	Path   string
	Exists bool
}

// Source makes home-directory and environment resolution deterministic in
// tests without changing process-global state.
type Source struct {
	UserHomeDir func() (string, error)
	LookupEnv   func(string) (string, bool)
}

// OSSource reads the current operating-system environment.
func OSSource() Source {
	return Source{
		UserHomeDir: os.UserHomeDir,
		LookupEnv:   os.LookupEnv,
	}
}

// LoadOS resolves configuration from the operating-system environment.
func LoadOS() (Runtime, Metadata, error) {
	return Load(OSSource())
}

// LoadAt reads one data directory without applying process environment
// overrides. It is used by setup to preserve fields the user did not
// explicitly change when an existing workspace is configured again.
func LoadAt(dataDir string) (Runtime, Metadata, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(dataDir))
	if err != nil || strings.TrimSpace(dataDir) == "" {
		return Runtime{}, Metadata{}, validationError(
			"resolve setup data directory", dataDir,
			"数据目录无效。", "提供有效的数据目录后重试。", err,
		)
	}
	return Load(Source{
		UserHomeDir: func() (string, error) { return filepath.Dir(absolute), nil },
		LookupEnv: func(name string) (string, bool) {
			if name == envDataDir {
				return absolute, true
			}
			return "", false
		},
	})
}

// Load applies defaults, an optional strict JSON file, then environment
// overrides. Missing configuration returns valid defaults with Exists=false.
func Load(source Source) (Runtime, Metadata, error) {
	if source.UserHomeDir == nil || source.LookupEnv == nil {
		return Runtime{}, Metadata{}, validationError(
			"resolve configuration source",
			"",
			"配置来源不完整。",
			"提供主目录和环境变量读取器后重试。",
			nil,
		)
	}

	home, err := source.UserHomeDir()
	if err != nil {
		return Runtime{}, Metadata{}, configError(
			"resolve user home",
			"",
			"无法确定本地配置目录。",
			"设置 INTERVIEWCRAFT_DATA_DIR 后重试。",
			err,
		)
	}
	dataDir := filepath.Join(home, ".interviewcraft")
	if value, ok := nonBlankEnvironment(source, envDataDir); ok {
		dataDir = value
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return Runtime{}, Metadata{}, configError(
			"resolve data directory",
			dataDir,
			"无法解析本地数据目录。",
			"改用有效的 INTERVIEWCRAFT_DATA_DIR 后重试。",
			err,
		)
	}

	runtime := defaults(dataDir)
	metadata := Metadata{Path: filepath.Join(dataDir, ConfigFileName)}
	payload, err := os.ReadFile(metadata.Path)
	switch {
	case err == nil:
		metadata.Exists = true
		if err := decodeStrict(payload, &runtime); err != nil {
			return Runtime{}, metadata, validationError(
				"decode runtime configuration",
				metadata.Path,
				"本地配置文件格式无效。",
				"修正标记字段或重新运行 init。",
				err,
			)
		}
	case errors.Is(err, os.ErrNotExist):
		metadata.Exists = false
	default:
		return Runtime{}, metadata, configError(
			"read runtime configuration",
			metadata.Path,
			"无法读取本地配置文件。",
			"检查文件权限后重试。",
			err,
		)
	}

	applyEnvironment(&runtime, source)
	runtime.DataDir, err = filepath.Abs(runtime.DataDir)
	if err != nil {
		return Runtime{}, metadata, configError(
			"resolve configured data directory",
			runtime.DataDir,
			"配置中的数据目录无效。",
			"修正 data_dir 后重试。",
			err,
		)
	}
	if err := runtime.Validate(); err != nil {
		return Runtime{}, metadata, err
	}
	return runtime, metadata, nil
}

// WriteInitial atomically creates a configuration file without overwriting an
// existing user's settings.
func WriteInitial(path string, runtime Runtime) (bool, error) {
	if err := runtime.Validate(); err != nil {
		return false, err
	}
	if strings.TrimSpace(path) == "" {
		return false, validationError(
			"write runtime configuration",
			path,
			"配置文件路径不能为空。",
			"提供明确路径后重试。",
			nil,
		)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return false, configError(
			"create configuration directory",
			filepath.Dir(path),
			"无法创建配置目录。",
			"检查路径和写入权限后重试。",
			err,
		)
	}

	payload, err := json.MarshalIndent(runtime, "", "  ")
	if err != nil {
		return false, configError(
			"encode runtime configuration",
			path,
			"无法编码本地配置。",
			"修正配置字段后重试。",
			err,
		)
	}
	payload = append(payload, '\n')

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, configError(
			"create runtime configuration",
			path,
			"无法创建本地配置文件。",
			"检查路径和写入权限后重试。",
			err,
		)
	}
	if _, err := file.Write(payload); err != nil {
		_ = file.Close()
		return false, configError(
			"write runtime configuration",
			path,
			"无法写入本地配置文件。",
			"检查磁盘空间和写入权限后重试。",
			err,
		)
	}
	if err := file.Close(); err != nil {
		return false, configError(
			"close runtime configuration",
			path,
			"无法确认本地配置已保存。",
			"检查磁盘后重试 init。",
			err,
		)
	}
	return true, nil
}

// Validate enforces Lite runtime modes without requiring optional services.
func (runtime Runtime) Validate() error {
	var issues []string
	if runtime.Version != CurrentVersion {
		issues = append(issues, fmt.Sprintf("version must be %d", CurrentVersion))
	}
	if strings.TrimSpace(runtime.DataDir) == "" {
		issues = append(issues, "data_dir must not be blank")
	}
	if strings.TrimSpace(runtime.DatabaseName) == "" ||
		filepath.Base(runtime.DatabaseName) != runtime.DatabaseName ||
		runtime.DatabaseName == "." {
		issues = append(issues, "database_name must be a file name")
	}

	switch runtime.LLM.Provider {
	case "":
		// An empty Provider is a valid pre-configuration state. Doctor reports
		// it as a blocking check before a session can start.
	case ProviderOpenAICompatible, ProviderOllama:
		if strings.TrimSpace(runtime.LLM.Endpoint) == "" {
			issues = append(issues, "llm.endpoint must not be blank")
		} else if err := validateHTTPEndpoint(runtime.LLM.Endpoint); err != nil {
			issues = append(issues, "llm.endpoint must be an http(s) URL")
		}
		if strings.TrimSpace(runtime.LLM.Model) == "" {
			issues = append(issues, "llm.model must not be blank")
		}
		if runtime.LLM.Provider == ProviderOpenAICompatible &&
			strings.TrimSpace(runtime.LLM.APIKeyEnv) == "" {
			issues = append(issues, "llm.api_key_env must not be blank")
		}
	default:
		issues = append(issues, "llm.provider must be openai-compatible or ollama")
	}

	if runtime.RunnerMode != RunnerDisabled && runtime.RunnerMode != RunnerDocker {
		issues = append(issues, "runner_mode must be disabled or docker")
	}
	if runtime.RunnerMode == RunnerDocker || !runtime.Runner.empty() {
		if runtime.Runner.Image != RunnerRepository {
			issues = append(issues, "runner.image must be the official repository")
		}
		if !runnerDigestPattern.MatchString(runtime.Runner.Digest) {
			issues = append(issues, "runner.digest must be a lowercase sha256 digest")
		}
		if !runnerVersionPattern.MatchString(runtime.Runner.Version) {
			issues = append(issues, "runner.version must be a semantic version")
		}
		if runtime.Runner.Protocol != RunnerProtocol {
			issues = append(issues, "runner.protocol is incompatible")
		}
		if runtime.Runner.Architecture != "amd64" && runtime.Runner.Architecture != "arm64" {
			issues = append(issues, "runner.architecture must be amd64 or arm64")
		}
	}
	if runtime.AudioProvider != AudioBrowser {
		issues = append(issues, "audio_provider must be browser for Lite MVP")
	}
	if len(issues) != 0 {
		return validationError(
			"validate runtime configuration",
			runtime.DataDir,
			"本地运行配置包含无效字段。",
			"修正这些字段后重试："+strings.Join(issues, "; "),
			errors.New(strings.Join(issues, "; ")),
		)
	}
	return nil
}

func defaults(dataDir string) Runtime {
	return Runtime{
		Version:      CurrentVersion,
		DataDir:      dataDir,
		DatabaseName: DefaultDatabaseName,
		LLM: LLM{
			APIKeyEnv: "OPENAI_API_KEY",
		},
		RunnerMode:    RunnerDisabled,
		AudioProvider: AudioBrowser,
	}
}

func applyEnvironment(runtime *Runtime, source Source) {
	overrides := []struct {
		name   string
		target *string
	}{
		{envDataDir, &runtime.DataDir},
		{envLLMProvider, &runtime.LLM.Provider},
		{envLLMEndpoint, &runtime.LLM.Endpoint},
		{envLLMModel, &runtime.LLM.Model},
		{envLLMAPIKey, &runtime.LLM.APIKeyEnv},
		{envRunnerMode, &runtime.RunnerMode},
		{envAudioProvider, &runtime.AudioProvider},
	}
	for _, override := range overrides {
		if value, ok := nonBlankEnvironment(source, override.name); ok {
			*override.target = value
		}
	}
}

func nonBlankEnvironment(source Source, name string) (string, bool) {
	value, ok := source.LookupEnv(name)
	value = strings.TrimSpace(value)
	return value, ok && value != ""
}

func decodeStrict(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func validateHTTPEndpoint(value string) error {
	parsed, err := url.ParseRequestURI(value)
	if err != nil {
		return err
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return errors.New("endpoint scheme must be http or https")
	}
	if parsed.Host == "" {
		return errors.New("endpoint host is missing")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("endpoint must not contain credentials, query, or fragment")
	}
	return nil
}

func configError(
	operation string,
	path string,
	message string,
	recovery string,
	cause error,
) *domainerr.Error {
	if path != "" {
		message += " 路径：" + path + "。"
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"local configuration",
		message,
		recovery,
		false,
		cause,
	)
}

func validationError(
	operation string,
	path string,
	message string,
	recovery string,
	cause error,
) *domainerr.Error {
	if path != "" {
		message += " 路径：" + path + "。"
	}
	return domainerr.Wrap(
		domainerr.CodeValidation,
		operation,
		"local configuration",
		message,
		recovery,
		false,
		cause,
	)
}
