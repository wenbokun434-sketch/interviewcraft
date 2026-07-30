package llm

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestCoachBuildsIsolatedStrictRequestAndRetriesSchema(t *testing.T) {
	t.Parallel()

	input := coachInput()
	valid := contracts.CoachResponse{
		Intent:            contracts.CoachGiveHint,
		HelpLevel:         contracts.HelpL2,
		KnowledgeTags:     []string{"cache consistency"},
		RecommendedAction: "先明确读写路径，再比较一致性选择。",
		PolicyNote:        "不提供完整答案。",
	}
	validJSON, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid response: %v", err)
	}
	generator := &sequenceGenerator{
		responses: [][]byte{
			[]byte(`{"intent":"give_hint"}`),
			validJSON,
		},
	}
	response, err := NewCoach(generator).Respond(
		context.Background(),
		input,
	)
	if err != nil {
		t.Fatalf("Respond: %v", err)
	}
	if !reflect.DeepEqual(response, valid) {
		t.Fatalf("response=%#v, want %#v", response, valid)
	}
	if len(generator.requests) != 2 {
		t.Fatalf("requests=%d, want 2", len(generator.requests))
	}
	request := generator.requests[0]
	if request.SchemaName != "CoachResponse" ||
		len(request.Messages) != 2 {
		t.Fatalf("request=%#v", request)
	}
	for _, rule := range []string{
		"Main-answer drafts, unexecuted code, Profile inferences",
		"never exceed allowed_max_level",
		"L4 may provide a model review only",
		"never provide a complete model answer",
		"Strict mode must remain practice",
	} {
		if !strings.Contains(request.Messages[0].Content, rule) {
			t.Errorf("system prompt missing %q", rule)
		}
	}
	payload := request.Messages[1].Content
	for _, forbidden := range []string{
		"draft",
		"inference",
		"previous_coach",
		"unexecuted",
	} {
		if strings.Contains(strings.ToLower(payload), forbidden) {
			t.Fatalf("Coach payload leaked %q: %s", forbidden, payload)
		}
	}
	for _, required := range []string{
		`"submitted-1"`,
		`"executed-1"`,
		`"fact-payment"`,
		`"allowed_max_level":"L2"`,
	} {
		if !strings.Contains(payload, required) {
			t.Fatalf("Coach payload lacks %q: %s", required, payload)
		}
	}
	if len(generator.requests[1].Messages) != 3 {
		t.Fatalf("retry messages=%#v", generator.requests[1].Messages)
	}
}

func TestCoachRequiresProviderAndValidInput(t *testing.T) {
	t.Parallel()

	if _, err := NewCoach(nil).Respond(
		context.Background(),
		coachInput(),
	); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil Provider error=%v", err)
	}
	invalid := coachInput()
	invalid.SubmittedAnswers = nil
	if _, err := NewCoach(&sequenceGenerator{}).Respond(
		context.Background(),
		invalid,
	); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("nil context array error=%v", err)
	}
	invalid = coachInput()
	invalid.AllowedMaxLevel = contracts.HelpL4
	if _, err := NewCoach(&sequenceGenerator{}).Respond(
		context.Background(),
		invalid,
	); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("active L4 input error=%v", err)
	}
}

func coachInput() corecoach.Input {
	question := contracts.ScenarioQuestion{
		ID:               "Q1",
		Prompt:           "Explain the cache consistency trade-off.",
		Intent:           "Assess trade-off reasoning",
		EstimatedSeconds: 300,
		Rubric:           []string{"Names one constraint"},
		EvidenceIDs:      []contracts.EvidenceID{"fact-payment"},
		MaxFollowUps:     2,
		EndCondition:     "One trade-off is explained",
	}
	return corecoach.Input{
		SessionID:       "session-1",
		Mode:            contracts.ScenarioStrict,
		Question:        question,
		QuestionState:   corecoach.QuestionActive,
		Intent:          contracts.CoachGiveHint,
		RequestedLevel:  contracts.HelpL2,
		AllowedMaxLevel: contracts.HelpL2,
		UserRequest:     "给我一个方向提示。",
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
		SubmittedAnswers: []corecoach.AnswerEvidence{{
			EventID:    "submitted-1",
			QuestionID: "Q1",
			Content:    "I chose version checks.",
		}},
		CodeRuns: []corecoach.CodeEvidence{{
			SubmissionID: "executed-1",
			QuestionID:   "Q1",
			Language:     "go",
			Source:       "package main",
			TestResult:   json.RawMessage(`{"passed":true}`),
			RuntimeStats: json.RawMessage(`{"duration_ms":10}`),
			SnapshotID:   "snapshot-1",
		}},
		Usage: corecoach.Usage{
			Used:      0,
			Limit:     1,
			Remaining: 1,
		},
	}
}
