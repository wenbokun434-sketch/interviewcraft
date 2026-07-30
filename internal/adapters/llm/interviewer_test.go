package llm

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
)

func TestInterviewerBuildsIsolatedStrictRequestAndRetriesSchema(t *testing.T) {
	t.Parallel()

	input := interviewerInput()
	valid := contracts.InterviewerAction{
		Action:       contracts.ActionFollowUp,
		QuestionID:   "Q1",
		Message:      "Which trade-off mattered most?",
		EvidenceIDs:  []contracts.EvidenceID{"answer-1"},
		SessionState: contracts.SessionInterviewing,
	}
	validJSON, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid action: %v", err)
	}
	generator := &sequenceGenerator{
		responses: [][]byte{
			[]byte(`{"action":"follow_up"}`),
			validJSON,
		},
	}

	action, err := NewInterviewer(generator).Respond(
		context.Background(),
		input,
	)

	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if !reflect.DeepEqual(action, valid) {
		t.Fatalf("action=%#v, want %#v", action, valid)
	}
	if len(generator.requests) != 2 {
		t.Fatalf("requests=%d, want 2", len(generator.requests))
	}
	request := generator.requests[0]
	if request.SchemaName != "InterviewerAction" ||
		len(request.Messages) != 2 {
		t.Fatalf("request=%#v", request)
	}
	for _, rule := range []string{
		"Draft answers and Coach/Sidebar content are intentionally unavailable",
		"Every evidence_ids entry must exactly match",
		"Do not exceed max_follow_ups",
		"copy next_question.prompt exactly",
	} {
		if !strings.Contains(request.Messages[0].Content, rule) {
			t.Errorf("system prompt missing %q", rule)
		}
	}
	payload := request.Messages[1].Content
	if strings.Contains(payload, "draft") ||
		strings.Contains(payload, "coach") ||
		strings.Contains(payload, "unconfirmed") {
		t.Fatalf("isolated Provider payload leaked context: %s", payload)
	}
	if !strings.Contains(payload, `"answer-1"`) ||
		!strings.Contains(payload, `"fact-payment"`) ||
		!strings.Contains(payload, `"constraint:Q1"`) {
		t.Fatalf("Provider payload lacks allowed evidence: %s", payload)
	}
	if len(generator.requests[1].Messages) != 3 {
		t.Fatalf("retry messages=%#v", generator.requests[1].Messages)
	}
}

func TestInterviewerRequiresProviderAndValidInput(t *testing.T) {
	t.Parallel()

	if _, err := NewInterviewer(nil).Respond(
		context.Background(),
		interviewerInput(),
	); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil Provider error=%v", err)
	}
	invalid := interviewerInput()
	invalid.AllowedEvidenceIDs = nil
	if _, err := NewInterviewer(&sequenceGenerator{}).Respond(
		context.Background(),
		invalid,
	); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("invalid input error=%v", err)
	}
}

func interviewerInput() coreinterview.Input {
	current := contracts.ScenarioQuestion{
		ID:               "Q1",
		Prompt:           "Explain the payment trade-off.",
		Intent:           "Assess project depth",
		EstimatedSeconds: 300,
		Rubric:           []string{"Explains one trade-off"},
		EvidenceIDs:      []contracts.EvidenceID{"fact-payment"},
		MaxFollowUps:     2,
		EndCondition:     "One trade-off is explained",
	}
	next := contracts.ScenarioQuestion{
		ID:               "Q2",
		Prompt:           "How would you diagnose an outage?",
		Intent:           "Assess diagnosis",
		EstimatedSeconds: 300,
		Rubric:           []string{"Clarifies impact"},
		EvidenceIDs:      []contracts.EvidenceID{},
		Generic:          true,
		MaxFollowUps:     1,
		EndCondition:     "A sequence is explained",
	}
	return coreinterview.Input{
		SessionID:       "session-1",
		Mode:            contracts.ScenarioStrict,
		CurrentQuestion: current,
		NextQuestion:    &next,
		FollowUpCount:   0,
		MaxFollowUps:    2,
		ConfirmedFacts: []contracts.ProfileFact{{
			ID:    "fact-payment",
			Field: "project",
			Value: "payment service",
			SourceSpan: contracts.SourceSpan{
				Start: 0,
				End:   23,
				Text:  "Built payment service",
			},
		}},
		SubmittedAnswers: []coreinterview.AnswerEvidence{{
			EventID:    "answer-1",
			QuestionID: "Q1",
			Content:    "I chose version checks.",
		}},
		CodeRuns: []coreinterview.CodeEvidence{},
		AllowedEvidenceIDs: []contracts.EvidenceID{
			"answer-1",
			"constraint:Q1",
			"constraint:Q2",
			"fact-payment",
		},
	}
}
