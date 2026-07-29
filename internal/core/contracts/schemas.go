package contracts

import "encoding/json"

// SchemaName identifies one published structured-output JSON Schema.
type SchemaName string

const (
	SchemaCandidateProfile  SchemaName = "CandidateProfile"
	SchemaScenario          SchemaName = "Scenario"
	SchemaInterviewerAction SchemaName = "InterviewerAction"
	SchemaCoachResponse     SchemaName = "CoachResponse"
	SchemaEvaluationFinding SchemaName = "EvaluationFinding"
)

var schemaOrder = []SchemaName{
	SchemaCandidateProfile,
	SchemaScenario,
	SchemaInterviewerAction,
	SchemaCoachResponse,
	SchemaEvaluationFinding,
}

var schemas = map[SchemaName]json.RawMessage{
	SchemaCandidateProfile:  json.RawMessage(candidateProfileSchema),
	SchemaScenario:          json.RawMessage(scenarioSchema),
	SchemaInterviewerAction: json.RawMessage(interviewerActionSchema),
	SchemaCoachResponse:     json.RawMessage(coachResponseSchema),
	SchemaEvaluationFinding: json.RawMessage(evaluationFindingSchema),
}

// JSONSchema returns a defensive copy of a published JSON Schema.
func JSONSchema(name SchemaName) (json.RawMessage, bool) {
	schema, ok := schemas[name]
	if !ok {
		return nil, false
	}
	result := make(json.RawMessage, len(schema))
	copy(result, schema)
	return result, true
}

// JSONSchemaNames returns the stable schema publication order.
func JSONSchemaNames() []SchemaName {
	result := make([]SchemaName, len(schemaOrder))
	copy(result, schemaOrder)
	return result
}

const candidateProfileSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://interviewcraft.local/schemas/candidate-profile.json",
  "title": "CandidateProfile",
  "type": "object",
  "additionalProperties": false,
  "required": ["target_role", "facts", "inferences", "projects", "skills"],
  "properties": {
    "target_role": {"type": "string", "minLength": 1},
    "facts": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "field", "value", "source_span"],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "field": {"type": "string", "minLength": 1},
          "value": {"type": "string", "minLength": 1},
          "source_span": {
            "type": "object",
            "additionalProperties": false,
            "required": ["start", "end", "text"],
            "properties": {
              "start": {"type": "integer", "minimum": 0},
              "end": {"type": "integer", "minimum": 1},
              "text": {"type": "string", "minLength": 1}
            }
          }
        }
      }
    },
    "inferences": {
      "type": "array",
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["id", "field", "value", "confidence", "needs_confirmation"],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "field": {"type": "string", "minLength": 1},
          "value": {"type": "string", "minLength": 1},
          "confidence": {"type": "number", "minimum": 0, "maximum": 1},
          "needs_confirmation": {"const": true}
        }
      }
    },
    "projects": {
      "type": "array",
      "items": {"type": "string", "minLength": 1}
    },
    "skills": {
      "type": "array",
      "items": {"type": "string", "minLength": 1}
    }
  }
}`

const scenarioSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://interviewcraft.local/schemas/scenario.json",
  "title": "Scenario",
  "type": "object",
  "additionalProperties": false,
  "required": ["template", "mode", "time_budget_seconds", "prompt_version", "questions"],
  "properties": {
    "template": {"type": "string", "minLength": 1},
    "mode": {"enum": ["strict", "standard", "coach"]},
    "time_budget_seconds": {"type": "integer", "minimum": 1},
    "prompt_version": {"type": "string", "minLength": 1},
    "questions": {
      "type": "array",
      "minItems": 1,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": [
          "id", "prompt", "intent", "estimated_seconds", "rubric",
          "evidence_ids", "generic", "max_follow_ups", "end_condition"
        ],
        "properties": {
          "id": {"type": "string", "minLength": 1},
          "prompt": {"type": "string", "minLength": 1},
          "intent": {"type": "string", "minLength": 1},
          "estimated_seconds": {"type": "integer", "minimum": 1},
          "rubric": {
            "type": "array",
            "minItems": 1,
            "items": {"type": "string", "minLength": 1}
          },
          "evidence_ids": {
            "type": "array",
            "items": {"type": "string", "minLength": 1}
          },
          "generic": {"type": "boolean"},
          "max_follow_ups": {"type": "integer", "minimum": 0},
          "end_condition": {"type": "string", "minLength": 1}
        },
        "anyOf": [
          {"properties": {"generic": {"const": true}}},
          {"properties": {"evidence_ids": {"minItems": 1}}}
        ]
      }
    }
  }
}`

const interviewerActionSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://interviewcraft.local/schemas/interviewer-action.json",
  "title": "InterviewerAction",
  "type": "object",
  "additionalProperties": false,
  "required": ["action", "question_id", "message", "evidence_ids", "session_state"],
  "properties": {
    "action": {
      "enum": ["follow_up", "close_question", "next_question", "finish_session"]
    },
    "question_id": {"type": "string", "minLength": 1},
    "message": {"type": "string", "minLength": 1},
    "evidence_ids": {
      "type": "array",
      "items": {"type": "string", "minLength": 1}
    },
    "session_state": {
      "enum": ["interviewing", "question_complete", "session_complete"]
    }
  }
}`

const coachResponseSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://interviewcraft.local/schemas/coach-response.json",
  "title": "CoachResponse",
  "type": "object",
  "additionalProperties": false,
  "required": ["intent", "help_level", "knowledge_tags", "recommended_action"],
  "properties": {
    "intent": {
      "enum": [
        "explain_concept", "give_hint", "answer_structure",
        "check_reasoning", "explain_failure", "add_to_review"
      ]
    },
    "help_level": {"enum": ["L1", "L2", "L3", "L4"]},
    "knowledge_tags": {
      "type": "array",
      "minItems": 1,
      "items": {"type": "string", "minLength": 1}
    },
    "recommended_action": {"type": "string", "minLength": 1},
    "policy_note": {"type": "string"}
  }
}`

const evaluationFindingSchema = `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://interviewcraft.local/schemas/evaluation-finding.json",
  "title": "EvaluationFinding",
  "type": "object",
  "additionalProperties": false,
  "required": ["dimension", "evidence_ids", "confidence", "next_action"],
  "properties": {
    "dimension": {
      "enum": [
        "answer_structure", "experience_credibility", "technical_depth",
        "problem_clarification", "problem_solving", "code_quality",
        "time_management", "independence"
      ]
    },
    "score": {"type": "integer", "minimum": 1, "maximum": 5},
    "not_applicable": {"const": true},
    "evidence_ids": {
      "type": "array",
      "items": {"type": "string", "minLength": 1}
    },
    "confidence": {"type": "number", "minimum": 0, "maximum": 1},
    "next_action": {"type": "string", "minLength": 1}
  },
  "oneOf": [
    {"required": ["score"], "not": {"required": ["not_applicable"]}},
    {"required": ["not_applicable"], "not": {"required": ["score"]}}
  ]
}`
