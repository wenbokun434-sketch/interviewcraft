package db

import (
	"context"
	"database/sql"
	"errors"

	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
)

// GetSessionProfile restores the confirmed profile associated with a session
// through its immutable Scenario version.
func (s *Store) GetSessionProfile(
	ctx context.Context,
	sessionID string,
) (coreprofile.Aggregate, bool, error) {
	if err := requireText("session id", sessionID); err != nil {
		return coreprofile.Aggregate{}, false, err
	}
	var profileID string
	err := s.sql.QueryRowContext(ctx, `
		SELECT scenarios.profile_id
		FROM sessions
		JOIN scenarios ON scenarios.id = sessions.scenario_id
		WHERE sessions.id = ?
	`, sessionID).Scan(&profileID)
	if errors.Is(err, sql.ErrNoRows) {
		return coreprofile.Aggregate{}, false, nil
	}
	if err != nil {
		return coreprofile.Aggregate{}, false, storageError(
			"read session profile",
			s.paths.Database,
			"确认会话与场景仍存在后重试。",
			err,
		)
	}
	return s.GetProfileAggregate(ctx, profileID)
}

// DeleteDraft removes one local buffer after its content has been durably
// submitted as an immutable event.
func (s *Store) DeleteDraft(
	ctx context.Context,
	sessionID string,
	questionID string,
	kind DraftKind,
) (bool, error) {
	if err := requireText("session id", sessionID); err != nil {
		return false, err
	}
	if err := requireText("question id", questionID); err != nil {
		return false, err
	}
	if !validDraftKind(kind) {
		return false, invalidInput("delete draft", "草稿类型无效。", nil)
	}
	result, err := s.sql.ExecContext(ctx, `
		DELETE FROM drafts
		WHERE session_id = ? AND question_id = ? AND kind = ?
	`, sessionID, questionID, kind)
	if err != nil {
		return false, storageError(
			"delete draft",
			s.paths.Database,
			"已提交答案不会丢失；检查数据库后重试清理草稿。",
			err,
		)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, storageError(
			"read draft deletion result",
			s.paths.Database,
			"已提交答案不会丢失；检查数据库后重试。",
			err,
		)
	}
	return count == 1, nil
}
