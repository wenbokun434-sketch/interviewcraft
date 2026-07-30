package db

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
)

func TestSessionProfileAndDraftDeletionQueries(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	aggregate := profileDBAggregate()
	if err := store.SaveProfileAggregate(ctx, aggregate); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	now := time.Date(2026, 7, 30, 16, 0, 0, 0, time.UTC)
	scenario := contracts.Scenario{
		Template:          "project_deep_dive",
		Mode:              contracts.ScenarioStandard,
		TimeBudgetSeconds: 1200,
		PromptVersion:     "scenario-planner-v1.r1",
		Questions: []contracts.ScenarioQuestion{{
			ID:               "Q1",
			Prompt:           "Explain the payment service trade-offs.",
			Intent:           "Assess confirmed project depth",
			EstimatedSeconds: 300,
			Rubric:           []string{"Explains one trade-off"},
			EvidenceIDs:      []contracts.EvidenceID{"fact-payment"},
			MaxFollowUps:     2,
			EndCondition:     "One trade-off is explained",
		}},
	}
	if err := store.SaveScenario(
		ctx,
		"scenario-interview",
		aggregate.ID,
		scenario,
		now,
	); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, Session{
		ID:         "session-interview",
		ScenarioID: "scenario-interview",
		Status:     SessionActive,
		StartedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.SaveDraft(ctx, Draft{
		SessionID:  "session-interview",
		QuestionID: "Q1",
		Kind:       DraftAnswer,
		Content:    "local answer",
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	restored, found, err := store.GetSessionProfile(
		ctx,
		"session-interview",
	)
	if err != nil || !found || !reflect.DeepEqual(restored, aggregate) {
		t.Fatalf(
			"GetSessionProfile: aggregate=%#v found=%v err=%v",
			restored,
			found,
			err,
		)
	}
	deleted, err := store.DeleteDraft(
		ctx,
		"session-interview",
		"Q1",
		DraftAnswer,
	)
	if err != nil || !deleted {
		t.Fatalf("DeleteDraft: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := store.LoadDraft(
		ctx,
		"session-interview",
		"Q1",
		DraftAnswer,
	); err != nil || found {
		t.Fatalf("LoadDraft after delete: found=%v err=%v", found, err)
	}
	deleted, err = store.DeleteDraft(
		ctx,
		"session-interview",
		"Q1",
		DraftAnswer,
	)
	if err != nil || deleted {
		t.Fatalf("idempotent DeleteDraft: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := store.GetSessionProfile(
		ctx,
		"missing",
	); err != nil || found {
		t.Fatalf("missing GetSessionProfile: found=%v err=%v", found, err)
	}
}
