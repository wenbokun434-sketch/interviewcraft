package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

type stubTester struct {
	diagnostic llm.Diagnostic
}

func (tester stubTester) Diagnose(context.Context) llm.Diagnostic {
	return tester.diagnostic
}

func TestNoProviderEmptyStateKeepsHistoryAndBlocksNewScenario(t *testing.T) {
	model := newSettingsModel(t, healthyRuntime(t, false), nil, 80, 24, false, false)

	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, expected := range []string{
		"还没有配置 LLM Provider",
		"新场景已禁用",
		"历史训练和本地报告仍可浏览",
		"Docker Runner 已禁用",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("empty settings missing %q", expected)
		}
	}
	if model.CanStartScenario() {
		t.Fatal("CanStartScenario = true without Provider")
	}
	if !model.HistoryAvailable() {
		t.Fatal("HistoryAvailable = false")
	}
}

func TestConnectionLifecycleAndReadyState(t *testing.T) {
	runtime := healthyRuntime(t, true)
	factory := func(config.LLM) (ConnectionTester, error) {
		return stubTester{diagnostic: llm.Diagnostic{
			Ready:    true,
			Kind:     llm.DiagnosticReady,
			Provider: config.ProviderOllama,
			Model:    "qwen3",
			Message:  "LLM Provider 与模型可用。",
		}}, nil
	}
	model := newSettingsModel(t, runtime, factory, 120, 36, false, false)
	var phases []async.Phase
	model.TestConnection(context.Background(), func(state async.State[llm.Diagnostic]) {
		phases = append(phases, state.Phase)
	})
	if len(phases) != 2 ||
		phases[0] != async.Pending ||
		phases[1] != async.Succeeded {
		t.Fatalf("phases = %#v", phases)
	}
	if !model.CanStartScenario() {
		t.Fatal("CanStartScenario = false after ready diagnostic")
	}
	rendered, err := model.Render()
	if err != nil || !strings.Contains(rendered, "model ready") {
		t.Fatalf("ready render err=%v", err)
	}
}

func TestLoadingStateAndFactoryError(t *testing.T) {
	runtime := healthyRuntime(t, true)
	loading := newSettingsModel(t, runtime, nil, 120, 36, false, true)
	loading.connection = async.NewPending[llm.Diagnostic]()
	rendered, err := loading.Render()
	if err != nil || !strings.Contains(rendered, "正在测试 Provider 连接") {
		t.Fatalf("loading render err=%v", err)
	}

	failure := domainerr.New(
		domainerr.CodeValidation,
		"configure Provider",
		"Provider 配置无效。",
		"修正 endpoint 后重试。",
		false,
	)
	failed := newSettingsModel(
		t,
		runtime,
		func(config.LLM) (ConnectionTester, error) { return nil, failure },
		120,
		36,
		false,
		false,
	)
	failed.TestConnection(context.Background(), nil)
	if failed.ConnectionState().Phase != async.Failed {
		t.Fatalf("phase = %q", failed.ConnectionState().Phase)
	}
	rendered, err = failed.Render()
	if err != nil {
		t.Fatalf("failed Render: %v", err)
	}
	for _, expected := range []string{"Provider 配置无效", "修正 endpoint", "[t] 重试"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("failed settings missing %q", expected)
		}
	}
}

func TestEndpointAuthenticationAndModelDiagnosticsRenderSeparately(t *testing.T) {
	testCases := []struct {
		kind    llm.DiagnosticKind
		message string
		status  string
	}{
		{llm.DiagnosticEndpoint, "Ollama endpoint 无响应。", "endpoint 错误"},
		{llm.DiagnosticAuthentication, "OpenAI-compatible 认证失败。", "认证错误"},
		{llm.DiagnosticModel, "配置的模型不存在。", "模型错误"},
	}
	for _, testCase := range testCases {
		t.Run(string(testCase.kind), func(t *testing.T) {
			runtime := healthyRuntime(t, true)
			factory := func(config.LLM) (ConnectionTester, error) {
				return stubTester{diagnostic: llm.Diagnostic{
					Kind:     testCase.kind,
					Message:  testCase.message,
					Recovery: "修正配置后按 [t] 重试。",
				}}, nil
			}
			model := newSettingsModel(t, runtime, factory, 120, 36, false, false)
			model.TestConnection(context.Background(), nil)
			rendered, err := model.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if !strings.Contains(rendered, testCase.message) ||
				!strings.Contains(rendered, testCase.status) {
				t.Fatalf("render missing %q or %q", testCase.message, testCase.status)
			}
			if model.CanStartScenario() || !model.HistoryAvailable() {
				t.Fatalf(
					"CanStart=%v History=%v",
					model.CanStartScenario(),
					model.HistoryAvailable(),
				)
			}
		})
	}
}

func TestUpdateAndSaveProviderNeverExposeSecret(t *testing.T) {
	runtime := healthyRuntime(t, false)
	var saved config.Runtime
	model, err := New(Options{
		Runtime: runtime,
		SaveConfig: func(value config.Runtime) error {
			saved = value
			return nil
		},
		Width:  120,
		Height: 36,
		Theme:  noColorTheme(t, false, false),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	provider := config.LLM{
		Provider:  config.ProviderOpenAICompatible,
		Endpoint:  "https://example.test/v1",
		Model:     "long-private-model-name",
		APIKeyEnv: "TOP_SECRET_REFERENCE",
	}
	if err := model.UpdateProvider(provider); err != nil {
		t.Fatalf("UpdateProvider: %v", err)
	}
	if err := model.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if saved.LLM != provider {
		t.Fatalf("saved LLM = %#v", saved.LLM)
	}
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if strings.Contains(rendered, "TOP_SECRET_REFERENCE") {
		t.Fatal("settings render exposed secret reference")
	}
	if !strings.Contains(rendered, "值不显示") {
		t.Fatal("settings render does not explain hidden auth")
	}
}

func TestSaveFailurePreservesCurrentProvider(t *testing.T) {
	runtime := healthyRuntime(t, true)
	model, err := New(Options{
		Runtime: runtime,
		SaveConfig: func(config.Runtime) error {
			return errors.New("read-only")
		},
		Width:  120,
		Height: 36,
		Theme:  noColorTheme(t, false, false),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	err = model.Save()
	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("Save error = %#v", err)
	}
	if model.Runtime().LLM != runtime.LLM {
		t.Fatal("Save failure discarded current Provider")
	}
}

func TestSafeEndpointRemovesCredentialsAndQuery(t *testing.T) {
	rendered := safeEndpoint(
		"https://user:secret@example.test/v1?api_key=secret#token",
	)
	if rendered != "https://example.test/v1" {
		t.Fatalf("safeEndpoint = %q", rendered)
	}
}

func TestResponsiveSettingsSnapshotsAndFocus(t *testing.T) {
	testCases := []struct {
		name         string
		width        int
		height       int
		ascii        bool
		reduceMotion bool
		required     []string
	}{
		{
			name: "wide_160x48", width: 160, height: 48,
			required: []string{"导航", "LLM PROVIDER", "LOCAL RUNTIME"},
		},
		{
			name: "split_120x36", width: 120, height: 36,
			required: []string{"LLM PROVIDER", "LOCAL RUNTIME"},
		},
		{
			name: "narrow_80x24_ascii", width: 80, height: 24,
			ascii: true, reduceMotion: true,
			required: []string{"+", "LOCAL RUNTIME", "model"},
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			runtime := healthyRuntime(t, true)
			model := newSettingsModel(
				t,
				runtime,
				nil,
				testCase.width,
				testCase.height,
				testCase.ascii,
				testCase.reduceMotion,
			)
			rendered, err := model.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertGeometry(t, rendered, testCase.width, testCase.height)
			for _, expected := range testCase.required {
				if !strings.Contains(rendered, expected) {
					t.Errorf("snapshot missing %q", expected)
				}
			}
			if strings.Contains(rendered, "TEST_KEY_REFERENCE") {
				t.Fatal("snapshot exposed auth reference")
			}
			model.HandleKey("tab")
			active := model.focus.Active()
			model.HandleKey("?")
			model.Resize(80, 24)
			model.HandleKey("esc")
			if model.focus.Active() != active {
				t.Fatalf("focus restored to %q, want %q", model.focus.Active(), active)
			}
		})
	}
}

func healthyRuntime(t *testing.T, provider bool) config.Runtime {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(dataDir, config.DefaultDatabaseName),
		[]byte("sqlite"),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	runtime := config.Runtime{
		Version:       config.CurrentVersion,
		DataDir:       dataDir,
		DatabaseName:  config.DefaultDatabaseName,
		RunnerMode:    config.RunnerDisabled,
		AudioProvider: config.AudioBrowser,
	}
	if provider {
		runtime.LLM = config.LLM{
			Provider:  config.ProviderOpenAICompatible,
			Endpoint:  "https://example.test/v1",
			Model:     "test-model",
			APIKeyEnv: "TEST_KEY_REFERENCE",
		}
	} else {
		runtime.LLM.APIKeyEnv = "OPENAI_API_KEY"
	}
	return runtime
}

func newSettingsModel(
	t *testing.T,
	runtime config.Runtime,
	factory TesterFactory,
	width int,
	height int,
	ascii bool,
	reduceMotion bool,
) *Model {
	t.Helper()
	model, err := New(Options{
		Runtime:       runtime,
		TesterFactory: factory,
		Width:         width,
		Height:        height,
		Theme:         noColorTheme(t, ascii, reduceMotion),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func noColorTheme(t *testing.T, ascii, reduceMotion bool) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode:         theme.Auto,
		ColorMode:    theme.NoColor,
		UseASCII:     ascii,
		ReduceMotion: reduceMotion,
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	return current
}

func assertGeometry(t *testing.T, rendered string, width, height int) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("rows = %d, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("row %d width=%d, want %d", index, got, width)
		}
	}
}
