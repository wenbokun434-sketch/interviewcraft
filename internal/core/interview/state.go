package interview

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreprofile "github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

type loadedState struct {
	snapshot Snapshot
	profile  coreprofile.Aggregate
}

func (service *Service) loadInternal(
	ctx context.Context,
	sessionID string,
) (loadedState, error) {
	if strings.TrimSpace(sessionID) == "" {
		return loadedState{}, interviewError(
			domainerr.CodeValidation,
			"会话 ID 不能为空。",
			"重新打开训练会话。",
			false,
		)
	}
	if service == nil || service.repository == nil {
		return loadedState{}, interviewError(
			domainerr.CodeDependencyUnavailable,
			"会话存储不可用。",
			"重新启动 InterviewCraft 后重试。",
			true,
		)
	}
	session, found, err := service.repository.GetSession(ctx, sessionID)
	if err != nil {
		return loadedState{}, interviewFailure(err)
	}
	if !found {
		return loadedState{}, interviewError(
			domainerr.CodeInvalidState,
			"找不到要恢复的训练会话。",
			"返回训练主页选择仍存在的会话。",
			false,
		)
	}
	scenario, found, err := service.repository.GetScenario(
		ctx,
		session.ScenarioID,
	)
	if err != nil {
		return loadedState{}, interviewFailure(err)
	}
	if !found {
		return loadedState{}, interviewError(
			domainerr.CodeInvalidState,
			"会话关联的场景版本不存在。",
			"返回训练主页并创建新场景。",
			false,
		)
	}
	profile, found, err := service.repository.GetSessionProfile(ctx, sessionID)
	if err != nil {
		return loadedState{}, interviewFailure(err)
	}
	if !found || profile.ConfirmedAt == nil ||
		profile.ConfirmedAt.IsZero() {
		return loadedState{}, interviewError(
			domainerr.CodeInvalidState,
			"会话关联的确认画像不存在。",
			"返回画像页重新确认后创建新场景。",
			false,
		)
	}
	events, err := service.repository.ListSessionEvents(ctx, sessionID)
	if err != nil {
		return loadedState{}, interviewFailure(err)
	}
	snapshot, err := deriveSnapshot(session, scenario, events)
	if err != nil {
		return loadedState{}, err
	}
	if snapshot.CurrentQuestion != nil &&
		snapshot.Phase != PhaseCompleted {
		draft, found, loadErr := service.repository.LoadDraft(
			ctx,
			sessionID,
			snapshot.CurrentQuestion.ID,
			db.DraftAnswer,
		)
		if loadErr != nil {
			return loadedState{}, interviewFailure(loadErr)
		}
		if found {
			value := draft
			snapshot.Draft = &value
		}
	}
	return loadedState{
		snapshot: cloneSnapshot(snapshot),
		profile:  cloneProfile(profile),
	}, nil
}

func deriveSnapshot(
	session db.Session,
	scenario contracts.Scenario,
	events []db.SessionEvent,
) (Snapshot, error) {
	snapshot := Snapshot{
		Session:      session,
		Scenario:     cloneScenario(scenario),
		Events:       cloneEvents(events),
		CurrentIndex: 0,
		Phase:        PhaseNotStarted,
	}
	if len(scenario.Questions) == 0 {
		return snapshot, interviewError(
			domainerr.CodeInvalidState,
			"当前场景没有可用题目。",
			"返回场景工厂生成至少一道题。",
			false,
		)
	}
	started := false
	paused := false
	questionClosed := false
	var pending *PendingEnd
	for _, event := range events {
		owned, ok := parseOwnedEvent(session.ID, event.EventID)
		if !ok {
			continue
		}
		switch owned.kind {
		case "question":
			if len(owned.parts) != 1 {
				continue
			}
			index := questionIndex(scenario, owned.parts[0])
			if index < 0 {
				return Snapshot{}, corruptedInterviewEvent(
					event,
					"题目事件引用了场景之外的题目",
				)
			}
			started = true
			questionClosed = false
			snapshot.CurrentIndex = index
			snapshot.FollowUpCount = 0
		case "action":
			if len(owned.parts) != 2 {
				continue
			}
			action := contracts.InterviewerActionType(owned.parts[1])
			switch action {
			case contracts.ActionFollowUp:
				if event.QuestionID == currentQuestionID(
					scenario,
					snapshot.CurrentIndex,
				) {
					snapshot.FollowUpCount++
				}
			case contracts.ActionNextQuestion:
				index := questionIndex(scenario, event.QuestionID)
				if index < 0 {
					return Snapshot{}, corruptedInterviewEvent(
						event,
						"下一题动作引用了场景之外的题目",
					)
				}
				started = true
				questionClosed = false
				snapshot.CurrentIndex = index
				snapshot.FollowUpCount = 0
			case contracts.ActionCloseQuestion:
				questionClosed = true
			case contracts.ActionFinishSession:
				questionClosed = true
			}
		case "control":
			if len(owned.parts) < 2 {
				continue
			}
			switch owned.parts[0] {
			case "pause":
				paused = true
			case "resume":
				paused = false
			case "end-request":
				if len(owned.parts) == 3 {
					pending = &PendingEnd{
						Scope:       EndScope(owned.parts[1]),
						OperationID: owned.parts[2],
					}
				}
			case "end-cancel", "end-confirm":
				if len(owned.parts) == 3 &&
					pending != nil &&
					pending.Scope == EndScope(owned.parts[1]) &&
					pending.OperationID == owned.parts[2] {
					pending = nil
				}
			}
		}
	}
	snapshot.PendingEnd = pending
	if snapshot.CurrentIndex >= 0 &&
		snapshot.CurrentIndex < len(scenario.Questions) {
		question := cloneQuestion(scenario.Questions[snapshot.CurrentIndex])
		snapshot.CurrentQuestion = &question
	}
	switch {
	case session.Status != db.SessionActive:
		snapshot.Phase = PhaseCompleted
		snapshot.PendingEnd = nil
	case pending != nil:
		snapshot.Phase = PhaseAwaitingEndConfirmation
	case paused:
		snapshot.Phase = PhasePaused
	case !started:
		snapshot.Phase = PhaseNotStarted
	case questionClosed:
		snapshot.Phase = PhaseQuestionComplete
	default:
		snapshot.Phase = PhaseAwaitingAnswer
	}
	return snapshot, nil
}

func questionIndex(
	scenario contracts.Scenario,
	questionID string,
) int {
	return slices.IndexFunc(
		scenario.Questions,
		func(question contracts.ScenarioQuestion) bool {
			return question.ID == questionID
		},
	)
}

func currentQuestionID(
	scenario contracts.Scenario,
	index int,
) string {
	if index < 0 || index >= len(scenario.Questions) {
		return ""
	}
	return scenario.Questions[index].ID
}

func corruptedInterviewEvent(
	event db.SessionEvent,
	reason string,
) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeInvalidState,
		"restore interview state",
		"session event stream",
		"无法恢复文字面试状态。",
		"停止当前会话并检查事件日志。",
		false,
		fmt.Errorf("%s: event %s", reason, event.EventID),
	)
}

func cloneSnapshot(value Snapshot) Snapshot {
	value.Scenario = cloneScenario(value.Scenario)
	value.Events = cloneEvents(value.Events)
	if value.CurrentQuestion != nil {
		question := cloneQuestion(*value.CurrentQuestion)
		value.CurrentQuestion = &question
	}
	if value.PendingEnd != nil {
		pending := *value.PendingEnd
		value.PendingEnd = &pending
	}
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	return value
}

func cloneScenario(value contracts.Scenario) contracts.Scenario {
	questions := value.Questions
	value.Questions = make([]contracts.ScenarioQuestion, len(questions))
	for index, question := range questions {
		value.Questions[index] = cloneQuestion(question)
	}
	return value
}

func cloneQuestion(
	value contracts.ScenarioQuestion,
) contracts.ScenarioQuestion {
	value.Rubric = slices.Clone(value.Rubric)
	value.EvidenceIDs = slices.Clone(value.EvidenceIDs)
	return value
}

func cloneEvents(values []db.SessionEvent) []db.SessionEvent {
	result := make([]db.SessionEvent, len(values))
	for index, value := range values {
		value.EvidenceRefs = slices.Clone(value.EvidenceRefs)
		result[index] = value
	}
	return result
}

func cloneProfile(value coreprofile.Aggregate) coreprofile.Aggregate {
	value.Candidate.Facts = slices.Clone(value.Candidate.Facts)
	value.Candidate.Inferences = slices.Clone(value.Candidate.Inferences)
	value.Candidate.Projects = slices.Clone(value.Candidate.Projects)
	value.Candidate.Skills = slices.Clone(value.Candidate.Skills)
	value.Metadata.LockedFactIDs = slices.Clone(value.Metadata.LockedFactIDs)
	value.Metadata.LockedInferenceIDs = slices.Clone(
		value.Metadata.LockedInferenceIDs,
	)
	if value.ConfirmedAt != nil {
		confirmedAt := *value.ConfirmedAt
		value.ConfirmedAt = &confirmedAt
	}
	return value
}
