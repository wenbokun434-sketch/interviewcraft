package interview

import (
	"fmt"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	coreinterview "github.com/interviewcraft/interviewcraft/internal/core/interview"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// Render draws the P-04 room at the current terminal size.
func (model *Model) Render() (string, error) {
	if model == nil {
		return "", fmt.Errorf("interview model is nil")
	}
	model.mu.RLock()
	defer model.mu.RUnlock()
	if err := model.operation.Validate(); err != nil {
		return "", err
	}
	plan := layout.Calculate(model.Width, model.Height)
	if err := plan.Validate(); err != nil {
		return "", err
	}

	mainWidth := max(1, plan.MainWidth-2)
	sessionWidth := max(1, plan.CoachWidth-2)
	traceWidth := max(1, plan.TraceWidth-2)
	if plan.Mode == layout.Narrow || plan.Mode == layout.Blocked {
		mainWidth = max(1, model.Width-2)
	}
	mainLines, err := model.mainLinesLocked(
		mainWidth,
		max(1, plan.ContentHeight-2),
		plan.Mode,
	)
	if err != nil {
		return "", err
	}
	traceItems := model.traceItemsLocked()
	traceLines := (components.AnswerTrace{
		Items:    traceItems,
		Selected: model.traceSelected,
		Focused:  model.focus.Active() == focusTrace,
	}).Render(
		model.Theme,
		traceWidth,
		max(1, plan.ContentHeight-2),
	)
	sessionLines := model.sessionLinesLocked(sessionWidth)
	timer := model.timerLocked()

	tracePane := components.Pane{
		Title:  "Answer trace",
		Status: fmt.Sprintf("%d events", len(traceItems)),
		State:  paneState(model.focus.Active() == focusTrace),
		Lines:  traceLines,
	}
	mainPane := components.Pane{
		Title:  "Interview room",
		Status: model.mainStatusLocked(timer),
		State:  paneState(model.focus.Active() == focusComposer),
		Lines:  mainLines,
	}
	sessionPane := components.Pane{
		Title:  "Session",
		Status: sessionStatus(model.snapshot.Phase),
		State:  paneState(model.focus.Active() == focusSession),
		Lines:  sessionLines,
	}
	var overlay *components.Pane
	if model.helpOpen {
		help := components.Pane{
			Title: "快捷键",
			State: components.PaneOverlay,
			Lines: model.helpLinesLocked(),
		}
		if plan.Mode == layout.Narrow {
			overlay = &help
		} else {
			mainPane = help
			tracePane.State = components.PaneInactive
			sessionPane.State = components.PaneInactive
		}
	}
	shell := components.AppShell{
		Width:           model.Width,
		Height:          model.Height,
		Screen:          "INTERVIEW",
		Provider:        model.badgeLocked(),
		ActivitySummary: model.activitySummaryLocked(traceItems),
		Trace:           tracePane,
		Main:            mainPane,
		Coach:           sessionPane,
		Overlay:         overlay,
		Commands:        model.commandsLocked(),
		Theme:           model.Theme,
	}
	return shell.Render()
}

func (model *Model) mainLinesLocked(
	width int,
	height int,
	mode layout.Mode,
) ([]string, error) {
	if model.snapshot.CurrentQuestion == nil {
		lines := []string{
			components.SectionLabel{
				Text: "Current question",
				Kind: components.LabelInfo,
			}.Render(model.Theme),
			model.Theme.Paint(
				theme.Muted,
				"-- 当前会话没有可用题目 --",
			),
		}
		stateLines, err := model.operationLinesLocked(width)
		if err != nil {
			return nil, err
		}
		lines = append(lines, stateLines...)
		if model.snapshot.Phase == coreinterview.PhaseCompleted {
			lines = append(lines, model.Theme.Paint(
				theme.Success,
				model.Theme.Glyphs.Success+
					" 会话已完成，等待生成评估报告。",
			))
		} else {
			lines = append(lines,
				"返回训练主页选择会话，或检查场景是否包含题目。",
			)
		}
		return fitScreenLines(lines, height), nil
	}

	question := model.snapshot.CurrentQuestion
	evidence := make([]string, len(question.EvidenceIDs))
	for index, id := range question.EvidenceIDs {
		evidence[index] = string(id)
	}
	questionHeight := 6
	if mode == layout.Narrow {
		questionHeight = 5
	}
	cardLines := (components.QuestionCard{
		Index:        model.snapshot.CurrentIndex + 1,
		Total:        len(model.snapshot.Scenario.Questions),
		Prompt:       question.Prompt,
		Intent:       question.Intent,
		Evidence:     evidence,
		Generic:      question.Generic,
		FollowUps:    model.snapshot.FollowUpCount,
		MaxFollowUps: question.MaxFollowUps,
		EndCondition: question.EndCondition,
		Estimated: time.Duration(
			question.EstimatedSeconds,
		) * time.Second,
		State: questionCardState(model.snapshot.Phase),
	}).Render(model.Theme, width, questionHeight)

	lines := append([]string(nil), cardLines...)
	if mode == layout.Narrow {
		summary := (components.AnswerTrace{
			Items:     model.traceItemsLocked(),
			Collapsed: true,
		}).Render(model.Theme, width, 1)
		if len(summary) > 0 {
			summary[0] = layout.TruncateRight(
				"TRACE · "+strings.TrimSpace(summary[0]),
				width,
				model.Theme.UseASCII,
			)
		}
		lines = append(lines, summary...)
	}
	lines = append(lines, components.SectionLabel{
		Text: "Transcript",
		Kind: components.LabelDefault,
	}.Render(model.Theme))

	stateLines, err := model.operationLinesLocked(width)
	if err != nil {
		return nil, err
	}
	confirmLines := model.confirmLinesLocked(width)
	composerHeight := 6
	if mode == layout.Narrow {
		composerHeight = 5
	}
	reserved := len(lines) + len(stateLines) + len(confirmLines) +
		1 + composerHeight
	transcriptHeight := max(2, height-reserved)
	transcript := model.transcriptLinesLocked(width, transcriptHeight)
	lines = append(lines, transcript...)
	lines = append(lines, stateLines...)
	lines = append(lines, confirmLines...)
	lines = append(lines, components.SectionLabel{
		Text: "Your answer",
		Kind: components.LabelInfo,
	}.Render(model.Theme))

	composer := components.TextComposer{
		Text:    model.draft,
		State:   model.composerStateLocked(),
		Focused: model.focus.Active() == focusComposer,
	}
	if composer.State == components.ComposerDisabled {
		composer.DisabledReason = model.composerDisabledReasonLocked()
	}
	if composer.State == components.ComposerValidationErr {
		composer.ValidationErr = model.operation.Err
	}
	composerLines, err := composer.Render(
		model.Theme,
		width,
		max(2, min(composerHeight, height-len(lines))),
	)
	if err != nil {
		return nil, err
	}
	lines = append(lines, composerLines...)
	return fitScreenLines(lines, height), nil
}

func (model *Model) operationLinesLocked(
	width int,
) ([]string, error) {
	switch model.operation.Phase {
	case async.Pending:
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在可靠保存回答",
		}).Render(model.Theme, width)
		return []string{line}, err
	case async.Streaming:
		message := "interviewer: ▌"
		if model.operation.Value != nil &&
			strings.TrimSpace(model.operation.Value.Message) != "" {
			message = model.operation.Value.Message
		}
		line, err := (components.ActivityLine{
			State: async.NewStreaming(&message),
			Label: message,
		}).Render(model.Theme, width)
		return []string{line}, err
	case async.Failed:
		notice := components.ErrorNotice(model.operation.Err, nil)
		lines, err := notice.Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		if model.snapshot.CurrentQuestion == nil {
			lines = append(
				lines,
				model.Theme.Paint(theme.Focus, "[h] 返回训练主页"),
			)
			return lines, nil
		}
		action := "[x] 结束本题"
		if model.canRetryLocked() {
			action = "[t] 重试 · " + action
		}
		lines = append(lines, model.Theme.Paint(theme.Focus, action))
		return lines, nil
	case async.Succeeded:
		if model.operation.Value == nil {
			return nil, fmt.Errorf("succeeded interview state has no value")
		}
		switch model.operation.Value.Stage {
		case StageComplete:
			return []string{model.Theme.Paint(
				theme.Success,
				model.Theme.Glyphs.Success+
					" 会话完成，已进入待评估状态",
			)}, nil
		case StagePaused:
			return []string{model.Theme.Paint(
				theme.Warning,
				"paused · 按 [p] 恢复面试",
			)}, nil
		case StageEnding:
			return nil, nil
		default:
			return []string{model.Theme.Paint(
				theme.Muted,
				model.operation.Value.Message,
			)}, nil
		}
	default:
		return nil, fmt.Errorf(
			"unsupported Interview operation phase %q",
			model.operation.Phase,
		)
	}
}

func (model *Model) transcriptLinesLocked(
	width int,
	height int,
) []string {
	questionID := currentQuestionID(model.snapshot)
	lines := make([]string, 0)
	for _, event := range model.snapshot.Events {
		if event.QuestionID != questionID ||
			(event.Speaker != db.SpeakerInterviewer &&
				event.Speaker != db.SpeakerUser) {
			continue
		}
		label := "YOU"
		role := theme.Primary
		if event.Speaker == db.SpeakerInterviewer {
			label = "INTERVIEWER"
			role = theme.Info
		}
		prefix := fmt.Sprintf(
			"> %s %s · ",
			label,
			event.OccurredAt.Local().Format("15:04"),
		)
		content := strings.Join(strings.Fields(event.Content), " ")
		line := prefix + content
		lines = append(lines, model.Theme.Paint(
			role,
			layout.TruncateRight(line, width, model.Theme.UseASCII),
		))
	}
	if len(lines) == 0 {
		lines = append(lines, model.Theme.Paint(
			theme.Muted,
			"-- 等待当前题目的第一条事件 --",
		))
	}
	if len(lines) > height {
		lines = lines[len(lines)-height:]
	}
	return fitScreenLines(lines, height)
}

func (model *Model) confirmLinesLocked(width int) []string {
	pending := model.snapshot.PendingEnd
	if pending == nil {
		return nil
	}
	message := "确认结束本题？"
	if pending.Scope == coreinterview.EndSession {
		message = "确认结束整场面试？"
	}
	line := (components.ConfirmPrompt{
		Message: message,
		Confirm: components.KeyHint{
			Key:    "y",
			Action: "确认",
		},
		Cancel: components.KeyHint{
			Key:    "n/Esc",
			Action: "继续作答",
		},
	}).Render(model.Theme, width)
	return []string{line}
}

func (model *Model) sessionLinesLocked(width int) []string {
	timer := model.timerLocked()
	lines := []string{
		components.SectionLabel{
			Text: "Question timer",
			Kind: components.LabelInfo,
		}.Render(model.Theme),
		timer.Render(model.Theme),
		"state: " + sessionStatus(model.snapshot.Phase),
		"",
		components.SectionLabel{
			Text: "Scenario",
			Kind: components.LabelDefault,
		}.Render(model.Theme),
		"template: " + oneLine(model.snapshot.Scenario.Template),
		"mode: " + modeLabel(model.snapshot.Scenario.Mode),
	}
	if question := model.snapshot.CurrentQuestion; question != nil {
		lines = append(lines,
			fmt.Sprintf(
				"question: %d/%d",
				model.snapshot.CurrentIndex+1,
				len(model.snapshot.Scenario.Questions),
			),
			fmt.Sprintf(
				"follow-ups: %d/%d",
				model.snapshot.FollowUpCount,
				question.MaxFollowUps,
			),
			"intent: "+oneLine(question.Intent),
			"source: "+questionSource(*question),
			"close: "+oneLine(question.EndCondition),
		)
	}
	lines = append(lines,
		"",
		components.SectionLabel{
			Text: "Session controls",
			Kind: components.LabelWarning,
		}.Render(model.Theme),
		"[p] 暂停/恢复",
		"[x] 结束本题",
		"[q] 结束面试",
	)
	return truncateScreenLines(lines, width, model.Theme.UseASCII)
}

func (model *Model) commandsLocked() []components.KeyHint {
	if model.helpOpen {
		return []components.KeyHint{
			{Key: "Esc", Action: "返回", Enabled: true},
		}
	}
	if model.isBusyLocked() {
		return []components.KeyHint{
			{Key: "Esc", Action: "停止等待", Enabled: true},
		}
	}
	if model.snapshot.PendingEnd != nil {
		return []components.KeyHint{
			{Key: "y/Enter", Action: "确认结束", Enabled: true},
			{Key: "n/Esc", Action: "继续作答", Enabled: true},
		}
	}
	if model.snapshot.Phase == coreinterview.PhaseCompleted {
		return []components.KeyHint{
			{Key: "h", Action: "返回训练", Enabled: true},
			{Key: "?", Action: "快捷键", Enabled: true},
		}
	}
	if model.operation.Phase == async.Failed {
		if model.snapshot.CurrentQuestion == nil {
			return []components.KeyHint{
				{Key: "h", Action: "返回训练", Enabled: true},
				{Key: "?", Action: "快捷键", Enabled: true},
			}
		}
		return []components.KeyHint{
			{
				Key:     "t",
				Action:  "重试",
				Enabled: model.canRetryLocked(),
			},
			{Key: "x", Action: "结束本题", Enabled: true},
			{Key: "q", Action: "结束面试", Enabled: true},
			{Key: "?", Action: "快捷键", Enabled: true},
		}
	}
	if model.snapshot.Phase == coreinterview.PhasePaused {
		return []components.KeyHint{
			{Key: "p", Action: "恢复", Enabled: true},
			{Key: "q", Action: "结束面试", Enabled: true},
			{Key: "?", Action: "快捷键", Enabled: true},
		}
	}
	return []components.KeyHint{
		{
			Key:     "Ctrl+Enter",
			Action:  "提交回答",
			Enabled: strings.TrimSpace(model.draft) != "",
		},
		{Key: "p", Action: "暂停", Enabled: true},
		{Key: "x", Action: "结束本题", Enabled: true},
		{Key: "q", Action: "结束面试", Enabled: true},
		{Key: "Tab", Action: "切换区域", Enabled: true},
		{Key: "?", Action: "快捷键", Enabled: true},
	}
}

func (model *Model) helpLinesLocked() []string {
	return []string{
		"INTERVIEW ROOM",
		"[Ctrl+Enter] 提交当前回答；[Enter] 只换行",
		"[Tab/Shift+Tab] 切换回答、Trace 与 Session",
		"[↑/↓] 阅读不可编辑的 Answer Trace",
		"[p] 暂停/恢复 · [x] 结束本题 · [q] 结束面试",
		"[Esc] 停止等待、取消确认或关闭帮助",
		"",
		"结束本题和结束面试都需要 [y] 再次确认。",
		"已提交历史只能追加修正，不能编辑。",
	}
}

func (model *Model) composerStateLocked() components.ComposerState {
	if model.isBusyLocked() ||
		model.snapshot.Phase == coreinterview.PhasePaused ||
		model.snapshot.Phase == coreinterview.PhaseCompleted ||
		model.snapshot.PendingEnd != nil {
		return components.ComposerDisabled
	}
	if model.operation.Phase == async.Failed &&
		model.operation.Err != nil &&
		model.operation.Err.Code == domainerr.CodeValidation {
		return components.ComposerValidationErr
	}
	if model.snapshot.Draft != nil &&
		strings.TrimSpace(model.draft) != "" {
		return components.ComposerDraftRestored
	}
	if strings.TrimSpace(model.draft) == "" {
		return components.ComposerEmpty
	}
	return components.ComposerTyping
}

func (model *Model) composerDisabledReasonLocked() string {
	switch {
	case model.isBusyLocked():
		return "答案已记录，面试官正在思考"
	case model.snapshot.Phase == coreinterview.PhasePaused:
		return "面试已暂停，按 [p] 恢复"
	case model.snapshot.Phase == coreinterview.PhaseCompleted:
		return "会话已完成"
	case model.snapshot.PendingEnd != nil:
		return "先确认或取消结束操作"
	default:
		return "当前不可编辑"
	}
}

func (model *Model) traceItemsLocked() []components.TraceItem {
	items := make([]components.TraceItem, 0, len(model.snapshot.Events))
	for _, event := range model.snapshot.Events {
		kind, label := traceIdentity(model.snapshot, event)
		items = append(items, components.TraceItem{
			ID:      event.EventID,
			At:      event.OccurredAt,
			Kind:    kind,
			Label:   label,
			Summary: event.Content,
		})
	}
	return items
}

func traceIdentity(
	snapshot coreinterview.Snapshot,
	event db.SessionEvent,
) (components.TraceKind, string) {
	switch event.Speaker {
	case db.SpeakerUser:
		return components.TraceAnswer, "answer"
	case db.SpeakerCode:
		return components.TraceCode, "code run"
	case db.SpeakerReport:
		return components.TraceReport, "report"
	case db.SpeakerSystem:
		switch {
		case strings.Contains(event.EventID, "/control/pause/"):
			return components.TracePause, "paused"
		case strings.Contains(event.EventID, "/control/resume/"):
			return components.TracePause, "resumed"
		case strings.Contains(event.EventID, "/control/end-request/"):
			return components.TraceStatus, "end?"
		case strings.Contains(event.EventID, "/control/end-cancel/"):
			return components.TraceStatus, "continued"
		case strings.Contains(event.EventID, "/control/end-confirm/"):
			return components.TraceStatus, "ended"
		default:
			return components.TraceStatus, "status"
		}
	case db.SpeakerInterviewer:
		switch {
		case strings.Contains(event.EventID, "/question/"),
			strings.HasSuffix(event.EventID, "/next_question"):
			return components.TraceQuestion, questionLabel(
				snapshot,
				event.QuestionID,
			)
		case strings.HasSuffix(event.EventID, "/follow_up"):
			return components.TraceQuestion, "follow-up"
		case strings.HasSuffix(event.EventID, "/close_question"):
			return components.TraceStatus, "closed"
		case strings.HasSuffix(event.EventID, "/finish_session"):
			return components.TraceReport, "completed"
		default:
			return components.TraceQuestion, "interviewer"
		}
	default:
		return components.TraceStatus, "event"
	}
}

func (model *Model) timerLocked() components.Timer {
	question := model.snapshot.CurrentQuestion
	if question == nil || question.EstimatedSeconds <= 0 {
		return components.Timer{State: components.TimerExpired}
	}
	duration := time.Duration(question.EstimatedSeconds) * time.Second
	now := model.currentTime
	if now.IsZero() {
		now = model.now().UTC()
	}
	startedAt := questionStartedAt(model.snapshot, question.ID)
	if startedAt.IsZero() {
		startedAt = model.snapshot.Session.StartedAt
	}
	pausedDuration, paused := pausedSince(
		model.snapshot,
		startedAt,
		now,
	)
	elapsed := now.Sub(startedAt) - pausedDuration
	if elapsed < 0 {
		elapsed = 0
	}
	remaining := duration - elapsed
	state := components.TimerNormal
	switch {
	case paused:
		state = components.TimerPaused
	case remaining <= 0:
		state = components.TimerExpired
	case remaining <= duration/5:
		state = components.TimerWarning
	}
	return components.Timer{
		Remaining: max(remaining, 0),
		State:     state,
	}
}

func questionStartedAt(
	snapshot coreinterview.Snapshot,
	questionID string,
) time.Time {
	var result time.Time
	for _, event := range snapshot.Events {
		if event.QuestionID != questionID ||
			event.Speaker != db.SpeakerInterviewer {
			continue
		}
		if strings.Contains(event.EventID, "/question/") ||
			strings.HasSuffix(event.EventID, "/next_question") {
			result = event.OccurredAt
		}
	}
	return result
}

func pausedSince(
	snapshot coreinterview.Snapshot,
	startedAt time.Time,
	now time.Time,
) (time.Duration, bool) {
	var (
		total   time.Duration
		paused  time.Time
		isPause bool
	)
	for _, event := range snapshot.Events {
		if event.OccurredAt.Before(startedAt) {
			continue
		}
		switch {
		case strings.Contains(event.EventID, "/control/pause/"):
			if paused.IsZero() {
				paused = event.OccurredAt
			}
		case strings.Contains(event.EventID, "/control/resume/"):
			if !paused.IsZero() {
				total += max(event.OccurredAt.Sub(paused), 0)
				paused = time.Time{}
			}
		}
	}
	if !paused.IsZero() {
		total += max(now.Sub(paused), 0)
		isPause = true
	}
	return total, isPause
}

func (model *Model) mainStatusLocked(timer components.Timer) string {
	if model.snapshot.CurrentQuestion == nil {
		return sessionStatus(model.snapshot.Phase)
	}
	return fmt.Sprintf(
		"Q %02d/%02d · %s",
		model.snapshot.CurrentIndex+1,
		len(model.snapshot.Scenario.Questions),
		plainTimer(timer),
	)
}

func (model *Model) badgeLocked() components.StatusBadge {
	switch {
	case model.operation.Phase == async.Pending ||
		model.operation.Phase == async.Streaming:
		return components.StatusBadge{
			State: components.BadgeWarning,
			Text:  "interviewer thinking",
		}
	case model.operation.Phase == async.Failed:
		return components.StatusBadge{
			State: components.BadgeError,
			Text:  "interviewer failed",
		}
	case model.snapshot.Phase == coreinterview.PhaseCompleted:
		return components.StatusBadge{
			State: components.BadgeReady,
			Text:  "session complete",
		}
	default:
		return components.StatusBadge{
			State: components.BadgeReady,
			Text:  "session local",
		}
	}
}

func (model *Model) activitySummaryLocked(
	items []components.TraceItem,
) string {
	if model.operation.Value != nil &&
		strings.TrimSpace(model.operation.Value.Message) != "" {
		return model.operation.Value.Message
	}
	if model.operation.Err != nil {
		return model.operation.Err.Message
	}
	if len(items) == 0 {
		return "还没有面试事件"
	}
	item := items[len(items)-1]
	return fmt.Sprintf(
		"%s %s",
		item.At.Local().Format("15:04"),
		item.Label,
	)
}

func questionCardState(
	phase coreinterview.Phase,
) components.QuestionCardState {
	if phase == coreinterview.PhaseQuestionComplete ||
		phase == coreinterview.PhaseCompleted {
		return components.QuestionClosed
	}
	return components.QuestionTimed
}

func sessionStatus(phase coreinterview.Phase) string {
	switch phase {
	case coreinterview.PhaseNotStarted:
		return "not started"
	case coreinterview.PhaseThinking:
		return "thinking"
	case coreinterview.PhasePaused:
		return "paused"
	case coreinterview.PhaseAwaitingEndConfirmation:
		return "confirm end"
	case coreinterview.PhaseQuestionComplete:
		return "question complete"
	case coreinterview.PhaseCompleted:
		return "evaluation pending"
	default:
		return "answering"
	}
}

func modeLabel(mode contracts.ScenarioMode) string {
	switch mode {
	case contracts.ScenarioStrict:
		return "strict"
	case contracts.ScenarioCoach:
		return "coach"
	default:
		return "standard"
	}
}

func questionSource(question contracts.ScenarioQuestion) string {
	if question.Generic {
		return "generic"
	}
	if len(question.EvidenceIDs) == 0 {
		return "evidence unavailable"
	}
	values := make([]string, len(question.EvidenceIDs))
	for index, id := range question.EvidenceIDs {
		values[index] = string(id)
	}
	return strings.Join(values, ",")
}

func questionLabel(
	snapshot coreinterview.Snapshot,
	questionID string,
) string {
	for index, question := range snapshot.Scenario.Questions {
		if question.ID == questionID {
			return fmt.Sprintf("Q%d", index+1)
		}
	}
	return "question"
}

func plainTimer(timer components.Timer) string {
	seconds := int(max(timer.Remaining, 0).Round(time.Second) / time.Second)
	value := fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
	switch timer.State {
	case components.TimerPaused:
		return "paused " + value
	case components.TimerExpired:
		return "time ended"
	case components.TimerWarning:
		return value + " warning"
	default:
		return value + " left"
	}
}

func paneState(focused bool) components.PaneState {
	if focused {
		return components.PaneFocused
	}
	return components.PaneInactive
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func fitScreenLines(lines []string, height int) []string {
	if height <= 0 {
		return nil
	}
	if len(lines) > height {
		lines = lines[:height]
	}
	for len(lines) < height {
		lines = append(lines, "")
	}
	return lines
}

func truncateScreenLines(
	lines []string,
	width int,
	ascii bool,
) []string {
	result := make([]string, len(lines))
	for index, line := range lines {
		result[index] = layout.TruncateRight(line, width, ascii)
	}
	return result
}
