package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
)

const interviewerSystemPrompt = `You are the InterviewCraft Interviewer.
Return only one InterviewerAction JSON object matching the supplied schema.
Use only confirmed_facts, submitted_answers, code_runs, current_question, next_question, and allowed_evidence_ids from the input.
Never invent candidate experience. Never use unconfirmed Profile inferences.
Draft answers and Coach/Sidebar content are intentionally unavailable and must never be inferred.
Every evidence_ids entry must exactly match one allowed_evidence_ids value.
Do not exceed max_follow_ups. When the limit is reached, close the question or move forward.
follow_up and close_question must target current_question.id.
next_question must use next_question.id and copy next_question.prompt exactly.
finish_session is valid only when next_question is absent.
Do not answer on the candidate's behalf or provide a model answer.`

// Interviewer adapts the shared structured Provider to text interview actions.
type Interviewer struct {
	generator Generator
}

// NewInterviewer constructs the Provider-backed Interviewer.
func NewInterviewer(generator Generator) *Interviewer {
	return &Interviewer{generator: generator}
}

// Respond implements interview.Provider with strict Schema retry.
func (interviewer *Interviewer) Respond(
	ctx context.Context,
	input coreinterview.Input,
) (contracts.InterviewerAction, error) {
	if interviewer == nil || interviewer.generator == nil {
		return contracts.InterviewerAction{}, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"generate Interviewer action",
			"Interviewer Provider 尚未初始化。",
			"配置并测试模型 Provider 后重试。",
			true,
		)
	}
	if err := validateInterviewerInput(input); err != nil {
		return contracts.InterviewerAction{}, err
	}
	schema, ok := contracts.JSONSchema(
		contracts.SchemaInterviewerAction,
	)
	if !ok {
		return contracts.InterviewerAction{}, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"prepare InterviewerAction schema",
			"无法加载 InterviewerAction 契约。",
			"重新安装或更新 InterviewCraft 后重试。",
			false,
		)
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return contracts.InterviewerAction{}, domainerr.Wrap(
			domainerr.CodeValidation,
			"encode Interviewer input",
			"Interviewer Provider",
			"无法准备面试官上下文。",
			"检查已持久化会话后重试。",
			false,
			err,
		)
	}
	return GenerateStructured(
		ctx,
		interviewer.generator,
		Request{
			SchemaName: "InterviewerAction",
			Schema:     schema,
			Messages: []Message{
				{Role: RoleSystem, Content: interviewerSystemPrompt},
				{Role: RoleUser, Content: string(payload)},
			},
		},
		contracts.DecodeInterviewerAction,
	)
}

func validateInterviewerInput(input coreinterview.Input) error {
	if strings.TrimSpace(input.SessionID) == "" {
		return invalidInterviewerInput("session_id is blank")
	}
	if input.ConfirmedFacts == nil ||
		input.SubmittedAnswers == nil ||
		input.CodeRuns == nil ||
		input.AllowedEvidenceIDs == nil {
		return invalidInterviewerInput("context arrays must be explicit")
	}
	if input.FollowUpCount < 0 ||
		input.MaxFollowUps < 0 ||
		input.FollowUpCount > input.MaxFollowUps ||
		input.MaxFollowUps != input.CurrentQuestion.MaxFollowUps {
		return invalidInterviewerInput("follow-up counters are invalid")
	}
	questions := []contracts.ScenarioQuestion{input.CurrentQuestion}
	if input.NextQuestion != nil {
		questions = append(questions, *input.NextQuestion)
	}
	total := 0
	for _, question := range questions {
		total += max(1, question.EstimatedSeconds)
	}
	scenario := contracts.Scenario{
		Template:          "interviewer-input",
		Mode:              input.Mode,
		TimeBudgetSeconds: total,
		PromptVersion:     "interviewer-input-v1",
		Questions:         questions,
	}
	if err := scenario.Validate(); err != nil {
		return invalidInterviewerInput(err.Error())
	}
	allowed := make(map[contracts.EvidenceID]struct{})
	for _, id := range input.AllowedEvidenceIDs {
		if strings.TrimSpace(string(id)) == "" {
			return invalidInterviewerInput("allowed evidence is blank")
		}
		if _, duplicate := allowed[id]; duplicate {
			return invalidInterviewerInput(
				fmt.Sprintf("duplicate allowed evidence %q", id),
			)
		}
		allowed[id] = struct{}{}
	}
	for _, fact := range input.ConfirmedFacts {
		if strings.TrimSpace(string(fact.ID)) == "" ||
			strings.TrimSpace(fact.SourceSpan.Text) == "" {
			return invalidInterviewerInput(
				"confirmed fact lacks source evidence",
			)
		}
	}
	return nil
}

func invalidInterviewerInput(reason string) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"validate Interviewer input",
		"Interviewer Provider",
		"面试官上下文无效。",
		"恢复已持久化会话后重试。",
		false,
		errors.New(reason),
	)
}

var _ coreinterview.Provider = (*Interviewer)(nil)
