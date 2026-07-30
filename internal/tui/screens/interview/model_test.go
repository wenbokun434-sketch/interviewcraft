package interview

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

func TestInterviewRoomCompleteKeyboardFlowAndTrace(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	clock := newRoomClock(fixture.now)
	provider := &roomProvider{
		respond: func(
			call int,
			input coreinterview.Input,
		) (contracts.InterviewerAction, error) {
			answerID := input.SubmittedAnswers[len(input.SubmittedAnswers)-1].EventID
			if call == 1 {
				return contracts.InterviewerAction{
					Action:       contracts.ActionCloseQuestion,
					QuestionID:   "Q1",
					Message:      "第一题已收束。",
					EvidenceIDs:  []contracts.EvidenceID{answerID},
					SessionState: contracts.SessionQuestionComplete,
				}, nil
			}
			return contracts.InterviewerAction{
				Action:       contracts.ActionFinishSession,
				QuestionID:   "Q2",
				Message:      "本轮面试完成。",
				EvidenceIDs:  []contracts.EvidenceID{answerID},
				SessionState: contracts.SessionComplete,
			}, nil
		},
	}
	service := coreinterview.NewService(
		store,
		provider,
		coreinterview.Options{Now: clock.Now},
	)
	model := newRoomModel(
		t,
		fixture.sessionID,
		service,
		clock,
		160,
		48,
		false,
	)
	var loadPhases []async.Phase
	if err := model.Load(context.Background(), func(
		state async.State[Progress],
	) {
		loadPhases = append(loadPhases, state.Phase)
	}); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(loadPhases, []async.Phase{
		async.Pending,
		async.Streaming,
		async.Succeeded,
	}) {
		t.Fatalf("load phases=%#v", loadPhases)
	}
	if model.focus.Active() != focusComposer ||
		model.Snapshot().CurrentQuestion.ID != "Q1" {
		t.Fatalf(
			"initial focus=%q snapshot=%#v",
			model.focus.Active(),
			model.Snapshot(),
		)
	}

	answerOne := "我会先说明写入版本号，再解释缓存失效窗口。"
	if err := model.UpdateDraft(context.Background(), answerOne); err != nil {
		t.Fatalf("UpdateDraft Q1: %v", err)
	}
	if action := model.HandleKey("enter"); action != (Action{}) {
		t.Fatalf("Enter submitted multiline answer: %#v", action)
	}
	if action := model.HandleKey("ctrl+enter"); action.Intent != IntentSubmit {
		t.Fatalf("Ctrl+Enter action=%#v", action)
	}
	var submitPhases []async.Phase
	clock.Advance(time.Second)
	if err := model.Submit(context.Background(), func(
		state async.State[Progress],
	) {
		submitPhases = append(submitPhases, state.Phase)
	}); err != nil {
		t.Fatalf("Submit Q1: %v", err)
	}
	if !reflect.DeepEqual(submitPhases, []async.Phase{
		async.Pending,
		async.Streaming,
		async.Succeeded,
	}) {
		t.Fatalf("submit phases=%#v", submitPhases)
	}
	snapshot := model.Snapshot()
	if snapshot.CurrentQuestion == nil ||
		snapshot.CurrentQuestion.ID != "Q2" ||
		model.Draft() != "" {
		t.Fatalf("after Q1 snapshot=%#v draft=%q", snapshot, model.Draft())
	}
	if got := countRoomSpeaker(snapshot.Events, db.SpeakerUser); got != 1 {
		t.Fatalf("submitted answers=%d, want 1", got)
	}

	model.HandleKey("tab")
	if model.focus.Active() != focusTrace {
		t.Fatalf("wide Tab focus=%q, want trace", model.focus.Active())
	}
	before := model.Snapshot().Events
	model.HandleKey("up")
	if !reflect.DeepEqual(before, model.Snapshot().Events) {
		t.Fatal("reading Answer Trace mutated immutable events")
	}
	model.HandleKey("shift+tab")
	if model.focus.Active() != focusComposer {
		t.Fatalf("Shift+Tab focus=%q", model.focus.Active())
	}

	clock.Advance(4*time.Minute + 5*time.Second)
	model.Tick(clock.Now())
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render warning timer: %v", err)
	}
	if !strings.Contains(rendered, "warning") {
		t.Fatalf("warning timer missing:\n%s", rendered)
	}

	if action := model.HandleKey("p"); action.Intent != IntentPause {
		t.Fatalf("pause action=%#v", action)
	}
	if err := model.TogglePause(context.Background()); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if model.Snapshot().Phase != coreinterview.PhasePaused {
		t.Fatalf("paused snapshot=%#v", model.Snapshot())
	}
	if action := model.HandleKey("p"); action.Intent != IntentResume {
		t.Fatalf("resume action=%#v", action)
	}
	clock.Advance(time.Minute)
	if err := model.TogglePause(context.Background()); err != nil {
		t.Fatalf("Resume: %v", err)
	}

	answerTwo := "我会验证错误率、积压和数据一致性三个恢复信号。"
	if err := model.UpdateDraft(context.Background(), answerTwo); err != nil {
		t.Fatalf("UpdateDraft Q2: %v", err)
	}
	clock.Advance(time.Second)
	if err := model.Submit(context.Background(), nil); err != nil {
		t.Fatalf("Submit Q2: %v", err)
	}
	snapshot = model.Snapshot()
	if snapshot.Phase != coreinterview.PhaseCompleted ||
		snapshot.Session.Status != db.SessionEvaluationPending {
		t.Fatalf("completed snapshot=%#v", snapshot)
	}
	if action := model.HandleKey("q"); action.Destination !=
		DestinationTraining {
		t.Fatalf("completed q action=%#v", action)
	}
	rendered, err = model.Render()
	if err != nil {
		t.Fatalf("Render complete: %v", err)
	}
	for _, expected := range []string{
		"ANSWER TRACE",
		"INTERVIEW ROOM",
		"SESSION",
		"evaluation pending",
		"会话完成，已进入待评估状态",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("completed room missing %q", expected)
		}
	}
}

func TestThinkingCancelPreservesSubmittedAnswerAndRetry(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	clock := newRoomClock(fixture.now)
	started := make(chan struct{})
	provider := &roomProvider{
		respondContext: func(
			ctx context.Context,
			call int,
			input coreinterview.Input,
		) (contracts.InterviewerAction, error) {
			if call == 1 {
				close(started)
				<-ctx.Done()
				return contracts.InterviewerAction{}, ctx.Err()
			}
			answerID := input.SubmittedAnswers[len(input.SubmittedAnswers)-1].EventID
			return contracts.InterviewerAction{
				Action:       contracts.ActionFollowUp,
				QuestionID:   "Q1",
				Message:      "这个权衡在故障时如何变化？",
				EvidenceIDs:  []contracts.EvidenceID{answerID},
				SessionState: contracts.SessionInterviewing,
			}, nil
		},
	}
	service := coreinterview.NewService(
		store,
		provider,
		coreinterview.Options{Now: clock.Now},
	)
	model := newRoomModel(
		t,
		fixture.sessionID,
		service,
		clock,
		120,
		36,
		false,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	answer := "答案必须先落库，再等待模型。"
	if err := model.UpdateDraft(context.Background(), answer); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	result := make(chan error, 1)
	go func() {
		result <- model.Submit(context.Background(), nil)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Provider did not start")
	}
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render thinking: %v", err)
	}
	if !strings.Contains(rendered, "interviewer: ▌") ||
		!strings.Contains(rendered, "[Esc] 停止等待") {
		t.Fatalf("thinking state missing:\n%s", rendered)
	}
	if action := model.HandleKey("esc"); action.Intent != IntentCancelWait {
		t.Fatalf("Esc while thinking action=%#v", action)
	}
	if !model.CancelWaiting() {
		t.Fatal("CancelWaiting returned false")
	}
	select {
	case err := <-result:
		if !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
			t.Fatalf("cancelled Submit error=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Submit did not stop after cancellation")
	}
	if model.Draft() != answer {
		t.Fatalf("cancelled draft=%q", model.Draft())
	}
	events, err := store.ListSessionEvents(
		context.Background(),
		fixture.sessionID,
	)
	if err != nil {
		t.Fatalf("ListSessionEvents: %v", err)
	}
	if countRoomSpeaker(events, db.SpeakerUser) != 1 {
		t.Fatalf("cancelled answer was not persisted: %#v", events)
	}
	rendered, err = model.Render()
	if err != nil {
		t.Fatalf("Render cancelled: %v", err)
	}
	for _, expected := range []string{answer, "[t] 重试", "[x] 结束本题"} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("cancel recovery missing %q", expected)
		}
	}

	recovered := newRoomModel(
		t,
		fixture.sessionID,
		service,
		clock,
		120,
		36,
		false,
	)
	if err := recovered.Load(context.Background(), nil); err != nil {
		t.Fatalf("recover pending answer: %v", err)
	}
	if recovered.Draft() != answer {
		t.Fatalf("recovered pending draft=%q", recovered.Draft())
	}
	if action := recovered.HandleKey("t"); action.Intent != IntentRetry {
		t.Fatalf("retry key action=%#v", action)
	}
	if err := recovered.Retry(context.Background(), nil); err != nil {
		t.Fatalf("Retry: %v", err)
	}
	events, _ = store.ListSessionEvents(
		context.Background(),
		fixture.sessionID,
	)
	if countRoomSpeaker(events, db.SpeakerUser) != 1 ||
		countRoomEvents(events, "/action/") != 1 {
		t.Fatalf("retry duplicated events: %#v", events)
	}
}

func TestInvalidModelOutputOffersRetryOrSafeEnd(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	clock := newRoomClock(fixture.now)
	provider := &roomProvider{
		respond: func(
			call int,
			input coreinterview.Input,
		) (contracts.InterviewerAction, error) {
			if call == 1 {
				return contracts.InterviewerAction{
					Action:       contracts.ActionFollowUp,
					QuestionID:   "Q1",
					Message:      "unsafe follow-up",
					EvidenceIDs:  []contracts.EvidenceID{"coach-secret"},
					SessionState: contracts.SessionInterviewing,
				}, nil
			}
			answerID := input.SubmittedAnswers[len(input.SubmittedAnswers)-1].EventID
			return contracts.InterviewerAction{
				Action:       contracts.ActionCloseQuestion,
				QuestionID:   "Q1",
				Message:      "第一题已安全结束。",
				EvidenceIDs:  []contracts.EvidenceID{answerID},
				SessionState: contracts.SessionQuestionComplete,
			}, nil
		},
	}
	service := coreinterview.NewService(
		store,
		provider,
		coreinterview.Options{Now: clock.Now},
	)
	model := newRoomModel(
		t,
		fixture.sessionID,
		service,
		clock,
		120,
		36,
		false,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	answer := "回答原文不会因非法动作而消失。"
	if err := model.UpdateDraft(context.Background(), answer); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	err := model.Submit(context.Background(), nil)
	if !domainerr.IsCode(err, domainerr.CodeInvalidModelOutput) {
		t.Fatalf("invalid action error=%v", err)
	}
	rendered, renderErr := model.Render()
	if renderErr != nil {
		t.Fatalf("Render error: %v", renderErr)
	}
	for _, expected := range []string{
		"模型返回了不安全或无效的面试动作",
		"[t] 重试",
		"[x] 结束本题",
		answer,
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("invalid-output UI missing %q", expected)
		}
	}
	if err := model.Retry(context.Background(), nil); err != nil {
		t.Fatalf("Retry invalid output: %v", err)
	}
	if model.Snapshot().CurrentQuestion.ID != "Q2" {
		t.Fatalf("retry snapshot=%#v", model.Snapshot())
	}

	secondStore := openRoomStore(t)
	secondFixture := seedRoomSession(t, secondStore)
	second := newRoomModel(
		t,
		secondFixture.sessionID,
		coreinterview.NewService(
			secondStore,
			nil,
			coreinterview.Options{Now: clock.Now},
		),
		clock,
		120,
		36,
		false,
	)
	if err := second.Load(context.Background(), nil); err != nil {
		t.Fatalf("second Load: %v", err)
	}
	if err := second.UpdateDraft(
		context.Background(),
		"Provider 不可用时也先保存",
	); err != nil {
		t.Fatalf("second UpdateDraft: %v", err)
	}
	if err := second.Submit(context.Background(), nil); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("nil Provider Submit error=%v", err)
	}
	if action := second.HandleKey("x"); action.Intent !=
		IntentRequestEndQuestion {
		t.Fatalf("safe-end action=%#v", action)
	}
	if err := second.RequestEnd(
		context.Background(),
		coreinterview.EndQuestion,
	); err != nil {
		t.Fatalf("RequestEnd question: %v", err)
	}
	if action := second.HandleKey("y"); action.Intent != IntentConfirmEnd {
		t.Fatalf("confirm key action=%#v", action)
	}
	if err := second.ConfirmEnd(context.Background()); err != nil {
		t.Fatalf("ConfirmEnd question: %v", err)
	}
	if second.Snapshot().CurrentQuestion.ID != "Q2" {
		t.Fatalf("safe end snapshot=%#v", second.Snapshot())
	}
}

func TestEndSessionRequiresPersistedSecondConfirmation(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	clock := newRoomClock(fixture.now)
	model := newRoomModel(
		t,
		fixture.sessionID,
		coreinterview.NewService(
			store,
			nil,
			coreinterview.Options{Now: clock.Now},
		),
		clock,
		80,
		24,
		true,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if action := model.HandleKey("esc"); action != (Action{}) {
		t.Fatalf("idle Esc left session: %#v", action)
	}
	action := model.HandleKey("q")
	if action.Intent != IntentRequestEndSession ||
		action.Destination != DestinationNone {
		t.Fatalf("q action=%#v", action)
	}
	if err := model.RequestEnd(
		context.Background(),
		coreinterview.EndSession,
	); err != nil {
		t.Fatalf("RequestEnd: %v", err)
	}
	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render confirm: %v", err)
	}
	if !strings.Contains(rendered, "确认结束整场面试") ||
		!strings.Contains(rendered, "[y/Enter] 确认结束") {
		t.Fatalf("confirmation missing:\n%s", rendered)
	}
	if action := model.HandleKey("esc"); action.Intent != IntentCancelEnd {
		t.Fatalf("confirm Esc action=%#v", action)
	}
	if err := model.CancelEnd(context.Background()); err != nil {
		t.Fatalf("CancelEnd: %v", err)
	}
	if model.Snapshot().PendingEnd != nil ||
		model.Snapshot().Phase != coreinterview.PhaseAwaitingAnswer {
		t.Fatalf("cancelled snapshot=%#v", model.Snapshot())
	}

	if err := model.RequestEnd(
		context.Background(),
		coreinterview.EndSession,
	); err != nil {
		t.Fatalf("second RequestEnd: %v", err)
	}
	if action := model.HandleKey("enter"); action.Intent != IntentConfirmEnd {
		t.Fatalf("confirm Enter action=%#v", action)
	}
	if err := model.ConfirmEnd(context.Background()); err != nil {
		t.Fatalf("ConfirmEnd: %v", err)
	}
	if model.Snapshot().Phase != coreinterview.PhaseCompleted {
		t.Fatalf("confirmed snapshot=%#v", model.Snapshot())
	}
}

func TestEmptyRoomAndResponsiveDraftRecovery(t *testing.T) {
	t.Parallel()

	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		failure := domainerr.New(
			domainerr.CodeInvalidState,
			"load interview",
			"当前场景没有可用题目。",
			"返回场景工厂生成至少一道题。",
			false,
		)
		model := newRoomModel(
			t,
			"session-empty",
			&loadFailureRoom{failure: failure},
			newRoomClock(time.Now()),
			120,
			36,
			false,
		)
		if err := model.Load(context.Background(), nil); !domainerr.IsCode(err, domainerr.CodeInvalidState) {
			t.Fatalf("empty Load error=%v", err)
		}
		rendered, err := model.Render()
		if err != nil {
			t.Fatalf("Render empty: %v", err)
		}
		for _, expected := range []string{
			"-- 当前会话没有可用题目 --",
			"当前场景没有可用题目",
			"返回场景工厂生成至少一道题",
		} {
			if !strings.Contains(rendered, expected) {
				t.Errorf("empty room missing %q", expected)
			}
		}
	})

	t.Run("restart_and_sizes", func(t *testing.T) {
		t.Parallel()
		ctx := context.Background()
		dataDir := filepath.Join(t.TempDir(), "data")
		store, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
		if err != nil {
			t.Fatalf("db.Open: %v", err)
		}
		fixture := seedRoomSession(t, store)
		clock := newRoomClock(fixture.now)
		service := coreinterview.NewService(
			store,
			nil,
			coreinterview.Options{Now: clock.Now},
		)
		first := newRoomModel(
			t,
			fixture.sessionID,
			service,
			clock,
			160,
			48,
			false,
		)
		if err := first.Load(ctx, nil); err != nil {
			t.Fatalf("first Load: %v", err)
		}
		draft := "重启、切换焦点和终端 resize 都不能丢失的中文草稿。"
		if err := first.UpdateDraft(ctx, draft); err != nil {
			t.Fatalf("UpdateDraft: %v", err)
		}
		first.HandleKey("?")
		first.Resize(80, 24)
		first.HandleKey("esc")
		if first.Draft() != draft ||
			first.focus.Active() != focusComposer {
			t.Fatalf(
				"resize/help draft=%q focus=%q",
				first.Draft(),
				first.focus.Active(),
			)
		}
		if err := store.Close(); err != nil {
			t.Fatalf("Close: %v", err)
		}
		reopened, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
		if err != nil {
			t.Fatalf("reopen: %v", err)
		}
		t.Cleanup(func() { _ = reopened.Close() })

		tests := []struct {
			name      string
			width     int
			height    int
			ascii     bool
			required  []string
			forbidden []string
		}{
			{
				name: "wide_160x48", width: 160, height: 48,
				required: []string{
					"ANSWER TRACE",
					"INTERVIEW ROOM",
					"SESSION",
					"QUESTION 01/02",
					"已恢复本地草稿",
				},
			},
			{
				name: "split_120x36", width: 120, height: 36,
				required: []string{
					"INTERVIEW ROOM",
					"SESSION",
					"缓存失效",
					"Ctrl+Enter",
				},
				forbidden: []string{"ANSWER TRACE"},
			},
			{
				name:  "narrow_80x24_ascii",
				width: 80, height: 24, ascii: true,
				required: []string{
					"+",
					"INTERVIEW ROOM",
					"TRACE",
					"缓存失效",
					"Ctrl+Enter",
				},
				forbidden: []string{"┌", "✓"},
			},
		}
		for _, test := range tests {
			test := test
			t.Run(test.name, func(t *testing.T) {
				current := noColorRoomTheme(t, test.ascii)
				model, err := New(Options{
					SessionID: fixture.sessionID,
					Room: coreinterview.NewService(
						reopened,
						nil,
						coreinterview.Options{Now: clock.Now},
					),
					Now:             clock.Now,
					NextSubmission:  sequenceID("answer"),
					NextOperationID: sequenceID("control"),
					Width:           test.width,
					Height:          test.height,
					Theme:           current,
				})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				if err := model.Load(ctx, nil); err != nil {
					t.Fatalf("Load: %v", err)
				}
				if model.Draft() != draft {
					t.Fatalf("restored draft=%q", model.Draft())
				}
				rendered, err := model.Render()
				if err != nil {
					t.Fatalf("Render: %v", err)
				}
				assertRoomGeometry(
					t,
					rendered,
					test.width,
					test.height,
				)
				for _, expected := range test.required {
					if !strings.Contains(rendered, expected) {
						t.Errorf("snapshot missing %q", expected)
					}
				}
				for _, forbidden := range test.forbidden {
					if strings.Contains(rendered, forbidden) {
						t.Errorf("snapshot contains %q", forbidden)
					}
				}
			})
		}
	})
}

type roomFixture struct {
	now       time.Time
	sessionID string
}

func seedRoomSession(t *testing.T, store *db.Store) roomFixture {
	t.Helper()
	ctx := context.Background()
	now := time.Date(2026, 7, 30, 14, 3, 0, 0, time.UTC)
	source := "负责 Go 支付平台与 Redis 缓存一致性。"
	confirmedAt := now
	profile := coreprofile.Aggregate{
		ID: "profile-room",
		Candidate: contracts.CandidateProfile{
			TargetRole: "后端平台工程师",
			Facts: []contracts.ProfileFact{{
				ID:    "fact-redis",
				Field: "project",
				Value: "支付平台",
				SourceSpan: contracts.SourceSpan{
					Start: 0,
					End:   len(source),
					Text:  source,
				},
			}},
			Inferences: []contracts.ProfileInference{},
			Projects:   []string{"支付平台"},
			Skills:     []string{"Go", "Redis"},
		},
		Metadata: coreprofile.Metadata{
			Source: coreprofile.Source{
				Kind: coreprofile.SourcePaste,
				Name: "resume.txt",
				Text: source,
			},
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
			CreatedAt:          now,
			UpdatedAt:          now,
		},
		ConfirmedAt: &confirmedAt,
	}
	scenario := contracts.Scenario{
		Template:          "项目深挖",
		Mode:              contracts.ScenarioStrict,
		TimeBudgetSeconds: 600,
		PromptVersion:     "scenario-planner-v1.r1",
		Questions: []contracts.ScenarioQuestion{
			{
				ID:               "Q1",
				Prompt:           "请说明缓存失效时你如何保证一致性？",
				Intent:           "评估已确认项目深度",
				EstimatedSeconds: 300,
				Rubric:           []string{"解释一种权衡"},
				EvidenceIDs:      []contracts.EvidenceID{"fact-redis"},
				MaxFollowUps:     2,
				EndCondition:     "说明一致性权衡",
			},
			{
				ID:               "Q2",
				Prompt:           "故障恢复后你会验证哪些信号？",
				Intent:           "评估恢复验证",
				EstimatedSeconds: 300,
				Rubric:           []string{"给出恢复信号"},
				EvidenceIDs:      []contracts.EvidenceID{},
				Generic:          true,
				MaxFollowUps:     1,
				EndCondition:     "说明至少一个信号",
			},
		},
	}
	if err := store.SaveProfileAggregate(ctx, profile); err != nil {
		t.Fatalf("SaveProfileAggregate: %v", err)
	}
	if err := store.SaveScenario(
		ctx,
		"scenario-room",
		profile.ID,
		scenario,
		now,
	); err != nil {
		t.Fatalf("SaveScenario: %v", err)
	}
	if err := store.CreateSession(ctx, db.Session{
		ID:         "session-room",
		ScenarioID: "scenario-room",
		Status:     db.SessionActive,
		StartedAt:  now,
		UpdatedAt:  now,
	}); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return roomFixture{now: now, sessionID: "session-room"}
}

type roomProvider struct {
	mu             sync.Mutex
	calls          int
	respond        func(int, coreinterview.Input) (contracts.InterviewerAction, error)
	respondContext func(
		context.Context,
		int,
		coreinterview.Input,
	) (contracts.InterviewerAction, error)
}

func (provider *roomProvider) Respond(
	ctx context.Context,
	input coreinterview.Input,
) (contracts.InterviewerAction, error) {
	provider.mu.Lock()
	provider.calls++
	call := provider.calls
	respond := provider.respond
	respondContext := provider.respondContext
	provider.mu.Unlock()
	if respondContext != nil {
		return respondContext(ctx, call, input)
	}
	return respond(call, input)
}

type roomClock struct {
	mu      sync.Mutex
	current time.Time
}

func newRoomClock(value time.Time) *roomClock {
	return &roomClock{current: value}
}

func (clock *roomClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.current
}

func (clock *roomClock) Advance(value time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.current = clock.current.Add(value)
}

type loadFailureRoom struct {
	Room
	failure error
}

func (room *loadFailureRoom) Load(
	context.Context,
	string,
) (coreinterview.Snapshot, error) {
	return coreinterview.Snapshot{}, room.failure
}

func newRoomModel(
	t *testing.T,
	sessionID string,
	room Room,
	clock *roomClock,
	width int,
	height int,
	ascii bool,
) *Model {
	t.Helper()
	model, err := New(Options{
		SessionID:       sessionID,
		Room:            room,
		Now:             clock.Now,
		NextSubmission:  sequenceID("answer"),
		NextOperationID: sequenceID("control"),
		Width:           width,
		Height:          height,
		Theme:           noColorRoomTheme(t, ascii),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return model
}

func sequenceID(prefix string) func() string {
	var (
		mu    sync.Mutex
		index int
	)
	return func() string {
		mu.Lock()
		defer mu.Unlock()
		index++
		return fmt.Sprintf("%s-%d", prefix, index)
	}
}

func openRoomStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(
		context.Background(),
		db.Config{DataDir: filepath.Join(t.TempDir(), "data")},
		nil,
	)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func noColorRoomTheme(t *testing.T, ascii bool) theme.Theme {
	t.Helper()
	current, err := theme.Resolve(theme.Options{
		Mode:         theme.Auto,
		ColorMode:    theme.NoColor,
		UseASCII:     ascii,
		ReduceMotion: true,
	})
	if err != nil {
		t.Fatalf("Resolve theme: %v", err)
	}
	return current
}

func countRoomSpeaker(
	events []db.SessionEvent,
	speaker db.EventSpeaker,
) int {
	count := 0
	for _, event := range events {
		if event.Speaker == speaker {
			count++
		}
	}
	return count
}

func countRoomEvents(events []db.SessionEvent, fragment string) int {
	count := 0
	for _, event := range events {
		if strings.Contains(event.EventID, fragment) {
			count++
		}
	}
	return count
}

func assertRoomGeometry(
	t *testing.T,
	rendered string,
	width int,
	height int,
) {
	t.Helper()
	lines := strings.Split(rendered, "\n")
	if len(lines) != height {
		t.Fatalf("rendered rows=%d, want %d", len(lines), height)
	}
	for index, line := range lines {
		if got := layout.VisibleWidth(line); got != width {
			t.Fatalf(
				"row %d width=%d, want %d: %q",
				index,
				got,
				width,
				line,
			)
		}
	}
}

func TestRoomFailureWrapping(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{}); !domainerr.IsCode(
		err,
		domainerr.CodeValidation,
	) {
		t.Fatalf("empty SessionID error=%v", err)
	}
	model := newRoomModel(
		t,
		"session-missing",
		nil,
		newRoomClock(time.Now()),
		120,
		36,
		false,
	)
	if err := model.Load(context.Background(), nil); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("missing Room error=%v", err)
	}
	if got := roomFailure(errors.New("offline")); !domainerr.IsCode(
		got,
		domainerr.CodeDependencyUnavailable,
	) {
		t.Fatalf("roomFailure=%v", got)
	}
}
