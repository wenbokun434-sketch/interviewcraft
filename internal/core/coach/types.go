// Package coach owns Coach policy, isolated context, and learning events.
package coach

import (
	"context"
	"encoding/json"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

// QuestionState controls whether a full L4 review is policy-eligible.
type QuestionState string

const (
	QuestionActive QuestionState = "active"
	QuestionClosed QuestionState = "closed"
	SessionClosed  QuestionState = "session_closed"
)

// LearningOutcome is the candidate's explicit understanding marker.
type LearningOutcome string

const (
	OutcomeUnmarked   LearningOutcome = "unmarked"
	OutcomeUnderstood LearningOutcome = "understood"
	OutcomeConfused   LearningOutcome = "still_confused"
	OutcomeReview     LearningOutcome = "review"
)

// Policy is the fixed assistance budget derived from confirmed Scenario mode.
// Limit=0 means unlimited.
type Policy struct {
	Mode     contracts.ScenarioMode
	Limit    int
	MaxLevel contracts.HelpLevel
}

// Usage exposes visible quota state without leaking prior Coach text.
type Usage struct {
	Used      int
	Limit     int
	Remaining int
	Unlimited bool
}

// AnswerEvidence is one already-submitted main answer.
type AnswerEvidence struct {
	EventID    contracts.EvidenceID `json:"event_id"`
	QuestionID string               `json:"question_id"`
	Content    string               `json:"content"`
}

// CodeEvidence is one already-executed code snapshot.
type CodeEvidence struct {
	SubmissionID contracts.EvidenceID `json:"submission_id"`
	QuestionID   string               `json:"question_id"`
	Language     string               `json:"language"`
	Source       string               `json:"source"`
	TestResult   json.RawMessage      `json:"test_result"`
	RuntimeStats json.RawMessage      `json:"runtime_stats"`
	SnapshotID   contracts.EvidenceID `json:"snapshot_id"`
}

// Input is the complete Provider context. It deliberately has no main-answer
// draft, code draft, Profile inference, or previous Coach response field.
type Input struct {
	SessionID        string                     `json:"session_id"`
	Mode             contracts.ScenarioMode     `json:"mode"`
	Question         contracts.ScenarioQuestion `json:"question"`
	QuestionState    QuestionState              `json:"question_state"`
	Intent           contracts.CoachIntent      `json:"intent"`
	RequestedLevel   contracts.HelpLevel        `json:"requested_level"`
	AllowedMaxLevel  contracts.HelpLevel        `json:"allowed_max_level"`
	UserRequest      string                     `json:"user_request"`
	ConfirmedFacts   []contracts.ProfileFact    `json:"confirmed_facts"`
	SubmittedAnswers []AnswerEvidence           `json:"submitted_answers"`
	CodeRuns         []CodeEvidence             `json:"code_runs"`
	Usage            Usage                      `json:"usage"`
	PausedForHelp    bool                       `json:"paused_for_help"`
}

// Provider returns one policy-aware CoachResponse.
type Provider interface {
	Respond(context.Context, Input) (contracts.CoachResponse, error)
}

// Repository is the complete persistence boundary used by Coach.
type Repository interface {
	GetSession(context.Context, string) (db.Session, bool, error)
	GetScenario(context.Context, string) (contracts.Scenario, bool, error)
	GetSessionProfile(
		context.Context,
		string,
	) (coreprofile.Aggregate, bool, error)
	ListSessionEvents(context.Context, string) ([]db.SessionEvent, error)
	AppendSessionEvent(context.Context, db.SessionEvent) error
	ListCodeSubmissions(context.Context, string) ([]db.CodeSubmission, error)
	AddSidebarEvent(context.Context, db.SidebarEvent) error
	ListSidebarEvents(context.Context, string) ([]db.SidebarEvent, error)
	GetSidebarEvent(
		context.Context,
		string,
		string,
	) (db.SidebarEvent, bool, error)
	CountSidebarEventsForQuestion(
		context.Context,
		string,
		string,
	) (int, error)
	UpdateSidebarEventOutcome(
		context.Context,
		string,
		string,
		string,
	) (bool, error)
	DeleteSidebarEvent(
		context.Context,
		string,
		string,
	) (bool, error)
	DeleteSidebarEventsByQuestion(
		context.Context,
		string,
		string,
	) (int64, error)
	DeleteSidebarEventsBySession(
		context.Context,
		string,
	) (int64, error)
}

// AskRequest carries one stable Coach request and explicit pause choice.
type AskRequest struct {
	SessionID      string
	QuestionID     string
	RequestID      string
	Intent         contracts.CoachIntent
	RequestedLevel contracts.HelpLevel
	UserRequest    string
	PauseForHelp   bool
}

// AskResult returns the policy-validated response and persisted learning event.
type AskResult struct {
	Response   contracts.CoachResponse
	Event      db.SidebarEvent
	Usage      Usage
	Idempotent bool
}

// Progress is emitted while Coach context is prepared and generated.
type Progress struct {
	Stage   string
	Message string
}

// Observer receives Coach lifecycle states.
type Observer func(async.State[Progress])

// Options injects deterministic time.
type Options struct {
	Now func() time.Time
}
