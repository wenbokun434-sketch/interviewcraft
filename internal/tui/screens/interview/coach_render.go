package interview

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
)

func (model *Model) coachLinesLocked(
	width int,
	height int,
) ([]string, error) {
	events := model.currentQuestionCoachEventsLocked()
	entries := make([]components.CoachEntry, len(events))
	for index, event := range events {
		entries[index] = components.CoachEntry{
			ID:         event.ID,
			At:         event.OccurredAt,
			Level:      event.HelpLevel,
			Tags:       append([]string(nil), event.Tags...),
			Content:    event.Content,
			PolicyNote: event.PolicyNote,
			Outcome:    event.Outcome,
		}
	}
	shortcuts := coachShortcutOrder()
	available := model.coachQuestionIDLocked() != "" &&
		!coachBusy(model.coachOperation) &&
		(model.coachUsage.Unlimited ||
			model.coachUsage.Limit == 0 ||
			model.coachUsage.Used < model.coachUsage.Limit)
	items := make([]components.CoachShortcut, len(shortcuts))
	unavailableReason := ""
	if !available {
		unavailableReason = "当前不可用"
		if !model.coachUsage.Unlimited &&
			model.coachUsage.Limit > 0 &&
			model.coachUsage.Used >= model.coachUsage.Limit {
			unavailableReason = "额度已用完"
		}
	}
	for index, shortcut := range shortcuts {
		items[index] = components.CoachShortcut{
			Key:     fmt.Sprintf("%d", index+1),
			Label:   coachShortcutLabels()[shortcut],
			Enabled: available,
			Reason:  unavailableReason,
		}
	}
	pausedForCoach := model.snapshot.Phase == coreinterview.PhasePaused &&
		len(events) > 0 &&
		events[len(events)-1].PausedTimer
	return (components.CoachPane{
		Meter: components.HintMeter{
			Mode:      model.coachPolicy.Mode,
			Used:      model.coachUsage.Used,
			Limit:     model.coachUsage.Limit,
			Unlimited: model.coachUsage.Unlimited,
			MaxLevel:  model.coachPolicy.MaxLevel,
		},
		Shortcuts:   items,
		History:     entries,
		Draft:       model.coachDraft,
		Focused:     model.focus.Active() == focusCoach,
		PauseOnAsk:  pausedForCoach,
		Operation:   cloneCoachState(model.coachOperation),
		Selected:    model.coachSelected,
		CurrentTime: model.currentTime,
	}).Render(model.Theme, width, height)
}

func (model *Model) coachStatusLocked() string {
	level := strings.TrimSpace(string(model.coachPolicy.MaxLevel))
	if level == "" {
		level = "—"
		if model.Theme.UseASCII {
			level = "-"
		}
	}
	if model.coachUsage.Unlimited {
		return "∞ · max " + level
	}
	return fmt.Sprintf(
		"%d/%d · max %s",
		model.coachUsage.Used,
		max(1, model.coachUsage.Limit),
		level,
	)
}

func (model *Model) coachCommandsLocked() []components.KeyHint {
	if coachBusy(model.coachOperation) {
		return []components.KeyHint{
			{Key: "c/Esc", Action: "返回主回答", Enabled: true},
			{Key: "?", Action: "快捷键", Enabled: true},
		}
	}
	if model.coachOperation.Phase == async.Failed {
		return []components.KeyHint{
			{
				Key:     "t",
				Action:  "重试",
				Enabled: model.canRetryCoachLocked(),
			},
			{Key: "1-6", Action: "换一种问法", Enabled: true},
			{Key: "c/Esc", Action: "独立作答", Enabled: true},
		}
	}
	commands := []components.KeyHint{
		{Key: "1-6", Action: "快捷提问", Enabled: true},
		{
			Key:     "Ctrl+Enter",
			Action:  "自由提问",
			Enabled: strings.TrimSpace(model.coachDraft) != "",
		},
		{
			Key:     "Ctrl+P",
			Action:  "暂停并求教",
			Enabled: strings.TrimSpace(model.coachDraft) != "",
		},
	}
	if _, found := model.selectedCoachEventLocked(); found {
		commands = append(commands,
			components.KeyHint{
				Key: "u/d/r", Action: "标记学习状态", Enabled: true,
			},
		)
	}
	commands = append(commands,
		components.KeyHint{Key: "c/Esc", Action: "返回回答", Enabled: true},
	)
	return commands
}

func coachShortcutOrder() []contracts.CoachIntent {
	return []contracts.CoachIntent{
		contracts.CoachExplainConcept,
		contracts.CoachGiveHint,
		contracts.CoachAnswerStructure,
		contracts.CoachCheckReasoning,
		contracts.CoachExplainFailure,
		contracts.CoachAddToReview,
	}
}
