package interview

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

const eventRoot = "ic/interview/"

var operationIDPattern = regexp.MustCompile(
	`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`,
)

type ownedEvent struct {
	kind  string
	parts []string
}

func questionEventID(sessionID, questionID string) string {
	return eventPrefix(sessionID) + "question/" + questionID
}

func answerEventID(sessionID, submissionID string) string {
	return eventPrefix(sessionID) + "answer/" + submissionID
}

func actionEventID(
	sessionID string,
	submissionID string,
	action contracts.InterviewerActionType,
) string {
	return eventPrefix(sessionID) +
		"action/" + submissionID + "/" + string(action)
}

func controlEventID(
	sessionID string,
	action string,
	scope EndScope,
	operationID string,
) string {
	parts := []string{eventPrefix(sessionID) + "control", action}
	if scope != "" {
		parts = append(parts, string(scope))
	}
	parts = append(parts, operationID)
	return strings.Join(parts, "/")
}

func eventPrefix(sessionID string) string {
	return eventRoot + sessionID + "/"
}

func parseOwnedEvent(sessionID, eventID string) (ownedEvent, bool) {
	value, found := strings.CutPrefix(eventID, eventPrefix(sessionID))
	if !found {
		return ownedEvent{}, false
	}
	parts := strings.Split(value, "/")
	if len(parts) == 0 {
		return ownedEvent{}, false
	}
	return ownedEvent{kind: parts[0], parts: parts[1:]}, true
}

func constraintEvidenceID(questionID string) contracts.EvidenceID {
	return contracts.EvidenceID("constraint:" + questionID)
}

func questionEvidence(
	question contracts.ScenarioQuestion,
) []contracts.EvidenceID {
	result := slices.Clone(question.EvidenceIDs)
	constraint := constraintEvidenceID(question.ID)
	if !slices.Contains(result, constraint) {
		result = append(result, constraint)
	}
	return result
}

func validateOperationID(operationID string) error {
	if !operationIDPattern.MatchString(strings.TrimSpace(operationID)) {
		return interviewError(
			domainerr.CodeValidation,
			"操作 ID 无效。",
			"使用 1–64 位字母、数字、连字符或下划线。",
			false,
		)
	}
	return nil
}

func findEvent(
	events []db.SessionEvent,
	eventID string,
) (db.SessionEvent, bool) {
	for _, event := range events {
		if event.EventID == eventID {
			return event, true
		}
	}
	return db.SessionEvent{}, false
}

func findAnswer(
	events []db.SessionEvent,
	sessionID string,
	submissionID string,
) (db.SessionEvent, bool) {
	return findEvent(events, answerEventID(sessionID, submissionID))
}

func findAction(
	events []db.SessionEvent,
	sessionID string,
	submissionID string,
) (db.SessionEvent, contracts.InterviewerActionType, bool, error) {
	var (
		foundEvent db.SessionEvent
		foundType  contracts.InterviewerActionType
		count      int
	)
	for _, event := range events {
		owned, ok := parseOwnedEvent(sessionID, event.EventID)
		if !ok || owned.kind != "action" || len(owned.parts) != 2 ||
			owned.parts[0] != submissionID {
			continue
		}
		count++
		foundEvent = event
		foundType = contracts.InterviewerActionType(owned.parts[1])
	}
	if count > 1 {
		return db.SessionEvent{}, "", false, interviewError(
			domainerr.CodeInvalidState,
			"同一次回答存在多个面试官动作。",
			"停止当前会话并检查事件日志。",
			false,
		)
	}
	return foundEvent, foundType, count == 1, nil
}

func actionFromEvent(
	event db.SessionEvent,
	actionType contracts.InterviewerActionType,
) (contracts.InterviewerAction, error) {
	state := contracts.SessionInterviewing
	switch actionType {
	case contracts.ActionCloseQuestion:
		state = contracts.SessionQuestionComplete
	case contracts.ActionFinishSession:
		state = contracts.SessionComplete
	case contracts.ActionNextQuestion, contracts.ActionFollowUp:
	default:
		return contracts.InterviewerAction{}, interviewError(
			domainerr.CodeInvalidState,
			fmt.Sprintf("未知的已持久化面试官动作 %q。", actionType),
			"停止当前会话并检查事件日志。",
			false,
		)
	}
	action := contracts.InterviewerAction{
		Action:       actionType,
		QuestionID:   event.QuestionID,
		Message:      event.Content,
		EvidenceIDs:  slices.Clone(event.EvidenceRefs),
		SessionState: state,
	}
	if err := action.Validate(); err != nil {
		return contracts.InterviewerAction{}, interviewFailure(err)
	}
	return action, nil
}
