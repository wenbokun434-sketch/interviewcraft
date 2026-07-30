package db

import (
	"context"
	"database/sql"
	"encoding/json"
)

// GetSidebarEvent returns one active Coach learning event by session and ID.
func (s *Store) GetSidebarEvent(
	ctx context.Context,
	sessionID string,
	eventID string,
) (SidebarEvent, bool, error) {
	if err := requireText("session id", sessionID); err != nil {
		return SidebarEvent{}, false, err
	}
	if err := requireText("Coach event id", eventID); err != nil {
		return SidebarEvent{}, false, err
	}
	event := SidebarEvent{
		ID:        eventID,
		SessionID: sessionID,
	}
	var (
		tags       string
		paused     int
		occurredAt string
	)
	err := s.sql.QueryRowContext(ctx, `
		SELECT question_id, intent, help_level, tags_json,
		       content, policy_note, outcome, paused_timer, occurred_at
		FROM sidebar_events
		WHERE session_id = ? AND id = ?
	`, sessionID, eventID).Scan(
		&event.QuestionID,
		&event.Intent,
		&event.HelpLevel,
		&tags,
		&event.Content,
		&event.PolicyNote,
		&event.Outcome,
		&paused,
		&occurredAt,
	)
	if err == sql.ErrNoRows {
		return SidebarEvent{}, false, nil
	}
	if err != nil {
		return SidebarEvent{}, false, storageError(
			"get Coach event",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	if err := json.Unmarshal([]byte(tags), &event.Tags); err != nil {
		return SidebarEvent{}, false, corruptedData(
			"decode Coach tags",
			s.paths.Database,
			err,
		)
	}
	event.PausedTimer = paused == 1
	event.OccurredAt, err = parseTime(occurredAt)
	if err != nil {
		return SidebarEvent{}, false, corruptedData(
			"parse Coach event time",
			s.paths.Database,
			err,
		)
	}
	return event, true, nil
}

// CountSidebarEventsForQuestion returns quota usage for one question.
func (s *Store) CountSidebarEventsForQuestion(
	ctx context.Context,
	sessionID string,
	questionID string,
) (int, error) {
	if err := requireText("session id", sessionID); err != nil {
		return 0, err
	}
	if err := requireText("question id", questionID); err != nil {
		return 0, err
	}
	var count int
	if err := s.sql.QueryRowContext(ctx, `
		SELECT COUNT(*)
		FROM coach_usage
		WHERE session_id = ? AND question_id = ?
	`, sessionID, questionID).Scan(&count); err != nil {
		return 0, storageError(
			"count Coach events",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return count, nil
}

// UpdateSidebarEventOutcome records the candidate's learning state.
func (s *Store) UpdateSidebarEventOutcome(
	ctx context.Context,
	sessionID string,
	eventID string,
	outcome string,
) (bool, error) {
	for field, value := range map[string]string{
		"session id":     sessionID,
		"Coach event id": eventID,
		"outcome":        outcome,
	} {
		if err := requireText(field, value); err != nil {
			return false, err
		}
	}
	result, err := s.sql.ExecContext(ctx, `
		UPDATE sidebar_events
		SET outcome = ?
		WHERE session_id = ? AND id = ?
	`, outcome, sessionID, eventID)
	if err != nil {
		return false, storageError(
			"mark Coach outcome",
			s.paths.Database,
			"保留当前学习状态，检查数据库后重试。",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, storageError(
			"read Coach outcome result",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return affected == 1, nil
}

// DeleteSidebarEvent removes one Coach event from every downstream query.
func (s *Store) DeleteSidebarEvent(
	ctx context.Context,
	sessionID string,
	eventID string,
) (bool, error) {
	if err := requireText("session id", sessionID); err != nil {
		return false, err
	}
	if err := requireText("Coach event id", eventID); err != nil {
		return false, err
	}
	result, err := s.sql.ExecContext(ctx, `
		DELETE FROM sidebar_events
		WHERE session_id = ? AND id = ?
	`, sessionID, eventID)
	if err != nil {
		return false, storageError(
			"delete Coach event",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, storageError(
			"read Coach delete result",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return affected == 1, nil
}

// DeleteSidebarEventsByQuestion removes one question's Coach history.
func (s *Store) DeleteSidebarEventsByQuestion(
	ctx context.Context,
	sessionID string,
	questionID string,
) (int64, error) {
	if err := requireText("session id", sessionID); err != nil {
		return 0, err
	}
	if err := requireText("question id", questionID); err != nil {
		return 0, err
	}
	return s.deleteSidebarEvents(
		ctx,
		"question",
		`DELETE FROM sidebar_events
		 WHERE session_id = ? AND question_id = ?`,
		sessionID,
		questionID,
	)
}

// DeleteSidebarEventsBySession removes a session's complete Coach history.
func (s *Store) DeleteSidebarEventsBySession(
	ctx context.Context,
	sessionID string,
) (int64, error) {
	if err := requireText("session id", sessionID); err != nil {
		return 0, err
	}
	return s.deleteSidebarEvents(
		ctx,
		"session",
		`DELETE FROM sidebar_events WHERE session_id = ?`,
		sessionID,
	)
}

func (s *Store) deleteSidebarEvents(
	ctx context.Context,
	scope string,
	statement string,
	args ...any,
) (int64, error) {
	result, err := s.sql.ExecContext(ctx, statement, args...)
	if err != nil {
		return 0, storageError(
			"delete Coach "+scope+" events",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, storageError(
			"read Coach delete result",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	return affected, nil
}
