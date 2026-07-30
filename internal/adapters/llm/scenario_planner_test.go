package llm

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	corescenario "github.com/interviewcraft/interviewcraft/internal/core/scenario"
)

func TestScenarioPlannerBuildsStrictRequestAndRetriesSchemaOnce(t *testing.T) {
	t.Parallel()

	input := plannerInput()
	valid := plannerGenerated(input)
	validJSON, err := json.Marshal(valid)
	if err != nil {
		t.Fatalf("marshal valid response: %v", err)
	}
	invalidJSON, err := json.Marshal(map[string]any{
		"scenario": valid.Scenario,
	})
	if err != nil {
		t.Fatalf("marshal invalid response: %v", err)
	}
	generator := &sequenceGenerator{
		responses: [][]byte{invalidJSON, validJSON},
	}

	result, err := NewScenarioPlanner(generator).Generate(
		context.Background(),
		input,
	)

	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(generator.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(generator.requests))
	}
	request := generator.requests[0]
	if request.SchemaName != "ScenarioPlan" ||
		len(request.Messages) != 2 {
		t.Fatalf("request = %#v", request)
	}
	for _, rule := range []string{
		"Use only confirmed_facts",
		"set generic=true",
		"at least three distinct jd_mappings",
		"Do not include unconfirmed inferences",
	} {
		if !strings.Contains(request.Messages[0].Content, rule) {
			t.Errorf("system prompt missing %q", rule)
		}
	}
	if strings.Contains(request.Messages[1].Content, "unconfirmed leadership") {
		t.Fatal("Planner request leaked an inference")
	}
	if !strings.Contains(request.Messages[1].Content, `"confirmed_facts"`) ||
		!strings.Contains(request.Messages[1].Content, `"fact-payment"`) {
		t.Fatalf("Planner input = %s", request.Messages[1].Content)
	}
	var schema map[string]any
	if err := json.Unmarshal(request.Schema, &schema); err != nil {
		t.Fatalf("decode schema: %v", err)
	}
	properties, ok := schema["properties"].(map[string]any)
	if !ok || properties["scenario"] == nil ||
		properties["jd_mappings"] == nil {
		t.Fatalf("ScenarioPlan schema = %#v", schema)
	}
	if len(generator.requests[1].Messages) != 3 ||
		!strings.Contains(
			generator.requests[1].Messages[2].Content,
			"previous response",
		) {
		t.Fatalf("retry messages = %#v", generator.requests[1].Messages)
	}
	if len(result.JDMappings) != 3 {
		t.Fatalf("result = %#v", result)
	}
}

func TestScenarioPlannerSchemaFailureStopsAfterOneRetry(t *testing.T) {
	t.Parallel()

	input := plannerInput()
	valid := plannerGenerated(input)
	invalidJSON, err := json.Marshal(map[string]any{
		"scenario": valid.Scenario,
	})
	if err != nil {
		t.Fatalf("marshal invalid response: %v", err)
	}
	generator := &sequenceGenerator{
		responses: [][]byte{invalidJSON, invalidJSON},
	}

	_, err = NewScenarioPlanner(generator).Generate(
		context.Background(),
		input,
	)

	if !domainerr.IsCode(err, domainerr.CodeInvalidModelOutput) {
		t.Fatalf("Generate error = %v", err)
	}
	if len(generator.requests) != 2 {
		t.Fatalf("requests = %d, want exactly 2", len(generator.requests))
	}
}

func TestScenarioPlannerRequiresProvider(t *testing.T) {
	t.Parallel()

	_, err := NewScenarioPlanner(nil).Generate(
		context.Background(),
		plannerInput(),
	)

	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("Generate error = %v", err)
	}
}

func plannerInput() corescenario.GenerationInput {
	return corescenario.GenerationInput{
		Template: corescenario.Template{
			ID:                       "project_deep_dive",
			Label:                    "项目深挖",
			Description:              "Evidence-backed project questions",
			DefaultMode:              contracts.ScenarioStandard,
			DefaultTimeBudgetSeconds: 1200,
			DefaultMaxFollowUps:      2,
			QuestionGuidance:         []string{"Use confirmed facts"},
			RubricGuidance:           []string{"Explain trade-offs"},
		},
		Mode:       contracts.ScenarioStandard,
		TimeBudget: 1200,
		TargetRole: "Backend Engineer",
		Facts: []contracts.ProfileFact{{
			ID:    "fact-payment",
			Field: "project",
			Value: "payment service",
			SourceSpan: contracts.SourceSpan{
				Start: 0,
				End:   23,
				Text:  "Built payment service",
			},
		}},
		Projects:      []string{"payment service"},
		Skills:        []string{"Go"},
		JD:            "Build Go backend services and distributed systems",
		PromptVersion: corescenario.PromptVersionBase,
	}
}

func plannerGenerated(
	input corescenario.GenerationInput,
) corescenario.GeneratedPlan {
	return corescenario.GeneratedPlan{
		Scenario: contracts.Scenario{
			Template:          input.Template.ID,
			Mode:              input.Mode,
			TimeBudgetSeconds: input.TimeBudget,
			PromptVersion:     input.PromptVersion,
			Questions: []contracts.ScenarioQuestion{{
				ID:               "Q1",
				Prompt:           "Explain your payment service.",
				Intent:           "Assess project depth",
				EstimatedSeconds: 300,
				Rubric:           []string{"Names a trade-off"},
				EvidenceIDs:      []contracts.EvidenceID{"fact-payment"},
				Generic:          false,
				MaxFollowUps:     2,
				EndCondition:     "One trade-off is explained",
			}},
		},
		JDMappings: []corescenario.JDMapping{
			{
				Requirement: "Go backend",
				EvidenceIDs: []contracts.EvidenceID{"fact-payment"},
				Gap:         "",
			},
			{
				Requirement: "Service ownership",
				EvidenceIDs: []contracts.EvidenceID{"fact-payment"},
				Gap:         "",
			},
			{
				Requirement: "Distributed systems",
				EvidenceIDs: []contracts.EvidenceID{},
				Gap:         "No confirmed evidence",
			},
		},
	}
}
