package contracts

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestPublishedSchemasAreValidAndComplete(t *testing.T) {
	t.Parallel()

	names := JSONSchemaNames()
	if len(names) != 5 {
		t.Fatalf("JSONSchemaNames count = %d, want 5", len(names))
	}

	for _, name := range names {
		name := name
		t.Run(string(name), func(t *testing.T) {
			t.Parallel()

			schema, ok := JSONSchema(name)
			if !ok {
				t.Fatalf("JSONSchema(%q) not found", name)
			}
			if !json.Valid(schema) {
				t.Fatalf("JSONSchema(%q) is not valid JSON", name)
			}

			var document map[string]any
			if err := json.Unmarshal(schema, &document); err != nil {
				t.Fatalf("decode JSONSchema(%q): %v", name, err)
			}
			if document["$schema"] == nil || document["title"] != string(name) {
				t.Fatalf("JSONSchema(%q) lacks schema dialect or matching title", name)
			}
			if document["additionalProperties"] != false {
				t.Fatalf("JSONSchema(%q) must reject unknown top-level fields", name)
			}
		})
	}

	first, _ := JSONSchema(SchemaCandidateProfile)
	first[0] = 'x'
	second, _ := JSONSchema(SchemaCandidateProfile)
	if second[0] != '{' {
		t.Fatal("JSONSchema returned shared mutable storage")
	}
}

func TestStrictDecodersAcceptValidContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		data   string
		decode func([]byte) (any, error)
	}{
		{
			name: "CandidateProfile",
			data: validProfileJSON,
			decode: func(data []byte) (any, error) {
				return DecodeCandidateProfile(data)
			},
		},
		{
			name: "Scenario",
			data: validScenarioJSON,
			decode: func(data []byte) (any, error) {
				return DecodeScenario(data)
			},
		},
		{
			name: "InterviewerAction",
			data: validInterviewerActionJSON,
			decode: func(data []byte) (any, error) {
				return DecodeInterviewerAction(data)
			},
		},
		{
			name: "CoachResponse",
			data: validCoachResponseJSON,
			decode: func(data []byte) (any, error) {
				return DecodeCoachResponse(data)
			},
		},
		{
			name: "EvaluationFinding",
			data: validEvaluationFindingJSON,
			decode: func(data []byte) (any, error) {
				return DecodeEvaluationFinding(data)
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := test.decode([]byte(test.data)); err != nil {
				t.Fatalf("decode valid %s: %v", test.name, err)
			}
		})
	}
}

func TestStrictDecodersRejectInvalidContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		data      string
		decode    func([]byte) (any, error)
		wantField string
	}{
		{
			name: "profile missing facts",
			data: `{
				"target_role":"Backend Engineer",
				"inferences":[],
				"projects":[],
				"skills":[]
			}`,
			decode: func(data []byte) (any, error) {
				return DecodeCandidateProfile(data)
			},
			wantField: "facts",
		},
		{
			name: "profile accepts no unknown fields",
			data: strings.TrimSuffix(validProfileJSON, "}") + `,"invented":true}`,
			decode: func(data []byte) (any, error) {
				return DecodeCandidateProfile(data)
			},
			wantField: "$",
		},
		{
			name: "scenario rejects unknown mode",
			data: strings.Replace(validScenarioJSON, `"mode":"strict"`, `"mode":"surprise"`, 1),
			decode: func(data []byte) (any, error) {
				return DecodeScenario(data)
			},
			wantField: "mode",
		},
		{
			name: "interviewer rejects blank evidence reference",
			data: strings.Replace(validInterviewerActionJSON, `"fact-1"`, `""`, 1),
			decode: func(data []byte) (any, error) {
				return DecodeInterviewerAction(data)
			},
			wantField: "evidence_ids[0]",
		},
		{
			name: "coach rejects invalid help level",
			data: strings.Replace(validCoachResponseJSON, `"L2"`, `"L5"`, 1),
			decode: func(data []byte) (any, error) {
				return DecodeCoachResponse(data)
			},
			wantField: "help_level",
		},
		{
			name: "scored finding requires evidence",
			data: strings.Replace(validEvaluationFindingJSON, `["answer-Q1"]`, `[]`, 1),
			decode: func(data []byte) (any, error) {
				return DecodeEvaluationFinding(data)
			},
			wantField: "evidence_ids",
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := test.decode([]byte(test.data))
			if err == nil {
				t.Fatal("invalid contract unexpectedly decoded")
			}
			if !domainerr.IsCode(err, domainerr.CodeValidation) {
				t.Fatalf("decode error = %v, want validation code", err)
			}

			var violation *Violation
			if !errors.As(err, &violation) {
				t.Fatalf("decode error %T does not retain a contract violation", err)
			}
			if !violationHasField(violation, test.wantField) {
				t.Fatalf("violation fields = %#v, want %q", violation.Issues, test.wantField)
			}
		})
	}
}

func TestProfileInferenceMustRemainUnconfirmed(t *testing.T) {
	t.Parallel()

	data := strings.Replace(
		validProfileJSON,
		`"inferences":[]`,
		`"inferences":[{
			"id":"inference-1",
			"field":"leadership",
			"value":"led five engineers",
			"confidence":0.7,
			"needs_confirmation":false
		}]`,
		1,
	)

	_, err := DecodeCandidateProfile([]byte(data))
	if err == nil {
		t.Fatal("confirmed model inference unexpectedly validated")
	}

	var violation *Violation
	if !errors.As(err, &violation) ||
		!violationHasField(violation, "inferences[0].needs_confirmation") {
		t.Fatalf("unexpected inference error: %v", err)
	}
}

func TestGenericScenarioQuestionMayHaveNoEvidence(t *testing.T) {
	t.Parallel()

	data := strings.NewReplacer(
		`"evidence_ids":["fact-1"]`, `"evidence_ids":[]`,
		`"generic":false`, `"generic":true`,
	).Replace(validScenarioJSON)

	if _, err := DecodeScenario([]byte(data)); err != nil {
		t.Fatalf("generic scenario question with explicit empty evidence: %v", err)
	}
}

func TestInterviewerActionMustMatchSessionState(t *testing.T) {
	t.Parallel()

	data := strings.Replace(
		validInterviewerActionJSON,
		`"session_state":"interviewing"`,
		`"session_state":"session_complete"`,
		1,
	)

	_, err := DecodeInterviewerAction([]byte(data))
	if err == nil {
		t.Fatal("follow-up with completed session state unexpectedly validated")
	}
	var violation *Violation
	if !errors.As(err, &violation) || !violationHasField(violation, "session_state") {
		t.Fatalf("unexpected action/state error: %v", err)
	}
}

func TestEvaluationFindingRequiresExactlyOneResultKind(t *testing.T) {
	t.Parallel()

	value := true
	score := 4
	finding := EvaluationFinding{
		Dimension:     DimensionCodeQuality,
		Score:         &score,
		NotApplicable: &value,
		EvidenceIDs:   []EvidenceID{"code-run-1"},
		Confidence:    0.9,
		NextAction:    "保持当前测试策略。",
	}

	err := finding.Validate()
	if err == nil {
		t.Fatal("finding with score and not-applicable unexpectedly validated")
	}
	if !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("finding error = %v, want validation code", err)
	}
}

func TestEvaluationFindingRejectsNonFiniteConfidence(t *testing.T) {
	t.Parallel()

	score := 4
	finding := EvaluationFinding{
		Dimension:   DimensionTechnicalDepth,
		Score:       &score,
		EvidenceIDs: []EvidenceID{"answer-Q1"},
		Confidence:  math.NaN(),
		NextAction:  "补充故障恢复的量化结果。",
	}

	err := finding.Validate()
	if err == nil {
		t.Fatal("finding with NaN confidence unexpectedly validated")
	}
	var violation *Violation
	if !errors.As(err, &violation) || !violationHasField(violation, "confidence") {
		t.Fatalf("unexpected confidence error: %v", err)
	}
}

func violationHasField(violation *Violation, field string) bool {
	for _, issue := range violation.Issues {
		if issue.Field == field {
			return true
		}
	}
	return false
}

const validProfileJSON = `{
  "target_role":"Backend Engineer",
  "facts":[{
    "id":"fact-1",
    "field":"project",
    "value":"Built a payment service",
    "source_span":{"start":0,"end":23,"text":"Built a payment service"}
  }],
  "inferences":[],
  "projects":["Payment service"],
  "skills":["Go","PostgreSQL"]
}`

const validScenarioJSON = `{
  "template":"project_deep_dive",
  "mode":"strict",
  "time_budget_seconds":1200,
  "prompt_version":"scenario-v1",
  "questions":[{
    "id":"Q1",
    "prompt":"How did you make the payment service reliable?",
    "intent":"Assess reliability trade-offs",
    "estimated_seconds":300,
    "rubric":["Names a failure mode","Explains a trade-off"],
    "evidence_ids":["fact-1"],
    "generic":false,
    "max_follow_ups":2,
    "end_condition":"A concrete trade-off is explained"
  }]
}`

const validInterviewerActionJSON = `{
  "action":"follow_up",
  "question_id":"Q1",
  "message":"Which failure mode was hardest to recover from?",
  "evidence_ids":["fact-1"],
  "session_state":"interviewing"
}`

const validCoachResponseJSON = `{
  "intent":"give_hint",
  "help_level":"L2",
  "knowledge_tags":["cache consistency"],
  "recommended_action":"先说明读写路径，再比较一致性策略。",
  "policy_note":"不提供当前题完整答案。"
}`

const validEvaluationFindingJSON = `{
  "dimension":"technical_depth",
  "score":4,
  "evidence_ids":["answer-Q1"],
  "confidence":0.85,
  "next_action":"补充故障恢复的量化结果。"
}`
