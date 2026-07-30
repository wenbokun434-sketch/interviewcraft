// Package report owns the persisted, evidence-resolvable evaluation report.
package report

import (
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
)

// SchemaVersion identifies the durable report payload contract.
const SchemaVersion = "report-v1"

// AssessmentStatus distinguishes evidence-backed conclusions from explicit
// non-applicability and conservative insufficient-evidence fallbacks.
type AssessmentStatus string

const (
	StatusEvidenceBacked AssessmentStatus = "evidence_backed"
	StatusNotApplicable  AssessmentStatus = "not_applicable"
	StatusInsufficient   AssessmentStatus = "insufficient_evidence"
)

// TransferStatus records only whether a timely follow-up event exists. It does
// not infer that learning succeeded or failed.
type TransferStatus string

const (
	TransferEvidenceObserved TransferStatus = "evidence_observed"
	TransferInsufficient     TransferStatus = "insufficient_evidence"
)

// EvidenceLink is the durable target behind every report evidence reference.
type EvidenceLink struct {
	ID         contracts.EvidenceID `json:"id"`
	Kind       string               `json:"kind"`
	QuestionID string               `json:"question_id,omitempty"`
	Label      string               `json:"label"`
	OccurredAt time.Time            `json:"occurred_at,omitempty"`
}

// SessionSummary contains factual run metadata, never a hero score.
type SessionSummary struct {
	SessionID        string                 `json:"session_id"`
	ScenarioID       string                 `json:"scenario_id"`
	Template         string                 `json:"template"`
	Mode             contracts.ScenarioMode `json:"mode"`
	StartedAt        time.Time              `json:"started_at"`
	CompletedAt      time.Time              `json:"completed_at"`
	DurationSeconds  int                    `json:"duration_seconds"`
	QuestionCount    int                    `json:"question_count"`
	CoachPromptCount int                    `json:"coach_prompt_count"`
	CodeRunCount     int                    `json:"code_run_count"`
}

// Insight is one evidence-safe review statement.
type Insight struct {
	Text        string                 `json:"text"`
	Status      AssessmentStatus       `json:"status"`
	EvidenceIDs []contracts.EvidenceID `json:"evidence_ids"`
	Confidence  float64                `json:"confidence"`
}

// QuestionReview connects one scenario question to a summary and next action.
type QuestionReview struct {
	QuestionID string  `json:"question_id"`
	Prompt     string  `json:"prompt"`
	Summary    Insight `json:"summary"`
	NextAction Insight `json:"next_action"`
}

// ScorecardItem is one of the eight fixed dimensions.
type ScorecardItem struct {
	Dimension   contracts.EvaluationDimension `json:"dimension"`
	Status      AssessmentStatus              `json:"status"`
	Score       *int                          `json:"score,omitempty"`
	EvidenceIDs []contracts.EvidenceID        `json:"evidence_ids"`
	Confidence  float64                       `json:"confidence"`
	NextAction  string                        `json:"next_action"`
}

// LearningGap aggregates each surviving SidebarEvent exactly once under its
// primary knowledge tag.
type LearningGap struct {
	Topic           string                 `json:"topic"`
	AskCount        int                    `json:"ask_count"`
	MaxHelpLevel    contracts.HelpLevel    `json:"max_help_level"`
	UnderstoodCount int                    `json:"understood_count"`
	ConfusedCount   int                    `json:"confused_count"`
	ReviewCount     int                    `json:"review_count"`
	UnmarkedCount   int                    `json:"unmarked_count"`
	QuestionIDs     []string               `json:"question_ids"`
	EvidenceIDs     []contracts.EvidenceID `json:"evidence_ids"`
	RelatedSkills   []string               `json:"related_skills"`
	RelatedJDNeeds  []string               `json:"related_jd_requirements"`
}

// TransferEvidence links one Coach event to same-question answer/code evidence
// in the following five minutes without asserting improvement.
type TransferEvidence struct {
	SidebarEventID     contracts.EvidenceID   `json:"sidebar_event_id"`
	QuestionID         string                 `json:"question_id"`
	Status             TransferStatus         `json:"status"`
	SubsequentEvidence []contracts.EvidenceID `json:"subsequent_evidence_ids"`
	Summary            string                 `json:"summary"`
}

// PracticeItem is one executable next-run recommendation.
type PracticeItem struct {
	Topic              string                 `json:"topic"`
	Mode               contracts.ScenarioMode `json:"mode"`
	DurationMinutes    int                    `json:"duration_minutes"`
	CompletionCriteria string                 `json:"completion_criteria"`
	Status             AssessmentStatus       `json:"status"`
	EvidenceIDs        []contracts.EvidenceID `json:"evidence_ids"`
}

// Document is the complete durable report payload.
type Document struct {
	ID             string             `json:"id"`
	SchemaVersion  string             `json:"schema_version"`
	GeneratedAt    time.Time          `json:"generated_at"`
	Degraded       bool               `json:"degraded"`
	Summary        SessionSummary     `json:"summary"`
	Evidence       []EvidenceLink     `json:"evidence"`
	QuestionReview []QuestionReview   `json:"question_reviews"`
	Scorecard      []ScorecardItem    `json:"scorecard"`
	LearningMap    []LearningGap      `json:"learning_map"`
	Transfer       []TransferEvidence `json:"transfer_evidence"`
	CrossInsights  []Insight          `json:"cross_source_insights"`
	PracticePlan   []PracticeItem     `json:"practice_plan"`
}
