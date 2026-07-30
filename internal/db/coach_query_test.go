package db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
)

func TestCoachEventContentOutcomeAndDeleteScopes(t *testing.T) {
	t.Parallel()

	store := openTestStore(t)
	fixture := seedTrainingGraph(t, store)
	ctx := context.Background()
	if _, err := store.DeleteSidebarEventsBySession(
		ctx,
		fixture.sessionID,
	); err != nil {
		t.Fatalf("clear seeded Coach events: %v", err)
	}
	events := []SidebarEvent{
		{
			ID:          "coach-query-1",
			SessionID:   fixture.sessionID,
			QuestionID:  "CQ1",
			Intent:      contracts.CoachGiveHint,
			HelpLevel:   contracts.HelpL1,
			Tags:        []string{"reliability"},
			Content:     "先列出一个具体故障模式。",
			PolicyNote:  "只提供 L1 提示。",
			Outcome:     "unmarked",
			PausedTimer: false,
			OccurredAt:  fixture.now.Add(time.Second),
		},
		{
			ID:          "coach-query-2",
			SessionID:   fixture.sessionID,
			QuestionID:  "CQ1",
			Intent:      contracts.CoachAnswerStructure,
			HelpLevel:   contracts.HelpL2,
			Tags:        []string{"structure"},
			Content:     "按背景、选择、权衡、结果四段组织。",
			PolicyNote:  "不提供完整答案。",
			Outcome:     "unmarked",
			PausedTimer: true,
			OccurredAt:  fixture.now.Add(2 * time.Second),
		},
		{
			ID:         "coach-query-3",
			SessionID:  fixture.sessionID,
			QuestionID: "CQ2",
			Intent:     contracts.CoachAddToReview,
			HelpLevel:  contracts.HelpL1,
			Tags:       []string{"review"},
			Content:    "已加入复习。",
			Outcome:    "review",
			OccurredAt: fixture.now.Add(3 * time.Second),
		},
	}
	for _, event := range events {
		if err := store.AddSidebarEvent(ctx, event); err != nil {
			t.Fatalf(
				"AddSidebarEvent %s: %v cause=%v",
				event.ID,
				err,
				errors.Unwrap(err),
			)
		}
	}

	event, found, err := store.GetSidebarEvent(
		ctx,
		fixture.sessionID,
		"coach-query-2",
	)
	if err != nil || !found ||
		event.Content != events[1].Content ||
		event.PolicyNote != events[1].PolicyNote ||
		!event.PausedTimer {
		t.Fatalf("GetSidebarEvent=%#v found=%v err=%v", event, found, err)
	}
	count, err := store.CountSidebarEventsForQuestion(
		ctx,
		fixture.sessionID,
		"CQ1",
	)
	if err != nil || count != 2 {
		t.Fatalf("CountSidebarEventsForQuestion=%d err=%v", count, err)
	}
	updated, err := store.UpdateSidebarEventOutcome(
		ctx,
		fixture.sessionID,
		"coach-query-1",
		"still_confused",
	)
	if err != nil || !updated {
		t.Fatalf("UpdateSidebarEventOutcome=%v err=%v", updated, err)
	}
	event, found, err = store.GetSidebarEvent(
		ctx,
		fixture.sessionID,
		"coach-query-1",
	)
	if err != nil || !found || event.Outcome != "still_confused" {
		t.Fatalf("updated event=%#v found=%v err=%v", event, found, err)
	}

	deleted, err := store.DeleteSidebarEvent(
		ctx,
		fixture.sessionID,
		"coach-query-1",
	)
	if err != nil || !deleted {
		t.Fatalf("DeleteSidebarEvent=%v err=%v", deleted, err)
	}
	if _, found, err := store.GetSidebarEvent(
		ctx,
		fixture.sessionID,
		"coach-query-1",
	); err != nil || found {
		t.Fatalf("deleted event found=%v err=%v", found, err)
	}
	count, err = store.CountSidebarEventsForQuestion(
		ctx,
		fixture.sessionID,
		"CQ1",
	)
	if err != nil || count != 2 {
		t.Fatalf("quota usage after event delete=%d err=%v", count, err)
	}
	deletedCount, err := store.DeleteSidebarEventsByQuestion(
		ctx,
		fixture.sessionID,
		"CQ1",
	)
	if err != nil || deletedCount != 1 {
		t.Fatalf("DeleteSidebarEventsByQuestion=%d err=%v", deletedCount, err)
	}
	history, err := store.ListSidebarEvents(ctx, fixture.sessionID)
	if err != nil || len(history) != 1 || history[0].ID != "coach-query-3" {
		t.Fatalf("history after question delete=%#v err=%v", history, err)
	}
	deletedCount, err = store.DeleteSidebarEventsBySession(
		ctx,
		fixture.sessionID,
	)
	if err != nil || deletedCount != 1 {
		t.Fatalf("DeleteSidebarEventsBySession=%d err=%v", deletedCount, err)
	}
	history, err = store.ListSidebarEvents(ctx, fixture.sessionID)
	if err != nil || len(history) != 0 {
		t.Fatalf("history after session delete=%#v err=%v", history, err)
	}
}
