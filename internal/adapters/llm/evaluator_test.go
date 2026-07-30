package llm

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreevaluation "github.com/interviewcraft/interviewcraft/internal/core/evaluation"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestEvaluatorBuildsEvidenceOnlyRequestAndRetriesSchema(t *testing.T) {
	t.Parallel()

	input := evaluatorInput()
	valid := coreevaluation.Draft{
		QuestionReviews: []coreevaluation.DraftQuestionReview{},
		Findings:        []contracts.EvaluationFinding{},
		CrossInsights:   []coreevaluation.DraftInsight{},
		PracticePlan:    []coreevaluation.DraftPracticeItem{},
	}
	validJSON, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	generator := &sequenceGenerator{
		responses: [][]byte{
			[]byte(`{"findings":[]}`),
			validJSON,
		},
	}
	draft, err := NewEvaluator(generator).Evaluate(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !reflect.DeepEqual(draft, valid) {
		t.Fatalf("draft=%#v want=%#v", draft, valid)
	}
	if len(generator.requests) != 2 {
		t.Fatalf("requests=%d", len(generator.requests))
	}
	request := generator.requests[0]
	if request.SchemaName != "EvaluationDraft" ||
		len(request.Messages) != 2 ||
		!json.Valid(request.Schema) {
		t.Fatalf("request=%#v", request)
	}
	for _, rule := range []string{
		"deleted Coach history",
		"allowed_evidence_ids",
		"personality, hiring",
		"five minutes",
	} {
		if !strings.Contains(request.Messages[0].Content, rule) {
			t.Errorf("system prompt missing %q", rule)
		}
	}
	payload := request.Messages[1].Content
	if strings.Contains(payload, "unconfirmed") ||
		strings.Contains(payload, "draft") ||
		!strings.Contains(payload, `"answer-1"`) ||
		!strings.Contains(payload, `"fact-service"`) {
		t.Fatalf("unsafe or incomplete payload: %s", payload)
	}
	if len(generator.requests[1].Messages) != 3 {
		t.Fatalf("retry=%#v", generator.requests[1].Messages)
	}
}

func TestEvaluatorRequiresProviderAndCompleteEvidenceInput(t *testing.T) {
	t.Parallel()

	if _, err := NewEvaluator(nil).Evaluate(
		context.Background(),
		evaluatorInput(),
	); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil Provider error=%v", err)
	}
	invalid := evaluatorInput()
	invalid.AllowedEvidenceIDs = []contracts.EvidenceID{"fact-service"}
	if _, err := NewEvaluator(&sequenceGenerator{}).Evaluate(
		context.Background(),
		invalid,
	); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("invalid input error=%v", err)
	}
}

func evaluatorInput() coreevaluation.Input {
	started := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	return coreevaluation.Input{
		SessionID:   "session-1",
		Template:    "project_deep_dive",
		Mode:        contracts.ScenarioStrict,
		StartedAt:   started,
		CompletedAt: started.Add(15 * time.Minute),
		Questions: []contracts.ScenarioQuestion{{
			ID:               "Q1",
			Prompt:           "Explain one service trade-off.",
			Intent:           "technical depth",
			EstimatedSeconds: 300,
			Rubric:           []string{"names a trade-off"},
			EvidenceIDs:      []contracts.EvidenceID{"fact-service"},
			MaxFollowUps:     1,
			EndCondition:     "trade-off explained",
		}},
		ConfirmedFacts: []contracts.ProfileFact{{
			ID:    "fact-service",
			Field: "project",
			Value: "Go service",
			SourceSpan: contracts.SourceSpan{
				Start: 0,
				End:   10,
				Text:  "Go service",
			},
		}},
		ConfirmedSkills: []string{"Go"},
		Events: []coreevaluation.EventEvidence{{
			ID:         "answer-1",
			Speaker:    db.SpeakerUser,
			QuestionID: "Q1",
			Content:    "I selected a versioned cache.",
			OccurredAt: started.Add(5 * time.Minute),
		}},
		CoachEvents: []coreevaluation.CoachEvidence{},
		CodeRuns:    []coreevaluation.CodeEvidence{},
		AllowedEvidenceIDs: []contracts.EvidenceID{
			"constraint:Q1",
			"fact-service",
			"answer-1",
		},
	}
}
