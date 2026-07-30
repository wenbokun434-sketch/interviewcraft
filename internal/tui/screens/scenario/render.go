package scenario

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// Render draws P-03 at the current terminal size.
func (model *Model) Render() (string, error) {
	if model == nil {
		return "", fmt.Errorf("scenario model is nil")
	}
	if err := model.operation.Validate(); err != nil {
		return "", err
	}
	plan := layout.Calculate(model.Width, model.Height)
	if err := plan.Validate(); err != nil {
		return "", err
	}

	mainWidth := max(1, plan.MainWidth-2)
	detailWidth := max(1, plan.CoachWidth-2)
	if plan.Mode == layout.Narrow || plan.Mode == layout.Blocked {
		mainWidth = max(1, model.Width-2)
	}
	mainLines, err := model.mainLines(
		mainWidth,
		max(1, plan.ContentHeight-2),
		plan.Mode == layout.Narrow,
	)
	if err != nil {
		return "", err
	}
	detailLines := model.detailLines(detailWidth)

	trace := components.Pane{
		Title: "导航",
		Lines: []string{
			"[h] 训练",
			"[p] 画像",
			"[s] 设置",
			"",
			"[?] 快捷键",
		},
	}
	main := components.Pane{
		Title:  "New Scenario",
		Status: model.planStatus(),
		State:  paneState(model.focus.Active() != focusPlan),
		Lines:  mainLines,
	}
	detail := components.Pane{
		Title:  "Plan detail",
		Status: model.detailStatus(),
		State:  paneState(model.focus.Active() == focusPlan),
		Lines:  detailLines,
	}
	var overlay *components.Pane
	if model.helpOpen {
		help := components.Pane{
			Title: "快捷键",
			State: components.PaneOverlay,
			Lines: model.helpLines(),
		}
		if plan.Mode == layout.Narrow {
			overlay = &help
		} else {
			main = help
			detail.State = components.PaneInactive
		}
	}
	shell := components.AppShell{
		Width:           model.Width,
		Height:          model.Height,
		Screen:          "NEW SCENARIO",
		Provider:        model.badge(),
		ActivitySummary: model.activitySummary(),
		Trace:           trace,
		Main:            main,
		Coach:           detail,
		Overlay:         overlay,
		Commands:        model.commands(),
		Theme:           model.Theme,
	}
	return shell.Render()
}

func (model *Model) mainLines(
	width int,
	height int,
	narrow bool,
) ([]string, error) {
	lines := []string{
		components.SectionLabel{
			Text: "Scenario settings",
			Kind: components.LabelInfo,
		}.Render(model.Theme),
		model.settingLine(
			focusTemplate,
			"template",
			model.templateLabel(),
			width,
		),
		model.settingLine(
			focusDifficulty,
			"difficulty",
			difficultyLabel(model.difficulty),
			width,
		),
		model.settingLine(
			focusMode,
			"mode",
			modeLabel(model.mode),
			width,
		),
		model.settingLine(
			focusDuration,
			"time",
			fmt.Sprintf("%d min", model.duration/60),
			width,
		),
		layout.TruncateRight(
			"coach policy: "+policyLabel(model.mode),
			width,
			model.Theme.UseASCII,
		),
		"",
	}
	stateLines, err := model.stateLines(width)
	if err != nil {
		return nil, err
	}
	lines = append(lines, stateLines...)
	lines = append(lines, components.SectionLabel{
		Text: "Run plan",
		Kind: components.LabelCoach,
	}.Render(model.Theme))

	available := max(1, height-len(lines))
	questionLines := model.questionLines(width, available)
	lines = append(lines, questionLines...)
	if narrow {
		if question, ok := model.selectedQuestion(); ok {
			lines = append(lines, layout.TruncateRight(
				"selected intent: "+oneLine(question.Intent),
				width,
				model.Theme.UseASCII,
			))
			lines = append(lines, layout.TruncateRight(
				"source: "+evidenceLabel(question),
				width,
				model.Theme.UseASCII,
			))
		}
	}
	return lines, nil
}

func (model *Model) stateLines(width int) ([]string, error) {
	switch model.operation.Phase {
	case async.Pending:
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在准备场景操作",
		}).Render(model.Theme, width)
		return []string{line}, err
	case async.Streaming:
		message := "正在创建场景计划"
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
		notice := components.ErrorNotice(
			model.operation.Err,
			&components.KeyHint{
				Key:     "g",
				Action:  "重试",
				Enabled: true,
			},
		)
		return notice.Render(model.Theme, width)
	case async.Succeeded:
		if model.plan == nil {
			return []string{
				model.Theme.Paint(theme.Muted, "还没有场景计划"),
				model.Theme.Paint(theme.Focus, "[g] 生成 Run Plan"),
			}, nil
		}
		message := "场景计划可编辑"
		if model.operation.Value != nil &&
			strings.TrimSpace(model.operation.Value.Message) != "" {
			message = model.operation.Value.Message
		}
		role := theme.Success
		if !model.plan.Locked {
			role = theme.Info
		}
		return []string{
			model.Theme.Paint(
				role,
				model.Theme.Glyphs.Success+" "+message,
			),
		}, nil
	default:
		return nil, fmt.Errorf(
			"unsupported Scenario operation phase %q",
			model.operation.Phase,
		)
	}
}

func (model *Model) questionLines(width, available int) []string {
	if model.plan == nil || len(model.plan.Scenario.Questions) == 0 {
		return []string{"-- 还没有场景计划 --"}
	}
	questions := model.plan.Scenario.Questions
	window := max(1, available)
	start := 0
	if model.selected >= window {
		start = model.selected - window + 1
	}
	end := min(len(questions), start+window)
	lines := make([]string, 0, end-start)
	for index := start; index < end; index++ {
		question := questions[index]
		marker := " "
		role := theme.Primary
		if index == model.selected {
			marker = model.Theme.Glyphs.Cursor
			if model.focus.Active() == focusPlan {
				role = theme.Focus
			}
		}
		line := fmt.Sprintf(
			"%s [%02d] %s · %s · %dm · [%s]",
			marker,
			index+1,
			oneLine(question.Prompt),
			oneLine(question.Intent),
			max(1, question.EstimatedSeconds/60),
			evidenceLabel(question),
		)
		lines = append(lines, model.Theme.Paint(
			role,
			layout.TruncateRight(line, width, model.Theme.UseASCII),
		))
	}
	return lines
}

func (model *Model) detailLines(width int) []string {
	question, found := model.selectedQuestion()
	if !found {
		return []string{
			"还没有场景计划",
			"",
			"生成后可查看题目意图、证据、评分量表与结束条件。",
		}
	}
	lines := []string{
		components.SectionLabel{
			Text: "Selected question",
			Kind: components.LabelInfo,
		}.Render(model.Theme),
		"prompt: " + oneLine(question.Prompt),
		"intent: " + oneLine(question.Intent),
		fmt.Sprintf(
			"time: %dm · follow-ups: %d",
			max(1, question.EstimatedSeconds/60),
			question.MaxFollowUps,
		),
		"source: " + evidenceLabel(question),
		"end: " + oneLine(question.EndCondition),
		"",
		components.SectionLabel{
			Text: "Rubric",
			Kind: components.LabelDefault,
		}.Render(model.Theme),
	}
	for _, criterion := range question.Rubric {
		lines = append(lines, "- "+oneLine(criterion))
	}
	lines = append(lines, "", components.SectionLabel{
		Text: "JD mapping",
		Kind: components.LabelCoach,
	}.Render(model.Theme))
	if model.plan == nil || len(model.plan.JDMappings) == 0 {
		lines = append(lines, "-- 未提供 JD，可正常开始 --")
		return truncateLines(lines, width, model.Theme.UseASCII)
	}
	for _, mapping := range model.plan.JDMappings {
		target := mapping.Gap
		if len(mapping.EvidenceIDs) > 0 {
			ids := make([]string, len(mapping.EvidenceIDs))
			for index, id := range mapping.EvidenceIDs {
				ids[index] = string(id)
			}
			target = strings.Join(ids, ",")
		}
		lines = append(lines, "- "+mapping.Requirement+" -> "+target)
	}
	return truncateLines(lines, width, model.Theme.UseASCII)
}

func (model *Model) settingLine(
	focusID string,
	label string,
	value string,
	width int,
) string {
	marker := " "
	role := theme.Primary
	if model.focus.Active() == focusID {
		marker = model.Theme.Glyphs.Cursor
		role = theme.Focus
	}
	line := fmt.Sprintf("%s %-10s [%s]", marker, label, value)
	return model.Theme.Paint(
		role,
		layout.TruncateRight(line, width, model.Theme.UseASCII),
	)
}

func (model *Model) commands() []components.KeyHint {
	if model.helpOpen {
		return []components.KeyHint{
			{Key: "Esc", Action: "返回", Enabled: true},
		}
	}
	if model.isBusy() {
		return []components.KeyHint{
			{Key: "b", Action: "返回画像", Enabled: true},
			{Key: "Tab", Action: "下一项", Enabled: true},
		}
	}
	return []components.KeyHint{
		{
			Key:     "g",
			Action:  "生成/刷新",
			Enabled: !model.isLocked(),
		},
		{
			Key:     "e",
			Action:  "替换题目",
			Enabled: model.canMutatePlan(),
		},
		{
			Key:     "d",
			Action:  "删除题目",
			Enabled: model.canMutatePlan(),
		},
		{
			Key:     "Ctrl+Enter",
			Action:  "开始",
			Enabled: model.plan != nil,
		},
		{Key: "Tab", Action: "下一项", Enabled: true},
		{Key: "?", Action: "快捷键", Enabled: true},
	}
}

func (model *Model) helpLines() []string {
	return []string{
		"SCENARIO SETTINGS",
		"[Tab/Shift+Tab] 模板、难度、模式、时长、Run Plan",
		"[↑/↓] 修改当前设置或选择题目",
		"[g] 生成/刷新计划 · [e] 替换 · [d] 删除",
		"[Ctrl+Enter] 确认版本并创建会话",
		"",
		"[b/p] 返回画像 · [h] 训练主页 · [s] 设置",
		"[Esc] 返回之前的焦点",
	}
}

func (model *Model) templateLabel() string {
	if len(model.templates) == 0 {
		return "unavailable"
	}
	return model.templates[model.templateIndex].Label
}

func (model *Model) planStatus() string {
	if model.plan == nil {
		return "empty"
	}
	status := fmt.Sprintf("v%d · %d questions", model.plan.Revision,
		len(model.plan.Scenario.Questions))
	if model.plan.Locked {
		status += " · locked"
	}
	return status
}

func (model *Model) detailStatus() string {
	if model.plan == nil || len(model.plan.Scenario.Questions) == 0 {
		return "empty"
	}
	return fmt.Sprintf(
		"%d/%d",
		model.selected+1,
		len(model.plan.Scenario.Questions),
	)
}

func (model *Model) badge() components.StatusBadge {
	badge := components.StatusBadge{
		State: components.BadgeWarning,
		Text:  "scenario empty",
	}
	if model.isBusy() {
		badge.Text = "scenario working"
		return badge
	}
	if model.operation.Phase == async.Failed {
		badge.State = components.BadgeError
		badge.Text = "scenario action failed"
		return badge
	}
	if model.plan != nil {
		badge.State = components.BadgeReady
		badge.Text = "scenario local"
	}
	return badge
}

func (model *Model) activitySummary() string {
	if model.operation.Value != nil &&
		strings.TrimSpace(model.operation.Value.Message) != "" {
		return model.operation.Value.Message
	}
	if model.operation.Err != nil {
		return model.operation.Err.Message
	}
	return "场景工厂已就绪"
}

func paneState(focused bool) components.PaneState {
	if focused {
		return components.PaneFocused
	}
	return components.PaneInactive
}

func difficultyLabel(value Difficulty) string {
	switch value {
	case DifficultyFoundation:
		return "基础"
	case DifficultyStretch:
		return "挑战"
	default:
		return "进阶"
	}
}

func modeLabel(value contracts.ScenarioMode) string {
	switch value {
	case contracts.ScenarioStrict:
		return "严格"
	case contracts.ScenarioCoach:
		return "教练"
	default:
		return "常规"
	}
}

func policyLabel(value contracts.ScenarioMode) string {
	switch value {
	case contracts.ScenarioStrict:
		return "Strict [1×L1/L2]"
	case contracts.ScenarioCoach:
		return "Coach [L1-L4; L4 after question]"
	default:
		return "Standard [2×L1/L2]"
	}
}

func evidenceLabel(question contracts.ScenarioQuestion) string {
	if question.Generic {
		return "generic"
	}
	if len(question.EvidenceIDs) == 0 {
		return "evidence unavailable"
	}
	ids := make([]string, len(question.EvidenceIDs))
	for index, id := range question.EvidenceIDs {
		ids[index] = string(id)
	}
	return strings.Join(ids, ",")
}

func oneLine(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

func truncateLines(values []string, width int, useASCII bool) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = layout.TruncateRight(value, width, useASCII)
	}
	return result
}
