package llm

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	corescenario "github.com/interviewcraft/interviewcraft/internal/core/scenario"
)

const scenarioPlannerSystemPrompt = `You are the InterviewCraft Scenario Planner.
Return only one ScenarioPlan JSON object matching the supplied schema.
Use only confirmed_facts, confirmed_projects, and confirmed_skills from the input. Never infer or invent candidate experience.
Every non-generic resume question must reference one or more exact confirmed fact IDs in evidence_ids.
If a question has no confirmed resume evidence, set generic=true, use an empty evidence_ids array, and phrase it as a general or clarifying question.
Keep template, mode, time_budget_seconds, and prompt_version exactly equal to the input.
Every question must include prompt, intent, estimated_seconds, a non-empty rubric, evidence_ids, generic, max_follow_ups, and end_condition.
When jd is present, return at least three distinct jd_mappings. Each mapping must use confirmed fact IDs or an explicit gap. When jd is absent, return an empty jd_mappings array.
Do not include unconfirmed inferences because none are provided.`

// ScenarioPlanner adapts the shared structured Provider to core Scenario
// generation without exposing unconfirmed Profile inferences.
type ScenarioPlanner struct {
	generator Generator
}

// NewScenarioPlanner constructs the Provider-backed planner.
func NewScenarioPlanner(generator Generator) *ScenarioPlanner {
	return &ScenarioPlanner{generator: generator}
}

// Generate implements scenario.Generator with strict Schema retry.
func (planner *ScenarioPlanner) Generate(
	ctx context.Context,
	input corescenario.GenerationInput,
) (corescenario.GeneratedPlan, error) {
	if planner == nil {
		return corescenario.GeneratedPlan{}, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"generate Scenario plan",
			"Scenario Planner 尚未初始化。",
			"配置模型 Provider 后重试。",
			true,
		)
	}
	schema, err := scenarioPlanSchema()
	if err != nil {
		return corescenario.GeneratedPlan{}, domainerr.Wrap(
			domainerr.CodeDependencyUnavailable,
			"prepare ScenarioPlan schema",
			"published Scenario schema",
			"无法准备场景结构契约。",
			"重新安装或更新 InterviewCraft 后重试。",
			false,
			err,
		)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return corescenario.GeneratedPlan{}, domainerr.Wrap(
			domainerr.CodeValidation,
			"encode Scenario Planner input",
			"Scenario Planner",
			"无法准备场景生成输入。",
			"检查已确认画像和模板后重试。",
			false,
			err,
		)
	}
	return GenerateStructured(
		ctx,
		planner.generator,
		Request{
			SchemaName: "ScenarioPlan",
			Schema:     schema,
			Messages: []Message{
				{Role: RoleSystem, Content: scenarioPlannerSystemPrompt},
				{Role: RoleUser, Content: string(payload)},
			},
		},
		corescenario.DecodeGeneratedPlan,
	)
}

func scenarioPlanSchema() (json.RawMessage, error) {
	scenarioSchema, ok := contracts.JSONSchema(contracts.SchemaScenario)
	if !ok {
		return nil, errors.New("Scenario schema is not published")
	}
	var scenarioObject any
	if err := json.Unmarshal(scenarioSchema, &scenarioObject); err != nil {
		return nil, err
	}
	schema := map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"title":                "ScenarioPlan",
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"scenario", "jd_mappings"},
		"properties": map[string]any{
			"scenario": scenarioObject,
			"jd_mappings": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required": []string{
						"requirement",
						"evidence_ids",
						"gap",
					},
					"properties": map[string]any{
						"requirement": map[string]any{
							"type":      "string",
							"minLength": 1,
						},
						"evidence_ids": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":      "string",
								"minLength": 1,
							},
						},
						"gap": map[string]any{"type": "string"},
					},
				},
			},
		},
	}
	return json.Marshal(schema)
}

var _ corescenario.Generator = (*ScenarioPlanner)(nil)
