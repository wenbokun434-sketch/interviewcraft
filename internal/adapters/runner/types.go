// Package runner implements the optional isolated Docker code-runner adapter.
// Constructing this package never enables Runner mode by itself.
package runner

import (
	"context"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
)

const (
	requestVersion         = "interviewcraft-runner-request-v1"
	responseVersion        = "interviewcraft-runner-response-v1"
	defaultImage           = "interviewcraft-runner:local"
	OfficialRepository     = "ghcr.io/wenbokun434-sketch/interviewcraft-runner"
	OIDCIssuer             = "https://token.actions.githubusercontent.com"
	CertificateIdentityURL = "https://github.com/wenbokun434-sketch/interviewcraft/.github/workflows/release.yml@refs/tags/v"
)

// ResponseProtocol is the compatibility label required on released images.
const ResponseProtocol = responseVersion

// Limits is the mandatory per-container security profile.
type Limits struct {
	CPUs             float64
	MemoryMB         int
	PIDs             int
	WallTime         time.Duration
	TmpfsMB          int
	MaxOutputBytes   int
	CleanupTimeout   time.Duration
	ProgressInterval time.Duration
}

// DefaultLimits returns the bounded MVP profile.
func DefaultLimits() Limits {
	return Limits{
		CPUs:             0.50,
		MemoryMB:         256,
		PIDs:             64,
		WallTime:         5 * time.Second,
		TmpfsMB:          64,
		MaxOutputBytes:   64 << 10,
		CleanupTimeout:   3 * time.Second,
		ProgressInterval: 100 * time.Millisecond,
	}
}

// Config selects the local image and Docker CLI path.
type Config struct {
	DockerBinary         string
	CosignBinary         string
	Image                string
	ExpectedDigest       string
	ExpectedVersion      string
	ExpectedProtocol     string
	ExpectedArchitecture string
	CertificateIdentity  string
	OIDCIssuer           string
	Limits               Limits
}

// DefaultConfig is safe but inert until passed to New and wired by an
// explicitly enabled runtime.
func DefaultConfig() Config {
	return Config{
		DockerBinary: "docker",
		Image:        defaultImage,
		Limits:       DefaultLimits(),
	}
}

// CommandResult is bounded process output. Stderr is diagnostic-only and is
// never copied into domain results or user-visible errors.
type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// CommandExecutor runs Docker without a shell.
type CommandExecutor interface {
	Run(context.Context, []byte, ...string) (CommandResult, error)
}

// SignatureVerifier verifies a registry image without exposing command output.
type SignatureVerifier interface {
	VerifyImage(context.Context, string, string, string) error
}

// Progress reports elapsed isolated execution time while editor state remains
// owned by the caller.
type Progress struct {
	Stage   string
	Elapsed time.Duration
}

// Observer receives the adapter's typed lifecycle.
type Observer func(async.State[Progress])

// Options injects deterministic command and naming boundaries for tests.
type Options struct {
	Command           CommandExecutor
	NewContainerName  func() string
	Observer          Observer
	Now               func() time.Time
	SignatureVerifier SignatureVerifier
}

// Diagnostic is a non-sensitive health and policy summary.
type Diagnostic struct {
	DockerVersion     string
	Image             string
	Digest            string
	Version           string
	Protocol          string
	Architecture      string
	ImageReady        bool
	SignatureVerified bool
	NetworkDisabled   bool
	ReadOnlyRoot      bool
	NonRootUser       bool
	CapabilitiesOff   bool
	NoNewPrivileges   bool
	CPUs              float64
	MemoryMB          int
	PIDs              int
	WallTime          time.Duration
}
