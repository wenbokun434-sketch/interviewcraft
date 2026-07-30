package db

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestOpenCreatesLayoutAndAppliesMigrationsOnce(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	var states []async.State[MigrationProgress]

	store, err := Open(ctx, Config{DataDir: dataDir}, func(state async.State[MigrationProgress]) {
		states = append(states, state)
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	paths := store.Paths()
	for _, path := range []string{
		paths.DataDir,
		paths.Uploads,
		paths.Exports,
		paths.Logs,
		paths.Database,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("expected local path %q: %v", path, err)
		}
	}
	version, err := store.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != 2 {
		t.Fatalf("SchemaVersion = %d, want 2", version)
	}
	assertPhases(t, states, []async.Phase{
		async.Pending,
		async.Streaming,
		async.Streaming,
		async.Succeeded,
	})

	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var reopenedStates []async.State[MigrationProgress]
	reopened, err := Open(ctx, Config{DataDir: dataDir}, func(state async.State[MigrationProgress]) {
		reopenedStates = append(reopenedStates, state)
	})
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	assertPhases(t, reopenedStates, []async.Phase{async.Pending, async.Succeeded})

	var foreignKeys int
	if err := reopened.sql.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys pragma: %v", err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
}

func TestNewDatabaseQueriesReturnExplicitEmptyValues(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)

	if _, found, err := store.GetProfile(ctx, "missing"); err != nil || found {
		t.Fatalf("GetProfile missing: found=%v err=%v", found, err)
	}
	if _, found, err := store.GetScenario(ctx, "missing"); err != nil || found {
		t.Fatalf("GetScenario missing: found=%v err=%v", found, err)
	}
	if _, found, err := store.GetSession(ctx, "missing"); err != nil || found {
		t.Fatalf("GetSession missing: found=%v err=%v", found, err)
	}
	events, err := store.ListSessionEvents(ctx, "missing")
	if err != nil || events == nil || len(events) != 0 {
		t.Fatalf("ListSessionEvents empty: %#v err=%v", events, err)
	}
	if _, found, err := store.LoadDraft(ctx, "missing", "Q1", DraftAnswer); err != nil || found {
		t.Fatalf("LoadDraft missing: found=%v err=%v", found, err)
	}
	sidebar, err := store.ListSidebarEvents(ctx, "missing")
	if err != nil || sidebar == nil || len(sidebar) != 0 {
		t.Fatalf("ListSidebarEvents empty: %#v err=%v", sidebar, err)
	}
	code, err := store.ListCodeSubmissions(ctx, "missing")
	if err != nil || code == nil || len(code) != 0 {
		t.Fatalf("ListCodeSubmissions empty: %#v err=%v", code, err)
	}
	if _, found, err := store.GetReport(ctx, "missing"); err != nil || found {
		t.Fatalf("GetReport missing: found=%v err=%v", found, err)
	}
}

func TestStorePersistsAndRestoresCompleteTrainingGraph(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := Open(ctx, Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	fixture := seedTrainingGraph(t, store)

	updatedProfile := fixture.profile
	updatedProfile.TargetRole = "Senior Backend Engineer"
	if err := store.SaveProfile(ctx, fixture.profileID, updatedProfile, &fixture.now); err != nil {
		t.Fatalf("update profile: %v", err)
	}
	if err := store.SaveDraft(ctx, Draft{
		SessionID:  fixture.sessionID,
		QuestionID: "Q1",
		Kind:       DraftAnswer,
		Content:    "restored second draft",
		UpdatedAt:  fixture.now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("update draft: %v", err)
	}
	updated, err := store.UpdateSessionStatus(
		ctx,
		fixture.sessionID,
		SessionEvaluationPending,
		fixture.now.Add(2*time.Minute),
	)
	if err != nil || !updated {
		t.Fatalf("UpdateSessionStatus: updated=%v err=%v", updated, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := Open(ctx, Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	profile, found, err := reopened.GetProfile(ctx, fixture.profileID)
	if err != nil || !found {
		t.Fatalf("GetProfile: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(profile, updatedProfile) {
		t.Fatalf("restored profile = %#v, want %#v", profile, updatedProfile)
	}
	scenario, found, err := reopened.GetScenario(ctx, fixture.scenarioID)
	if err != nil || !found {
		t.Fatalf("GetScenario: found=%v err=%v", found, err)
	}
	if !reflect.DeepEqual(scenario, fixture.scenario) {
		t.Fatalf("restored scenario = %#v, want %#v", scenario, fixture.scenario)
	}
	session, found, err := reopened.GetSession(ctx, fixture.sessionID)
	if err != nil || !found || session.Status != SessionEvaluationPending {
		t.Fatalf("GetSession: %#v found=%v err=%v", session, found, err)
	}

	events, err := reopened.ListSessionEvents(ctx, fixture.sessionID)
	if err != nil {
		t.Fatalf("ListSessionEvents: %v", err)
	}
	if len(events) != 2 || events[0].EventID != "event-early" || events[1].EventID != "event-late" {
		t.Fatalf("event order = %#v", events)
	}
	if events[0].Sequence == 0 || events[1].Sequence == 0 ||
		events[0].Sequence == events[1].Sequence {
		t.Fatalf("event sequence is not unique and persisted: %d, %d", events[0].Sequence, events[1].Sequence)
	}

	draft, found, err := reopened.LoadDraft(ctx, fixture.sessionID, "Q1", DraftAnswer)
	if err != nil || !found || draft.Content != "restored second draft" {
		t.Fatalf("LoadDraft: %#v found=%v err=%v", draft, found, err)
	}
	sidebar, err := reopened.ListSidebarEvents(ctx, fixture.sessionID)
	if err != nil || len(sidebar) != 1 || sidebar[0].ID != "coach-1" {
		t.Fatalf("ListSidebarEvents: %#v err=%v", sidebar, err)
	}
	submissions, err := reopened.ListCodeSubmissions(ctx, fixture.sessionID)
	if err != nil || len(submissions) != 1 || submissions[0].ID != "code-1" {
		t.Fatalf("ListCodeSubmissions: %#v err=%v", submissions, err)
	}
	report, found, err := reopened.GetReport(ctx, fixture.sessionID)
	if err != nil || !found || report.ID != "report-1" {
		t.Fatalf("GetReport: %#v found=%v err=%v", report, found, err)
	}
}

func TestSessionEventsAreAppendOnlyAndRejectDuplicates(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	fixture := seedTrainingGraph(t, store)

	err := store.AppendSessionEvent(ctx, SessionEvent{
		EventID:      "event-early",
		SessionID:    fixture.sessionID,
		Speaker:      SpeakerUser,
		QuestionID:   "Q1",
		Content:      "attempted rewrite",
		OccurredAt:   fixture.now.Add(3 * time.Minute),
		EvidenceRefs: []contracts.EvidenceID{},
	})
	if err == nil {
		t.Fatal("duplicate immutable event unexpectedly succeeded")
	}
	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("duplicate event error = %v, want persistence code", err)
	}

	events, listErr := store.ListSessionEvents(ctx, fixture.sessionID)
	if listErr != nil {
		t.Fatalf("ListSessionEvents: %v", listErr)
	}
	if len(events) != 2 || events[0].Content == "attempted rewrite" {
		t.Fatalf("duplicate event changed history: %#v", events)
	}
}

func TestDeleteProfileCascadesAllDerivedRows(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	fixture := seedTrainingGraph(t, store)

	deleted, err := store.DeleteProfile(ctx, fixture.profileID)
	if err != nil || !deleted {
		t.Fatalf("DeleteProfile: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := store.GetProfile(ctx, fixture.profileID); err != nil || found {
		t.Fatalf("profile remains after delete: found=%v err=%v", found, err)
	}

	for _, table := range []string{
		"candidate_profiles",
		"profile_sources",
		"scenarios",
		"sessions",
		"session_events",
		"drafts",
		"sidebar_events",
		"code_submissions",
		"reports",
	} {
		if count := tableCount(t, store, table); count != 0 {
			t.Errorf("%s rows after cascade = %d, want 0", table, count)
		}
	}
}

func TestDeleteProfileRollsBackOnCascadeFailure(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	fixture := seedTrainingGraph(t, store)

	if _, err := store.sql.ExecContext(ctx, `
		CREATE TRIGGER block_session_delete
		BEFORE DELETE ON sessions
		BEGIN
			SELECT RAISE(ABORT, 'test cascade failure');
		END
	`); err != nil {
		t.Fatalf("create blocking trigger: %v", err)
	}

	deleted, err := store.DeleteProfile(ctx, fixture.profileID)
	if err == nil || deleted {
		t.Fatalf("DeleteProfile with blocked cascade: deleted=%v err=%v", deleted, err)
	}
	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("delete error = %v, want persistence code", err)
	}
	if _, found, getErr := store.GetProfile(ctx, fixture.profileID); getErr != nil || !found {
		t.Fatalf("profile was partially deleted: found=%v err=%v", found, getErr)
	}
	if count := tableCount(t, store, "sessions"); count != 1 {
		t.Fatalf("sessions after rollback = %d, want 1", count)
	}
}

func TestOpenReportsPathFailureWithRecovery(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	root := t.TempDir()
	blockedPath := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(blockedPath, []byte("file"), 0o600); err != nil {
		t.Fatalf("create blocking file: %v", err)
	}
	var states []async.State[MigrationProgress]

	_, err := Open(ctx, Config{DataDir: blockedPath}, func(state async.State[MigrationProgress]) {
		states = append(states, state)
	})

	if err == nil {
		t.Fatal("Open with file data directory unexpectedly succeeded")
	}
	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		t.Fatalf("Open error type = %T, want *domainerr.Error", err)
	}
	if typed.Code != domainerr.CodePersistenceFailed ||
		typed.RecoveryAction == "" ||
		!strings.Contains(typed.Message, blockedPath) {
		t.Fatalf("Open error is not actionable: %#v", typed)
	}
	assertPhases(t, states, []async.Phase{async.Pending, async.Failed})
}

func TestStorageRejectsInvalidEvidenceBeforeWrite(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	store := openTestStore(t)
	fixture := seedTrainingGraph(t, store)

	err := store.AppendSessionEvent(ctx, SessionEvent{
		EventID:      "invalid-event",
		SessionID:    fixture.sessionID,
		Speaker:      SpeakerUser,
		QuestionID:   "Q1",
		Content:      "answer",
		OccurredAt:   fixture.now,
		EvidenceRefs: []contracts.EvidenceID{""},
	})
	if err == nil || !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("invalid evidence error = %v, want validation code", err)
	}
}

type trainingFixture struct {
	now        time.Time
	profileID  string
	scenarioID string
	sessionID  string
	profile    contracts.CandidateProfile
	scenario   contracts.Scenario
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(
		context.Background(),
		Config{DataDir: filepath.Join(t.TempDir(), "data")},
		nil,
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store
}

func seedTrainingGraph(t *testing.T, store *Store) trainingFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 8, 0, 0, 0, time.UTC)
	profile := contracts.CandidateProfile{
		TargetRole: "Backend Engineer",
		Facts: []contracts.ProfileFact{{
			ID:    "fact-1",
			Field: "project",
			Value: "Built a payment service",
			SourceSpan: contracts.SourceSpan{
				Start: 0,
				End:   23,
				Text:  "Built a payment service",
			},
		}},
		Inferences: []contracts.ProfileInference{},
		Projects:   []string{"Payment service"},
		Skills:     []string{"Go", "PostgreSQL"},
	}
	scenario := contracts.Scenario{
		Template:          "project_deep_dive",
		Mode:              contracts.ScenarioStrict,
		TimeBudgetSeconds: 1200,
		PromptVersion:     "scenario-v1",
		Questions: []contracts.ScenarioQuestion{{
			ID:               "Q1",
			Prompt:           "How did you make the payment service reliable?",
			Intent:           "Assess reliability trade-offs",
			EstimatedSeconds: 300,
			Rubric:           []string{"Names a failure mode"},
			EvidenceIDs:      []contracts.EvidenceID{"fact-1"},
			Generic:          false,
			MaxFollowUps:     2,
			EndCondition:     "A concrete trade-off is explained",
		}},
	}
	fixture := trainingFixture{
		now:        now,
		profileID:  "profile-1",
		scenarioID: "scenario-1",
		sessionID:  "session-1",
		profile:    profile,
		scenario:   scenario,
	}

	if err := store.SaveProfile(ctx, fixture.profileID, profile, &now); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	if err := store.SaveScenario(
		ctx,
		fixture.scenarioID,
		fixture.profileID,
		scenario,
		now,
	); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, Session{
		ID:         fixture.sessionID,
		ScenarioID: fixture.scenarioID,
		Status:     SessionActive,
		StartedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	late := SessionEvent{
		EventID:      "event-late",
		SessionID:    fixture.sessionID,
		Speaker:      SpeakerUser,
		QuestionID:   "Q1",
		Content:      "late answer",
		OccurredAt:   now.Add(2 * time.Minute),
		EvidenceRefs: []contracts.EvidenceID{"fact-1"},
	}
	early := SessionEvent{
		EventID:      "event-early",
		SessionID:    fixture.sessionID,
		Speaker:      SpeakerInterviewer,
		QuestionID:   "Q1",
		Content:      "early question",
		OccurredAt:   now.Add(time.Minute),
		EvidenceRefs: []contracts.EvidenceID{"fact-1"},
	}
	if err := store.AppendSessionEvent(ctx, late); err != nil {
		t.Fatalf("AppendSessionEvent late: %v", err)
	}
	if err := store.AppendSessionEvent(ctx, early); err != nil {
		t.Fatalf("AppendSessionEvent early: %v", err)
	}
	if err := store.SaveDraft(ctx, Draft{
		SessionID:  fixture.sessionID,
		QuestionID: "Q1",
		Kind:       DraftAnswer,
		Content:    "first draft",
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := store.AddSidebarEvent(ctx, SidebarEvent{
		ID:          "coach-1",
		SessionID:   fixture.sessionID,
		QuestionID:  "Q1",
		Intent:      contracts.CoachGiveHint,
		HelpLevel:   contracts.HelpL2,
		Tags:        []string{"reliability"},
		Outcome:     "still_confused",
		PausedTimer: true,
		OccurredAt:  now.Add(90 * time.Second),
	}); err != nil {
		t.Fatalf("AddSidebarEvent: %v", err)
	}
	if err := store.AddCodeSubmission(ctx, CodeSubmission{
		ID:           "code-1",
		SessionID:    fixture.sessionID,
		QuestionID:   "Q1",
		Language:     "python",
		Source:       "print('ok')",
		TestResult:   json.RawMessage(`{"passed":1,"total":1}`),
		RuntimeStats: json.RawMessage(`{"milliseconds":12,"memory_mb":8}`),
		SnapshotID:   "snapshot-1",
		CreatedAt:    now.Add(3 * time.Minute),
	}); err != nil {
		t.Fatalf("AddCodeSubmission: %v", err)
	}
	if err := store.SaveReport(ctx, Report{
		ID:        "report-1",
		SessionID: fixture.sessionID,
		Payload:   json.RawMessage(`{"summary":"evidence based"}`),
		CreatedAt: now.Add(4 * time.Minute),
		UpdatedAt: now.Add(4 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveReport: %v", err)
	}
	return fixture
}

func tableCount(t *testing.T, store *Store, table string) int {
	t.Helper()
	allowed := map[string]bool{
		"candidate_profiles": true,
		"profile_sources":    true,
		"scenarios":          true,
		"sessions":           true,
		"session_events":     true,
		"drafts":             true,
		"sidebar_events":     true,
		"code_submissions":   true,
		"reports":            true,
	}
	if !allowed[table] {
		t.Fatalf("tableCount called with unsupported table %q", table)
	}
	var count int
	if err := store.sql.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func assertPhases(
	t *testing.T,
	states []async.State[MigrationProgress],
	want []async.Phase,
) {
	t.Helper()
	got := make([]async.Phase, len(states))
	for index, state := range states {
		if err := state.Validate(); err != nil {
			t.Fatalf("state %d is invalid: %v", index, err)
		}
		got[index] = state.Phase
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migration phases = %v, want %v", got, want)
	}
}
