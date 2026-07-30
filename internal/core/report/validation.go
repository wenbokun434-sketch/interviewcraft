package report

import (
	"fmt"
	"math"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

var fixedDimensions = []contracts.EvaluationDimension{
	contracts.DimensionAnswerStructure,
	contracts.DimensionExperienceCredibility,
	contracts.DimensionTechnicalDepth,
	contracts.DimensionProblemClarification,
	contracts.DimensionProblemSolving,
	contracts.DimensionCodeQuality,
	contracts.DimensionTimeManagement,
	contracts.DimensionIndependence,
}

var prohibitedJudgments = []string{
	"personality", "personality fit", "hire", "hiring decision",
	"人格", "性格", "录用", "招聘结论",
}

// FixedDimensions returns a defensive copy of the scorecard order.
func FixedDimensions() []contracts.EvaluationDimension {
	return append([]contracts.EvaluationDimension(nil), fixedDimensions...)
}

// Validate checks that every conclusion is conservative and every evidence
// reference resolves within the durable document.
func (document Document) Validate() error {
	if strings.TrimSpace(document.ID) == "" ||
		document.SchemaVersion != SchemaVersion ||
		document.GeneratedAt.IsZero() {
		return invalidReport("报告 ID、版本或生成时间无效。", nil)
	}
	if err := validateSummary(document.Summary); err != nil {
		return err
	}
	evidence, err := evidenceIndex(document.Evidence)
	if err != nil {
		return err
	}
	if err := validateQuestionReviews(
		document.QuestionReview,
		document.Summary.QuestionCount,
		evidence,
	); err != nil {
		return err
	}
	if err := validateScorecard(document.Scorecard, document.Summary, evidence); err != nil {
		return err
	}
	if err := validateLearningMap(document.LearningMap, document.Summary, evidence); err != nil {
		return err
	}
	if err := validateTransfer(document.Transfer, document.Summary, evidence); err != nil {
		return err
	}
	for index, insight := range document.CrossInsights {
		if err := validateInsight(
			fmt.Sprintf("cross_source_insights[%d]", index),
			insight,
			evidence,
		); err != nil {
			return err
		}
	}
	if document.CrossInsights == nil {
		return invalidReport("三源洞察必须是显式数组。", nil)
	}
	if len(document.PracticePlan) < 3 {
		return invalidReport("下一轮训练计划至少需要 3 项。", nil)
	}
	for index, item := range document.PracticePlan {
		if strings.TrimSpace(item.Topic) == "" ||
			strings.TrimSpace(item.CompletionCriteria) == "" ||
			item.DurationMinutes <= 0 ||
			!validMode(item.Mode) ||
			item.Status == StatusNotApplicable ||
			prohibited(item.Topic+" "+item.CompletionCriteria) {
			return invalidReport(
				fmt.Sprintf("第 %d 项训练计划无效。", index+1),
				nil,
			)
		}
		if err := validateAssessment(
			fmt.Sprintf("practice_plan[%d]", index),
			item.Status,
			nil,
			item.EvidenceIDs,
			0,
			item.CompletionCriteria,
			evidence,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateSummary(summary SessionSummary) error {
	if strings.TrimSpace(summary.SessionID) == "" ||
		strings.TrimSpace(summary.ScenarioID) == "" ||
		strings.TrimSpace(summary.Template) == "" ||
		!validMode(summary.Mode) ||
		summary.StartedAt.IsZero() ||
		summary.CompletedAt.IsZero() ||
		summary.CompletedAt.Before(summary.StartedAt) ||
		summary.DurationSeconds < 0 ||
		summary.QuestionCount <= 0 ||
		summary.CoachPromptCount < 0 ||
		summary.CodeRunCount < 0 {
		return invalidReport("会话总览不完整或计数无效。", nil)
	}
	return nil
}

func evidenceIndex(values []EvidenceLink) (map[contracts.EvidenceID]EvidenceLink, error) {
	if values == nil {
		return nil, invalidReport("证据目录必须是显式数组。", nil)
	}
	result := make(map[contracts.EvidenceID]EvidenceLink, len(values))
	for _, value := range values {
		if strings.TrimSpace(string(value.ID)) == "" ||
			strings.TrimSpace(value.Kind) == "" ||
			strings.TrimSpace(value.Label) == "" {
			return nil, invalidReport("证据链接缺少 ID、类型或标签。", nil)
		}
		if _, duplicate := result[value.ID]; duplicate {
			return nil, invalidReport(
				fmt.Sprintf("证据 ID %q 重复。", value.ID),
				nil,
			)
		}
		result[value.ID] = value
	}
	return result, nil
}

func validateQuestionReviews(
	values []QuestionReview,
	expected int,
	evidence map[contracts.EvidenceID]EvidenceLink,
) error {
	if values == nil || len(values) != expected {
		return invalidReport("逐题复盘数量必须与场景题目数一致。", nil)
	}
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		if strings.TrimSpace(value.QuestionID) == "" ||
			strings.TrimSpace(value.Prompt) == "" {
			return invalidReport("逐题复盘缺少题目或题干。", nil)
		}
		if _, duplicate := seen[value.QuestionID]; duplicate {
			return invalidReport("逐题复盘包含重复题目。", nil)
		}
		seen[value.QuestionID] = struct{}{}
		if err := validateInsight(
			fmt.Sprintf("question_reviews[%d].summary", index),
			value.Summary,
			evidence,
		); err != nil {
			return err
		}
		if err := validateInsight(
			fmt.Sprintf("question_reviews[%d].next_action", index),
			value.NextAction,
			evidence,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateScorecard(
	values []ScorecardItem,
	summary SessionSummary,
	evidence map[contracts.EvidenceID]EvidenceLink,
) error {
	if len(values) != len(fixedDimensions) {
		return invalidReport("能力评分卡必须包含 8 个固定维度。", nil)
	}
	for index, dimension := range fixedDimensions {
		item := values[index]
		if item.Dimension != dimension {
			return invalidReport("能力评分卡维度缺失、重复或顺序无效。", nil)
		}
		if item.Dimension == contracts.DimensionCodeQuality {
			if summary.CodeRunCount == 0 &&
				item.Status != StatusNotApplicable {
				return invalidReport("无代码运行时代码质量必须为“不适用”。", nil)
			}
			if summary.CodeRunCount > 0 &&
				item.Status == StatusNotApplicable {
				return invalidReport("存在代码运行时代码质量不能标记为“不适用”。", nil)
			}
		} else if item.Status == StatusNotApplicable {
			return invalidReport("只有代码质量可以标记为“不适用”。", nil)
		}
		if item.Status == StatusEvidenceBacked && item.Score == nil {
			return invalidReport(
				"有证据的评分维度必须包含 1–5 分。",
				nil,
			)
		}
		if strings.TrimSpace(item.NextAction) == "" ||
			prohibited(item.NextAction) {
			return invalidReport("评分维度缺少安全、可行动的下一步。", nil)
		}
		if err := validateAssessment(
			"scorecard."+string(item.Dimension),
			item.Status,
			item.Score,
			item.EvidenceIDs,
			item.Confidence,
			item.NextAction,
			evidence,
		); err != nil {
			return err
		}
	}
	return nil
}

func validateLearningMap(
	values []LearningGap,
	summary SessionSummary,
	evidence map[contracts.EvidenceID]EvidenceLink,
) error {
	if values == nil {
		return invalidReport("学习地图必须是显式数组。", nil)
	}
	total := 0
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		topic := strings.TrimSpace(value.Topic)
		if topic == "" || prohibited(topic) {
			return invalidReport("学习地图主题无效。", nil)
		}
		key := strings.ToLower(topic)
		if _, duplicate := seen[key]; duplicate {
			return invalidReport("学习地图包含重复主题。", nil)
		}
		seen[key] = struct{}{}
		if value.AskCount <= 0 ||
			value.AskCount != len(value.EvidenceIDs) ||
			value.UnderstoodCount+value.ConfusedCount+
				value.ReviewCount+value.UnmarkedCount != value.AskCount ||
			!validHelpLevel(value.MaxHelpLevel) ||
			value.QuestionIDs == nil ||
			value.RelatedSkills == nil ||
			value.RelatedJDNeeds == nil {
			return invalidReport("学习地图计数或关联字段无效。", nil)
		}
		if err := resolveAll(value.EvidenceIDs, evidence); err != nil {
			return err
		}
		for _, id := range value.EvidenceIDs {
			if evidence[id].Kind != "sidebar_event" {
				return invalidReport("学习地图只能引用 Coach 事件。", nil)
			}
		}
		total += value.AskCount
	}
	if total != summary.CoachPromptCount {
		return invalidReport("学习地图提问数与 Coach 事件数不一致。", nil)
	}
	return nil
}

func validateTransfer(
	values []TransferEvidence,
	summary SessionSummary,
	evidence map[contracts.EvidenceID]EvidenceLink,
) error {
	if values == nil || len(values) != summary.CoachPromptCount {
		return invalidReport("迁移证据必须逐条对应 Coach 事件。", nil)
	}
	for _, value := range values {
		source, found := evidence[value.SidebarEventID]
		if !found || source.Kind != "sidebar_event" ||
			strings.TrimSpace(value.QuestionID) == "" ||
			strings.TrimSpace(value.Summary) == "" ||
			value.SubsequentEvidence == nil {
			return invalidReport("迁移证据来源或摘要无效。", nil)
		}
		switch value.Status {
		case TransferEvidenceObserved:
			if len(value.SubsequentEvidence) == 0 {
				return invalidReport("已观察迁移必须引用后续事件。", nil)
			}
		case TransferInsufficient:
			if len(value.SubsequentEvidence) != 0 ||
				!strings.Contains(value.Summary, "不足以判断") {
				return invalidReport("缺少迁移事件时必须显示“不足以判断”。", nil)
			}
		default:
			return invalidReport("迁移证据状态无效。", nil)
		}
		if err := resolveAll(value.SubsequentEvidence, evidence); err != nil {
			return err
		}
	}
	return nil
}

func validateInsight(
	field string,
	insight Insight,
	evidence map[contracts.EvidenceID]EvidenceLink,
) error {
	if strings.TrimSpace(insight.Text) == "" || prohibited(insight.Text) {
		return invalidReport(field+" 文案无效。", nil)
	}
	return validateAssessment(
		field,
		insight.Status,
		nil,
		insight.EvidenceIDs,
		insight.Confidence,
		insight.Text,
		evidence,
	)
}

func validateAssessment(
	field string,
	status AssessmentStatus,
	score *int,
	evidenceIDs []contracts.EvidenceID,
	confidence float64,
	text string,
	evidence map[contracts.EvidenceID]EvidenceLink,
) error {
	if evidenceIDs == nil || math.IsNaN(confidence) ||
		math.IsInf(confidence, 0) || confidence < 0 || confidence > 1 {
		return invalidReport(field+" 的证据数组或置信度无效。", nil)
	}
	switch status {
	case StatusEvidenceBacked:
		if len(evidenceIDs) == 0 {
			return invalidReport(field+" 的结论缺少证据。", nil)
		}
		if score != nil && (*score < 1 || *score > 5) {
			return invalidReport(field+" 的分数必须在 1–5。", nil)
		}
	case StatusNotApplicable:
		if score != nil || len(evidenceIDs) != 0 {
			return invalidReport(field+" 的不适用状态不能带分数或证据。", nil)
		}
	case StatusInsufficient:
		if score != nil || len(evidenceIDs) != 0 ||
			!strings.Contains(text, "不足以判断") {
			return invalidReport(field+" 缺少证据时必须显示“不足以判断”。", nil)
		}
	default:
		return invalidReport(field+" 的评估状态无效。", nil)
	}
	return resolveAll(evidenceIDs, evidence)
}

func resolveAll(
	ids []contracts.EvidenceID,
	evidence map[contracts.EvidenceID]EvidenceLink,
) error {
	seen := make(map[contracts.EvidenceID]struct{}, len(ids))
	for _, id := range ids {
		if strings.TrimSpace(string(id)) == "" {
			return invalidReport("证据引用不能为空。", nil)
		}
		if _, duplicate := seen[id]; duplicate {
			return invalidReport("同一结论不能重复引用证据。", nil)
		}
		seen[id] = struct{}{}
		if _, found := evidence[id]; !found {
			return invalidReport(
				fmt.Sprintf("证据 %q 无法解析。", id),
				nil,
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

func validHelpLevel(level contracts.HelpLevel) bool {
	return level == contracts.HelpL1 || level == contracts.HelpL2 ||
		level == contracts.HelpL3 || level == contracts.HelpL4
}

func prohibited(value string) bool {
	lower := strings.ToLower(value)
	for _, term := range prohibitedJudgments {
		if strings.Contains(lower, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func invalidReport(message string, cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"validate report",
		"",
		message,
		"保留会话证据并重新生成报告。",
		false,
		cause,
	)
}
