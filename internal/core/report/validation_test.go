package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestDocumentValidationEnforcesEvidenceAndCodeApplicability(t *testing.T) {
	t.Parallel()

	valid := validDocument()
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid document: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*Document)
	}{
		{
			name: "unresolved evidence",
			mutate: func(document *Document) {
				document.QuestionReview[0].Summary.EvidenceIDs =
					[]contracts.EvidenceID{"missing"}
			},
		},
		{
			name: "code score without a run",
			mutate: func(document *Document) {
				score := 4
				document.Scorecard[5] = ScorecardItem{
					Dimension:   contracts.DimensionCodeQuality,
					Status:      StatusEvidenceBacked,
					Score:       &score,
					EvidenceIDs: []contracts.EvidenceID{"answer-1"},
					Confidence:  0.8,
					NextAction:  "Keep testing edge cases.",
				}
			},
		},
		{
			name: "personality judgment",
			mutate: func(document *Document) {
				document.CrossInsights[0].Text =
					"Personality fit appears strong."
			},
		},
		{
			name: "insufficient without explicit copy",
			mutate: func(document *Document) {
				document.Scorecard[0] = ScorecardItem{
					Dimension:   contracts.DimensionAnswerStructure,
					Status:      StatusInsufficient,
					EvidenceIDs: []contracts.EvidenceID{},
					NextAction:  "Collect more evidence.",
				}
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			document := validDocument()
			test.mutate(&document)
			if err := document.Validate(); !domainerr.IsCode(
				err,
				domainerr.CodeValidation,
			) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestDecodeIsStrictAndPreservesExplicitInsufficientState(t *testing.T) {
	t.Parallel()

	document := validDocument()
	document.Scorecard[0] = ScorecardItem{
		Dimension:   contracts.DimensionAnswerStructure,
		Status:      StatusInsufficient,
		EvidenceIDs: []contracts.EvidenceID{},
		NextAction:  "不足以判断；需要更多已提交回答。",
	}
	payload, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	decoded, err := Decode(payload)
	if err != nil ||
		decoded.Scorecard[0].Status != StatusInsufficient {
		t.Fatalf("decoded=%#v err=%v", decoded.Scorecard[0], err)
	}
	withUnknown := strings.Replace(
		string(payload),
		`"degraded":false`,
		`"degraded":false,"unknown":true`,
		1,
	)
	if _, err := Decode([]byte(withUnknown)); !domainerr.IsCode(
		err,
		domainerr.CodePersistenceFailed,
	) {
		t.Fatalf("strict decode error=%v", err)
	}
}

func validDocument() Document {
	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	scorecard := make([]ScorecardItem, 0, 8)
	for _, dimension := range FixedDimensions() {
		if dimension == contracts.DimensionCodeQuality {
			scorecard = append(scorecard, ScorecardItem{
				Dimension:   dimension,
				Status:      StatusNotApplicable,
				EvidenceIDs: []contracts.EvidenceID{},
				NextAction:  "本场没有已运行代码，代码质量不适用。",
			})
			continue
		}
		score := 3
		scorecard = append(scorecard, ScorecardItem{
			Dimension:   dimension,
			Status:      StatusEvidenceBacked,
			Score:       &score,
			EvidenceIDs: []contracts.EvidenceID{"answer-1"},
			Confidence:  0.8,
			NextAction:  "Repeat the answer with one explicit trade-off.",
		})
	}
	plans := make([]PracticeItem, 0, 3)
	for _, topic := range []string{
		"回答结构",
		"技术深度",
		"问题澄清",
	} {
		plans = append(plans, PracticeItem{
			Topic:              topic,
			Mode:               contracts.ScenarioStrict,
			DurationMinutes:    15,
			CompletionCriteria: "Complete one timed answer and review the trace.",
			Status:             StatusEvidenceBacked,
			EvidenceIDs:        []contracts.EvidenceID{"answer-1"},
		})
	}
	return Document{
		ID:            "report-session-1",
		SchemaVersion: SchemaVersion,
		GeneratedAt:   now.Add(20 * time.Minute),
		Summary: SessionSummary{
			SessionID:        "session-1",
			ScenarioID:       "scenario-1",
			Template:         "project_deep_dive",
			Mode:             contracts.ScenarioStrict,
			StartedAt:        now,
			CompletedAt:      now.Add(15 * time.Minute),
			DurationSeconds:  900,
			QuestionCount:    1,
			CoachPromptCount: 0,
			CodeRunCount:     0,
		},
		Evidence: []EvidenceLink{{
			ID:         "answer-1",
			Kind:       "session_user",
			QuestionID: "Q1",
			Label:      "user Q1",
			OccurredAt: now.Add(5 * time.Minute),
		}},
		QuestionReview: []QuestionReview{{
			QuestionID: "Q1",
			Prompt:     "Explain the trade-off.",
			Summary: Insight{
				Text:        "The answer named one trade-off.",
				Status:      StatusEvidenceBacked,
				EvidenceIDs: []contracts.EvidenceID{"answer-1"},
				Confidence:  0.8,
			},
			NextAction: Insight{
				Text:        "Add one measurable result.",
				Status:      StatusEvidenceBacked,
				EvidenceIDs: []contracts.EvidenceID{"answer-1"},
				Confidence:  0.8,
			},
		}},
		Scorecard:   scorecard,
		LearningMap: []LearningGap{},
		Transfer:    []TransferEvidence{},
		CrossInsights: []Insight{{
			Text:        "The answer and question form a reviewable chain.",
			Status:      StatusEvidenceBacked,
			EvidenceIDs: []contracts.EvidenceID{"answer-1"},
			Confidence:  0.8,
		}},
		PracticePlan: plans,
	}
}
