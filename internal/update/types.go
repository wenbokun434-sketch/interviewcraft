// Package update implements verified release upgrades, complete data backups
// and fail-closed rollback for user-scoped InterviewCraft installations.
package update

import (
	"context"
	"io"
	"net/http"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/version"
)

const (
	StateSchema  = "interviewcraft-update-state-v1"
	BackupSchema = "interviewcraft-backup-manifest-v1"
)

type Stage string

const (
	StageCheck    Stage = "check"
	StageDownload Stage = "download"
	StageVerify   Stage = "verify"
	StageBackup   Stage = "backup"
	StageSwitch   Stage = "switch"
	StageMigrate  Stage = "migrate"
	StageDoctor   Stage = "doctor"
	StageCommit   Stage = "commit"
	StageRollback Stage = "rollback"
)

type Progress struct {
	Stage   Stage
	Current int
	Total   int
	Message string
}

type Observer func(async.State[Progress])

type Request struct {
	CheckOnly bool
	Version   string
}

type Result struct {
	CurrentVersion   string
	AvailableVersion string
	Updated          bool
	Scheduled        bool
	RolledBack       bool
	BackupDirectory  string
	DiagnosticPath   string
}

type StatePhase string

const (
	PhasePrepared   StatePhase = "prepared"
	PhaseSwitched   StatePhase = "switched"
	PhaseCommitted  StatePhase = "committed"
	PhaseFailed     StatePhase = "failed"
	PhaseRolledBack StatePhase = "rolled_back"
)

type State struct {
	Schema          string     `json:"schema"`
	Phase           StatePhase `json:"phase"`
	FromVersion     string     `json:"from_version"`
	ToVersion       string     `json:"to_version"`
	DataDir         string     `json:"data_dir"`
	BackupDirectory string     `json:"backup_directory"`
	ForwardBackup   string     `json:"forward_backup,omitempty"`
	BinaryPath      string     `json:"binary_path"`
	StagedBinary    string     `json:"staged_binary"`
	ReceiptPath     string     `json:"receipt_path"`
	DiagnosticPath  string     `json:"diagnostic_path"`
	GuardToken      string     `json:"guard_token"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

type CommandResult struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

type CommandRunner interface {
	Run(context.Context, []string, string, ...string) (CommandResult, error)
}

type SignatureVerifier interface {
	VerifyBlob(context.Context, string, string, string, string) error
}

type Options struct {
	Client              *http.Client
	Current             version.Info
	ExecutablePath      string
	DataDir             string
	ReceiptPath         string
	LatestURL           string
	ReleaseBaseURL      string
	GOOS                string
	GOARCH              string
	Verifier            SignatureVerifier
	LocalVerifierPath   string
	LocalVerifierSHA256 string
	Commands            CommandRunner
	AvailableBytes      func(string) (uint64, error)
	Now                 func() time.Time
	Output              io.Writer
	ForceDirect         bool
	ScheduleHelper      func(State, string) error
	CreateBackup        func(context.Context, string, string, string, time.Time, func(string) (uint64, error)) (string, error)
	RestoreBackup       func(context.Context, string, string, string, string, string) error
	InstallBinary       func(string, string) error
}

type ReleaseAsset struct {
	GOOS     string
	GOARCH   string
	Filename string
	SHA256   string
	Size     int64
}

type ReleaseManifest struct {
	Version string
	Commit  string
	Created time.Time
	Assets  []ReleaseAsset
}
