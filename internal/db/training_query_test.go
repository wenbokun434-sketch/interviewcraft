package db

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestLoadTrainingHomeEmptyDatabase(t *testing.T) {
	store := openTestStore(t)

	home, err := store.LoadTrainingHome(context.Background(), 5)
	if err != nil {
		t.Fatalf("LoadTrainingHome: %v", err)
	}
	if home.Resume != nil {
		t.Fatalf("Resume = %#v, want nil", home.Resume)
	}
	if home.Recent == nil || len(home.Recent) != 0 {
		t.Fatalf("Recent = %#v, want explicit empty slice", home.Recent)
	}
	if home.PracticeQueue == nil || len(home.PracticeQueue) != 0 {
		t.Fatalf("PracticeQueue = %#v, want explicit empty slice", home.PracticeQueue)
	}
}

func TestLoadTrainingHomeRestoresLatestEventDraftScoreAndQueue(t *testing.T) {
	store := openTestStore(t)
	fixture := seedTrainingGraph(t, store)
	payload := json.RawMessage(`{
		"scorecard": [
			{"dimension":"technical_depth","score":3}
		],
		"practice_plan": [
			{
				"id":"practice-redis",
				"topic":"Redis consistency",
				"mode":"strict",
				"duration_minutes":15,
				"completion_criteria":"Explain two failure modes"
			}
		]
	}`)
	if err := store.SaveReport(context.Background(), Report{
		ID:        "report-1",
		SessionID: fixture.sessionID,
		Payload:   payload,
		CreatedAt: fixture.now,
		UpdatedAt: fixture.now,
	}); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}

	home, err := store.LoadTrainingHome(context.Background(), 5)
	if err != nil {
		t.Fatalf("LoadTrainingHome: %v", err)
	}
	if home.Resume == nil || home.Resume.Session.ID != fixture.sessionID {
		t.Fatalf("Resume = %#v", home.Resume)
	}
	if home.Resume.LastEvent == nil ||
		home.Resume.LastEvent.EventID != "event-late" ||
		home.Resume.LastEvent.Content != "late answer" {
		t.Fatalf("LastEvent = %#v, want final persisted event", home.Resume.LastEvent)
	}
	if home.Resume.Draft == nil || home.Resume.Draft.Content != "first draft" {
		t.Fatalf("Draft = %#v", home.Resume.Draft)
	}
	if len(home.Recent) != 1 {
		t.Fatalf("Recent = %#v", home.Recent)
	}
	recent := home.Recent[0]
	if recent.Template != fixture.scenario.Template ||
		recent.Mode != contracts.ScenarioStrict ||
		recent.ReportID != "report-1" {
		t.Fatalf("Recent[0] = %#v", recent)
	}
	if recent.Score == nil ||
		recent.Score.Dimension != contracts.DimensionTechnicalDepth ||
		recent.Score.Score != 3 ||
		recent.Score.Scale != 5 {
		t.Fatalf("Recent score = %#v", recent.Score)
	}
	if len(home.PracticeQueue) != 1 {
		t.Fatalf("PracticeQueue = %#v", home.PracticeQueue)
	}
	item := home.PracticeQueue[0]
	if item.ID != "practice-redis" ||
		item.ReportID != "report-1" ||
		item.SessionID != fixture.sessionID ||
		item.Topic != "Redis consistency" ||
		item.DurationMinutes != 15 {
		t.Fatalf("PracticeQueue[0] = %#v", item)
	}
}

func TestLoadTrainingHomeToleratesUnavailableDerivedScore(t *testing.T) {
	store := openTestStore(t)
	fixture := seedTrainingGraph(t, store)
	if err := store.SaveReport(context.Background(), Report{
		ID:        "report-1",
		SessionID: fixture.sessionID,
		Payload: json.RawMessage(`{
			"scorecard":[{"dimension":"technical_depth","not_applicable":true}],
			"practice_plan":[{"topic":""}]
		}`),
		CreatedAt: fixture.now,
		UpdatedAt: fixture.now,
	}); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}

	home, err := store.LoadTrainingHome(context.Background(), 5)
	if err != nil {
		t.Fatalf("LoadTrainingHome: %v", err)
	}
	if home.Recent[0].Score != nil {
		t.Fatalf("Score = %#v, want unavailable", home.Recent[0].Score)
	}
	if len(home.PracticeQueue) != 0 {
		t.Fatalf("PracticeQueue = %#v, want empty", home.PracticeQueue)
	}
}

func TestLoadTrainingHomeClosedSQLiteReturnsActionableError(t *testing.T) {
	store, err := Open(
		context.Background(),
		Config{DataDir: filepath.Join(t.TempDir(), "data")},
		nil,
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, err = store.LoadTrainingHome(context.Background(), 5)
	if err == nil {
		t.Fatal("LoadTrainingHome error = nil")
	}
	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("error = %#v, want persistence_failed", err)
	}
	if !strings.Contains(err.Error(), "本地存储") {
		t.Fatalf("error = %q, want concrete storage cause", err)
	}
}
