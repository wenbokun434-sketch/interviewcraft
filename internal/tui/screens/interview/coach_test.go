package interview

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	corecoach "github.com/interviewcraft/interviewcraft/internal/core/coach"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestCoachPaneQuickFreeAskOutcomePauseAndResponsiveFocus(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	setRoomScenarioMode(t, store, fixture, contracts.ScenarioCoach)
	clock := newRoomClock(fixture.now)
	provider := &coachUIProvider{
		respond: func(
			_ int,
			input corecoach.Input,
		) (contracts.CoachResponse, error) {
			return coachUIResponse(input), nil
		},
	}
	coachService := corecoach.NewService(
		store,
		provider,
		corecoach.Options{Now: clock.Now},
	)
	model := newCoachUIModel(
		t,
		fixture,
		store,
		coachService,
		clock,
		160,
		48,
		false,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mainDraft := "主回答草稿：先说明读写路径，再解释一致性取舍。"
	if err := model.UpdateDraft(context.Background(), mainDraft); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	if err := model.SetDraftCursor(9); err != nil {
		t.Fatalf("SetDraftCursor: %v", err)
	}
	before, err := store.ListSessionEvents(context.Background(), fixture.sessionID)
	if err != nil {
		t.Fatalf("ListSessionEvents before Coach: %v", err)
	}

	rendered, err := model.Render()
	if err != nil {
		t.Fatalf("Render empty Coach: %v", err)
	}
	assertRoomGeometry(t, rendered, 160, 48)
	for _, expected := range []string{
		"ANSWER TRACE",
		"INTERVIEW ROOM",
		"COACH",
		"COACH READY",
		"[1] 解释概念",
		"[2] 给我提示",
		"[3] 梳理回答结构",
		"COACH · hints ∞ · max L3",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("empty Coach screen missing %q", expected)
		}
	}

	model.HandleKey("c")
	if model.focus.Active() != focusCoach {
		t.Fatalf("wide Coach focus=%q", model.focus.Active())
	}
	action := model.HandleKey("2")
	if action.Intent != IntentCoachAsk ||
		action.CoachIntent != contracts.CoachGiveHint {
		t.Fatalf("quick Coach action=%#v", action)
	}
	if err := model.AskCoach(
		context.Background(),
		action.CoachIntent,
		false,
	); err != nil {
		t.Fatalf("quick AskCoach: %v", err)
	}
	if model.Draft() != mainDraft || model.DraftCursor() != 9 {
		t.Fatalf(
			"quick Coach draft=%q cursor=%d",
			model.Draft(),
			model.DraftCursor(),
		)
	}
	afterQuick, err := store.ListSessionEvents(
		context.Background(),
		fixture.sessionID,
	)
	if err != nil || len(afterQuick) != len(before) {
		t.Fatalf(
			"ordinary Coach changed main events: before=%d after=%d err=%v",
			len(before),
			len(afterQuick),
			err,
		)
	}
	rendered, err = model.Render()
	if err != nil {
		t.Fatalf("Render Coach response: %v", err)
	}
	for _, expected := range []string{
		"COACH · L3",
		"先明确一个约束",
		"topics: cache consistency",
		"[u] 已理解",
	} {
		if !strings.Contains(rendered, expected) {
			t.Errorf("Coach response missing %q", expected)
		}
	}

	action = model.HandleKey("u")
	if action.Intent != IntentCoachMark ||
		action.CoachOutcome != corecoach.OutcomeUnderstood {
		t.Fatalf("mark understood action=%#v", action)
	}
	if err := model.MarkCoachOutcome(
		context.Background(),
		action.CoachOutcome,
	); err != nil {
		t.Fatalf("MarkCoachOutcome: %v", err)
	}
	history, err := coachService.History(
		context.Background(),
		fixture.sessionID,
	)
	if err != nil || len(history) != 1 ||
		history[0].Outcome != string(corecoach.OutcomeUnderstood) {
		t.Fatalf("Coach history=%#v err=%v", history, err)
	}

	if err := model.UpdateCoachDraft(
		"请检查我对故障恢复信号的思路。",
	); err != nil {
		t.Fatalf("UpdateCoachDraft: %v", err)
	}
	action = model.HandleKey("ctrl+p")
	if action.Intent != IntentCoachAskPaused ||
		action.CoachIntent != contracts.CoachCheckReasoning {
		t.Fatalf("pause-and-ask action=%#v", action)
	}
	if err := model.AskCoach(
		context.Background(),
		action.CoachIntent,
		true,
	); err != nil {
		t.Fatalf("paused AskCoach: %v", err)
	}
	if model.Snapshot().Phase != coreinterview.PhasePaused ||
		model.Draft() != mainDraft ||
		model.DraftCursor() != 9 {
		t.Fatalf(
			"paused snapshot=%#v draft=%q cursor=%d",
			model.Snapshot(),
			model.Draft(),
			model.DraftCursor(),
		)
	}
	mainEvents, err := store.ListSessionEvents(
		context.Background(),
		fixture.sessionID,
	)
	if err != nil ||
		countRoomEvents(mainEvents, "/control/pause/coach-") != 1 {
		t.Fatalf("Coach pause events=%#v err=%v", mainEvents, err)
	}
	foundReason := false
	for _, event := range mainEvents {
		if event.Content == "pause_reason=coach_help" {
			foundReason = true
		}
	}
	if !foundReason {
		t.Fatal("explicit Coach pause lacks pause_reason=coach_help")
	}

	model.Resize(80, 24)
	if model.focus.Active() != focusCoach || !model.coachOverlay {
		t.Fatalf(
			"narrow Coach focus=%q overlay=%v",
			model.focus.Active(),
			model.coachOverlay,
		)
	}
	rendered, err = model.Render()
	if err != nil {
		t.Fatalf("Render narrow Coach: %v", err)
	}
	assertRoomGeometry(t, rendered, 80, 24)
	if !strings.Contains(rendered, "COACH") ||
		!strings.Contains(rendered, "先明确一个约束") {
		t.Fatalf("narrow Coach overlay missing:\n%s", rendered)
	}
	model.HandleKey("esc")
	if model.focus.Active() != focusComposer || model.coachOverlay {
		t.Fatalf(
			"closed Coach focus=%q overlay=%v",
			model.focus.Active(),
			model.coachOverlay,
		)
	}
	if model.DraftCursor() != 9 {
		t.Fatalf("closed overlay cursor=%d, want 9", model.DraftCursor())
	}
	rendered, err = model.Render()
	if err != nil {
		t.Fatalf("Render narrow main: %v", err)
	}
	assertRoomGeometry(t, rendered, 80, 24)
	if !strings.Contains(rendered, mainDraft) ||
		strings.Contains(rendered, "先明确一个约束") {
		t.Fatalf("narrow main leaked/lost content:\n%s", rendered)
	}

	for _, size := range []struct {
		width    int
		height   int
		required []string
		forbid   string
	}{
		{160, 48, []string{"ANSWER TRACE", "INTERVIEW ROOM", "COACH"}, ""},
		{120, 36, []string{"INTERVIEW ROOM", "COACH"}, "ANSWER TRACE"},
		{80, 24, []string{"INTERVIEW ROOM", "TRACE"}, "COACH · L3"},
	} {
		model.Resize(size.width, size.height)
		rendered, err = model.Render()
		if err != nil {
			t.Fatalf("Render %dx%d: %v", size.width, size.height, err)
		}
		assertRoomGeometry(t, rendered, size.width, size.height)
		for _, expected := range size.required {
			if !strings.Contains(rendered, expected) {
				t.Errorf("%dx%d missing %q", size.width, size.height, expected)
			}
		}
		if size.forbid != "" && strings.Contains(rendered, size.forbid) {
			t.Errorf("%dx%d contains %q", size.width, size.height, size.forbid)
		}
	}
}

func TestCoachThinkingKeepsMainInputAvailableAndIsolated(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	setRoomScenarioMode(t, store, fixture, contracts.ScenarioCoach)
	clock := newRoomClock(fixture.now)
	started := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	provider := &coachUIProvider{
		respondContext: func(
			_ context.Context,
			_ int,
			input corecoach.Input,
		) (contracts.CoachResponse, error) {
			if strings.Contains(input.UserRequest, "main draft") {
				t.Fatalf("Coach input leaked main draft: %#v", input)
			}
			close(started)
			<-release
			return coachUIResponse(input), nil
		},
	}
	model := newCoachUIModel(
		t,
		fixture,
		store,
		corecoach.NewService(
			store,
			provider,
			corecoach.Options{Now: clock.Now},
		),
		clock,
		120,
		36,
		false,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if err := model.UpdateDraft(
		context.Background(),
		"main draft before Coach",
	); err != nil {
		t.Fatalf("UpdateDraft before Coach: %v", err)
	}
	model.HandleKey("c")
	result := make(chan error, 1)
	go func() {
		result <- model.AskCoach(
			context.Background(),
			contracts.CoachGiveHint,
			false,
		)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("Coach Provider did not start")
	}
	rendered, err := model.Render()
	if err != nil || !strings.Contains(rendered, "coach: thinking") {
		t.Fatalf("Coach thinking screen err=%v:\n%s", err, rendered)
	}
	model.HandleKey("c")
	if err := model.UpdateDraft(
		context.Background(),
		"main draft while Coach thinking",
	); err != nil {
		t.Fatalf("main input blocked by Coach: %v", err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("AskCoach: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("AskCoach did not finish")
	}
	if model.Draft() != "main draft while Coach thinking" {
		t.Fatalf("main draft after Coach=%q", model.Draft())
	}
}

func TestCoachProviderErrorRetryAndQuotaRecovery(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	clock := newRoomClock(fixture.now)
	provider := &coachUIProvider{
		respond: func(
			call int,
			input corecoach.Input,
		) (contracts.CoachResponse, error) {
			if call == 1 {
				return contracts.CoachResponse{}, errors.New("offline")
			}
			return coachUIResponse(input), nil
		},
	}
	model := newCoachUIModel(
		t,
		fixture,
		store,
		corecoach.NewService(
			store,
			provider,
			corecoach.Options{Now: clock.Now},
		),
		clock,
		120,
		36,
		false,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	mainDraft := "Provider 失败也不能丢失的主回答。"
	if err := model.UpdateDraft(context.Background(), mainDraft); err != nil {
		t.Fatalf("UpdateDraft: %v", err)
	}
	model.HandleKey("c")
	err := model.AskCoach(
		context.Background(),
		contracts.CoachGiveHint,
		false,
	)
	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("Provider error=%v", err)
	}
	rendered, renderErr := model.Render()
	if renderErr != nil ||
		!strings.Contains(rendered, "Coach Provider 暂时不可用") ||
		!strings.Contains(rendered, "[t] 重试") {
		t.Fatalf("Provider recovery err=%v:\n%s", renderErr, rendered)
	}
	if model.Draft() != mainDraft {
		t.Fatalf("Provider failure changed main draft=%q", model.Draft())
	}
	if action := model.HandleKey("t"); action.Intent != IntentCoachRetry {
		t.Fatalf("Coach retry action=%#v", action)
	}
	if err := model.RetryCoach(context.Background()); err != nil {
		t.Fatalf("RetryCoach: %v", err)
	}
	if provider.Calls() != 2 {
		t.Fatalf("Provider calls=%d", provider.Calls())
	}

	err = model.AskCoach(
		context.Background(),
		contracts.CoachExplainConcept,
		false,
	)
	if !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("quota error=%v", err)
	}
	rendered, renderErr = model.Render()
	if renderErr != nil ||
		!strings.Contains(rendered, "额度已用完") ||
		!strings.Contains(rendered, "继续独立作答") {
		t.Fatalf("quota recovery err=%v:\n%s", renderErr, rendered)
	}
	if model.Draft() != mainDraft || provider.Calls() != 2 {
		t.Fatalf(
			"quota failure draft=%q calls=%d",
			model.Draft(),
			provider.Calls(),
		)
	}
}

func TestCoachKeyboardOverlayAndValidationPaths(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	clock := newRoomClock(fixture.now)
	service := corecoach.NewService(
		store,
		&coachUIProvider{
			respond: func(
				_ int,
				input corecoach.Input,
			) (contracts.CoachResponse, error) {
				return coachUIResponse(input), nil
			},
		},
		corecoach.Options{Now: clock.Now},
	)
	model := newCoachUIModel(
		t,
		fixture,
		store,
		service,
		clock,
		80,
		24,
		true,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if state := model.CoachState(); state.Phase != "succeeded" {
		t.Fatalf("CoachState=%#v", state)
	}
	model.HandleKey("c")
	if !model.coachOverlay || model.focus.Active() != focusCoach {
		t.Fatalf(
			"open overlay=%v focus=%q",
			model.coachOverlay,
			model.focus.Active(),
		)
	}
	intents := coachShortcutOrder()
	for index, expected := range intents {
		action := model.HandleKey(string(rune('1' + index)))
		if action.Intent != IntentCoachAsk ||
			action.CoachIntent != expected {
			t.Errorf("shortcut %d action=%#v", index+1, action)
		}
	}
	model.HandleKey("tab")
	model.HandleKey("shift+tab")
	if model.focus.Active() != focusCoach {
		t.Fatalf("narrow overlay did not trap focus")
	}
	if err := model.UpdateCoachDraft("自由输入问题"); err != nil {
		t.Fatalf("UpdateCoachDraft: %v", err)
	}
	if err := model.SetDraftCursor(-1); !domainerr.IsCode(
		err,
		domainerr.CodeValidation,
	) {
		t.Fatalf("invalid main cursor error=%v", err)
	}
	if model.CoachDraft() != "自由输入问题" {
		t.Fatalf("CoachDraft=%q", model.CoachDraft())
	}
	if action := model.HandleKey("ctrl+enter"); action.Intent !=
		IntentCoachAsk {
		t.Fatalf("free ask action=%#v", action)
	}
	if action := model.HandleKey("ctrl+p"); action.Intent !=
		IntentCoachAskPaused {
		t.Fatalf("pause ask action=%#v", action)
	}
	model.HandleKey("?")
	if !model.helpOpen || model.coachOverlay ||
		model.focus.Active() != focusHelp {
		t.Fatalf(
			"Coach help open=%v overlay=%v focus=%q",
			model.helpOpen,
			model.coachOverlay,
			model.focus.Active(),
		)
	}
	model.HandleKey("esc")
	if model.helpOpen || model.focus.Active() != focusComposer {
		t.Fatalf(
			"Coach help close=%v focus=%q",
			model.helpOpen,
			model.focus.Active(),
		)
	}
	model.HandleKey("c")
	model.Resize(120, 36)
	if model.coachOverlay || model.focus.Active() != focusCoach {
		t.Fatalf(
			"resize overlay=%v focus=%q",
			model.coachOverlay,
			model.focus.Active(),
		)
	}
	model.HandleKey("up")
	model.HandleKey("down")
	model.HandleKey("shift+tab")
	if model.focus.Active() != focusComposer {
		t.Fatalf("split Shift+Tab focus=%q", model.focus.Active())
	}

	if err := model.AskCoach(
		context.Background(),
		contracts.CoachIntent("invalid"),
		false,
	); !domainerr.IsCode(err, domainerr.CodeValidation) {
		t.Fatalf("invalid Coach intent error=%v", err)
	}
	if err := model.RetryCoach(
		context.Background(),
	); !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("empty RetryCoach error=%v", err)
	}
	if err := model.MarkCoachOutcome(
		context.Background(),
		corecoach.OutcomeReview,
	); !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("empty MarkCoachOutcome error=%v", err)
	}

	unavailable := newCoachUIModel(
		t,
		fixture,
		store,
		nil,
		clock,
		120,
		36,
		false,
	)
	if err := unavailable.Load(context.Background(), nil); err != nil {
		t.Fatalf("unavailable main Load: %v", err)
	}
	if state := unavailable.CoachState(); state.Phase != "failed" ||
		!domainerr.IsCode(
			state.Err,
			domainerr.CodeDependencyUnavailable,
		) {
		t.Fatalf("unavailable CoachState=%#v", state)
	}
	if err := unavailable.AskCoach(
		context.Background(),
		contracts.CoachGiveHint,
		false,
	); !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("unavailable AskCoach error=%v", err)
	}
}

func TestCoachCompletedSessionAllowsL4Review(t *testing.T) {
	t.Parallel()

	store := openRoomStore(t)
	fixture := seedRoomSession(t, store)
	setRoomScenarioMode(t, store, fixture, contracts.ScenarioCoach)
	clock := newRoomClock(fixture.now)
	updated, err := store.UpdateSessionStatus(
		context.Background(),
		fixture.sessionID,
		db.SessionEvaluationPending,
		clock.Now(),
	)
	if err != nil || !updated {
		t.Fatalf("UpdateSessionStatus=%v err=%v", updated, err)
	}
	provider := &coachUIProvider{
		respond: func(
			_ int,
			input corecoach.Input,
		) (contracts.CoachResponse, error) {
			if input.QuestionState != corecoach.SessionClosed ||
				input.AllowedMaxLevel != contracts.HelpL4 {
				t.Fatalf("closed Coach input=%#v", input)
			}
			return coachUIResponse(input), nil
		},
	}
	model := newCoachUIModel(
		t,
		fixture,
		store,
		corecoach.NewService(
			store,
			provider,
			corecoach.Options{Now: clock.Now},
		),
		clock,
		120,
		36,
		false,
	)
	if err := model.Load(context.Background(), nil); err != nil {
		t.Fatalf("Load completed session: %v", err)
	}
	model.HandleKey("c")
	if err := model.AskCoach(
		context.Background(),
		contracts.CoachExplainConcept,
		false,
	); err != nil {
		t.Fatalf("completed L4 AskCoach: %v", err)
	}
	rendered, err := model.Render()
	if err != nil ||
		!strings.Contains(rendered, "max L4") ||
		!strings.Contains(rendered, "COACH · L4") {
		t.Fatalf("completed L4 screen err=%v:\n%s", err, rendered)
	}
}

func newCoachUIModel(
	t *testing.T,
	fixture roomFixture,
	store *db.Store,
	coach CoachRoom,
	clock *roomClock,
	width int,
	height int,
	ascii bool,
) *Model {
	t.Helper()
	model, err := New(Options{
		SessionID: fixture.sessionID,
		Room: coreinterview.NewService(
			store,
			nil,
			coreinterview.Options{Now: clock.Now},
		),
		Coach:            coach,
		Now:              clock.Now,
		NextSubmission:   sequenceID("answer"),
		NextOperationID:  sequenceID("control"),
		NextCoachRequest: sequenceID("coach"),
		Width:            width,
		Height:           height,
		Theme:            noColorRoomTheme(t, ascii),
	})
	if err != nil {
		t.Fatalf("New Coach UI model: %v", err)
	}
	return model
}

func setRoomScenarioMode(
	t *testing.T,
	store *db.Store,
	fixture roomFixture,
	mode contracts.ScenarioMode,
) {
	t.Helper()
	scenario, found, err := store.GetScenario(
		context.Background(),
		"scenario-room",
	)
	if err != nil || !found {
		t.Fatalf("GetScenario found=%v err=%v", found, err)
	}
	scenario.Mode = mode
	if err := store.SaveScenario(
		context.Background(),
		"scenario-room",
		"profile-room",
		scenario,
		fixture.now,
	); err != nil {
		t.Fatalf("SaveScenario mode: %v", err)
	}
}

type coachUIProvider struct {
	mu             sync.Mutex
	calls          int
	respond        func(int, corecoach.Input) (contracts.CoachResponse, error)
	respondContext func(
		context.Context,
		int,
		corecoach.Input,
	) (contracts.CoachResponse, error)
}

func (provider *coachUIProvider) Respond(
	ctx context.Context,
	input corecoach.Input,
) (contracts.CoachResponse, error) {
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

func (provider *coachUIProvider) Calls() int {
	provider.mu.Lock()
	defer provider.mu.Unlock()
	return provider.calls
}

func coachUIResponse(input corecoach.Input) contracts.CoachResponse {
	return contracts.CoachResponse{
		Intent:            input.Intent,
		HelpLevel:         input.AllowedMaxLevel,
		KnowledgeTags:     []string{"cache consistency"},
		RecommendedAction: "先明确一个约束，再说明你的判断依据。",
		PolicyNote:        "保持练习边界。",
	}
}
