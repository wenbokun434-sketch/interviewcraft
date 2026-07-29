// Package contracts defines the structured data exchanged by core modules and
// model adapters. Contract values are validated before entering domain logic.
package contracts

// EvidenceID identifies a persisted fact, answer, code run, or other source.
type EvidenceID string

// SourceSpan ties a profile fact to exact resume text.
type SourceSpan struct {
	Start int    `json:"start"`
	End   int    `json:"end"`
	Text  string `json:"text"`
}

// ProfileFact is a resume-backed statement that can be used as evidence.
type ProfileFact struct {
	ID         EvidenceID `json:"id"`
	Field      string     `json:"field"`
	Value      string     `json:"value"`
	SourceSpan SourceSpan `json:"source_span"`
}

// ProfileInference is model-derived and must remain unconfirmed on creation.
type ProfileInference struct {
	ID                string  `json:"id"`
	Field             string  `json:"field"`
	Value             string  `json:"value"`
	Confidence        float64 `json:"confidence"`
	NeedsConfirmation bool    `json:"needs_confirmation"`
}

// CandidateProfile is the structured output of resume and role processing.
type CandidateProfile struct {
	TargetRole string             `json:"target_role"`
	Facts      []ProfileFact      `json:"facts"`
	Inferences []ProfileInference `json:"inferences"`
	Projects   []string           `json:"projects"`
	Skills     []string           `json:"skills"`
}

// ScenarioMode fixes the Coach policy for a confirmed scenario.
type ScenarioMode string

const (
	ScenarioStrict   ScenarioMode = "strict"
	ScenarioStandard ScenarioMode = "standard"
	ScenarioCoach    ScenarioMode = "coach"
)

// ScenarioQuestion describes one evidence-anchored interview question.
type ScenarioQuestion struct {
	ID               string       `json:"id"`
	Prompt           string       `json:"prompt"`
	Intent           string       `json:"intent"`
	EstimatedSeconds int          `json:"estimated_seconds"`
	Rubric           []string     `json:"rubric"`
	EvidenceIDs      []EvidenceID `json:"evidence_ids"`
	Generic          bool         `json:"generic"`
	MaxFollowUps     int          `json:"max_follow_ups"`
	EndCondition     string       `json:"end_condition"`
}

// Scenario is the confirmed, versioned plan used to create a session.
type Scenario struct {
	Template          string             `json:"template"`
	Mode              ScenarioMode       `json:"mode"`
	TimeBudgetSeconds int                `json:"time_budget_seconds"`
	PromptVersion     string             `json:"prompt_version"`
	Questions         []ScenarioQuestion `json:"questions"`
}

// InterviewerActionType identifies the next state-machine action.
type InterviewerActionType string

const (
	ActionFollowUp      InterviewerActionType = "follow_up"
	ActionCloseQuestion InterviewerActionType = "close_question"
	ActionNextQuestion  InterviewerActionType = "next_question"
	ActionFinishSession InterviewerActionType = "finish_session"
)

// SessionState is the state asserted by an interviewer action.
type SessionState string

const (
	SessionInterviewing     SessionState = "interviewing"
	SessionQuestionComplete SessionState = "question_complete"
	SessionComplete         SessionState = "session_complete"
)

// InterviewerAction is the only structured output accepted from Interviewer.
type InterviewerAction struct {
	Action       InterviewerActionType `json:"action"`
	QuestionID   string                `json:"question_id"`
	Message      string                `json:"message"`
	EvidenceIDs  []EvidenceID          `json:"evidence_ids"`
	SessionState SessionState          `json:"session_state"`
}

// CoachIntent identifies one of the six MVP Coach entry points.
type CoachIntent string

const (
	CoachExplainConcept  CoachIntent = "explain_concept"
	CoachGiveHint        CoachIntent = "give_hint"
	CoachAnswerStructure CoachIntent = "answer_structure"
	CoachCheckReasoning  CoachIntent = "check_reasoning"
	CoachExplainFailure  CoachIntent = "explain_failure"
	CoachAddToReview     CoachIntent = "add_to_review"
)

// HelpLevel is the graduated Coach assistance level.
type HelpLevel string

const (
	HelpL1 HelpLevel = "L1"
	HelpL2 HelpLevel = "L2"
	HelpL3 HelpLevel = "L3"
	HelpL4 HelpLevel = "L4"
)

// CoachResponse is Coach's policy-aware structured output.
type CoachResponse struct {
	Intent            CoachIntent `json:"intent"`
	HelpLevel         HelpLevel   `json:"help_level"`
	KnowledgeTags     []string    `json:"knowledge_tags"`
	RecommendedAction string      `json:"recommended_action"`
	PolicyNote        string      `json:"policy_note,omitempty"`
}

// EvaluationDimension is one of the fixed MVP scorecard dimensions.
type EvaluationDimension string

const (
	DimensionAnswerStructure       EvaluationDimension = "answer_structure"
	DimensionExperienceCredibility EvaluationDimension = "experience_credibility"
	DimensionTechnicalDepth        EvaluationDimension = "technical_depth"
	DimensionProblemClarification  EvaluationDimension = "problem_clarification"
	DimensionProblemSolving        EvaluationDimension = "problem_solving"
	DimensionCodeQuality           EvaluationDimension = "code_quality"
	DimensionTimeManagement        EvaluationDimension = "time_management"
	DimensionIndependence          EvaluationDimension = "independence"
)

// EvaluationFinding carries either a 1-5 score or an explicit not-applicable
// marker. NotApplicable is a pointer so a missing JSON field is distinguishable
// from false.
type EvaluationFinding struct {
	Dimension     EvaluationDimension `json:"dimension"`
	Score         *int                `json:"score,omitempty"`
	NotApplicable *bool               `json:"not_applicable,omitempty"`
	EvidenceIDs   []EvidenceID        `json:"evidence_ids"`
	Confidence    float64             `json:"confidence"`
	NextAction    string              `json:"next_action"`
}
