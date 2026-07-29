package training

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

type stubQuery struct {
	data db.TrainingHomeData
	err  error
}

func (stub stubQuery) LoadTrainingHome(
	context.Context,
	int,
) (db.TrainingHomeData, error) {
	return stub.data, stub.err
}

func TestLoadCoversPendingSucceededAndFailedLifecycle(t *testing.T) {
	model := newTestModel(t, stubQuery{data: populatedHome()}, 120, 36, false, false)
	var phases []async.Phase
	model.Load(context.Background(), func(state async.State[db.TrainingHomeData]) {
		phases = append(phases, state.Phase)
	})
	if got := phases; len(got) != 2 ||
		got[0] != async.Pending ||
		got[1] != async.Succeeded {
		t.Fatalf("phases = %#v", got)
	}

	loading, err := New(stubQuery{}, 120, 36, noColorTheme(t, false, false))
	if err != nil {
		t.Fatalf("New loading model: %v", err)
	}
	rendered, err := loading.Render()
	if err != nil || !strings.Contains(rendered, "正在加载训练记录") {
		t.Fatalf("loading render err=%v output=%q", err, rendered)
	}

	failure := domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		"query training",
		"SQLite",
		"无法查询 SQLite 训练记录。",
		"检查数据库后按 [t] 重试。",
		true,
		errors.New("database is closed"),
	)
	failed := newTestModel(t, stubQuery{err: failure}, 120, 36, false, false)
	failed.Load(context.Background(), nil)
	rendered, err = failed.Render()
	if err != nil {
		t.Fatalf("failed Render: %v", err)
	}
	for _, expected := range []string{
		"无法查询 SQLite 训练记录",
		"检查数据库后按 [t] 重试",
		"[t] 重试",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("failed render missing %q", expected)
		}
	}
}

func TestEmptyHomeHasSinglePrimaryPathToProfile(t *testing.T) {
	model := newTestModel(t, stubQuery{data: db.TrainingHomeData{}}, 80, 24, false, false)
	model.Load(context.Background(), nil)

	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	for _, expected := range []string{
		"还没有训练记录",
		"[n] 创建第一场模拟",
		"还没有练习队列",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("empty render missing %q", expected)
		}
	}
	if action := model.HandleKey("enter"); action.Destination != DestinationProfile {
		t.Fatalf("Enter action = %#v", action)
	}
	if action := model.HandleKey("n"); action.Destination != DestinationProfile {
		t.Fatalf("n action = %#v", action)
	}
}

func TestMainNavigationResumesExactSessionAndOpensReportAndQueue(t *testing.T) {
	model := newTestModel(t, stubQuery{data: populatedHome()}, 160, 48, false, false)
	model.Load(context.Background(), nil)

	action := model.HandleKey("enter")
	if action.Destination != DestinationInterview || action.SessionID != "session-active" {
		t.Fatalf("primary Enter = %#v", action)
	}

	model.HandleKey("tab")
	model.HandleKey("down")
	action = model.HandleKey("enter")
	if action.Destination != DestinationReport ||
		action.ReportID != "report-complete" ||
		action.SessionID != "session-complete" {
		t.Fatalf("recent Enter = %#v", action)
	}

	model.HandleKey("tab")
	action = model.HandleKey("enter")
	if action.Destination != DestinationScenario ||
		action.PracticeID != "practice-redis" {
		t.Fatalf("queue Enter = %#v", action)
	}
}

func TestHelpRestoresExactFocusAndResizePreservesState(t *testing.T) {
	model := newTestModel(t, stubQuery{data: populatedHome()}, 120, 36, false, false)
	model.Load(context.Background(), nil)
	model.HandleKey("tab")
	if got := model.focus.Active(); got != focusRecent {
		t.Fatalf("focus = %q", got)
	}
	model.HandleKey("?")
	if got := model.focus.Active(); got != focusHelp {
		t.Fatalf("help focus = %q", got)
	}
	rendered, err := model.Render()
	if err != nil || !strings.Contains(rendered, "GLOBAL NAVIGATION") {
		t.Fatalf("help Render err=%v", err)
	}
	model.Resize(80, 24)
	model.HandleKey("esc")
	if got := model.focus.Active(); got != focusRecent {
		t.Fatalf("restored focus = %q", got)
	}
	if model.State().Phase != async.Succeeded {
		t.Fatalf("state after resize = %q", model.State().Phase)
	}
}

func TestResponsiveSnapshotsAndAccessibilityModes(t *testing.T) {
	testCases := []struct {
		name         string
		width        int
		height       int
		ascii        bool
		reduceMotion bool
		required     []string
		forbidden    []string
	}{
		{
			name: "wide_160x48", width: 160, height: 48,
			required: []string{"导航", "TRAINING", "PRACTICE QUEUE", "技术深度 3/5"},
		},
		{
			name: "split_120x36", width: 120, height: 36,
			required:  []string{"TRAINING", "PRACTICE QUEUE", "技术深度 3/5"},
			forbidden: []string{"[t] 训练"},
		},
		{
			name: "narrow_80x24_ascii_reduce", width: 80, height: 24,
			ascii: true, reduceMotion: true,
			required:  []string{"+", "PRACTICE QUEUE", "Redis 一致性与故障恢复"},
			forbidden: []string{"┌", "· 正在"},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			model := newTestModel(
				t,
				stubQuery{data: populatedHome()},
				testCase.width,
				testCase.height,
				testCase.ascii,
				testCase.reduceMotion,
			)
			model.Load(context.Background(), nil)
			rendered, err := model.Render()
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			assertSnapshotGeometry(t, rendered, testCase.width, testCase.height)
			for _, expected := range testCase.required {
				if !strings.Contains(rendered, expected) {
					t.Errorf("snapshot missing %q", expected)
				}
			}
			for _, forbidden := range testCase.forbidden {
				if strings.Contains(rendered, forbidden) {
					t.Errorf("snapshot contains forbidden %q", forbidden)
				}
			}
		})
	}
}

func TestStreamingStateRendersWithoutBlockingNavigation(t *testing.T) {
	model := newTestModel(t, stubQuery{}, 120, 36, false, true)
	partial := populatedHome()
	model.state = async.NewStreaming(&partial)

	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if !strings.Contains(rendered, "正在加载最近训练与练习队列") ||
		!strings.Contains(rendered, "项目深挖") {
		t.Fatalf("streaming render missing state/data")
	}
	if action := model.HandleKey("s"); action.Destination != DestinationSettings {
		t.Fatalf("settings action = %#v", action)
	}
}

func populatedHome() db.TrainingHomeData {
	now := time.Date(2026, 7, 30, 9, 30, 0, 0, time.UTC)
	scenario := contracts.Scenario{
		Template:          "project_deep_dive",
		Mode:              contracts.ScenarioStrict,
		TimeBudgetSeconds: 1200,
		PromptVersion:     "scenario-v1",
		Questions: []contracts.ScenarioQuestion{{
			ID:               "Q2",
			Prompt:           "如何处理缓存一致性？",
			Intent:           "Assess trade-offs",
			EstimatedSeconds: 300,
			Rubric:           []string{"Names a trade-off"},
			EvidenceIDs:      []contracts.EvidenceID{"fact-1"},
			MaxFollowUps:     2,
			EndCondition:     "Trade-off explained",
		}},
	}
	last := db.SessionEvent{
		EventID:    "event-latest",
		SessionID:  "session-active",
		QuestionID: "Q2",
		Content:    "已提交回答",
		OccurredAt: now,
	}
	draft := db.Draft{
		SessionID:  "session-active",
		QuestionID: "Q2",
		Kind:       db.DraftAnswer,
		Content:    "未提交的补充",
		UpdatedAt:  now,
	}
	return db.TrainingHomeData{
		Resume: &db.ResumePoint{
			Session: db.Session{
				ID:         "session-active",
				ScenarioID: "scenario-active",
				Status:     db.SessionActive,
				StartedAt:  now.Add(-10 * time.Minute),
				UpdatedAt:  now,
			},
			Scenario:  scenario,
			LastEvent: &last,
			Draft:     &draft,
		},
		Recent: []db.RecentTraining{
			{
				SessionID:  "session-active",
				ScenarioID: "scenario-active",
				Template:   "project_deep_dive",
				Mode:       contracts.ScenarioStrict,
				Status:     db.SessionActive,
				UpdatedAt:  now,
			},
			{
				SessionID:  "session-complete",
				ScenarioID: "scenario-complete",
				Template:   "系统设计与一个非常长的中文场景名称",
				Mode:       contracts.ScenarioStandard,
				Status:     db.SessionCompleted,
				UpdatedAt:  now.Add(-24 * time.Hour),
				ReportID:   "report-complete",
				Score: &db.DimensionScore{
					Dimension: contracts.DimensionTechnicalDepth,
					Score:     3,
					Scale:     5,
				},
			},
		},
		PracticeQueue: []db.PracticeItem{
			{
				ID:              "practice-redis",
				ReportID:        "report-complete",
				SessionID:       "session-complete",
				Topic:           "Redis 一致性与故障恢复",
				Mode:            contracts.ScenarioStrict,
				DurationMinutes: 15,
			},
		},
	}
}

func newTestModel(
	t *testing.T,
	query Query,
	width int,
	height int,
	ascii bool,
	reduceMotion bool,
) *Model {
	t.Helper()
	model, err := New(
		query,
		width,
		height,
		noColorTheme(t, ascii, reduceMotion),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func noColorTheme(t *testing.T, ascii, reduceMotion bool) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode:         theme.Auto,
		ColorMode:    theme.NoColor,
		UseASCII:     ascii,
		ReduceMotion: reduceMotion,
	})
	if err != nil {
		t.Fatalf("Resolve theme: %v", err)
	}
	return current
}

func assertSnapshotGeometry(t *testing.T, rendered string, width, height int) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("snapshot rows = %d, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf("snapshot row %d width = %d, want %d", index, got, width)
		}
	}
}
