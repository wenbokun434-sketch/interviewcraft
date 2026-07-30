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
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// SaveProfile creates or updates one validated profile.
func (s *Store) SaveProfile(
	ctx context.Context,
	id string,
	profile contracts.CandidateProfile,
	confirmedAt *time.Time,
) error {
	if err := requireText("profile id", id); err != nil {
		return err
	}
	if err := profile.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(profile)
	if err != nil {
		return invalidInput("encode profile", "画像无法编码为 JSON。", err)
	}
	now := nowText()
	var confirmed any
	if confirmedAt != nil {
		confirmed = timeText(*confirmedAt)
	}
	_, err = s.sql.ExecContext(ctx, `
		INSERT INTO candidate_profiles(
			id, payload_json, confirmed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			payload_json = excluded.payload_json,
			confirmed_at = excluded.confirmed_at,
			updated_at = excluded.updated_at
	`, id, string(payload), confirmed, now, now)
	if err != nil {
		return storageError(
			"save profile",
			s.paths.Database,
			"保留当前输入，检查数据库后重试保存。",
			err,
		)
	}
	return nil
}

// GetProfile reads and validates one stored profile.
func (s *Store) GetProfile(
	ctx context.Context,
	id string,
) (contracts.CandidateProfile, bool, error) {
	var result contracts.CandidateProfile
	if err := requireText("profile id", id); err != nil {
		return result, false, err
	}
	var payload string
	err := s.sql.QueryRowContext(
		ctx,
		`SELECT payload_json FROM candidate_profiles WHERE id = ?`,
		id,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, storageError(
			"read profile",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	result, err = contracts.DecodeCandidateProfile([]byte(payload))
	if err != nil {
		return contracts.CandidateProfile{}, false, corruptedData(
			"decode stored profile",
			s.paths.Database,
			err,
		)
	}
	return result, true, nil
}

// SaveScenario creates or updates one validated scenario.
func (s *Store) SaveScenario(
	ctx context.Context,
	id string,
	profileID string,
	scenario contracts.Scenario,
	confirmedAt time.Time,
) error {
	if err := requireText("scenario id", id); err != nil {
		return err
	}
	if err := requireText("profile id", profileID); err != nil {
		return err
	}
	if confirmedAt.IsZero() {
		return invalidInput("save scenario", "场景确认时间不能为空。", nil)
	}
	if err := scenario.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(scenario)
	if err != nil {
		return invalidInput("encode scenario", "场景无法编码为 JSON。", err)
	}
	now := nowText()
	_, err = s.sql.ExecContext(ctx, `
		INSERT INTO scenarios(
			id, profile_id, payload_json, prompt_version,
			confirmed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			profile_id = excluded.profile_id,
			payload_json = excluded.payload_json,
			prompt_version = excluded.prompt_version,
			confirmed_at = excluded.confirmed_at,
			updated_at = excluded.updated_at
	`, id, profileID, string(payload), scenario.PromptVersion,
		timeText(confirmedAt), now, now)
	if err != nil {
		return storageError(
			"save scenario",
			s.paths.Database,
			"确认画像仍存在且数据库可写后重试。",
			err,
		)
	}
	return nil
}

// GetScenario reads and validates one stored scenario.
func (s *Store) GetScenario(
	ctx context.Context,
	id string,
) (contracts.Scenario, bool, error) {
	var result contracts.Scenario
	if err := requireText("scenario id", id); err != nil {
		return result, false, err
	}
	var payload string
	err := s.sql.QueryRowContext(
		ctx,
		`SELECT payload_json FROM scenarios WHERE id = ?`,
		id,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return result, false, nil
	}
	if err != nil {
		return result, false, storageError(
			"read scenario",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	result, err = contracts.DecodeScenario([]byte(payload))
	if err != nil {
		return contracts.Scenario{}, false, corruptedData(
			"decode stored scenario",
			s.paths.Database,
			err,
		)
	}
	return result, true, nil
}

// CreateSession inserts a new session for a confirmed scenario.
func (s *Store) CreateSession(ctx context.Context, session Session) error {
	if err := validateSession(session); err != nil {
		return err
	}
	_, err := s.sql.ExecContext(ctx, `
		INSERT INTO sessions(id, scenario_id, status, started_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
	`, session.ID, session.ScenarioID, session.Status,
		timeText(session.StartedAt), timeText(session.UpdatedAt))
	if err != nil {
		return storageError(
			"create session",
			s.paths.Database,
			"确认场景仍存在且数据库可写后重试。",
			err,
		)
	}
	return nil
}

// GetSession reads mutable session metadata without altering its event stream.
func (s *Store) GetSession(
	ctx context.Context,
	sessionID string,
) (Session, bool, error) {
	var session Session
	if err := requireText("session id", sessionID); err != nil {
		return session, false, err
	}
	var startedAt string
	var updatedAt string
	err := s.sql.QueryRowContext(ctx, `
		SELECT scenario_id, status, started_at, updated_at
		FROM sessions
		WHERE id = ?
	`, sessionID).Scan(
		&session.ScenarioID,
		&session.Status,
		&startedAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, false, nil
	}
	if err != nil {
		return Session{}, false, storageError(
			"read session",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	session.ID = sessionID
	session.StartedAt, err = parseTime(startedAt)
	if err != nil {
		return Session{}, false, corruptedData("parse session time", s.paths.Database, err)
	}
	session.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Session{}, false, corruptedData("parse session time", s.paths.Database, err)
	}
	if !validSessionStatus(session.Status) {
		return Session{}, false, corruptedData(
			"decode session state",
			s.paths.Database,
			fmt.Errorf("invalid session status %q", session.Status),
		)
	}
	return session, true, nil
}

// UpdateSessionStatus changes mutable session state without rewriting events.
func (s *Store) UpdateSessionStatus(
	ctx context.Context,
	sessionID string,
	status SessionStatus,
	updatedAt time.Time,
) (bool, error) {
	if err := requireText("session id", sessionID); err != nil {
		return false, err
	}
	if !validSessionStatus(status) {
		return false, invalidInput("update session", "会话状态无效。", nil)
	}
	if updatedAt.IsZero() {
		return false, invalidInput("update session", "会话更新时间不能为空。", nil)
	}
	result, err := s.sql.ExecContext(
		ctx,
		`UPDATE sessions SET status = ?, updated_at = ? WHERE id = ?`,
		status,
		timeText(updatedAt),
		sessionID,
	)
	if err != nil {
		return false, storageError(
			"update session",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, storageError(
			"read session update result",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return count == 1, nil
}

// AppendSessionEvent adds one immutable event. No update API is exposed.
func (s *Store) AppendSessionEvent(ctx context.Context, event SessionEvent) error {
	if err := validateSessionEvent(event); err != nil {
		return err
	}
	evidence, err := json.Marshal(event.EvidenceRefs)
	if err != nil {
		return invalidInput("encode event evidence", "事件证据无法编码。", err)
	}
	_, err = s.sql.ExecContext(ctx, `
		INSERT INTO session_events(
			event_id, session_id, speaker, question_id,
			content, occurred_at, evidence_refs_json
		) VALUES (?, ?, ?, ?, ?, ?, ?)
	`, event.EventID, event.SessionID, event.Speaker, event.QuestionID,
		event.Content, timeText(event.OccurredAt), string(evidence))
	if err != nil {
		return storageError(
			"append session event",
			s.paths.Database,
			"保留原事件，确认会话存在且事件未重复后重试。",
			err,
		)
	}
	return nil
}

// ListSessionEvents returns immutable events in timestamp and insertion order.
func (s *Store) ListSessionEvents(
	ctx context.Context,
	sessionID string,
) ([]SessionEvent, error) {
	if err := requireText("session id", sessionID); err != nil {
		return nil, err
	}
	rows, err := s.sql.QueryContext(ctx, `
		SELECT sequence, event_id, speaker, question_id,
		       content, occurred_at, evidence_refs_json
		FROM session_events
		WHERE session_id = ?
		ORDER BY julianday(occurred_at), sequence
	`, sessionID)
	if err != nil {
		return nil, storageError(
			"list session events",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	defer rows.Close()

	events := make([]SessionEvent, 0)
	for rows.Next() {
		event := SessionEvent{SessionID: sessionID}
		var occurredAt string
		var evidence string
		if err := rows.Scan(
			&event.Sequence,
			&event.EventID,
			&event.Speaker,
			&event.QuestionID,
			&event.Content,
			&occurredAt,
			&evidence,
		); err != nil {
			return nil, corruptedData("scan session event", s.paths.Database, err)
		}
		event.OccurredAt, err = parseTime(occurredAt)
		if err != nil {
			return nil, corruptedData("parse session event time", s.paths.Database, err)
		}
		if err := json.Unmarshal([]byte(evidence), &event.EvidenceRefs); err != nil {
			return nil, corruptedData("decode event evidence", s.paths.Database, err)
		}
		if event.EvidenceRefs == nil {
			return nil, corruptedData(
				"decode event evidence",
				s.paths.Database,
				errors.New("evidence array is null"),
			)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(
			"list session events",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return events, nil
}

// SaveDraft creates or replaces a local, unsubmitted draft.
func (s *Store) SaveDraft(ctx context.Context, draft Draft) error {
	if err := validateDraft(draft); err != nil {
		return err
	}
	_, err := s.sql.ExecContext(ctx, `
		INSERT INTO drafts(session_id, question_id, kind, content, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id, question_id, kind) DO UPDATE SET
			content = excluded.content,
			updated_at = excluded.updated_at
	`, draft.SessionID, draft.QuestionID, draft.Kind,
		draft.Content, timeText(draft.UpdatedAt))
	if err != nil {
		return storageError(
			"save draft",
			s.paths.Database,
			"保留编辑内容，检查数据库后重试。",
			err,
		)
	}
	return nil
}

// LoadDraft reads one local draft without exposing it as a session event.
func (s *Store) LoadDraft(
	ctx context.Context,
	sessionID string,
	questionID string,
	kind DraftKind,
) (Draft, bool, error) {
	var draft Draft
	if err := requireText("session id", sessionID); err != nil {
		return draft, false, err
	}
	if err := requireText("question id", questionID); err != nil {
		return draft, false, err
	}
	if !validDraftKind(kind) {
		return draft, false, invalidInput("load draft", "草稿类型无效。", nil)
	}
	var updatedAt string
	err := s.sql.QueryRowContext(ctx, `
		SELECT content, updated_at
		FROM drafts
		WHERE session_id = ? AND question_id = ? AND kind = ?
	`, sessionID, questionID, kind).Scan(&draft.Content, &updatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Draft{}, false, nil
	}
	if err != nil {
		return Draft{}, false, storageError(
			"load draft",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	draft.SessionID = sessionID
	draft.QuestionID = questionID
	draft.Kind = kind
	draft.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Draft{}, false, corruptedData("parse draft time", s.paths.Database, err)
	}
	return draft, true, nil
}

// AddSidebarEvent persists one Coach learning event.
func (s *Store) AddSidebarEvent(ctx context.Context, event SidebarEvent) error {
	if err := validateSidebarEvent(event); err != nil {
		return err
	}
	tags, err := json.Marshal(event.Tags)
	if err != nil {
		return invalidInput("encode sidebar tags", "Coach 标签无法编码。", err)
	}
	_, err = s.sql.ExecContext(ctx, `
		INSERT INTO sidebar_events(
			id, session_id, question_id, intent, help_level,
			tags_json, outcome, paused_timer, occurred_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, event.ID, event.SessionID, event.QuestionID, event.Intent,
		event.HelpLevel, string(tags), event.Outcome,
		boolInteger(event.PausedTimer), timeText(event.OccurredAt))
	if err != nil {
		return storageError(
			"save Coach event",
			s.paths.Database,
			"保留当前学习状态，检查数据库后重试。",
			err,
		)
	}
	return nil
}

// ListSidebarEvents returns Coach events in stable chronological order.
func (s *Store) ListSidebarEvents(
	ctx context.Context,
	sessionID string,
) ([]SidebarEvent, error) {
	if err := requireText("session id", sessionID); err != nil {
		return nil, err
	}
	rows, err := s.sql.QueryContext(ctx, `
		SELECT id, question_id, intent, help_level, tags_json,
		       outcome, paused_timer, occurred_at
		FROM sidebar_events
		WHERE session_id = ?
		ORDER BY occurred_at, id
	`, sessionID)
	if err != nil {
		return nil, storageError(
			"list Coach events",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	defer rows.Close()

	events := make([]SidebarEvent, 0)
	for rows.Next() {
		event := SidebarEvent{SessionID: sessionID}
		var tags string
		var paused int
		var occurredAt string
		if err := rows.Scan(
			&event.ID,
			&event.QuestionID,
			&event.Intent,
			&event.HelpLevel,
			&tags,
			&event.Outcome,
			&paused,
			&occurredAt,
		); err != nil {
			return nil, corruptedData("scan Coach event", s.paths.Database, err)
		}
		if err := json.Unmarshal([]byte(tags), &event.Tags); err != nil {
			return nil, corruptedData("decode Coach tags", s.paths.Database, err)
		}
		event.PausedTimer = paused == 1
		event.OccurredAt, err = parseTime(occurredAt)
		if err != nil {
			return nil, corruptedData("parse Coach event time", s.paths.Database, err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(
			"list Coach events",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return events, nil
}

// AddCodeSubmission persists one immutable executed code snapshot.
func (s *Store) AddCodeSubmission(ctx context.Context, submission CodeSubmission) error {
	if err := validateCodeSubmission(submission); err != nil {
		return err
	}
	_, err := s.sql.ExecContext(ctx, `
		INSERT INTO code_submissions(
			id, session_id, question_id, language, source,
			test_result_json, runtime_stats_json, snapshot_id, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, submission.ID, submission.SessionID, submission.QuestionID,
		submission.Language, submission.Source, string(submission.TestResult),
		string(submission.RuntimeStats), submission.SnapshotID,
		timeText(submission.CreatedAt))
	if err != nil {
		return storageError(
			"save code submission",
			s.paths.Database,
			"保留代码，确认会话存在后重试。",
			err,
		)
	}
	return nil
}

// ListCodeSubmissions returns executed snapshots in stable order.
func (s *Store) ListCodeSubmissions(
	ctx context.Context,
	sessionID string,
) ([]CodeSubmission, error) {
	if err := requireText("session id", sessionID); err != nil {
		return nil, err
	}
	rows, err := s.sql.QueryContext(ctx, `
		SELECT id, question_id, language, source, test_result_json,
		       runtime_stats_json, snapshot_id, created_at
		FROM code_submissions
		WHERE session_id = ?
		ORDER BY created_at, id
	`, sessionID)
	if err != nil {
		return nil, storageError(
			"list code submissions",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	defer rows.Close()

	submissions := make([]CodeSubmission, 0)
	for rows.Next() {
		item := CodeSubmission{SessionID: sessionID}
		var testResult string
		var runtimeStats string
		var createdAt string
		if err := rows.Scan(
			&item.ID,
			&item.QuestionID,
			&item.Language,
			&item.Source,
			&testResult,
			&runtimeStats,
			&item.SnapshotID,
			&createdAt,
		); err != nil {
			return nil, corruptedData("scan code submission", s.paths.Database, err)
		}
		item.TestResult = json.RawMessage(testResult)
		item.RuntimeStats = json.RawMessage(runtimeStats)
		if err := requireJSONObject("test result", item.TestResult); err != nil {
			return nil, corruptedData("decode test result", s.paths.Database, err)
		}
		if err := requireJSONObject("runtime stats", item.RuntimeStats); err != nil {
			return nil, corruptedData("decode runtime stats", s.paths.Database, err)
		}
		item.CreatedAt, err = parseTime(createdAt)
		if err != nil {
			return nil, corruptedData("parse code submission time", s.paths.Database, err)
		}
		submissions = append(submissions, item)
	}
	if err := rows.Err(); err != nil {
		return nil, storageError(
			"list code submissions",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return submissions, nil
}

// SaveReport creates or updates the single report for a session.
func (s *Store) SaveReport(ctx context.Context, report Report) error {
	if err := validateReport(report); err != nil {
		return err
	}
	_, err := s.sql.ExecContext(ctx, `
		INSERT INTO reports(id, session_id, payload_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			id = excluded.id,
			payload_json = excluded.payload_json,
			updated_at = excluded.updated_at
	`, report.ID, report.SessionID, string(report.Payload),
		timeText(report.CreatedAt), timeText(report.UpdatedAt))
	if err != nil {
		return storageError(
			"save report",
			s.paths.Database,
			"确认会话存在且数据库可写后重试。",
			err,
		)
	}
	return nil
}

// GetReport reads the report for one session.
func (s *Store) GetReport(
	ctx context.Context,
	sessionID string,
) (Report, bool, error) {
	var report Report
	if err := requireText("session id", sessionID); err != nil {
		return report, false, err
	}
	var payload string
	var createdAt string
	var updatedAt string
	err := s.sql.QueryRowContext(ctx, `
		SELECT id, payload_json, created_at, updated_at
		FROM reports WHERE session_id = ?
	`, sessionID).Scan(
		&report.ID,
		&payload,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, false, nil
	}
	if err != nil {
		return Report{}, false, storageError(
			"read report",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	report.SessionID = sessionID
	report.Payload = json.RawMessage(payload)
	report.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Report{}, false, corruptedData("parse report time", s.paths.Database, err)
	}
	report.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Report{}, false, corruptedData("parse report time", s.paths.Database, err)
	}
	if err := requireJSONObject("report payload", report.Payload); err != nil {
		return Report{}, false, corruptedData("decode report", s.paths.Database, err)
	}
	return report, true, nil
}

// DeleteProfile removes a profile and all derived rows in one transaction.
func (s *Store) DeleteProfile(ctx context.Context, profileID string) (bool, error) {
	if err := requireText("profile id", profileID); err != nil {
		return false, err
	}
	transaction, err := s.sql.BeginTx(ctx, nil)
	if err != nil {
		return false, storageError(
			"start profile deletion",
			s.paths.Database,
			"保留现有数据并重试。",
			err,
		)
	}
	result, err := transaction.ExecContext(
		ctx,
		`DELETE FROM candidate_profiles WHERE id = ?`,
		profileID,
	)
	if err != nil {
		_ = transaction.Rollback()
		return false, storageError(
			"delete profile",
			s.paths.Database,
			"现有数据未被确认删除；查看日志后重试。",
			err,
		)
	}
	count, err := result.RowsAffected()
	if err != nil {
		_ = transaction.Rollback()
		return false, storageError(
			"read profile deletion result",
			s.paths.Database,
			"现有数据未被确认删除；查看日志后重试。",
			err,
		)
	}
	if err := transaction.Commit(); err != nil {
		return false, storageError(
			"commit profile deletion",
			s.paths.Database,
			"现有数据未被确认删除；查看日志后重试。",
			err,
		)
	}
	return count == 1, nil
}

func validateSession(session Session) error {
	if err := requireText("session id", session.ID); err != nil {
		return err
	}
	if err := requireText("scenario id", session.ScenarioID); err != nil {
		return err
	}
	if !validSessionStatus(session.Status) {
		return invalidInput("create session", "会话状态无效。", nil)
	}
	if session.StartedAt.IsZero() || session.UpdatedAt.IsZero() {
		return invalidInput("create session", "会话时间不能为空。", nil)
	}
	return nil
}

func validateSessionEvent(event SessionEvent) error {
	for field, value := range map[string]string{
		"event id":    event.EventID,
		"session id":  event.SessionID,
		"question id": event.QuestionID,
		"content":     event.Content,
	} {
		if err := requireText(field, value); err != nil {
			return err
		}
	}
	if !validEventSpeaker(event.Speaker) {
		return invalidInput("append session event", "事件来源无效。", nil)
	}
	if event.OccurredAt.IsZero() {
		return invalidInput("append session event", "事件时间不能为空。", nil)
	}
	if event.EvidenceRefs == nil {
		return invalidInput("append session event", "证据引用必须是数组。", nil)
	}
	for _, evidenceID := range event.EvidenceRefs {
		if strings.TrimSpace(string(evidenceID)) == "" {
			return invalidInput("append session event", "证据引用不能为空。", nil)
		}
	}
	return nil
}

func validateDraft(draft Draft) error {
	if err := requireText("session id", draft.SessionID); err != nil {
		return err
	}
	if err := requireText("question id", draft.QuestionID); err != nil {
		return err
	}
	if !validDraftKind(draft.Kind) {
		return invalidInput("save draft", "草稿类型无效。", nil)
	}
	if draft.UpdatedAt.IsZero() {
		return invalidInput("save draft", "草稿更新时间不能为空。", nil)
	}
	return nil
}

func validateSidebarEvent(event SidebarEvent) error {
	for field, value := range map[string]string{
		"Coach event id": event.ID,
		"session id":     event.SessionID,
		"question id":    event.QuestionID,
		"outcome":        event.Outcome,
	} {
		if err := requireText(field, value); err != nil {
			return err
		}
	}
	response := contracts.CoachResponse{
		Intent:            event.Intent,
		HelpLevel:         event.HelpLevel,
		KnowledgeTags:     event.Tags,
		RecommendedAction: "persist event",
	}
	if err := response.Validate(); err != nil {
		return err
	}
	if event.OccurredAt.IsZero() {
		return invalidInput("save Coach event", "Coach 事件时间不能为空。", nil)
	}
	return nil
}

func validateCodeSubmission(submission CodeSubmission) error {
	for field, value := range map[string]string{
		"submission id": submission.ID,
		"session id":    submission.SessionID,
		"question id":   submission.QuestionID,
		"language":      submission.Language,
		"source":        submission.Source,
		"snapshot id":   submission.SnapshotID,
	} {
		if err := requireText(field, value); err != nil {
			return err
		}
	}
	if err := requireJSONObject("test result", submission.TestResult); err != nil {
		return err
	}
	if err := requireJSONObject("runtime stats", submission.RuntimeStats); err != nil {
		return err
	}
	if submission.CreatedAt.IsZero() {
		return invalidInput("save code submission", "代码提交时间不能为空。", nil)
	}
	return nil
}

func validateReport(report Report) error {
	if err := requireText("report id", report.ID); err != nil {
		return err
	}
	if err := requireText("session id", report.SessionID); err != nil {
		return err
	}
	if err := requireJSONObject("report payload", report.Payload); err != nil {
		return err
	}
	if report.CreatedAt.IsZero() || report.UpdatedAt.IsZero() {
		return invalidInput("save report", "报告时间不能为空。", nil)
	}
	return nil
}

func requireJSONObject(field string, data json.RawMessage) error {
	if !json.Valid(data) {
		return invalidInput("validate JSON", field+" 必须是有效 JSON 对象。", nil)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		if err == nil {
			err = errors.New("JSON value is not an object")
		}
		return invalidInput("validate JSON", field+" 必须是有效 JSON 对象。", err)
	}
	return nil
}

func requireText(field string, value string) error {
	if strings.TrimSpace(value) == "" {
		return invalidInput("validate storage input", field+" 不能为空。", nil)
	}
	return nil
}

func invalidInput(operation string, message string, cause error) *domainerr.Error {
	if cause == nil {
		return domainerr.New(
			domainerr.CodeValidation,
			operation,
			message,
			"修正标记字段后重试。",
			false,
		)
	}
	return domainerr.Wrap(
		domainerr.CodeValidation,
		operation,
		"",
		message,
		"修正标记字段后重试。",
		false,
		cause,
	)
}

func corruptedData(operation string, path string, cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"SQLite",
		fmt.Sprintf("本地数据库包含无法读取的数据，路径：%s。", path),
		"保留数据库文件并查看日志后恢复或导出。",
		false,
		cause,
	)
}

func validSessionStatus(status SessionStatus) bool {
	return status == SessionActive ||
		status == SessionEvaluationPending ||
		status == SessionCompleted
}

func validEventSpeaker(speaker EventSpeaker) bool {
	switch speaker {
	case SpeakerInterviewer, SpeakerUser, SpeakerSystem, SpeakerCode, SpeakerReport:
		return true
	default:
		return false
	}
}

func validDraftKind(kind DraftKind) bool {
	return kind == DraftAnswer || kind == DraftCode || kind == DraftCoach
}

func boolInteger(value bool) int {
	if value {
		return 1
	}
	return 0
}

func nowText() string {
	return timeText(time.Now())
}

func timeText(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
