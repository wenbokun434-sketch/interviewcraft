// Package scenario owns template-driven, evidence-safe Scenario planning.
package scenario

import (
	"context"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
)

// PromptVersionBase identifies the planner instructions and validation policy.
const PromptVersionBase = "scenario-planner-v1"

// Template is one embedded MVP scenario definition.
type Template struct {
	ID                       string                 `json:"id"`
	Label                    string                 `json:"label"`
	Description              string                 `json:"description"`
	DefaultMode              contracts.ScenarioMode `json:"default_mode"`
	DefaultTimeBudgetSeconds int                    `json:"default_time_budget_seconds"`
	DefaultMaxFollowUps      int                    `json:"default_max_follow_ups"`
	QuestionGuidance         []string               `json:"question_guidance"`
	RubricGuidance           []string               `json:"rubric_guidance"`
}

// JDMapping connects one JD requirement to confirmed facts or an explicit gap.
type JDMapping struct {
	Requirement string                 `json:"requirement"`
	EvidenceIDs []contracts.EvidenceID `json:"evidence_ids"`
	Gap         string                 `json:"gap"`
}

// GeneratedPlan is the strict Provider output before local evidence policy.
type GeneratedPlan struct {
	Scenario   contracts.Scenario `json:"scenario"`
	JDMappings []JDMapping        `json:"jd_mappings"`
}

// GenerationInput contains only confirmed candidate data. Inferences are
// deliberately absent from this type and therefore cannot reach the Provider.
type GenerationInput struct {
	Template      Template                `json:"template"`
	Mode          contracts.ScenarioMode  `json:"mode"`
	TimeBudget    int                     `json:"time_budget_seconds"`
	TargetRole    string                  `json:"target_role"`
	Facts         []contracts.ProfileFact `json:"confirmed_facts"`
	Projects      []string                `json:"confirmed_projects"`
	Skills        []string                `json:"confirmed_skills"`
	JD            string                  `json:"jd,omitempty"`
	PromptVersion string                  `json:"prompt_version"`
}

// Generator is the structured Provider boundary used by the core Planner.
type Generator interface {
	Generate(context.Context, GenerationInput) (GeneratedPlan, error)
}

// Repository persists only confirmed, immutable Scenario versions.
type Repository interface {
	SaveScenario(
		context.Context,
		string,
		string,
		contracts.Scenario,
		time.Time,
	) error
	GetScenario(
		context.Context,
		string,
	) (contracts.Scenario, bool, error)
	GetProfileAggregate(
		context.Context,
		string,
	) (coreprofile.Aggregate, bool, error)
}

// Request selects a template and confirmed candidate input for generation.
type Request struct {
	PlanID            string
	Profile           coreprofile.Aggregate
	TemplateID        string
	Mode              contracts.ScenarioMode
	TimeBudgetSeconds int
	JD                string
}

// Plan is an editable local draft or a locked persisted version.
type Plan struct {
	ID                string
	BaseID            string
	ProfileID         string
	Scenario          contracts.Scenario
	JDMappings        []JDMapping
	EvidenceIDs       []contracts.EvidenceID
	Revision          int
	ManualQuestionIDs []string
	ManualRules       bool
	JDProvided        bool
	Locked            bool
	ConfirmedAt       *time.Time
}

// Progress identifies one generation or persistence stage.
type Progress struct {
	Stage string
}

// Observer receives typed Planner lifecycle states.
type Observer func(async.State[Progress])

// Controls captures the core Start/Back policy consumed by P-03.
type Controls struct {
	GenerateEnabled bool
	StartEnabled    bool
	BackEnabled     bool
}
