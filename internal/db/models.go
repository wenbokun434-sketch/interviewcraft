package db

import (
	"encoding/json"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
)

// SessionStatus is persisted separately from the immutable event stream.
type SessionStatus string

const (
	SessionActive            SessionStatus = "active"
	SessionEvaluationPending SessionStatus = "evaluation_pending"
	SessionCompleted         SessionStatus = "completed"
)

// Session identifies one run of a confirmed scenario.
type Session struct {
	ID         string
	ScenarioID string
	Status     SessionStatus
	StartedAt  time.Time
	UpdatedAt  time.Time
}

// EventSpeaker identifies the source of an immutable session event.
type EventSpeaker string

const (
	SpeakerInterviewer EventSpeaker = "interviewer"
	SpeakerUser        EventSpeaker = "user"
	SpeakerSystem      EventSpeaker = "system"
	SpeakerCode        EventSpeaker = "code"
	SpeakerReport      EventSpeaker = "report"
)

// SessionEvent is append-only training evidence.
type SessionEvent struct {
	Sequence     int64
	EventID      string
	SessionID    string
	Speaker      EventSpeaker
	QuestionID   string
	Content      string
	OccurredAt   time.Time
	EvidenceRefs []contracts.EvidenceID
}

// DraftKind separates answer, code, and Coach buffers.
type DraftKind string

const (
	DraftAnswer DraftKind = "answer"
	DraftCode   DraftKind = "code"
	DraftCoach  DraftKind = "coach"
)

// Draft is a replaceable local buffer that is never a submitted event.
type Draft struct {
	SessionID  string
	QuestionID string
	Kind       DraftKind
	Content    string
	UpdatedAt  time.Time
}

// SidebarEvent is a persisted Coach learning event.
type SidebarEvent struct {
	ID          string
	SessionID   string
	QuestionID  string
	Intent      contracts.CoachIntent
	HelpLevel   contracts.HelpLevel
	Tags        []string
	Outcome     string
	PausedTimer bool
	OccurredAt  time.Time
}

// CodeSubmission stores an executed code snapshot and safe result payloads.
type CodeSubmission struct {
	ID           string
	SessionID    string
	QuestionID   string
	Language     string
	Source       string
	TestResult   json.RawMessage
	RuntimeStats json.RawMessage
	SnapshotID   string
	CreatedAt    time.Time
}

// Report stores one evidence-based report per session.
type Report struct {
	ID        string
	SessionID string
	Payload   json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}
