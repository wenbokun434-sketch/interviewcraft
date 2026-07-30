package coach

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

var completeSolutionMarkers = []string{
	"```",
	"完整答案",
	"标准答案",
	"完整实现",
	"最终代码",
	"可直接提交",
	"直接复制",
	"照着回答",
	"final answer",
	"complete solution",
	"copy and paste",
	"the answer is",
}

// PolicyFor derives the immutable mode policy for the current question state.
func PolicyFor(
	mode contracts.ScenarioMode,
	state QuestionState,
) (Policy, error) {
	if !validQuestionState(state) {
		return Policy{}, coachError(
			domainerr.CodeValidation,
			"Coach 问题状态无效。",
			"刷新会话状态后重试。",
			false,
		)
	}
	switch mode {
	case contracts.ScenarioStrict:
		return Policy{
			Mode:     mode,
			Limit:    1,
			MaxLevel: contracts.HelpL2,
		}, nil
	case contracts.ScenarioStandard:
		return Policy{
			Mode:     mode,
			Limit:    2,
			MaxLevel: contracts.HelpL2,
		}, nil
	case contracts.ScenarioCoach:
		level := contracts.HelpL3
		if state == QuestionClosed || state == SessionClosed {
			level = contracts.HelpL4
		}
		return Policy{
			Mode:     mode,
			Limit:    0,
			MaxLevel: level,
		}, nil
	default:
		return Policy{}, coachError(
			domainerr.CodeValidation,
			"场景的 Coach 模式无效。",
			"返回场景工厂创建新的确认场景。",
			false,
		)
	}
}

func usageFor(policy Policy, used int) Usage {
	usage := Usage{
		Used:      max(0, used),
		Limit:     max(0, policy.Limit),
		Unlimited: policy.Limit == 0,
	}
	if !usage.Unlimited {
		usage.Remaining = max(0, policy.Limit-usage.Used)
	}
	return usage
}

func ensureQuota(policy Policy, usage Usage) error {
	if !usage.Unlimited && usage.Used >= policy.Limit {
		return coachError(
			domainerr.CodePolicyDenied,
			fmt.Sprintf(
				"当前题目的 Coach 额度已用完（%d/%d）。",
				usage.Used,
				policy.Limit,
			),
			"继续独立作答，或安全结束本题。",
			false,
		)
	}
	return nil
}

func allowedLevel(
	request AskRequest,
	policy Policy,
) (contracts.HelpLevel, error) {
	if !validIntent(request.Intent) {
		return "", coachError(
			domainerr.CodeValidation,
			"Coach 意图无效。",
			"选择解释概念、提示、回答结构、检查思路、解释失败或加入复习。",
			false,
		)
	}
	if !validLevel(request.RequestedLevel) {
		return "", coachError(
			domainerr.CodeValidation,
			"Coach 帮助层级无效。",
			"选择 L1、L2、L3 或 L4。",
			false,
		)
	}
	allowed := policy.MaxLevel
	if levelRank(request.RequestedLevel) < levelRank(allowed) {
		allowed = request.RequestedLevel
	}
	if policy.Mode == contracts.ScenarioStrict &&
		requestsCompleteSolution(request.UserRequest) {
		allowed = contracts.HelpL1
	}
	if levelRank(request.RequestedLevel) >
		levelRank(policy.MaxLevel) {
		return "", coachError(
			domainerr.CodePolicyDenied,
			fmt.Sprintf(
				"%s 模式当前最多允许 %s 帮助。",
				policy.Mode,
				policy.MaxLevel,
			),
			"降低帮助层级后重试，或独立完成当前题。",
			false,
		)
	}
	return allowed, nil
}

func validateResponse(
	input Input,
	response contracts.CoachResponse,
) error {
	if err := response.Validate(); err != nil {
		return invalidCoachOutput(err)
	}
	if response.Intent != input.Intent {
		return invalidCoachOutput(errors.New(
			"response intent does not match request",
		))
	}
	if levelRank(response.HelpLevel) >
		levelRank(input.AllowedMaxLevel) {
		return invalidCoachOutput(fmt.Errorf(
			"response level %s exceeds %s",
			response.HelpLevel,
			input.AllowedMaxLevel,
		))
	}
	action := strings.TrimSpace(response.RecommendedAction)
	limit := responseLengthLimit(response.HelpLevel)
	if utf8.RuneCountInString(action) > limit {
		return invalidCoachOutput(fmt.Errorf(
			"%s response exceeds %d characters",
			response.HelpLevel,
			limit,
		))
	}
	guardedOutput := strings.Join(
		[]string{
			action,
			response.PolicyNote,
			strings.Join(response.KnowledgeTags, "\n"),
		},
		"\n",
	)
	if response.HelpLevel != contracts.HelpL4 &&
		containsCompleteSolution(guardedOutput) {
		return invalidCoachOutput(errors.New(
			"response contains a complete or directly submittable solution",
		))
	}
	if response.HelpLevel == contracts.HelpL4 &&
		input.QuestionState == QuestionActive {
		return invalidCoachOutput(errors.New(
			"L4 is unavailable while the question is active",
		))
	}
	return nil
}

func requestsCompleteSolution(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range []string{
		"完整答案",
		"标准答案",
		"直接回答",
		"替我回答",
		"帮我写完",
		"可提交代码",
		"final answer",
		"answer for me",
		"complete solution",
		"write the code",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func containsCompleteSolution(value string) bool {
	lower := strings.ToLower(value)
	for _, marker := range completeSolutionMarkers {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func responseLengthLimit(level contracts.HelpLevel) int {
	switch level {
	case contracts.HelpL1:
		return 200
	case contracts.HelpL2:
		return 400
	case contracts.HelpL3:
		return 1000
	default:
		return 5000
	}
}

func levelRank(level contracts.HelpLevel) int {
	switch level {
	case contracts.HelpL1:
		return 1
	case contracts.HelpL2:
		return 2
	case contracts.HelpL3:
		return 3
	case contracts.HelpL4:
		return 4
	default:
		return 0
	}
}

func validLevel(level contracts.HelpLevel) bool {
	return levelRank(level) != 0
}

func validQuestionState(state QuestionState) bool {
	switch state {
	case QuestionActive, QuestionClosed, SessionClosed:
		return true
	default:
		return false
	}
}

func validIntent(intent contracts.CoachIntent) bool {
	switch intent {
	case contracts.CoachExplainConcept,
		contracts.CoachGiveHint,
		contracts.CoachAnswerStructure,
		contracts.CoachCheckReasoning,
		contracts.CoachExplainFailure,
		contracts.CoachAddToReview:
		return true
	default:
		return false
	}
}

func validOutcome(outcome LearningOutcome) bool {
	switch outcome {
	case OutcomeUnmarked,
		OutcomeUnderstood,
		OutcomeConfused,
		OutcomeReview:
		return true
	default:
		return false
	}
}
