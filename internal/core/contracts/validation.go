package contracts

import (
	"fmt"
	"math"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// ValidationIssue identifies one rejected contract field.
type ValidationIssue struct {
	Field  string
	Reason string
}

// Violation is retained as the cause of a safe domain validation error.
type Violation struct {
	Contract string
	Issues   []ValidationIssue
}

func (v *Violation) Error() string {
	if v == nil {
		return ""
	}
	parts := make([]string, 0, len(v.Issues))
	for _, issue := range v.Issues {
		parts = append(parts, issue.Field+": "+issue.Reason)
	}
	return v.Contract + " contract violation: " + strings.Join(parts, "; ")
}

// Validate checks the complete CandidateProfile contract.
func (p CandidateProfile) Validate() error {
	var issues []ValidationIssue
	requiredText(&issues, "target_role", p.TargetRole)
	if p.Facts == nil {
		addIssue(&issues, "facts", "is required")
	} else if len(p.Facts) == 0 {
		addIssue(&issues, "facts", "must contain at least one source-backed fact")
	}
	if p.Inferences == nil {
		addIssue(&issues, "inferences", "is required; use an empty array when none exist")
	}
	if p.Projects == nil {
		addIssue(&issues, "projects", "is required; use an empty array when none exist")
	}
	if p.Skills == nil {
		addIssue(&issues, "skills", "is required; use an empty array when none exist")
	}

	for index, fact := range p.Facts {
		prefix := fmt.Sprintf("facts[%d]", index)
		requiredEvidenceID(&issues, prefix+".id", fact.ID)
		requiredText(&issues, prefix+".field", fact.Field)
		requiredText(&issues, prefix+".value", fact.Value)
		if fact.SourceSpan.Start < 0 {
			addIssue(&issues, prefix+".source_span.start", "must be zero or greater")
		}
		if fact.SourceSpan.End <= fact.SourceSpan.Start {
			addIssue(&issues, prefix+".source_span.end", "must be greater than start")
		}
		requiredText(&issues, prefix+".source_span.text", fact.SourceSpan.Text)
	}

	for index, inference := range p.Inferences {
		prefix := fmt.Sprintf("inferences[%d]", index)
		requiredText(&issues, prefix+".id", inference.ID)
		requiredText(&issues, prefix+".field", inference.Field)
		requiredText(&issues, prefix+".value", inference.Value)
		confidence(&issues, prefix+".confidence", inference.Confidence)
		if !inference.NeedsConfirmation {
			addIssue(&issues, prefix+".needs_confirmation", "must be true for an unconfirmed inference")
		}
	}
	nonBlankStrings(&issues, "projects", p.Projects)
	nonBlankStrings(&issues, "skills", p.Skills)

	return validationResult("CandidateProfile", issues)
}

// Validate checks the complete Scenario contract.
func (s Scenario) Validate() error {
	var issues []ValidationIssue
	requiredText(&issues, "template", s.Template)
	if !validScenarioMode(s.Mode) {
		addIssue(&issues, "mode", "must be strict, standard, or coach")
	}
	if s.TimeBudgetSeconds <= 0 {
		addIssue(&issues, "time_budget_seconds", "must be greater than zero")
	}
	requiredText(&issues, "prompt_version", s.PromptVersion)
	if s.Questions == nil {
		addIssue(&issues, "questions", "is required")
	} else if len(s.Questions) == 0 {
		addIssue(&issues, "questions", "must contain at least one question")
	}

	for index, question := range s.Questions {
		prefix := fmt.Sprintf("questions[%d]", index)
		requiredText(&issues, prefix+".id", question.ID)
		requiredText(&issues, prefix+".prompt", question.Prompt)
		requiredText(&issues, prefix+".intent", question.Intent)
		if question.EstimatedSeconds <= 0 {
			addIssue(&issues, prefix+".estimated_seconds", "must be greater than zero")
		}
		if question.Rubric == nil || len(question.Rubric) == 0 {
			addIssue(&issues, prefix+".rubric", "must contain at least one scoring criterion")
		} else {
			nonBlankStrings(&issues, prefix+".rubric", question.Rubric)
		}
		evidenceIDs(&issues, prefix+".evidence_ids", question.EvidenceIDs, question.Generic)
		if question.MaxFollowUps < 0 {
			addIssue(&issues, prefix+".max_follow_ups", "must be zero or greater")
		}
		requiredText(&issues, prefix+".end_condition", question.EndCondition)
	}

	return validationResult("Scenario", issues)
}

// Validate checks the complete InterviewerAction contract.
func (a InterviewerAction) Validate() error {
	var issues []ValidationIssue
	if !validInterviewerAction(a.Action) {
		addIssue(&issues, "action", "contains an unsupported action")
	}
	requiredText(&issues, "question_id", a.QuestionID)
	requiredText(&issues, "message", a.Message)
	evidenceIDs(&issues, "evidence_ids", a.EvidenceIDs, true)
	if !validSessionState(a.SessionState) {
		addIssue(&issues, "session_state", "contains an unsupported state")
	}
	if validInterviewerAction(a.Action) && validSessionState(a.SessionState) &&
		!actionMatchesState(a.Action, a.SessionState) {
		addIssue(&issues, "session_state", "does not match the requested action")
	}
	return validationResult("InterviewerAction", issues)
}

// Validate checks the complete CoachResponse contract.
func (r CoachResponse) Validate() error {
	var issues []ValidationIssue
	if !validCoachIntent(r.Intent) {
		addIssue(&issues, "intent", "contains an unsupported intent")
	}
	if !validHelpLevel(r.HelpLevel) {
		addIssue(&issues, "help_level", "must be L1, L2, L3, or L4")
	}
	if r.KnowledgeTags == nil || len(r.KnowledgeTags) == 0 {
		addIssue(&issues, "knowledge_tags", "must contain at least one topic")
	} else {
		nonBlankStrings(&issues, "knowledge_tags", r.KnowledgeTags)
	}
	requiredText(&issues, "recommended_action", r.RecommendedAction)
	return validationResult("CoachResponse", issues)
}

// Validate checks the complete EvaluationFinding contract.
func (f EvaluationFinding) Validate() error {
	var issues []ValidationIssue
	if !validEvaluationDimension(f.Dimension) {
		addIssue(&issues, "dimension", "contains an unsupported scorecard dimension")
	}
	if (f.Score == nil) == (f.NotApplicable == nil) {
		addIssue(&issues, "score", "provide exactly one of score or not_applicable")
	}
	if f.Score != nil && (*f.Score < 1 || *f.Score > 5) {
		addIssue(&issues, "score", "must be between 1 and 5")
	}
	if f.NotApplicable != nil && !*f.NotApplicable {
		addIssue(&issues, "not_applicable", "must be true when provided")
	}
	evidenceIDs(&issues, "evidence_ids", f.EvidenceIDs, true)
	if f.Score != nil && len(f.EvidenceIDs) == 0 {
		addIssue(&issues, "evidence_ids", "a scored finding requires evidence")
	}
	confidence(&issues, "confidence", f.Confidence)
	requiredText(&issues, "next_action", f.NextAction)
	return validationResult("EvaluationFinding", issues)
}

func validationResult(contract string, issues []ValidationIssue) error {
	if len(issues) == 0 {
		return nil
	}
	violation := &Violation{Contract: contract, Issues: issues}
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"validate "+contract,
		"",
		"结构化数据不符合 "+contract+" 契约。",
		"修正标记字段后重试。",
		false,
		violation,
	)
}

func addIssue(issues *[]ValidationIssue, field, reason string) {
	*issues = append(*issues, ValidationIssue{Field: field, Reason: reason})
}

func requiredText(issues *[]ValidationIssue, field, value string) {
	if strings.TrimSpace(value) == "" {
		addIssue(issues, field, "must not be blank")
	}
}

func requiredEvidenceID(issues *[]ValidationIssue, field string, value EvidenceID) {
	if strings.TrimSpace(string(value)) == "" {
		addIssue(issues, field, "must reference non-blank evidence")
	}
}

func confidence(issues *[]ValidationIssue, field string, value float64) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value > 1 {
		addIssue(issues, field, "must be between 0 and 1")
	}
}

func nonBlankStrings(issues *[]ValidationIssue, field string, values []string) {
	for index, value := range values {
		requiredText(issues, fmt.Sprintf("%s[%d]", field, index), value)
	}
}

func evidenceIDs(issues *[]ValidationIssue, field string, values []EvidenceID, allowEmpty bool) {
	if values == nil {
		addIssue(issues, field, "is required; use an empty array when no evidence applies")
		return
	}
	if len(values) == 0 && !allowEmpty {
		addIssue(issues, field, "requires evidence or an explicit generic marker")
	}
	for index, value := range values {
		requiredEvidenceID(issues, fmt.Sprintf("%s[%d]", field, index), value)
	}
}

func validScenarioMode(value ScenarioMode) bool {
	return value == ScenarioStrict || value == ScenarioStandard || value == ScenarioCoach
}

func validInterviewerAction(value InterviewerActionType) bool {
	switch value {
	case ActionFollowUp, ActionCloseQuestion, ActionNextQuestion, ActionFinishSession:
		return true
	default:
		return false
	}
}

func validSessionState(value SessionState) bool {
	return value == SessionInterviewing || value == SessionQuestionComplete || value == SessionComplete
}

func actionMatchesState(action InterviewerActionType, state SessionState) bool {
	switch action {
	case ActionFollowUp, ActionNextQuestion:
		return state == SessionInterviewing
	case ActionCloseQuestion:
		return state == SessionQuestionComplete
	case ActionFinishSession:
		return state == SessionComplete
	default:
		return false
	}
}

func validCoachIntent(value CoachIntent) bool {
	switch value {
	case CoachExplainConcept, CoachGiveHint, CoachAnswerStructure,
		CoachCheckReasoning, CoachExplainFailure, CoachAddToReview:
		return true
	default:
		return false
	}
}

func validHelpLevel(value HelpLevel) bool {
	return value == HelpL1 || value == HelpL2 || value == HelpL3 || value == HelpL4
}

func validEvaluationDimension(value EvaluationDimension) bool {
	switch value {
	case DimensionAnswerStructure, DimensionExperienceCredibility,
		DimensionTechnicalDepth, DimensionProblemClarification,
		DimensionProblemSolving, DimensionCodeQuality,
		DimensionTimeManagement, DimensionIndependence:
		return true
	default:
		return false
	}
}
