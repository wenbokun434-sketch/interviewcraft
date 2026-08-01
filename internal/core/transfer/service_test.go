package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/db"
	reportscreen "github.com/interviewcraft/interviewcraft/internal/tui/screens/report"
)

func TestTransferPackageRoundTripExcludesSecretsAndCoachContentByDefault(t *testing.T) {
	ctx := context.Background()
	source, sourcePath := openTransferStore(t)
	document := seedTransferGraph(t, source)
	insertProviderSecret(t, sourcePath, "actual-secret-reference")
	service := NewService(sourcePath, Options{Now: func() time.Time { return document.GeneratedAt }})
	output := filepath.Join(t.TempDir(), "transfer.json")
	var phases []async.Phase
	var stages []string
	result, err := service.Export(ctx, ExportOptions{
		Format: FormatPackage, OutputPath: output,
	}, func(state async.State[Progress]) {
		phases = append(phases, state.Phase)
		if state.Value != nil && state.Value.Stage != "" {
			stages = append(stages, state.Value.Stage)
		}
	})
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if result.RecordCount < 10 || result.Path != output {
		t.Fatalf("result=%#v", result)
	}
	assertPhases(t, phases, []async.Phase{
		async.Pending, async.Streaming, async.Streaming,
		async.Streaming, async.Succeeded,
	})
	if strings.Join(stages, ",") != "reading_local_data,validating_links,writing_artifact,completed" {
		t.Fatalf("stages=%v", stages)
	}
	payload, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	for _, forbidden := range []string{
		"actual-secret-reference", "provider_configs", "Coach private explanation",
		"private policy note", "secret_ref",
	} {
		if strings.Contains(string(payload), forbidden) {
			t.Errorf("default package leaked %q", forbidden)
		}
	}
	bundle, err := decodeBundle(payload)
	if err != nil || bundle.CoachContentIncluded {
		t.Fatalf("decode bundle=%#v err=%v", bundle, err)
	}

	target, targetPath := openTransferStore(t)
	insertProviderSecret(t, targetPath, "target-local-secret-reference")
	targetService := NewService(targetPath, Options{})
	var importStages []string
	imported, err := targetService.Import(ctx, output, func(state async.State[Progress]) {
		if state.Value != nil && state.Value.Stage != "" {
			importStages = append(importStages, state.Value.Stage)
		}
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if imported.Profiles != 1 || imported.Sessions != 1 || imported.Reports != 1 {
		t.Fatalf("imported=%#v", imported)
	}
	if strings.Join(importStages, ",") != "reading_package,validating_package,restoring_profiles,restoring_sessions,restoring_reports,completed" {
		t.Fatalf("import stages=%v", importStages)
	}
	assertRestoredGraph(t, target, document, false)
	assertProviderSecret(t, targetPath, "target-local-secret-reference")
	home, err := target.LoadTrainingHome(ctx, 5)
	if err != nil || len(home.PracticeQueue) != 3 || home.PracticeQueue[0].ReportID != document.ID {
		t.Fatalf("practice queue=%#v err=%v", home.PracticeQueue, err)
	}
	reportModel, err := reportscreen.New(reportscreen.Options{
		SessionID: "session-transfer", ReportID: document.ID,
		Reports: corereport.NewService(target, corereport.Options{}),
		Width:   120, Height: 36,
	})
	if err != nil {
		t.Fatalf("New report screen: %v", err)
	}
	reportModel.Load(ctx, nil)
	action := reportModel.HandleKey("n")
	if action.Destination != reportscreen.DestinationScenario ||
		action.Practice == nil || action.Practice.ReportID != document.ID ||
		action.Practice.Topic != document.PracticePlan[0].Topic {
		t.Fatalf("migrated report next practice=%#v", action)
	}
}

func TestTransferPackageIncludesCoachContentOnlyWhenExplicit(t *testing.T) {
	store, databasePath := openTransferStore(t)
	document := seedTransferGraph(t, store)
	insertProviderSecret(t, databasePath, "never-export-this")
	service := NewService(databasePath, Options{Now: func() time.Time { return document.GeneratedAt }})
	output := filepath.Join(t.TempDir(), "with-coach.json")
	_, err := service.Export(context.Background(), ExportOptions{
		Format: FormatPackage, OutputPath: output, IncludeCoachContent: true,
	}, nil)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	payload, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(payload), "Coach private explanation") ||
		!strings.Contains(string(payload), "private policy note") ||
		strings.Contains(string(payload), "never-export-this") {
		t.Fatalf("explicit package privacy mismatch: %s", payload)
	}

	target, targetPath := openTransferStore(t)
	if _, err := NewService(targetPath, Options{}).Import(
		context.Background(), output, nil,
	); err != nil {
		t.Fatalf("Import: %v", err)
	}
	assertRestoredGraph(t, target, document, true)
}

func TestStandaloneMarkdownAndJSONReportExports(t *testing.T) {
	store, databasePath := openTransferStore(t)
	document := seedTransferGraph(t, store)
	service := NewService(databasePath, Options{Now: func() time.Time { return document.GeneratedAt }})

	jsonPath := filepath.Join(t.TempDir(), "report.json")
	if _, err := service.Export(context.Background(), ExportOptions{
		Format: FormatJSON, OutputPath: jsonPath, SessionID: "session-transfer",
	}, nil); err != nil {
		t.Fatalf("JSON Export: %v", err)
	}
	jsonPayload, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatalf("Read JSON: %v", err)
	}
	var reportExport ReportExport
	if err := json.Unmarshal(jsonPayload, &reportExport); err != nil {
		t.Fatalf("Unmarshal report: %v", err)
	}
	if reportExport.Version != ReportExportVersion ||
		reportExport.Report.ID != document.ID ||
		len(reportExport.CoachTranscript) != 0 ||
		strings.Contains(string(jsonPayload), "Coach private explanation") {
		t.Fatalf("JSON report=%#v", reportExport)
	}

	markdownPath := filepath.Join(t.TempDir(), "report.md")
	if _, err := service.Export(context.Background(), ExportOptions{
		Format: FormatMarkdown, OutputPath: markdownPath,
		SessionID: "session-transfer", IncludeCoachContent: true,
	}, nil); err != nil {
		t.Fatalf("Markdown Export: %v", err)
	}
	markdown, err := os.ReadFile(markdownPath)
	if err != nil {
		t.Fatalf("Read markdown: %v", err)
	}
	for _, expected := range []string{
		"# InterviewCraft report", "## Scorecard", "## Question review",
		"## Learning map", "## Practice next", "## Evidence index",
		"Coach transcript (explicitly included)", "Coach private explanation",
	} {
		if !strings.Contains(string(markdown), expected) {
			t.Errorf("markdown missing %q", expected)
		}
	}
}

func TestTransferEmptyCorruptVersionTargetAndPathFailures(t *testing.T) {
	ctx := context.Background()
	emptyStore, emptyPath := openTransferStore(t)
	_ = emptyStore
	service := NewService(emptyPath, Options{})
	_, err := service.Export(ctx, ExportOptions{
		Format: FormatPackage, OutputPath: filepath.Join(t.TempDir(), "empty.json"),
	}, nil)
	if !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("empty export err=%#v", err)
	}

	source, sourcePath := openTransferStore(t)
	seedTransferGraph(t, source)
	sourceService := NewService(sourcePath, Options{})
	validPath := filepath.Join(t.TempDir(), "valid.json")
	if _, err := sourceService.Export(ctx, ExportOptions{
		Format: FormatPackage, OutputPath: validPath,
	}, nil); err != nil {
		t.Fatalf("Export: %v", err)
	}
	if _, err := sourceService.Export(ctx, ExportOptions{
		Format: FormatPackage, OutputPath: validPath,
	}, nil); !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("overwrite err=%#v", err)
	}
	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	_, err = sourceService.Export(ctx, ExportOptions{
		Format: FormatPackage, OutputPath: filepath.Join(parentFile, "blocked.json"),
	}, nil)
	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("unwritable path err=%#v", err)
	}

	broken := filepath.Join(t.TempDir(), "broken.json")
	if err := os.WriteFile(broken, []byte(`{"version":`), 0o600); err != nil {
		t.Fatalf("Write broken: %v", err)
	}
	_, targetPath := openTransferStore(t)
	targetService := NewService(targetPath, Options{})
	if _, err := targetService.Import(ctx, broken, nil); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("broken import err=%#v", err)
	}

	payload, err := os.ReadFile(validPath)
	if err != nil {
		t.Fatalf("Read valid: %v", err)
	}
	var bundle Bundle
	if err := json.Unmarshal(payload, &bundle); err != nil {
		t.Fatalf("Unmarshal valid: %v", err)
	}
	bundle.Version = "interviewcraft-transfer-v999"
	incompatible := filepath.Join(t.TempDir(), "incompatible.json")
	encoded, err := json.Marshal(bundle)
	if err != nil {
		t.Fatalf("Marshal incompatible: %v", err)
	}
	if err := os.WriteFile(incompatible, encoded, 0o600); err != nil {
		t.Fatalf("Write incompatible: %v", err)
	}
	if _, err := targetService.Import(ctx, incompatible, nil); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("version import err=%#v", err)
	}

	seedTransferGraph(t, emptyStore)
	if _, err := service.Import(ctx, validPath, nil); !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("nonempty target err=%#v", err)
	}
	inventory, err := service.Inventory(ctx)
	if err != nil || inventory.Sessions != 1 || inventory.Reports != 1 {
		t.Fatalf("nonempty target changed inventory=%#v err=%v", inventory, err)
	}
}

func TestImportAndDeletionRollbackRemainAtomic(t *testing.T) {
	ctx := context.Background()
	source, sourcePath := openTransferStore(t)
	seedTransferGraph(t, source)
	packagePath := filepath.Join(t.TempDir(), "transfer.json")
	if _, err := NewService(sourcePath, Options{}).Export(ctx, ExportOptions{
		Format: FormatPackage, OutputPath: packagePath,
	}, nil); err != nil {
		t.Fatalf("Export: %v", err)
	}

	_, targetPath := openTransferStore(t)
	failingImport := NewService(targetPath, Options{
		BeforeCommit: func(operation string) error {
			if operation == "import" {
				return errors.New("injected import commit failure")
			}
			return nil
		},
	})
	if _, err := failingImport.Import(ctx, packagePath, nil); !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("failing import err=%#v", err)
	}
	inventory, err := NewService(targetPath, Options{}).Inventory(ctx)
	if err != nil || inventory.Profiles != 0 || inventory.Sessions != 0 || inventory.Reports != 0 {
		t.Fatalf("partial import inventory=%#v err=%v", inventory, err)
	}

	failingDelete := NewService(sourcePath, Options{
		BeforeCommit: func(operation string) error {
			if operation == "delete" {
				return errors.New("injected delete commit failure")
			}
			return nil
		},
	})
	_, err = failingDelete.Delete(ctx, Confirmation{
		Scope: DeleteSession, SessionID: "session-transfer",
		Phrase: SessionDeletePhrase("session-transfer"),
	}, nil)
	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("failing delete err=%#v", err)
	}
	inventory, err = NewService(sourcePath, Options{}).Inventory(ctx)
	if err != nil || inventory.Sessions != 1 || inventory.Reports != 1 {
		t.Fatalf("partial delete inventory=%#v err=%v", inventory, err)
	}
}

func TestDeleteSessionAndAllRequireExactConfirmationAndRetainProviderConfig(t *testing.T) {
	ctx := context.Background()
	store, databasePath := openTransferStore(t)
	seedTransferGraph(t, store)
	insertProviderSecret(t, databasePath, "local-secret-reference")
	service := NewService(databasePath, Options{})

	if _, err := service.Delete(ctx, Confirmation{
		Scope: DeleteSession, SessionID: "session-transfer", Phrase: "yes",
	}, nil); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("unconfirmed session delete err=%#v", err)
	}
	if _, err := service.Delete(ctx, Confirmation{
		Scope: DeleteSession, SessionID: "session-transfer",
		Phrase: SessionDeletePhrase("session-transfer"),
	}, nil); err != nil {
		t.Fatalf("Delete session: %v", err)
	}
	if _, found, err := store.GetSession(ctx, "session-transfer"); err != nil || found {
		t.Fatalf("session found=%v err=%v", found, err)
	}
	if _, found, err := store.GetScenario(ctx, "scenario-transfer"); err != nil || !found {
		t.Fatalf("scenario found=%v err=%v", found, err)
	}
	assertProviderSecret(t, databasePath, "local-secret-reference")

	seedSecondSession(t, store)
	if _, err := service.Delete(ctx, Confirmation{
		Scope: DeleteAll, Phrase: "delete everything",
	}, nil); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("unconfirmed all delete err=%#v", err)
	}
	if _, err := service.Delete(ctx, Confirmation{
		Scope: DeleteAll, Phrase: AllDeletePhrase(),
	}, nil); err != nil {
		t.Fatalf("Delete all: %v", err)
	}
	inventory, err := service.Inventory(ctx)
	if err != nil || inventory.Profiles != 0 || inventory.Sessions != 0 || inventory.Reports != 0 {
		t.Fatalf("inventory=%#v err=%v", inventory, err)
	}
	assertProviderSecret(t, databasePath, "local-secret-reference")
}

func openTransferStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	store, err := db.Open(context.Background(), db.Config{
		DataDir: filepath.Join(t.TempDir(), "data"),
	}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return store, store.Paths().Database
}

func seedTransferGraph(t *testing.T, store *db.Store) corereport.Document {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	confirmedAt := now.Add(time.Minute)
	sourceText := "Built a Redis cache service with Go."
	aggregate := profile.Aggregate{
		ID: "profile-transfer",
		Candidate: contracts.CandidateProfile{
			TargetRole: "Backend Engineer",
			Facts: []contracts.ProfileFact{{
				ID: "fact-cache", Field: "project", Value: "Redis cache service",
				SourceSpan: contracts.SourceSpan{Start: 8, End: 27, Text: "Redis cache service"},
			}},
			Inferences: []contracts.ProfileInference{},
			Projects:   []string{"Redis cache service"}, Skills: []string{"Go", "Redis"},
		},
		Metadata: profile.Metadata{
			Source:        profile.Source{Kind: profile.SourcePaste, Name: "pasted resume", Text: sourceText},
			LockedFactIDs: []contracts.EvidenceID{"fact-cache"}, LockedInferenceIDs: []string{},
			CreatedAt: now, UpdatedAt: now,
		},
		ConfirmedAt: &confirmedAt,
	}
	if err := store.SaveProfileAggregate(ctx, aggregate); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	scenario := contracts.Scenario{
		Template: "project_deep_dive", Mode: contracts.ScenarioStrict,
		TimeBudgetSeconds: 1200, PromptVersion: "scenario-v1",
		Questions: []contracts.ScenarioQuestion{{
			ID: "Q1", Prompt: "Explain the Redis consistency design.",
			Intent: "Assess trade-offs", EstimatedSeconds: 300,
			Rubric:      []string{"Names a failure mode"},
			EvidenceIDs: []contracts.EvidenceID{"fact-cache"}, MaxFollowUps: 2,
			EndCondition: "A verifiable trade-off is explained",
		}},
	}
	if err := store.SaveScenario(ctx, "scenario-transfer", aggregate.ID, scenario, confirmedAt); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID: "session-transfer", ScenarioID: "scenario-transfer", Status: db.SessionCompleted,
		StartedAt: now, UpdatedAt: now.Add(20 * time.Minute),
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if err := store.AppendSessionEvent(ctx, db.SessionEvent{
		EventID: "answer-transfer", SessionID: "session-transfer",
		Speaker: db.SpeakerUser, QuestionID: "Q1",
		Content:      "Use cache-aside with versioned invalidation.",
		OccurredAt:   now.Add(5 * time.Minute),
		EvidenceRefs: []contracts.EvidenceID{"fact-cache"},
	}); err != nil {
		t.Fatalf("AppendSessionEvent: %v", err)
	}
	if err := store.SaveDraft(ctx, db.Draft{
		SessionID: "session-transfer", QuestionID: "Q1", Kind: db.DraftAnswer,
		Content: "retained local draft", UpdatedAt: now.Add(6 * time.Minute),
	}); err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if err := store.AddSidebarEvent(ctx, db.SidebarEvent{
		ID: "coach-transfer", SessionID: "session-transfer", QuestionID: "Q1",
		Intent: contracts.CoachExplainConcept, HelpLevel: contracts.HelpL2,
		Tags: []string{"Redis consistency"}, Content: "Coach private explanation",
		PolicyNote: "private policy note", Outcome: "review", PausedTimer: false,
		OccurredAt: now.Add(7 * time.Minute),
	}); err != nil {
		t.Fatalf("AddSidebarEvent: %v", err)
	}
	if err := store.AddCodeSubmission(ctx, db.CodeSubmission{
		ID: "code-transfer", SessionID: "session-transfer", QuestionID: "Q1",
		Language: "go", Source: "package main",
		TestResult:   json.RawMessage(`{"passed":1,"total":1}`),
		RuntimeStats: json.RawMessage(`{"duration_ms":12}`),
		SnapshotID:   "snapshot-transfer", CreatedAt: now.Add(9 * time.Minute),
	}); err != nil {
		t.Fatalf("AddCodeSubmission: %v", err)
	}
	document := transferReport(now)
	if err := corereport.NewService(store, corereport.Options{
		Now: func() time.Time { return document.GeneratedAt },
	}).Save(ctx, document); err != nil {
		t.Fatalf("Save report: %v", err)
	}
	return document
}

func transferReport(now time.Time) corereport.Document {
	evidence := []corereport.EvidenceLink{
		{ID: "fact-cache", Kind: "profile_fact", Label: "Redis cache fact"},
		{ID: "answer-transfer", Kind: "session_user", QuestionID: "Q1", Label: "Q1 answer", OccurredAt: now.Add(5 * time.Minute)},
		{ID: "coach-transfer", Kind: "sidebar_event", QuestionID: "Q1", Label: "Q1 Coach L2", OccurredAt: now.Add(7 * time.Minute)},
		{ID: "code-transfer", Kind: "code_submission", QuestionID: "Q1", Label: "Q1 code", OccurredAt: now.Add(9 * time.Minute)},
	}
	scorecard := make([]corereport.ScorecardItem, 0, 8)
	for _, dimension := range corereport.FixedDimensions() {
		score := 4
		scorecard = append(scorecard, corereport.ScorecardItem{
			Dimension: dimension, Status: corereport.StatusEvidenceBacked,
			Score: &score, EvidenceIDs: []contracts.EvidenceID{"answer-transfer"},
			Confidence: 0.8, NextAction: "Add one measurable verification step.",
		})
	}
	return corereport.Document{
		ID: "report-transfer", SchemaVersion: corereport.SchemaVersion,
		GeneratedAt: now.Add(21 * time.Minute),
		Summary: corereport.SessionSummary{
			SessionID: "session-transfer", ScenarioID: "scenario-transfer",
			Template: "project_deep_dive", Mode: contracts.ScenarioStrict,
			StartedAt: now, CompletedAt: now.Add(20 * time.Minute), DurationSeconds: 1200,
			QuestionCount: 1, CoachPromptCount: 1, CodeRunCount: 1,
		},
		Evidence: evidence,
		QuestionReview: []corereport.QuestionReview{{
			QuestionID: "Q1", Prompt: "Explain the Redis consistency design.",
			Summary:    corereport.Insight{Text: "Explained cache invalidation.", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"answer-transfer"}, Confidence: 0.8},
			NextAction: corereport.Insight{Text: "Add failure recovery.", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"answer-transfer"}, Confidence: 0.8},
		}},
		Scorecard: scorecard,
		LearningMap: []corereport.LearningGap{{
			Topic: "Redis consistency", AskCount: 1, MaxHelpLevel: contracts.HelpL2,
			ReviewCount: 1, QuestionIDs: []string{"Q1"},
			EvidenceIDs:   []contracts.EvidenceID{"coach-transfer"},
			RelatedSkills: []string{"Redis"}, RelatedJDNeeds: []string{},
		}},
		Transfer: []corereport.TransferEvidence{{
			SidebarEventID: "coach-transfer", QuestionID: "Q1",
			Status:             corereport.TransferEvidenceObserved,
			SubsequentEvidence: []contracts.EvidenceID{"code-transfer"},
			Summary:            "A same-question code event followed within five minutes.",
		}},
		CrossInsights: []corereport.Insight{{
			Text:        "Profile, answer, and Coach evidence share one topic.",
			Status:      corereport.StatusEvidenceBacked,
			EvidenceIDs: []contracts.EvidenceID{"fact-cache", "answer-transfer", "coach-transfer"},
			Confidence:  0.8,
		}},
		PracticePlan: []corereport.PracticeItem{
			{Topic: "Failure recovery", Mode: contracts.ScenarioStrict, DurationMinutes: 15, CompletionCriteria: "Explain one recovery path.", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"answer-transfer"}},
			{Topic: "Consistency trade-offs", Mode: contracts.ScenarioStandard, DurationMinutes: 20, CompletionCriteria: "Compare two designs.", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"answer-transfer"}},
			{Topic: "Independent answer", Mode: contracts.ScenarioStrict, DurationMinutes: 15, CompletionCriteria: "Finish without a new hint.", Status: corereport.StatusEvidenceBacked, EvidenceIDs: []contracts.EvidenceID{"coach-transfer"}},
		},
	}
}

func assertRestoredGraph(
	t *testing.T,
	store *db.Store,
	document corereport.Document,
	coachContent bool,
) {
	t.Helper()
	ctx := context.Background()
	if _, found, err := store.GetProfileAggregate(ctx, "profile-transfer"); err != nil || !found {
		t.Fatalf("profile found=%v err=%v", found, err)
	}
	if _, found, err := store.GetScenario(ctx, "scenario-transfer"); err != nil || !found {
		t.Fatalf("scenario found=%v err=%v", found, err)
	}
	if _, found, err := store.GetSession(ctx, "session-transfer"); err != nil || !found {
		t.Fatalf("session found=%v err=%v", found, err)
	}
	events, err := store.ListSessionEvents(ctx, "session-transfer")
	if err != nil || len(events) != 1 || events[0].EventID != "answer-transfer" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	coach, err := store.ListSidebarEvents(ctx, "session-transfer")
	if err != nil || len(coach) != 1 {
		t.Fatalf("coach=%#v err=%v", coach, err)
	}
	if coachContent && coach[0].Content != "Coach private explanation" {
		t.Fatalf("coach content=%q", coach[0].Content)
	}
	if !coachContent && (coach[0].Content != "" || coach[0].PolicyNote != "") {
		t.Fatalf("default coach leaked=%#v", coach[0])
	}
	code, err := store.ListCodeSubmissions(ctx, "session-transfer")
	if err != nil || len(code) != 1 || code[0].ID != "code-transfer" {
		t.Fatalf("code=%#v err=%v", code, err)
	}
	restored, found, err := corereport.NewService(store, corereport.Options{}).Get(ctx, "session-transfer")
	if err != nil || !found || restored.ID != document.ID {
		t.Fatalf("report=%#v found=%v err=%v", restored, found, err)
	}
}

func insertProviderSecret(t *testing.T, databasePath, secret string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer database.Close()
	_, err = database.Exec(`
		INSERT INTO provider_configs(id, provider, model, secret_ref, enabled, updated_at)
		VALUES (1, 'openai-compatible', 'private-model', ?, 1, ?)
	`, secret, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatalf("insert provider config: %v", err)
	}
}

func assertProviderSecret(t *testing.T, databasePath, want string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer database.Close()
	var got string
	if err := database.QueryRow(`SELECT secret_ref FROM provider_configs WHERE id = 1`).Scan(&got); err != nil {
		t.Fatalf("read provider config: %v", err)
	}
	if got != want {
		t.Fatalf("secret_ref=%q want=%q", got, want)
	}
}

func seedSecondSession(t *testing.T, store *db.Store) {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 8, 2, 9, 0, 0, 0, time.UTC)
	if err := store.CreateSession(ctx, db.Session{
		ID: "session-second", ScenarioID: "scenario-transfer", Status: db.SessionActive,
		StartedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("CreateSession second: %v", err)
	}
}

func assertPhases(t *testing.T, got, want []async.Phase) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("phases=%v want=%v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("phase[%d]=%s want=%s", index, got[index], want[index])
		}
	}
}
