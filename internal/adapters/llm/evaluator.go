package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreevaluation "github.com/interviewcraft/interviewcraft/internal/core/evaluation"
)

const evaluatorSystemPrompt = `You are the InterviewCraft evidence-first Evaluator.
Return only one EvaluationDraft JSON object matching the supplied schema.
Use only confirmed_facts, persisted session_events, executed code_runs, surviving coach_events, question constraints, and allowed_evidence_ids.
Never infer from drafts, unconfirmed profile inferences, deleted Coach history, or evidence not present in allowed_evidence_ids.
Every score, review, cross-source insight, and practice recommendation must cite one or more resolvable allowed_evidence_ids.
Use all eight fixed score dimensions exactly once when evidence supports them. With no code_runs, code_quality must use not_applicable=true.
Do not make personality, hiring, employability, or person-level judgments.
Transfer success/failure is intentionally absent from this output; the core service only records whether a same-question answer or code event exists within five minutes.
When evidence is insufficient, omit the unsupported draft item; the core service will render “不足以判断”.
Keep practice_plan executable: topic, mode, duration_minutes, completion_criteria, and evidence_ids.`

const evaluationDraftSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "EvaluationDraft",
  "type": "object",
  "additionalProperties": false,
  "required": [
    "question_reviews", "findings",
    "cross_source_insights", "practice_plan"
  ],
  "$defs": {
    "evidence_ids": {
      "type": "array",
      "items": {"type": "string", "minLength": 1}
    },
    "insight": {
      "type": "object",
      "additionalProperties": false,
      "required": ["text", "evidence_ids", "confidence"],
      "properties": {
        "text": {"type": "string", "minLength": 1},
        "evidence_ids": {"$ref": "#/$defs/evidence_ids"},
        "confidence": {"type": "number", "minimum": 0, "maximum": 1}
      }
    },
    "finding": {
      "type": "object",
      "additionalProperties": false,
      "required": ["dimension", "evidence_ids", "confidence", "next_action"],
      "properties": {
        "dimension": {
          "enum": [
            "answer_structure", "experience_credibility",
            "technical_depth", "problem_clarification",
            "problem_solving", "code_quality",
            "time_management", "independence"
          ]
        },
        "score": {"type": "integer", "minimum": 1, "maximum": 5},
        "not_applicable": {"const": true},
        "evidence_ids": {"$ref": "#/$defs/evidence_ids"},
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "next_action": {"type": "string", "minLength": 1}
      },
      "oneOf": [
        {"required": ["score"], "not": {"required": ["not_applicable"]}},
        {"required": ["not_applicable"], "not": {"required": ["score"]}}
      ]
    }
  },
  "properties": {
    "question_reviews": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["question_id", "summary", "next_action"],
        "properties": {
          "question_id": {"type": "string", "minLength": 1},
          "summary": {"$ref": "#/$defs/insight"},
          "next_action": {"$ref": "#/$defs/insight"}
        }
      }
    },
    "findings": {
      "type": "array",
      "items": {"$ref": "#/$defs/finding"}
    },
    "cross_source_insights": {
      "type": "array",
      "items": {"$ref": "#/$defs/insight"}
    },
    "practice_plan": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": [
          "topic", "mode", "duration_minutes",
          "completion_criteria", "evidence_ids"
        ],
        "properties": {
          "topic": {"type": "string", "minLength": 1},
          "mode": {"enum": ["strict", "standard", "coach"]},
          "duration_minutes": {"type": "integer", "minimum": 1},
          "completion_criteria": {"type": "string", "minLength": 1},
          "evidence_ids": {"$ref": "#/$defs/evidence_ids"}
        }
      }
    }
  }
}`

// Evaluator adapts the shared structured Provider to report drafts.
type Evaluator struct {
	generator Generator
}

// NewEvaluator constructs the Provider-backed Evaluator.
func NewEvaluator(generator Generator) *Evaluator {
	return &Evaluator{generator: generator}
}

// Evaluate implements evaluation.Provider with one strict Schema retry.
func (evaluator *Evaluator) Evaluate(
	ctx context.Context,
	input coreevaluation.Input,
) (coreevaluation.Draft, error) {
	if evaluator == nil || evaluator.generator == nil {
		return coreevaluation.Draft{}, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"generate evaluation draft",
			"Evaluator Provider 尚未初始化。",
			"配置并测试模型 Provider 后重试。",
			true,
		)
	}
	if err := validateEvaluationInput(input); err != nil {
		return coreevaluation.Draft{}, err
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return coreevaluation.Draft{}, domainerr.Wrap(
			domainerr.CodeValidation,
			"encode evaluation input",
			"Evaluator Provider",
			"无法准备评估上下文。",
			"检查已持久化会话后重试。",
			false,
			err,
		)
	}
	return GenerateStructured(
		ctx,
		evaluator.generator,
		Request{
			SchemaName: "EvaluationDraft",
			Schema:     json.RawMessage(evaluationDraftSchema),
			Messages: []Message{
				{Role: RoleSystem, Content: evaluatorSystemPrompt},
				{Role: RoleUser, Content: string(payload)},
			},
		},
		coreevaluation.DecodeDraft,
	)
}

func validateEvaluationInput(input coreevaluation.Input) error {
	if strings.TrimSpace(input.SessionID) == "" ||
		strings.TrimSpace(input.Template) == "" ||
		input.StartedAt.IsZero() ||
		input.CompletedAt.IsZero() ||
		input.CompletedAt.Before(input.StartedAt) {
		return invalidEvaluationInput("session metadata is invalid")
	}
	if input.Questions == nil || len(input.Questions) == 0 ||
		input.ConfirmedFacts == nil ||
		input.ConfirmedSkills == nil ||
		input.Events == nil ||
		input.CoachEvents == nil ||
		input.CodeRuns == nil ||
		input.AllowedEvidenceIDs == nil {
		return invalidEvaluationInput("context arrays must be explicit")
	}
	scenario := contracts.Scenario{
		Template:          input.Template,
		Mode:              input.Mode,
		TimeBudgetSeconds: 1,
		PromptVersion:     "evaluation-input-v1",
		Questions:         input.Questions,
	}
	if err := scenario.Validate(); err != nil {
		return invalidEvaluationInput(err.Error())
	}
	allowed := make(map[contracts.EvidenceID]struct{},
		len(input.AllowedEvidenceIDs))
	for _, id := range input.AllowedEvidenceIDs {
		if strings.TrimSpace(string(id)) == "" {
			return invalidEvaluationInput("allowed evidence id is blank")
		}
		if _, duplicate := allowed[id]; duplicate {
			return invalidEvaluationInput(
				fmt.Sprintf("duplicate allowed evidence %q", id),
			)
		}
		allowed[id] = struct{}{}
	}
	requireAllowed := func(id contracts.EvidenceID, kind string) error {
		if _, found := allowed[id]; !found {
			return invalidEvaluationInput(kind + " is not allowed evidence")
		}
		return nil
	}
	for _, fact := range input.ConfirmedFacts {
		if err := requireAllowed(fact.ID, "profile fact"); err != nil {
			return err
		}
		if strings.TrimSpace(fact.SourceSpan.Text) == "" {
			return invalidEvaluationInput("confirmed fact lacks source span")
		}
	}
	for _, event := range input.Events {
		if err := requireAllowed(event.ID, "session event"); err != nil {
			return err
		}
		if strings.TrimSpace(event.Content) == "" ||
			event.OccurredAt.IsZero() {
			return invalidEvaluationInput("session event is incomplete")
		}
	}
	for _, event := range input.CoachEvents {
		if err := requireAllowed(event.ID, "Coach event"); err != nil {
			return err
		}
		if event.Tags == nil || event.OccurredAt.IsZero() {
			return invalidEvaluationInput("Coach event is incomplete")
		}
	}
	for _, run := range input.CodeRuns {
		if err := requireAllowed(run.SubmissionID, "code submission"); err != nil {
			return err
		}
		if err := requireAllowed(run.SnapshotID, "code snapshot"); err != nil {
			return err
		}
		if strings.TrimSpace(run.Source) == "" || run.OccurredAt.IsZero() {
			return invalidEvaluationInput("code run is incomplete")
		}
	}
	return nil
}

func invalidEvaluationInput(reason string) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"validate evaluation input",
		"Evaluator Provider",
		"评估上下文无效。",
		"恢复已持久化会话后重试。",
		false,
		errors.New(reason),
	)
}

var _ coreevaluation.Provider = (*Evaluator)(nil)
