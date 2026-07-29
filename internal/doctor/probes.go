package doctor

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/config"
)

// EnvironmentTerminalProbe reads portable terminal dimensions when provided by
// the host shell. Unknown dimensions remain a warning rather than a false pass.
type EnvironmentTerminalProbe struct {
	LookupEnv func(string) (string, bool)
}

// Size implements TerminalProbe.
func (probe EnvironmentTerminalProbe) Size() (int, int, bool, error) {
	if probe.LookupEnv == nil {
		return 0, 0, false, errors.New("environment lookup is unavailable")
	}
	columns, columnsOK := probe.LookupEnv("COLUMNS")
	lines, linesOK := probe.LookupEnv("LINES")
	if !columnsOK || !linesOK {
		return 0, 0, false, nil
	}
	width, err := strconv.Atoi(strings.TrimSpace(columns))
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid COLUMNS: %w", err)
	}
	height, err := strconv.Atoi(strings.TrimSpace(lines))
	if err != nil {
		return 0, 0, false, fmt.Errorf("invalid LINES: %w", err)
	}
	return width, height, true, nil
}

// HTTPModelProbe performs the minimum Provider connectivity check used by
// doctor. Structured generation remains owned by the later LLM adapter task.
type HTTPModelProbe struct {
	Client    *http.Client
	LookupEnv func(string) (string, bool)
}

// Check implements ModelProbe.
func (probe HTTPModelProbe) Check(ctx context.Context, llm config.LLM) error {
	client := probe.Client
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	lookup := probe.LookupEnv
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}

	target := strings.TrimRight(llm.Endpoint, "/")
	switch llm.Provider {
	case config.ProviderOllama:
		target += "/api/tags"
	case config.ProviderOpenAICompatible:
		target += "/models"
	default:
		return fmt.Errorf("unsupported provider %q", llm.Provider)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return err
	}
	if llm.Provider == config.ProviderOpenAICompatible {
		key, ok := lookup(llm.APIKeyEnv)
		if !ok || strings.TrimSpace(key) == "" {
			return fmt.Errorf("API key environment variable %q is empty", llm.APIKeyEnv)
		}
		request.Header.Set("Authorization", "Bearer "+key)
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("provider returned HTTP %d", response.StatusCode)
	}
	return nil
}

// DockerRunnerProbe checks Docker without starting a container.
type DockerRunnerProbe struct{}

// Check implements RunnerProbe.
func (DockerRunnerProbe) Check(ctx context.Context) error {
	path, err := exec.LookPath("docker")
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, path, "info", "--format", "{{.ServerVersion}}")
	if output, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("docker info failed: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	return nil
}
