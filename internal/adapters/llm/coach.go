package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

const coachSystemPrompt = `You are the InterviewCraft practice Coach.
Return only one CoachResponse JSON object matching the supplied schema.
Use only current question constraints, confirmed_facts, submitted_answers, executed code_runs, and the user's explicit Coach request.
Main-answer drafts, unexecuted code, Profile inferences, and previous Coach response text are intentionally unavailable. Never infer them.
Match the requested intent and never exceed allowed_max_level.
L1 asks one Socratic or clarifying question. L2 gives a compact directional hint or answer structure. L3 may guide reasoning without completing the active task.
L4 may provide a model review only when question_state is closed or session_closed.
For every non-L4 response, never provide a complete model answer, complete implementation, directly submittable code, or copy-paste response.
Strict mode must remain practice, not interview代答. If the user asks for a full answer, give only an L1 question within policy.
recommended_action is the Coach reply shown to the user. policy_note briefly states the enforced boundary when relevant.`

// Coach adapts the shared structured Provider to policy-aware help.
type Coach struct {
	generator Generator
}

// NewCoach constructs the Provider-backed Coach.
func NewCoach(generator Generator) *Coach {
	return &Coach{generator: generator}
}

// Respond implements coach.Provider with one strict Schema retry.
func (coach *Coach) Respond(
	ctx context.Context,
	input corecoach.Input,
) (contracts.CoachResponse, error) {
	if coach == nil || coach.generator == nil {
		return contracts.CoachResponse{}, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"generate Coach response",
			"Coach Provider 尚未初始化。",
			"配置并测试模型 Provider 后重试。",
			true,
		)
	}
	if err := validateCoachInput(input); err != nil {
		return contracts.CoachResponse{}, err
	}
	schema, ok := contracts.JSONSchema(contracts.SchemaCoachResponse)
	if !ok {
		return contracts.CoachResponse{}, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"prepare CoachResponse schema",
			"无法加载 CoachResponse 契约。",
			"重新安装或更新 InterviewCraft 后重试。",
			false,
		)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return contracts.CoachResponse{}, domainerr.Wrap(
			domainerr.CodeValidation,
			"encode Coach input",
			"Coach Provider",
			"无法准备 Coach 上下文。",
			"恢复已持久化会话后重试。",
			false,
			err,
		)
	}
	return GenerateStructured(
		ctx,
		coach.generator,
		Request{
			SchemaName: "CoachResponse",
			Schema:     schema,
			Messages: []Message{
				{Role: RoleSystem, Content: coachSystemPrompt},
				{Role: RoleUser, Content: string(payload)},
			},
		},
		contracts.DecodeCoachResponse,
	)
}

func validateCoachInput(input corecoach.Input) error {
	if strings.TrimSpace(input.SessionID) == "" ||
		strings.TrimSpace(input.UserRequest) == "" {
		return invalidCoachInput("session_id or user_request is blank")
	}
	if input.ConfirmedFacts == nil ||
		input.SubmittedAnswers == nil ||
		input.CodeRuns == nil {
		return invalidCoachInput("context arrays must be explicit")
	}
	if input.QuestionState != corecoach.QuestionActive &&
		input.QuestionState != corecoach.QuestionClosed &&
		input.QuestionState != corecoach.SessionClosed {
		return invalidCoachInput("question_state is invalid")
	}
	if input.Usage.Used < 0 ||
		input.Usage.Limit < 0 ||
		input.Usage.Remaining < 0 ||
		(input.Usage.Unlimited && input.Usage.Limit != 0) {
		return invalidCoachInput("usage is invalid")
	}
	question := input.Question
	scenario := contracts.Scenario{
		Template:          "coach-input",
		Mode:              input.Mode,
		TimeBudgetSeconds: max(1, question.EstimatedSeconds),
		PromptVersion:     "coach-input-v1",
		Questions:         []contracts.ScenarioQuestion{question},
	}
	if err := scenario.Validate(); err != nil {
		return invalidCoachInput(err.Error())
	}
	probe := contracts.CoachResponse{
		Intent:            input.Intent,
		HelpLevel:         input.RequestedLevel,
		KnowledgeTags:     []string{"input"},
		RecommendedAction: "input",
	}
	if err := probe.Validate(); err != nil {
		return invalidCoachInput(err.Error())
	}
	probe.HelpLevel = input.AllowedMaxLevel
	if err := probe.Validate(); err != nil {
		return invalidCoachInput(err.Error())
	}
	if input.QuestionState == corecoach.QuestionActive &&
		input.AllowedMaxLevel == contracts.HelpL4 {
		return invalidCoachInput("active question cannot allow L4")
	}
	for _, fact := range input.ConfirmedFacts {
		if strings.TrimSpace(string(fact.ID)) == "" ||
			strings.TrimSpace(fact.SourceSpan.Text) == "" {
			return invalidCoachInput("confirmed fact lacks source evidence")
		}
	}
	return nil
}

func invalidCoachInput(reason string) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"validate Coach input",
		"Coach Provider",
		"Coach 上下文无效。",
		"恢复已持久化会话后重试。",
		false,
		errors.New(reason),
	)
}

var _ corecoach.Provider = (*Coach)(nil)
