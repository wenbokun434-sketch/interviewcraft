package evaluation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// DraftInsight is one model-authored statement before local evidence policy.
type DraftInsight struct {
	Text        string                 `json:"text"`
	EvidenceIDs []contracts.EvidenceID `json:"evidence_ids"`
	Confidence  float64                `json:"confidence"`
}

// DraftQuestionReview is one model-authored per-question review.
type DraftQuestionReview struct {
	QuestionID string       `json:"question_id"`
	Summary    DraftInsight `json:"summary"`
	NextAction DraftInsight `json:"next_action"`
}

// DraftPracticeItem is one model-authored next-run recommendation.
type DraftPracticeItem struct {
	Topic              string                 `json:"topic"`
	Mode               contracts.ScenarioMode `json:"mode"`
	DurationMinutes    int                    `json:"duration_minutes"`
	CompletionCriteria string                 `json:"completion_criteria"`
	EvidenceIDs        []contracts.EvidenceID `json:"evidence_ids"`
}

// Draft is the strict Provider output before evidence resolution and
// deterministic Coach/transfer aggregation.
type Draft struct {
	QuestionReviews []DraftQuestionReview         `json:"question_reviews"`
	Findings        []contracts.EvaluationFinding `json:"findings"`
	CrossInsights   []DraftInsight                `json:"cross_source_insights"`
	PracticePlan    []DraftPracticeItem           `json:"practice_plan"`
}

// Validate checks structural validity while deliberately allowing unknown
// evidence IDs for the local resolver to downgrade safely.
func (draft Draft) Validate() error {
	if draft.QuestionReviews == nil || draft.Findings == nil ||
		draft.CrossInsights == nil || draft.PracticePlan == nil {
		return invalidDraft("all draft arrays must be explicit")
	}
	questions := make(map[string]struct{}, len(draft.QuestionReviews))
	for index, review := range draft.QuestionReviews {
		if strings.TrimSpace(review.QuestionID) == "" {
			return invalidDraft(fmt.Sprintf(
				"question_reviews[%d].question_id is blank",
				index,
			))
		}
		if _, duplicate := questions[review.QuestionID]; duplicate {
			return invalidDraft("question review is duplicated")
		}
		questions[review.QuestionID] = struct{}{}
		if err := validateDraftInsight(review.Summary); err != nil {
			return err
		}
		if err := validateDraftInsight(review.NextAction); err != nil {
			return err
		}
	}
	dimensions := make(map[contracts.EvaluationDimension]struct{}, len(draft.Findings))
	for _, finding := range draft.Findings {
		if err := finding.Validate(); err != nil {
			return err
		}
		if _, duplicate := dimensions[finding.Dimension]; duplicate {
			return invalidDraft("scorecard dimension is duplicated")
		}
		dimensions[finding.Dimension] = struct{}{}
	}
	for _, insight := range draft.CrossInsights {
		if err := validateDraftInsight(insight); err != nil {
			return err
		}
	}
	for index, item := range draft.PracticePlan {
		if strings.TrimSpace(item.Topic) == "" ||
			strings.TrimSpace(item.CompletionCriteria) == "" ||
			item.DurationMinutes <= 0 ||
			item.EvidenceIDs == nil ||
			!validDraftMode(item.Mode) {
			return invalidDraft(fmt.Sprintf(
				"practice_plan[%d] is invalid",
				index,
			))
		}
		for _, id := range item.EvidenceIDs {
			if strings.TrimSpace(string(id)) == "" {
				return invalidDraft("practice evidence id is blank")
			}
		}
	}
	return nil
}

// DecodeDraft strictly decodes and validates one Evaluator result.
func DecodeDraft(payload []byte) (Draft, error) {
	var draft Draft
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&draft); err != nil {
		return Draft{}, invalidDraft(err.Error())
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return Draft{}, invalidDraft(err.Error())
	}
	if err := draft.Validate(); err != nil {
		return Draft{}, err
	}
	return draft, nil
}

func validateDraftInsight(insight DraftInsight) error {
	if strings.TrimSpace(insight.Text) == "" || insight.EvidenceIDs == nil ||
		math.IsNaN(insight.Confidence) || math.IsInf(insight.Confidence, 0) ||
		insight.Confidence < 0 || insight.Confidence > 1 {
		return invalidDraft("draft insight is invalid")
	}
	for _, id := range insight.EvidenceIDs {
		if strings.TrimSpace(string(id)) == "" {
			return invalidDraft("draft insight evidence id is blank")
		}
	}
	return nil
}

func validDraftMode(mode contracts.ScenarioMode) bool {
	return mode == contracts.ScenarioStrict ||
		mode == contracts.ScenarioStandard ||
		mode == contracts.ScenarioCoach
}

func invalidDraft(reason string) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"validate evaluation draft",
		"",
		"评估模型输出不符合报告草稿契约。",
		"保留会话证据并使用保守报告。",
		false,
		errors.New(reason),
	)
}
