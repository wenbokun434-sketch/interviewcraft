// Package interview owns the evidence-safe text interview state machine.
package interview

import (
	"context"
	"encoding/json"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

// Phase is the recoverable state derived from persisted session data.
type Phase string

const (
	PhaseNotStarted              Phase = "not_started"
	PhaseAwaitingAnswer          Phase = "awaiting_answer"
	PhaseThinking                Phase = "thinking"
	PhasePaused                  Phase = "paused"
	PhaseAwaitingEndConfirmation Phase = "awaiting_end_confirmation"
	PhaseQuestionComplete        Phase = "question_complete"
	PhaseCompleted               Phase = "completed"
)

// EndScope separates ending one question from ending the full session.
type EndScope string

const (
	EndQuestion EndScope = "question"
	EndSession  EndScope = "session"
)

// PendingEnd is a persisted first-step end request awaiting confirmation.
type PendingEnd struct {
	Scope       EndScope
	OperationID string
}

// Snapshot is the complete UI-neutral recoverable interview state.
type Snapshot struct {
	Session         db.Session
	Scenario        contracts.Scenario
	Events          []db.SessionEvent
	CurrentQuestion *contracts.ScenarioQuestion
	CurrentIndex    int
	FollowUpCount   int
	Phase           Phase
	PendingEnd      *PendingEnd
	Draft           *db.Draft
}

// Progress is emitted while an answer is durably recorded and evaluated.
type Progress struct {
	Stage   string
	Message string
}

// Observer receives typed submit lifecycle states.
type Observer func(async.State[Progress])

// AnswerEvidence is one submitted answer available to Interviewer.
type AnswerEvidence struct {
	EventID    contracts.EvidenceID `json:"event_id"`
	QuestionID string               `json:"question_id"`
	Content    string               `json:"content"`
}

// CodeEvidence is one executed code snapshot available to Interviewer.
type CodeEvidence struct {
	SubmissionID contracts.EvidenceID `json:"submission_id"`
	QuestionID   string               `json:"question_id"`
	Language     string               `json:"language"`
	Source       string               `json:"source"`
	TestResult   json.RawMessage      `json:"test_result"`
	RuntimeStats json.RawMessage      `json:"runtime_stats"`
	SnapshotID   contracts.EvidenceID `json:"snapshot_id"`
}

// Input contains only allowed Interviewer context. It deliberately has no
// draft, Profile inference, SidebarEvent, or Coach response field.
type Input struct {
	SessionID          string                      `json:"session_id"`
	Mode               contracts.ScenarioMode      `json:"mode"`
	CurrentQuestion    contracts.ScenarioQuestion  `json:"current_question"`
	NextQuestion       *contracts.ScenarioQuestion `json:"next_question,omitempty"`
	FollowUpCount      int                         `json:"follow_up_count"`
	MaxFollowUps       int                         `json:"max_follow_ups"`
	ConfirmedFacts     []contracts.ProfileFact     `json:"confirmed_facts"`
	SubmittedAnswers   []AnswerEvidence            `json:"submitted_answers"`
	CodeRuns           []CodeEvidence              `json:"code_runs"`
	AllowedEvidenceIDs []contracts.EvidenceID      `json:"allowed_evidence_ids"`
}

// Provider returns one strict InterviewerAction for a persisted answer.
type Provider interface {
	Respond(context.Context, Input) (contracts.InterviewerAction, error)
}

// Repository is the complete persistence boundary used by the state machine.
type Repository interface {
	GetSession(context.Context, string) (db.Session, bool, error)
	GetScenario(context.Context, string) (contracts.Scenario, bool, error)
	GetSessionProfile(
		context.Context,
		string,
	) (coreprofile.Aggregate, bool, error)
	ListSessionEvents(context.Context, string) ([]db.SessionEvent, error)
	AppendSessionEvent(context.Context, db.SessionEvent) error
	UpdateSessionStatus(
		context.Context,
		string,
		db.SessionStatus,
		time.Time,
	) (bool, error)
	SaveDraft(context.Context, db.Draft) error
	LoadDraft(
		context.Context,
		string,
		string,
		db.DraftKind,
	) (db.Draft, bool, error)
	DeleteDraft(
		context.Context,
		string,
		string,
		db.DraftKind,
	) (bool, error)
	ListCodeSubmissions(
		context.Context,
		string,
	) ([]db.CodeSubmission, error)
}

// LatencyRecorder receives Provider response latency samples.
type LatencyRecorder interface {
	Observe(time.Duration)
	P95() time.Duration
}

// Options injects deterministic time and latency measurement.
type Options struct {
	Now     func() time.Time
	Latency LatencyRecorder
}

// SubmitRequest carries a caller-stable idempotency key and answer.
type SubmitRequest struct {
	SessionID    string
	SubmissionID string
	Answer       string
}

// SubmitResult returns the persisted action and recovered state.
type SubmitResult struct {
	Action          contracts.InterviewerAction
	Snapshot        Snapshot
	ProviderLatency time.Duration
	Idempotent      bool
}
