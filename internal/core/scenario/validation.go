package scenario

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// Validate checks one embedded template.
func (template Template) Validate() error {
	if strings.TrimSpace(template.ID) == "" ||
		strings.TrimSpace(template.Label) == "" ||
		strings.TrimSpace(template.Description) == "" {
		return fmt.Errorf("scenario template identity fields cannot be blank")
	}
	if template.DefaultMode != contracts.ScenarioStrict &&
		template.DefaultMode != contracts.ScenarioStandard &&
		template.DefaultMode != contracts.ScenarioCoach {
		return fmt.Errorf(
			"scenario template %q has invalid default mode %q",
			template.ID,
			template.DefaultMode,
		)
	}
	if template.DefaultTimeBudgetSeconds <= 0 ||
		template.DefaultMaxFollowUps < 0 {
		return fmt.Errorf("scenario template %q has invalid limits", template.ID)
	}
	if len(template.QuestionGuidance) == 0 ||
		len(template.RubricGuidance) == 0 {
		return fmt.Errorf("scenario template %q has empty guidance", template.ID)
	}
	for _, values := range [][]string{
		template.QuestionGuidance,
		template.RubricGuidance,
	} {
		for _, value := range values {
			if strings.TrimSpace(value) == "" {
				return fmt.Errorf(
					"scenario template %q contains blank guidance",
					template.ID,
				)
			}
		}
	}
	return nil
}

// Validate checks the Provider-only output shape before request-specific
// evidence validation.
func (generated GeneratedPlan) Validate() error {
	if err := generated.Scenario.Validate(); err != nil {
		return err
	}
	if generated.JDMappings == nil {
		return validationError(
			"validate generated ScenarioPlan",
			"JD 映射必须是显式数组。",
			"没有 JD 时返回空数组。",
		)
	}
	for index, mapping := range generated.JDMappings {
		if strings.TrimSpace(mapping.Requirement) == "" {
			return validationError(
				"validate generated ScenarioPlan",
				fmt.Sprintf("第 %d 条 JD 要求不能为空。", index+1),
				"修正 JD 映射后重试。",
			)
		}
		if mapping.EvidenceIDs == nil {
			return validationError(
				"validate generated ScenarioPlan",
				fmt.Sprintf("第 %d 条 JD evidence_ids 必须是数组。", index+1),
				"没有证据时返回空数组并填写 gap。",
			)
		}
		if len(mapping.EvidenceIDs) == 0 &&
			strings.TrimSpace(mapping.Gap) == "" {
			return validationError(
				"validate generated ScenarioPlan",
				fmt.Sprintf("第 %d 条 JD 映射既无证据也无待补足项。", index+1),
				"关联已确认事实或填写 gap。",
			)
		}
	}
	return nil
}

// DecodeGeneratedPlan strictly decodes one Scenario Planner response.
func DecodeGeneratedPlan(data []byte) (GeneratedPlan, error) {
	var value GeneratedPlan
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, invalidGeneratedJSON(err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return value, invalidGeneratedJSON(err)
	}
	if err := value.Validate(); err != nil {
		return value, err
	}
	return value, nil
}

func validatePlan(plan Plan, template Template) error {
	if strings.TrimSpace(plan.ID) == "" ||
		strings.TrimSpace(plan.BaseID) == "" ||
		strings.TrimSpace(plan.ProfileID) == "" ||
		plan.Revision <= 0 {
		return validationError(
			"validate Scenario plan",
			"场景版本标识无效。",
			"重新生成场景计划。",
		)
	}
	if !planIDPattern.MatchString(plan.BaseID) ||
		plan.ID != versionID(plan.BaseID, plan.Revision) {
		return validationError(
			"validate Scenario version",
			"场景版本 ID 与基础 ID 不一致。",
			"重新生成场景计划。",
		)
	}
	if err := plan.Scenario.Validate(); err != nil {
		return err
	}
	if plan.Scenario.Template != template.ID {
		return validationError(
			"validate Scenario template",
			"模型返回的场景模板与用户选择不一致。",
			"保留当前选择并重试生成。",
		)
	}
	if plan.Scenario.PromptVersion != versionString(plan.Revision) {
		return validationError(
			"validate Scenario version",
			"场景 prompt_version 与本地版本不一致。",
			"重新生成当前场景版本。",
		)
	}
	allowed := make(map[contracts.EvidenceID]struct{}, len(plan.EvidenceIDs))
	for _, id := range plan.EvidenceIDs {
		if strings.TrimSpace(string(id)) == "" {
			return validationError(
				"validate Scenario evidence",
				"确认事实 ID 不能为空。",
				"重新确认候选人画像。",
			)
		}
		if _, duplicate := allowed[id]; duplicate {
			return validationError(
				"validate Scenario evidence",
				"确认事实 ID 不能重复。",
				"重新生成当前计划。",
			)
		}
		allowed[id] = struct{}{}
	}
	questionIDs := make(map[string]struct{}, len(plan.Scenario.Questions))
	totalSeconds := 0
	for _, question := range plan.Scenario.Questions {
		if _, duplicate := questionIDs[question.ID]; duplicate {
			return validationError(
				"validate Scenario questions",
				"场景题目 ID 不能重复。",
				"重新生成或修正重复题目。",
			)
		}
		questionIDs[question.ID] = struct{}{}
		totalSeconds += question.EstimatedSeconds
		if question.MaxFollowUps > template.DefaultMaxFollowUps {
			return validationError(
				"validate Scenario follow-ups",
				fmt.Sprintf(
					"题目 %q 的追问上限超过模板限制。",
					question.ID,
				),
				"降低 max_follow_ups 后重试。",
			)
		}
		if question.Generic && len(question.EvidenceIDs) > 0 {
			return validationError(
				"validate generic Scenario question",
				"通用问题不能伪装成履历证据题。",
				"移除 evidence_ids 或取消 generic 标记。",
			)
		}
		for _, id := range question.EvidenceIDs {
			if _, exists := allowed[id]; !exists {
				return validationError(
					"validate Scenario evidence",
					fmt.Sprintf("题目引用了未确认事实 %q。", id),
					"只使用已确认画像事实或标记为通用问题。",
				)
			}
		}
	}
	if totalSeconds > plan.Scenario.TimeBudgetSeconds {
		return validationError(
			"validate Scenario time budget",
			"题目预计时长总和超过场景时长。",
			"缩短题目时长或增加场景时长。",
		)
	}
	if plan.JDMappings == nil {
		return validationError(
			"validate JD mapping",
			"JD 映射必须是显式数组。",
			"没有 JD 时使用空数组。",
		)
	}
	if plan.JDProvided {
		if len(plan.JDMappings) < 3 {
			return validationError(
				"validate JD mapping",
				"提供 JD 时至少需要 3 条要求映射。",
				"补足 JD 要求与证据/待补足项后重试。",
			)
		}
	} else if len(plan.JDMappings) != 0 {
		return validationError(
			"validate JD mapping",
			"未提供 JD 时不应生成 JD 映射。",
			"返回空 jd_mappings 数组。",
		)
	}
	requirements := make(map[string]struct{}, len(plan.JDMappings))
	for _, mapping := range plan.JDMappings {
		key := strings.ToLower(strings.TrimSpace(mapping.Requirement))
		if _, duplicate := requirements[key]; duplicate {
			return validationError(
				"validate JD mapping",
				"JD 要求映射不能重复。",
				"为每条映射选择不同要求。",
			)
		}
		requirements[key] = struct{}{}
		if mapping.EvidenceIDs == nil {
			return validationError(
				"validate JD mapping",
				"JD evidence_ids 必须是显式数组。",
				"没有证据时使用空数组并填写 gap。",
			)
		}
		if len(mapping.EvidenceIDs) == 0 &&
			strings.TrimSpace(mapping.Gap) == "" {
			return validationError(
				"validate JD mapping",
				"无证据的 JD 要求必须标记待补足项。",
				"填写 gap 后重试。",
			)
		}
		for _, id := range mapping.EvidenceIDs {
			if _, exists := allowed[id]; !exists {
				return validationError(
					"validate JD mapping evidence",
					fmt.Sprintf("JD 映射引用了未确认事实 %q。", id),
					"只关联已确认画像事实。",
				)
			}
		}
	}
	for _, id := range plan.ManualQuestionIDs {
		if _, exists := questionIDs[id]; !exists {
			return validationError(
				"validate manual Scenario edits",
				fmt.Sprintf("手工编辑题目 %q 已不存在。", id),
				"恢复该题或移除手工编辑标记。",
			)
		}
	}
	if plan.Locked && plan.ConfirmedAt == nil {
		return validationError(
			"validate locked Scenario",
			"锁定场景必须包含确认时间。",
			"重新确认场景计划。",
		)
	}
	if plan.ConfirmedAt != nil && plan.ConfirmedAt.IsZero() {
		return validationError(
			"validate Scenario confirmation",
			"场景确认时间无效。",
			"重新确认场景计划。",
		)
	}
	return nil
}

func validateQuestion(
	question contracts.ScenarioQuestion,
	evidenceIDs []contracts.EvidenceID,
) error {
	scenario := contracts.Scenario{
		Template:          "validation",
		Mode:              contracts.ScenarioStandard,
		TimeBudgetSeconds: max(1, question.EstimatedSeconds),
		PromptVersion:     "validation",
		Questions:         []contracts.ScenarioQuestion{question},
	}
	if err := scenario.Validate(); err != nil {
		return err
	}
	allowed := make(map[contracts.EvidenceID]struct{}, len(evidenceIDs))
	for _, id := range evidenceIDs {
		allowed[id] = struct{}{}
	}
	if question.Generic && len(question.EvidenceIDs) > 0 {
		return validationError(
			"edit Scenario question",
			"通用问题不能引用履历证据。",
			"移除 evidence_ids 或取消 generic 标记。",
		)
	}
	for _, id := range question.EvidenceIDs {
		if _, exists := allowed[id]; !exists {
			return validationError(
				"edit Scenario question",
				fmt.Sprintf("题目引用了未确认事实 %q。", id),
				"只选择已确认事实。",
			)
		}
	}
	return nil
}

func validMode(mode contracts.ScenarioMode) bool {
	return mode == contracts.ScenarioStrict ||
		mode == contracts.ScenarioStandard ||
		mode == contracts.ScenarioCoach
}

func versionString(revision int) string {
	return fmt.Sprintf("%s.r%d", PromptVersionBase, revision)
}

func versionID(baseID string, revision int) string {
	return fmt.Sprintf("%s-v%03d", baseID, revision)
}

func validationError(
	operation string,
	message string,
	recovery string,
) *domainerr.Error {
	return domainerr.New(
		domainerr.CodeValidation,
		operation,
		message,
		recovery,
		false,
	)
}

func policyError(message, recovery string) *domainerr.Error {
	return domainerr.New(
		domainerr.CodePolicyDenied,
		"mutate locked Scenario",
		message,
		recovery,
		false,
	)
}

func invalidGeneratedJSON(cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"decode ScenarioPlan",
		"model provider",
		"模型返回的场景计划不是有效结构。",
		"自动修复失败时可重试生成。",
		false,
		cause,
	)
}

func addManualID(items []string, id string) []string {
	if slices.Contains(items, id) {
		return items
	}
	return append(items, id)
}
