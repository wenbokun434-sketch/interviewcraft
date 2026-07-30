// Package evaluation generates conservative reports from persisted evidence.
package evaluation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

// EventEvidence is one already-persisted main interview event.
type EventEvidence struct {
	ID         contracts.EvidenceID `json:"id"`
	Speaker    db.EventSpeaker      `json:"speaker"`
	QuestionID string               `json:"question_id"`
	Content    string               `json:"content"`
	OccurredAt time.Time            `json:"occurred_at"`
}

// CoachEvidence is one surviving SidebarEvent. Deleted events cannot be
// represented because the repository query physically excludes them.
type CoachEvidence struct {
	ID          contracts.EvidenceID  `json:"id"`
	QuestionID  string                `json:"question_id"`
	Intent      contracts.CoachIntent `json:"intent"`
	HelpLevel   contracts.HelpLevel   `json:"help_level"`
	Tags        []string              `json:"tags"`
	Content     string                `json:"content"`
	Outcome     string                `json:"outcome"`
	PausedTimer bool                  `json:"paused_timer"`
	OccurredAt  time.Time             `json:"occurred_at"`
}

// CodeEvidence is one executed, immutable code snapshot.
type CodeEvidence struct {
	SubmissionID contracts.EvidenceID `json:"submission_id"`
	SnapshotID   contracts.EvidenceID `json:"snapshot_id"`
	QuestionID   string               `json:"question_id"`
	Language     string               `json:"language"`
	Source       string               `json:"source"`
	TestResult   json.RawMessage      `json:"test_result"`
	RuntimeStats json.RawMessage      `json:"runtime_stats"`
	OccurredAt   time.Time            `json:"occurred_at"`
}

// Input is the complete Evaluator Provider context. It contains no draft,
// unconfirmed inference, or deleted Coach event.
type Input struct {
	SessionID          string                       `json:"session_id"`
	Template           string                       `json:"template"`
	Mode               contracts.ScenarioMode       `json:"mode"`
	StartedAt          time.Time                    `json:"started_at"`
	CompletedAt        time.Time                    `json:"completed_at"`
	Questions          []contracts.ScenarioQuestion `json:"questions"`
	ConfirmedFacts     []contracts.ProfileFact      `json:"confirmed_facts"`
	ConfirmedSkills    []string                     `json:"confirmed_skills"`
	Events             []EventEvidence              `json:"session_events"`
	CoachEvents        []CoachEvidence              `json:"coach_events"`
	CodeRuns           []CodeEvidence               `json:"code_runs"`
	AllowedEvidenceIDs []contracts.EvidenceID       `json:"allowed_evidence_ids"`
}

// Provider generates a strict evaluation draft from allowed evidence.
type Provider interface {
	Evaluate(context.Context, Input) (Draft, error)
}

// Repository is the complete read/write boundary for evaluation.
type Repository interface {
	report.Repository
	GetSession(context.Context, string) (db.Session, bool, error)
	GetScenario(context.Context, string) (contracts.Scenario, bool, error)
	GetSessionProfile(
		context.Context,
		string,
	) (coreprofile.Aggregate, bool, error)
	ListSessionEvents(context.Context, string) ([]db.SessionEvent, error)
	ListSidebarEvents(context.Context, string) ([]db.SidebarEvent, error)
	ListCodeSubmissions(context.Context, string) ([]db.CodeSubmission, error)
	UpdateSessionStatus(
		context.Context,
		string,
		db.SessionStatus,
		time.Time,
	) (bool, error)
}

// Progress is emitted for the required staged report loading state.
type Progress struct {
	Stage   string
	Message string
}

// Observer receives typed report-generation lifecycle states.
type Observer func(async.State[Progress])

// Options injects deterministic timestamps.
type Options struct {
	Now func() time.Time
}

// Result returns the persisted report and whether it was restored or degraded.
type Result struct {
	Report     report.Document
	Degraded   bool
	Idempotent bool
}
