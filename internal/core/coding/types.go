// Package coding owns coding-question contracts, local three-language drafts,
// and evidence-safe execution snapshots. Docker execution is an adapter concern.
package coding

import (
	"context"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

const (
	// DraftVersion is the durable JSON contract stored in the existing code
	// draft slot.
	DraftVersion = "interviewcraft-code-draft-v1"
	// ResultVersion is the durable safe-result contract stored with a code run.
	ResultVersion = "interviewcraft-code-result-v1"
)

// Language is one supported MVP editor language.
type Language string

const (
	LanguagePython     Language = "python"
	LanguageJavaScript Language = "javascript"
	LanguageJava       Language = "java"
)

var languages = []Language{
	LanguagePython,
	LanguageJavaScript,
	LanguageJava,
}

// Languages returns the stable product order.
func Languages() []Language {
	return append([]Language(nil), languages...)
}

// Example is one public problem example, never a hidden test.
type Example struct {
	Input       string `json:"input"`
	Output      string `json:"output"`
	Explanation string `json:"explanation"`
}

// Complexity records the expected asymptotic target.
type Complexity struct {
	Time  string `json:"time"`
	Space string `json:"space"`
}

// RubricItem is one visible interview scoring dimension.
type RubricItem struct {
	Dimension   string `json:"dimension"`
	Description string `json:"description"`
}

// Question is a complete editor-neutral coding prompt.
type Question struct {
	ID               string              `json:"id"`
	Title            string              `json:"title"`
	Description      string              `json:"description"`
	InputFormat      string              `json:"input_format"`
	OutputFormat     string              `json:"output_format"`
	Constraints      []string            `json:"constraints"`
	Examples         []Example           `json:"examples"`
	TargetComplexity Complexity          `json:"target_complexity"`
	Rubric           []RubricItem        `json:"rubric"`
	Templates        map[Language]string `json:"templates"`
}

// DraftDocument stores all three language buffers together so switching
// languages cannot discard another local draft.
type DraftDocument struct {
	Version        string              `json:"version"`
	QuestionID     string              `json:"question_id"`
	ActiveLanguage Language            `json:"active_language"`
	Sources        map[Language]string `json:"sources"`
}

// TestStatus is the only public per-test state exposed by the core domain.
type TestStatus string

const (
	TestPassed TestStatus = "passed"
	TestFailed TestStatus = "failed"
	TestError  TestStatus = "error"
)

// PublicTestResult deliberately carries no hidden input, expected value, raw
// stderr, or container path.
type PublicTestResult struct {
	Name   string     `json:"name"`
	Status TestStatus `json:"status"`
}

// HiddenTestSummary exposes counts only. Hidden cases have no representation
// in the public domain contract.
type HiddenTestSummary struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

// RunStatus is the safe aggregate execution state.
type RunStatus string

const (
	RunPassed RunStatus = "passed"
	RunFailed RunStatus = "failed"
	RunError  RunStatus = "error"
)

// ErrorKind is an enumerated safe diagnostic. Raw runner output never crosses
// this boundary.
type ErrorKind string

const (
	ErrorNone            ErrorKind = "none"
	ErrorCompile         ErrorKind = "compile_error"
	ErrorRuntime         ErrorKind = "runtime_error"
	ErrorTimeout         ErrorKind = "timeout"
	ErrorOutOfMemory     ErrorKind = "out_of_memory"
	ErrorPolicyDenied    ErrorKind = "policy_denied"
	ErrorRunnerUnhealthy ErrorKind = "runner_unhealthy"
)

// SafeResult is persisted as test_result_json.
type SafeResult struct {
	Version     string             `json:"version"`
	Status      RunStatus          `json:"status"`
	PublicTests []PublicTestResult `json:"public_tests"`
	HiddenTests HiddenTestSummary  `json:"hidden_tests"`
	ErrorKind   ErrorKind          `json:"error_kind"`
}

// RuntimeStats is persisted separately and contains no host/container paths.
type RuntimeStats struct {
	DurationMilliseconds int64 `json:"duration_ms"`
	PeakMemoryKB         int64 `json:"peak_memory_kb"`
}

// ExecutionRequest is the complete adapter input. Test assets stay inside the
// runner adapter rather than entering UI/domain output.
type ExecutionRequest struct {
	QuestionID string
	Language   Language
	Source     string
}

// ExecutionResult is the only result a runner adapter may return to core.
type ExecutionResult struct {
	Result  SafeResult
	Runtime RuntimeStats
}

// Runner is implemented by the optional isolated adapter in the next task.
// A nil Runner is the Lite default.
type Runner interface {
	Run(context.Context, ExecutionRequest) (ExecutionResult, error)
}

// Formatter supports all three languages without requiring external tooling.
type Formatter interface {
	Format(context.Context, Language, string) (string, error)
}

// Repository is the existing SQLite boundary used by coding workflows.
type Repository interface {
	SaveDraft(context.Context, db.Draft) error
	LoadDraft(
		context.Context,
		string,
		string,
		db.DraftKind,
	) (db.Draft, bool, error)
	AddCodeSubmission(context.Context, db.CodeSubmission) error
	ListCodeSubmissions(context.Context, string) ([]db.CodeSubmission, error)
}

// RunSnapshot is one immutable, executed code evidence item.
type RunSnapshot struct {
	SubmissionID string       `json:"submission_id"`
	SnapshotID   string       `json:"snapshot_id"`
	SessionID    string       `json:"session_id"`
	QuestionID   string       `json:"question_id"`
	Language     Language     `json:"language"`
	Source       string       `json:"source"`
	Result       SafeResult   `json:"result"`
	Runtime      RuntimeStats `json:"runtime"`
	CreatedAt    time.Time    `json:"created_at"`
	Idempotent   bool         `json:"idempotent"`
}

// Workspace is the UI-neutral recoverable coding state. LatestRun is nil for
// the explicit unrun empty state.
type Workspace struct {
	Question  Question
	Draft     DraftDocument
	LatestRun *RunSnapshot
}

// Progress is emitted in deterministic save/run stage order.
type Progress struct {
	Stage   string
	Message string
}

// Observer receives typed draft and run lifecycle states.
type Observer func(async.State[Progress])

// RunnerStatus is the non-error status displayed before a user attempts Run.
type RunnerStatus struct {
	Enabled        bool
	Message        string
	RecoveryAction string
}

// RunRequest carries one caller-stable idempotency key.
type RunRequest struct {
	SessionID  string
	QuestionID string
	Language   Language
	RunID      string
}

// Options injects deterministic services without enabling Docker implicitly.
type Options struct {
	Now       func() time.Time
	Formatter Formatter
	Runner    Runner
}
