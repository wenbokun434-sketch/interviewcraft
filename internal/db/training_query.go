package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
)

const defaultTrainingHomeLimit = 5

// DimensionScore is a labeled report score suitable for a compact home row.
// A score is never returned without its dimension.
type DimensionScore struct {
	Dimension contracts.EvaluationDimension
	Score     int
	Scale     int
}

// RecentTraining is one persisted session shown on the training home.
type RecentTraining struct {
	SessionID  string
	ScenarioID string
	Template   string
	Mode       contracts.ScenarioMode
	Status     SessionStatus
	UpdatedAt  time.Time
	ReportID   string
	Score      *DimensionScore
}

// PracticeItem is one report-derived next-run recommendation.
type PracticeItem struct {
	ID                 string
	ReportID           string
	SessionID          string
	Topic              string
	Mode               contracts.ScenarioMode
	DurationMinutes    int
	CompletionCriteria string
}

// ResumePoint restores an active session from its last immutable event. Draft
// remains separate so it cannot be mistaken for submitted evidence.
type ResumePoint struct {
	Session   Session
	Scenario  contracts.Scenario
	LastEvent *SessionEvent
	Draft     *Draft
}

// TrainingHomeData is the complete read model used by P-01.
type TrainingHomeData struct {
	Recent        []RecentTraining
	PracticeQueue []PracticeItem
	Resume        *ResumePoint
}

// LoadTrainingHome reads the P-01 home model without mutating session history.
func (s *Store) LoadTrainingHome(
	ctx context.Context,
	limit int,
) (TrainingHomeData, error) {
	if s == nil || s.sql == nil {
		return TrainingHomeData{}, storageError(
			"load training home",
			"",
			"重新打开本地数据目录后按 [t] 重试。",
			errors.New("store is closed"),
		)
	}
	if limit <= 0 {
		limit = defaultTrainingHomeLimit
	}

	recent, err := s.loadRecentTraining(ctx, limit)
	if err != nil {
		return TrainingHomeData{}, err
	}
	queue, err := s.loadPracticeQueue(ctx, limit)
	if err != nil {
		return TrainingHomeData{}, err
	}
	resume, err := s.loadResumePoint(ctx)
	if err != nil {
		return TrainingHomeData{}, err
	}
	return TrainingHomeData{
		Recent:        recent,
		PracticeQueue: queue,
		Resume:        resume,
	}, nil
}

func (s *Store) loadRecentTraining(
	ctx context.Context,
	limit int,
) ([]RecentTraining, error) {
	rows, err := s.sql.QueryContext(ctx, `
		SELECT
			sessions.id,
			sessions.scenario_id,
			sessions.status,
			sessions.updated_at,
			scenarios.payload_json,
			reports.id,
			reports.payload_json
		FROM sessions
		JOIN scenarios ON scenarios.id = sessions.scenario_id
		LEFT JOIN reports ON reports.session_id = sessions.id
		ORDER BY sessions.updated_at DESC, sessions.id DESC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, s.trainingQueryError("query recent training", err)
	}
	defer rows.Close()

	recent := make([]RecentTraining, 0, limit)
	for rows.Next() {
		var item RecentTraining
		var status string
		var updatedAt string
		var scenarioPayload string
		var reportID sql.NullString
		var reportPayload sql.NullString
		if err := rows.Scan(
			&item.SessionID,
			&item.ScenarioID,
			&status,
			&updatedAt,
			&scenarioPayload,
			&reportID,
			&reportPayload,
		); err != nil {
			return nil, s.trainingQueryError("read recent training", err)
		}

		item.Status = SessionStatus(status)
		if !validSessionStatus(item.Status) {
			return nil, corruptedData(
				"decode recent session state",
				s.paths.Database,
				fmt.Errorf("invalid session status %q", status),
			)
		}
		item.UpdatedAt, err = parseTime(updatedAt)
		if err != nil {
			return nil, corruptedData("parse recent session time", s.paths.Database, err)
		}
		scenario, decodeErr := contracts.DecodeScenario([]byte(scenarioPayload))
		if decodeErr != nil {
			return nil, corruptedData("decode recent scenario", s.paths.Database, decodeErr)
		}
		item.Template = scenario.Template
		item.Mode = scenario.Mode
		if reportID.Valid {
			item.ReportID = reportID.String
		}
		if reportPayload.Valid {
			item.Score = firstDimensionScore([]byte(reportPayload.String))
		}
		recent = append(recent, item)
	}
	if err := rows.Err(); err != nil {
		return nil, s.trainingQueryError("iterate recent training", err)
	}
	return recent, nil
}

func (s *Store) loadPracticeQueue(
	ctx context.Context,
	limit int,
) ([]PracticeItem, error) {
	rows, err := s.sql.QueryContext(ctx, `
		SELECT reports.id, reports.session_id, reports.payload_json
		FROM reports
		ORDER BY reports.updated_at DESC, reports.id DESC
	`)
	if err != nil {
		return nil, s.trainingQueryError("query practice queue", err)
	}
	defer rows.Close()

	queue := make([]PracticeItem, 0, limit)
	seen := make(map[string]struct{})
	for rows.Next() {
		var reportID string
		var sessionID string
		var payload []byte
		if err := rows.Scan(&reportID, &sessionID, &payload); err != nil {
			return nil, s.trainingQueryError("read practice queue", err)
		}
		for index, parsed := range parsePracticePlan(payload) {
			key := strings.ToLower(parsed.Topic) + "\x00" + string(parsed.Mode)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			parsed.ReportID = reportID
			parsed.SessionID = sessionID
			if parsed.ID == "" {
				parsed.ID = fmt.Sprintf("%s-practice-%d", reportID, index+1)
			}
			queue = append(queue, parsed)
			if len(queue) == limit {
				break
			}
		}
		if len(queue) == limit {
			break
		}
	}
	if err := rows.Err(); err != nil {
		return nil, s.trainingQueryError("iterate practice queue", err)
	}
	return queue, nil
}

func (s *Store) loadResumePoint(ctx context.Context) (*ResumePoint, error) {
	var session Session
	var status string
	var startedAt string
	var updatedAt string
	err := s.sql.QueryRowContext(ctx, `
		SELECT id, scenario_id, status, started_at, updated_at
		FROM sessions
		WHERE status = ?
		ORDER BY updated_at DESC, id DESC
		LIMIT 1
	`, SessionActive).Scan(
		&session.ID,
		&session.ScenarioID,
		&status,
		&startedAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, s.trainingQueryError("query resumable session", err)
	}
	session.Status = SessionStatus(status)
	session.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return nil, corruptedData("parse resumable session start", s.paths.Database, err)
	}
	session.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return nil, corruptedData("parse resumable session update", s.paths.Database, err)
	}

	scenario, found, err := s.GetScenario(ctx, session.ScenarioID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, corruptedData(
			"restore resumable session",
			s.paths.Database,
			errors.New("active session scenario is missing"),
		)
	}
	events, err := s.ListSessionEvents(ctx, session.ID)
	if err != nil {
		return nil, err
	}
	result := &ResumePoint{Session: session, Scenario: scenario}
	questionID := ""
	if len(events) > 0 {
		last := events[len(events)-1]
		result.LastEvent = &last
		questionID = last.QuestionID
	} else if len(scenario.Questions) > 0 {
		questionID = scenario.Questions[0].ID
	}
	if questionID != "" {
		draft, draftFound, draftErr := s.LoadDraft(
			ctx,
			session.ID,
			questionID,
			DraftAnswer,
		)
		if draftErr != nil {
			return nil, draftErr
		}
		if draftFound {
			result.Draft = &draft
		}
	}
	return result, nil
}

func (s *Store) trainingQueryError(operation string, cause error) error {
	return storageError(
		operation,
		s.paths.Database,
		"检查本地数据库后按 [t] 重试，或运行 `interviewcraft doctor`。",
		cause,
	)
}

type homeReportPayload struct {
	Scorecard    json.RawMessage    `json:"scorecard"`
	PracticePlan []homePracticeItem `json:"practice_plan"`
}

type homePracticeItem struct {
	ID                 string                 `json:"id"`
	Topic              string                 `json:"topic"`
	Mode               contracts.ScenarioMode `json:"mode"`
	DurationMinutes    int                    `json:"duration_minutes"`
	DurationSeconds    int                    `json:"duration_seconds"`
	CompletionCriteria string                 `json:"completion_criteria"`
}

func parsePracticePlan(payload []byte) []PracticeItem {
	var report homeReportPayload
	if json.Unmarshal(payload, &report) != nil {
		return nil
	}
	items := make([]PracticeItem, 0, len(report.PracticePlan))
	for _, item := range report.PracticePlan {
		topic := strings.TrimSpace(item.Topic)
		if topic == "" {
			continue
		}
		duration := item.DurationMinutes
		if duration <= 0 && item.DurationSeconds > 0 {
			duration = (item.DurationSeconds + 59) / 60
		}
		items = append(items, PracticeItem{
			ID:                 strings.TrimSpace(item.ID),
			Topic:              topic,
			Mode:               item.Mode,
			DurationMinutes:    duration,
			CompletionCriteria: strings.TrimSpace(item.CompletionCriteria),
		})
	}
	return items
}

type scorecardItem struct {
	Dimension contracts.EvaluationDimension `json:"dimension"`
	Score     *int                          `json:"score"`
}

func firstDimensionScore(payload []byte) *DimensionScore {
	var report homeReportPayload
	if json.Unmarshal(payload, &report) != nil || len(report.Scorecard) == 0 {
		return nil
	}

	var list []scorecardItem
	if json.Unmarshal(report.Scorecard, &list) == nil {
		for _, item := range list {
			if item.Score != nil && *item.Score >= 1 && *item.Score <= 5 &&
				item.Dimension != "" {
				return &DimensionScore{
					Dimension: item.Dimension,
					Score:     *item.Score,
					Scale:     5,
				}
			}
		}
	}

	var object map[string]json.RawMessage
	if json.Unmarshal(report.Scorecard, &object) != nil {
		return nil
	}
	order := []contracts.EvaluationDimension{
		contracts.DimensionTechnicalDepth,
		contracts.DimensionAnswerStructure,
		contracts.DimensionExperienceCredibility,
		contracts.DimensionProblemClarification,
		contracts.DimensionProblemSolving,
		contracts.DimensionCodeQuality,
		contracts.DimensionTimeManagement,
		contracts.DimensionIndependence,
	}
	for _, dimension := range order {
		raw, exists := object[string(dimension)]
		if !exists {
			continue
		}
		var score int
		if json.Unmarshal(raw, &score) != nil {
			var wrapped struct {
				Score *int `json:"score"`
			}
			if json.Unmarshal(raw, &wrapped) != nil || wrapped.Score == nil {
				continue
			}
			score = *wrapped.Score
		}
		if score >= 1 && score <= 5 {
			return &DimensionScore{Dimension: dimension, Score: score, Scale: 5}
		}
	}
	return nil
}
