package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/config"
	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestOpenAICompatibleStructuredRequestAndResponse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if got := request.Header.Get("Authorization"); got != "Bearer test-secret" {
			t.Errorf("Authorization = %q", got)
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(response, `{
			"choices":[{"message":{"content":"{\"intent\":\"give_hint\",\"help_level\":\"L1\",\"knowledge_tags\":[\"cache\"],\"recommended_action\":\"name one invariant\"}"}}]
		}`)
	}))
	t.Cleanup(server.Close)

	client := newOpenAIClient(t, server.URL+"/v1", 2*time.Second)
	content, err := client.Generate(context.Background(), coachRequest(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := contracts.DecodeCoachResponse(content); err != nil {
		t.Fatalf("DecodeCoachResponse: %v", err)
	}
	if captured["model"] != "test-model" || captured["stream"] != false {
		t.Fatalf("request = %#v", captured)
	}
	responseFormat, ok := captured["response_format"].(map[string]any)
	if !ok || responseFormat["type"] != "json_schema" {
		t.Fatalf("response_format = %#v", captured["response_format"])
	}
	jsonSchema, ok := responseFormat["json_schema"].(map[string]any)
	if !ok || jsonSchema["name"] != "CoachResponse" || jsonSchema["strict"] != true {
		t.Fatalf("json_schema = %#v", responseFormat["json_schema"])
	}
}

func TestOllamaStructuredRequestAndResponse(t *testing.T) {
	var captured map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/api/chat" {
			t.Errorf("path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "" {
			t.Error("Ollama request unexpectedly has Authorization")
		}
		if err := json.NewDecoder(request.Body).Decode(&captured); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_, _ = io.WriteString(response, `{
			"message":{"role":"assistant","content":"{\"intent\":\"give_hint\",\"help_level\":\"L1\",\"knowledge_tags\":[\"cache\"],\"recommended_action\":\"state an invariant\"}"},
			"done":true
		}`)
	}))
	t.Cleanup(server.Close)

	client, err := New(config.LLM{
		Provider: config.ProviderOllama,
		Endpoint: server.URL,
		Model:    "qwen-test",
	}, Options{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	content, err := client.Generate(context.Background(), coachRequest(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if _, err := contracts.DecodeCoachResponse(content); err != nil {
		t.Fatalf("DecodeCoachResponse: %v", err)
	}
	if captured["stream"] != false {
		t.Fatalf("stream = %#v", captured["stream"])
	}
	if _, ok := captured["format"].(map[string]any); !ok {
		t.Fatalf("format = %#v, want JSON Schema object", captured["format"])
	}
	options, ok := captured["options"].(map[string]any)
	if !ok || options["temperature"] != float64(0) {
		t.Fatalf("options = %#v", captured["options"])
	}
}

func TestGenerateStructuredRetriesSchemaOnlyOnce(t *testing.T) {
	schema, ok := contracts.JSONSchema(contracts.SchemaCoachResponse)
	if !ok {
		t.Fatal("CoachResponse schema missing")
	}
	generator := &sequenceGenerator{responses: [][]byte{
		[]byte(`{"intent":"unsupported"}`),
		[]byte(`{
			"intent":"give_hint",
			"help_level":"L1",
			"knowledge_tags":["cache"],
			"recommended_action":"state an invariant"
		}`),
	}}
	request := Request{
		SchemaName: "CoachResponse",
		Schema:     schema,
		Messages:   []Message{{Role: RoleUser, Content: "help"}},
	}

	result, err := GenerateStructured(
		context.Background(),
		generator,
		request,
		contracts.DecodeCoachResponse,
	)
	if err != nil {
		t.Fatalf("GenerateStructured: %v", err)
	}
	if result.HelpLevel != contracts.HelpL1 || len(generator.requests) != 2 {
		t.Fatalf("result=%#v requests=%d", result, len(generator.requests))
	}
	if len(generator.requests[0].Messages) != 1 ||
		len(generator.requests[1].Messages) != 2 ||
		!strings.Contains(
			generator.requests[1].Messages[1].Content,
			"did not match",
		) {
		t.Fatalf("retry messages = %#v", generator.requests)
	}
	if len(request.Messages) != 1 {
		t.Fatal("GenerateStructured mutated caller request")
	}
}

func TestGenerateStructuredDoesNotRetryTransportFailure(t *testing.T) {
	generator := &sequenceGenerator{err: errors.New("offline")}
	_, err := GenerateStructured(
		context.Background(),
		generator,
		coachRequest(t),
		contracts.DecodeCoachResponse,
	)
	if len(generator.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(generator.requests))
	}
	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("error = %#v", err)
	}
}

func TestGenerateTimeoutAndCancellationAreTyped(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		select {
		case <-request.Context().Done():
		case <-time.After(200 * time.Millisecond):
			_, _ = io.WriteString(response, `{"message":{"content":"{}"}}`)
		}
	}))
	t.Cleanup(server.Close)

	t.Run("timeout", func(t *testing.T) {
		client, err := New(config.LLM{
			Provider: config.ProviderOllama,
			Endpoint: server.URL,
			Model:    "test-model",
		}, Options{Timeout: 20 * time.Millisecond})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		_, err = client.Generate(context.Background(), coachRequest(t))
		if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) ||
			!strings.Contains(err.Error(), "超时") {
			t.Fatalf("timeout error = %#v", err)
		}
	})

	t.Run("cancel", func(t *testing.T) {
		client, err := New(config.LLM{
			Provider: config.ProviderOllama,
			Endpoint: server.URL,
			Model:    "test-model",
		}, Options{Timeout: time.Second})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err = client.Generate(ctx, coachRequest(t))
		if !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
			t.Fatalf("cancel error = %#v", err)
		}
	})
}

func TestDiagnoseDistinguishesEndpointAuthenticationAndModel(t *testing.T) {
	testCases := []struct {
		name       string
		status     int
		payload    string
		kind       DiagnosticKind
		ready      bool
		wantSecret bool
	}{
		{
			name: "ready", status: http.StatusOK,
			payload: `{"data":[{"id":"test-model"}]}`,
			kind:    DiagnosticReady, ready: true, wantSecret: true,
		},
		{
			name: "authentication", status: http.StatusUnauthorized,
			payload: `{"error":{"message":"bad key test-secret"}}`,
			kind:    DiagnosticAuthentication, wantSecret: true,
		},
		{
			name: "model", status: http.StatusOK,
			payload: `{"data":[{"id":"different-model"}]}`,
			kind:    DiagnosticModel, wantSecret: true,
		},
		{
			name: "endpoint", status: http.StatusServiceUnavailable,
			payload: `{"error":"offline"}`,
			kind:    DiagnosticEndpoint, wantSecret: true,
		},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(
				response http.ResponseWriter,
				request *http.Request,
			) {
				if got := request.Header.Get("Authorization"); got != "Bearer test-secret" {
					t.Errorf("Authorization = %q", got)
				}
				response.WriteHeader(testCase.status)
				_, _ = io.WriteString(response, testCase.payload)
			}))
			t.Cleanup(server.Close)

			diagnostic := newOpenAIClient(
				t,
				server.URL,
				time.Second,
			).Diagnose(context.Background())
			if diagnostic.Kind != testCase.kind || diagnostic.Ready != testCase.ready {
				t.Fatalf("diagnostic = %#v", diagnostic)
			}
			safe := diagnostic.Message + diagnostic.Recovery
			if strings.Contains(safe, "test-secret") {
				t.Fatalf("diagnostic leaked secret: %q", safe)
			}
		})
	}
}

func TestDiagnoseOllamaAcceptsImplicitLatestTag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		_, _ = io.WriteString(response, `{
			"models":[{"name":"qwen3:latest","model":"qwen3:latest"}]
		}`)
	}))
	t.Cleanup(server.Close)
	client, err := New(config.LLM{
		Provider: config.ProviderOllama,
		Endpoint: server.URL,
		Model:    "qwen3",
	}, Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if diagnostic := client.Diagnose(context.Background()); !diagnostic.Ready {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
}

func TestMissingAPIKeyIsAuthenticationFailureWithoutRequest(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		requests++
	}))
	t.Cleanup(server.Close)
	client, err := New(config.LLM{
		Provider:  config.ProviderOpenAICompatible,
		Endpoint:  server.URL,
		Model:     "test-model",
		APIKeyEnv: "MISSING_KEY",
	}, Options{
		Timeout:       time.Second,
		ResolveSecret: func(string) (string, bool) { return "", false },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	diagnostic := client.Diagnose(context.Background())
	if diagnostic.Kind != DiagnosticAuthentication || diagnostic.Ready {
		t.Fatalf("diagnostic = %#v", diagnostic)
	}
	_, generateErr := client.Generate(context.Background(), coachRequest(t))
	if !domainerr.IsCode(generateErr, domainerr.CodeDependencyUnavailable) ||
		!strings.Contains(generateErr.Error(), "认证") {
		t.Fatalf("Generate error = %#v", generateErr)
	}
	if requests != 0 {
		t.Fatalf("HTTP requests = %d, want 0", requests)
	}
}

func TestSecretNeverAppearsInProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		_ *http.Request,
	) {
		response.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(response, `{"error":"test-secret"}`)
	}))
	t.Cleanup(server.Close)
	client := newOpenAIClient(t, server.URL, time.Second)
	_, err := client.Generate(context.Background(), coachRequest(t))
	if err == nil {
		t.Fatal("Generate error = nil")
	}
	if strings.Contains(err.Error(), "test-secret") {
		t.Fatalf("error leaked secret: %q", err)
	}
}

func TestNewRejectsEndpointCredentialsAndInvalidURL(t *testing.T) {
	testCases := []string{
		"not-a-url",
		"https://user:secret@example.test/v1",
		"https://example.test/v1?api_key=secret",
	}
	for _, endpoint := range testCases {
		t.Run(endpoint, func(t *testing.T) {
			_, err := New(config.LLM{
				Provider:  config.ProviderOpenAICompatible,
				Endpoint:  endpoint,
				Model:     "test-model",
				APIKeyEnv: "TEST_KEY",
			}, Options{})
			if !domainerr.IsCode(err, domainerr.CodeValidation) {
				t.Fatalf("New error = %#v", err)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked endpoint credentials: %q", err)
			}
		})
	}
}

func TestAsyncPackageStillSupportsProviderLifecycle(t *testing.T) {
	pending := async.NewPending[Diagnostic]()
	ready := Diagnostic{Ready: true, Kind: DiagnosticReady}
	next, err := pending.Transition(async.NewSucceeded(ready))
	if err != nil || next.Value == nil || !next.Value.Ready {
		t.Fatalf("transition = %#v err=%v", next, err)
	}
}

type sequenceGenerator struct {
	mu        sync.Mutex
	responses [][]byte
	err       error
	requests  []Request
}

func (generator *sequenceGenerator) Generate(
	_ context.Context,
	request Request,
) ([]byte, error) {
	generator.mu.Lock()
	defer generator.mu.Unlock()
	generator.requests = append(generator.requests, cloneRequest(request))
	if generator.err != nil {
		return nil, generator.err
	}
	index := len(generator.requests) - 1
	if index >= len(generator.responses) {
		return nil, errors.New("no response")
	}
	return generator.responses[index], nil
}

func coachRequest(t *testing.T) Request {
	t.Helper()
	schema, ok := contracts.JSONSchema(contracts.SchemaCoachResponse)
	if !ok {
		t.Fatal("CoachResponse schema missing")
	}
	return Request{
		SchemaName: "CoachResponse",
		Schema:     schema,
		Messages: []Message{
			{Role: RoleSystem, Content: "Return JSON."},
			{Role: RoleUser, Content: "Give one hint."},
		},
	}
}

func newOpenAIClient(t *testing.T, endpoint string, timeout time.Duration) *Client {
	t.Helper()
	client, err := New(config.LLM{
		Provider:  config.ProviderOpenAICompatible,
		Endpoint:  endpoint,
		Model:     "test-model",
		APIKeyEnv: "TEST_API_KEY",
	}, Options{
		Timeout: timeout,
		ResolveSecret: func(name string) (string, bool) {
			return "test-secret", name == "TEST_API_KEY"
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return client
}
