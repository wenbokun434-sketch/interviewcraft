package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
)

// SaveProfileAggregate atomically persists the strict Agent contract and its
// local-only source/lock metadata.
func (s *Store) SaveProfileAggregate(
	ctx context.Context,
	aggregate profile.Aggregate,
) error {
	if err := validateProfileAggregate(aggregate); err != nil {
		return err
	}
	profilePayload, err := json.Marshal(aggregate.Candidate)
	if err != nil {
		return invalidInput("encode profile", "画像无法编码为 JSON。", err)
	}
	lockedFacts, err := json.Marshal(aggregate.Metadata.LockedFactIDs)
	if err != nil {
		return invalidInput("encode profile locks", "事实锁定状态无法编码。", err)
	}
	lockedInferences, err := json.Marshal(aggregate.Metadata.LockedInferenceIDs)
	if err != nil {
		return invalidInput("encode profile locks", "推断锁定状态无法编码。", err)
	}
	var confirmed any
	if aggregate.ConfirmedAt != nil {
		confirmed = timeText(*aggregate.ConfirmedAt)
	}

	transaction, err := s.sql.BeginTx(ctx, nil)
	if err != nil {
		return storageError(
			"start profile save",
			s.paths.Database,
			"保留简历输入并重试。",
			err,
		)
	}
	now := timeText(aggregate.Metadata.UpdatedAt)
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO candidate_profiles(
			id, payload_json, confirmed_at, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			payload_json = excluded.payload_json,
			confirmed_at = excluded.confirmed_at,
			updated_at = excluded.updated_at
	`, aggregate.ID, string(profilePayload), confirmed,
		timeText(aggregate.Metadata.CreatedAt), now)
	if err != nil {
		_ = transaction.Rollback()
		return storageError(
			"save profile",
			s.paths.Database,
			"简历输入未被确认保存；检查数据库后重试。",
			err,
		)
	}
	_, err = transaction.ExecContext(ctx, `
		INSERT INTO profile_sources(
			profile_id, source_kind, source_name, source_text,
			locked_fact_ids_json, locked_inference_ids_json,
			created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(profile_id) DO UPDATE SET
			source_kind = excluded.source_kind,
			source_name = excluded.source_name,
			source_text = excluded.source_text,
			locked_fact_ids_json = excluded.locked_fact_ids_json,
			locked_inference_ids_json = excluded.locked_inference_ids_json,
			updated_at = excluded.updated_at
	`, aggregate.ID, aggregate.Metadata.Source.Kind,
		aggregate.Metadata.Source.Name, aggregate.Metadata.Source.Text,
		string(lockedFacts), string(lockedInferences),
		timeText(aggregate.Metadata.CreatedAt), now)
	if err != nil {
		_ = transaction.Rollback()
		return storageError(
			"save profile source metadata",
			s.paths.Database,
			"简历输入未被确认保存；检查数据库后重试。",
			err,
		)
	}
	if err := transaction.Commit(); err != nil {
		return storageError(
			"commit profile save",
			s.paths.Database,
			"简历输入未被确认保存；检查数据库后重试。",
			err,
		)
	}
	return nil
}

// GetProfileAggregate restores the profile, source text, and lock state.
func (s *Store) GetProfileAggregate(
	ctx context.Context,
	profileID string,
) (profile.Aggregate, bool, error) {
	var aggregate profile.Aggregate
	if err := requireText("profile id", profileID); err != nil {
		return aggregate, false, err
	}
	var payload string
	var sourceKind string
	var lockedFacts string
	var lockedInferences string
	var confirmedAt sql.NullString
	var createdAt string
	var updatedAt string
	err := s.sql.QueryRowContext(ctx, `
		SELECT
			candidate_profiles.payload_json,
			candidate_profiles.confirmed_at,
			profile_sources.source_kind,
			profile_sources.source_name,
			profile_sources.source_text,
			profile_sources.locked_fact_ids_json,
			profile_sources.locked_inference_ids_json,
			profile_sources.created_at,
			profile_sources.updated_at
		FROM candidate_profiles
		JOIN profile_sources
			ON profile_sources.profile_id = candidate_profiles.id
		WHERE candidate_profiles.id = ?
	`, profileID).Scan(
		&payload,
		&confirmedAt,
		&sourceKind,
		&aggregate.Metadata.Source.Name,
		&aggregate.Metadata.Source.Text,
		&lockedFacts,
		&lockedInferences,
		&createdAt,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return profile.Aggregate{}, false, nil
	}
	if err != nil {
		return profile.Aggregate{}, false, storageError(
			"read profile aggregate",
			s.paths.Database,
			"检查数据库后重试。",
			err,
		)
	}
	aggregate.ID = profileID
	aggregate.Metadata.Source.Kind = profile.SourceKind(sourceKind)
	aggregate.Candidate, err = contracts.DecodeCandidateProfile([]byte(payload))
	if err != nil {
		return profile.Aggregate{}, false, corruptedData(
			"decode stored profile",
			s.paths.Database,
			err,
		)
	}
	if err := json.Unmarshal(
		[]byte(lockedFacts),
		&aggregate.Metadata.LockedFactIDs,
	); err != nil {
		return profile.Aggregate{}, false, corruptedData(
			"decode profile fact locks",
			s.paths.Database,
			err,
		)
	}
	if err := json.Unmarshal(
		[]byte(lockedInferences),
		&aggregate.Metadata.LockedInferenceIDs,
	); err != nil {
		return profile.Aggregate{}, false, corruptedData(
			"decode profile inference locks",
			s.paths.Database,
			err,
		)
	}
	aggregate.Metadata.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return profile.Aggregate{}, false, corruptedData(
			"parse profile source creation time",
			s.paths.Database,
			err,
		)
	}
	aggregate.Metadata.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return profile.Aggregate{}, false, corruptedData(
			"parse profile source update time",
			s.paths.Database,
			err,
		)
	}
	if confirmedAt.Valid {
		value, parseErr := parseTime(confirmedAt.String)
		if parseErr != nil {
			return profile.Aggregate{}, false, corruptedData(
				"parse profile confirmation time",
				s.paths.Database,
				parseErr,
			)
		}
		aggregate.ConfirmedAt = &value
	}
	if err := validateProfileAggregate(aggregate); err != nil {
		return profile.Aggregate{}, false, corruptedData(
			"validate stored profile aggregate",
			s.paths.Database,
			err,
		)
	}
	return aggregate, true, nil
}

func validateProfileAggregate(aggregate profile.Aggregate) error {
	if err := requireText("profile id", aggregate.ID); err != nil {
		return err
	}
	if err := aggregate.Metadata.Source.Validate(); err != nil {
		return err
	}
	if err := profile.ValidateTrace(
		aggregate.Candidate,
		aggregate.Metadata.Source.Text,
	); err != nil {
		return err
	}
	if aggregate.Metadata.CreatedAt.IsZero() ||
		aggregate.Metadata.UpdatedAt.IsZero() {
		return invalidInput("save profile", "画像元数据时间不能为空。", nil)
	}
	if aggregate.Metadata.UpdatedAt.Before(aggregate.Metadata.CreatedAt) {
		return invalidInput("save profile", "画像更新时间不能早于创建时间。", nil)
	}
	if aggregate.ConfirmedAt != nil && aggregate.ConfirmedAt.IsZero() {
		return invalidInput("save profile", "画像确认时间无效。", nil)
	}
	facts := make(map[contracts.EvidenceID]struct{}, len(aggregate.Candidate.Facts))
	for _, fact := range aggregate.Candidate.Facts {
		facts[fact.ID] = struct{}{}
	}
	seenFactLocks := make(map[contracts.EvidenceID]struct{})
	for _, id := range aggregate.Metadata.LockedFactIDs {
		if _, exists := facts[id]; !exists {
			return invalidInput(
				"save profile",
				fmt.Sprintf("锁定事实 %q 不存在。", id),
				nil,
			)
		}
		if _, duplicate := seenFactLocks[id]; duplicate {
			return invalidInput("save profile", "事实锁定 ID 不能重复。", nil)
		}
		seenFactLocks[id] = struct{}{}
	}
	inferences := make(map[string]struct{}, len(aggregate.Candidate.Inferences))
	for _, inference := range aggregate.Candidate.Inferences {
		inferences[inference.ID] = struct{}{}
	}
	seenInferenceLocks := make(map[string]struct{})
	for _, id := range aggregate.Metadata.LockedInferenceIDs {
		if _, exists := inferences[id]; !exists {
			return invalidInput(
				"save profile",
				fmt.Sprintf("锁定推断 %q 不存在。", id),
				nil,
			)
		}
		if _, duplicate := seenInferenceLocks[id]; duplicate {
			return invalidInput("save profile", "推断锁定 ID 不能重复。", nil)
		}
		seenInferenceLocks[id] = struct{}{}
	}
	return nil
}
