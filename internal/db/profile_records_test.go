package db

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
)

func TestProfileAggregatePersistsSourceAndLocksAcrossRestart(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := Open(ctx, Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	aggregate := profileDBAggregate()
	if err := store.SaveProfileAggregate(ctx, aggregate); err != nil {
		_ = store.Close()
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restored, found, err := reopened.GetProfileAggregate(ctx, aggregate.ID)

	if err != nil || !found {
		t.Fatalf("GetProfileAggregate: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(restored, aggregate) {
		t.Fatalf("restored = %#v, want %#v", restored, aggregate)
	}
}

func TestProfileAggregateSaveRollsBackOnMetadataFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	original := profileDBAggregate()
	if err := store.SaveProfileAggregate(ctx, original); err != nil {
		t.Fatalf("initial SaveProfileAggregate: %v", err)
	}
	if _, err := store.sql.ExecContext(ctx, `
		CREATE TRIGGER block_profile_source_update
		BEFORE UPDATE ON profile_sources
		BEGIN
			SELECT RAISE(ABORT, 'test metadata failure');
		END
	`); err != nil {
		t.Fatalf("create blocking trigger: %v", err)
	}
	changed := original
	changed.Candidate.TargetRole = "Staff Backend Engineer"
	changed.Metadata.UpdatedAt = changed.Metadata.UpdatedAt.Add(time.Minute)

	err := store.SaveProfileAggregate(ctx, changed)

	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("SaveProfileAggregate error = %v", err)
	}
	restored, found, getErr := store.GetProfileAggregate(ctx, original.ID)
	if getErr != nil || !found {
		t.Fatalf("GetProfileAggregate: found=%v err=%v", found, getErr)
	}
	if !reflect.DeepEqual(restored, original) {
		t.Fatalf("failed save changed stored aggregate: %#v", restored)
	}
}

func TestDeleteProfileCascadesSourceMetadata(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	aggregate := profileDBAggregate()
	if err := store.SaveProfileAggregate(ctx, aggregate); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}

	deleted, err := store.DeleteProfile(ctx, aggregate.ID)

	if err != nil || !deleted {
		t.Fatalf("DeleteProfile: deleted=%v err=%v", deleted, err)
	}
	if count := tableCount(t, store, "candidate_profiles"); count != 0 {
		t.Fatalf("candidate profile rows = %d, want 0", count)
	}
	if count := tableCount(t, store, "profile_sources"); count != 0 {
		t.Fatalf("profile source rows = %d, want 0", count)
	}
	if _, found, err := store.GetProfileAggregate(
		ctx,
		aggregate.ID,
	); err != nil || found {
		t.Fatalf("aggregate remains after delete: found=%v err=%v", found, err)
	}
}

func TestProfileAggregateRejectsUnknownLockBeforeWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	aggregate := profileDBAggregate()
	aggregate.Metadata.LockedFactIDs = append(
		aggregate.Metadata.LockedFactIDs,
		"unknown-fact",
	)

	err := store.SaveProfileAggregate(ctx, aggregate)

	if !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("SaveProfileAggregate error = %v, want validation", err)
	}
	if count := tableCount(t, store, "candidate_profiles"); count != 0 {
		t.Fatalf("invalid aggregate was partially saved: %d rows", count)
	}
}

func TestProfileAggregateRejectsInventedFactBeforeWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	aggregate := profileDBAggregate()
	aggregate.Candidate.Facts[0].SourceSpan.Text = "invented experience"

	err := store.SaveProfileAggregate(ctx, aggregate)

	if !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("SaveProfileAggregate error = %v, want validation", err)
	}
	if count := tableCount(t, store, "candidate_profiles"); count != 0 {
		t.Fatalf("invented aggregate was partially saved: %d rows", count)
	}
}

func profileDBAggregate() profile.Aggregate {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	confirmedAt := now.Add(time.Minute)
	source := "Built payment service with Go."
	return profile.Aggregate{
		ID: "profile-traceable",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID:    "fact-payment",
				Field: "project",
				Value: "payment service",
				SourceSpan: contracts.SourceSpan{
					Start: 6,
					End:   len(source),
					Text:  "payment service with Go.",
				},
			}},
			Inferences: []contracts.ProfileInference{{
				ID:                "inference-lead",
				Field:             "leadership",
				Value:             "May have led delivery",
				Confidence:        0.55,
				NeedsConfirmation: true,
			}},
			Projects: []string{"payment service"},
			Skills:   []string{"Go"},
		},
		Metadata: profile.Metadata{
			Source: profile.Source{
				Kind: profile.SourcePDF,
				Name: "resume.pdf",
				Text: source,
			},
			LockedFactIDs:      []contracts.EvidenceID{"fact-payment"},
			LockedInferenceIDs: []string{"inference-lead"},
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		ConfirmedAt: &confirmedAt,
	}
}
