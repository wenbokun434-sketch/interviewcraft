package training

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/db"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// Render draws the training home at the current terminal size.
func (model *Model) Render() (string, error) {
	if model == nil {
		return "", fmt.Errorf("training model is nil")
	}
	if err := model.state.Validate(); err != nil {
		return "", err
	}
	plan := layout.Calculate(model.Width, model.Height)
	if err := plan.Validate(); err != nil {
		return "", err
	}

	trace, main, queue, overlay, err := model.panes(plan)
	if err != nil {
		return "", err
	}
	shell := components.AppShell{
		Width:           model.Width,
		Height:          model.Height,
		Screen:          model.screenTitle(),
		Provider:        model.Provider,
		ActivitySummary: model.activitySummary(),
		Trace:           trace,
		Main:            main,
		Coach:           queue,
		Overlay:         overlay,
		Commands:        model.commands(),
		Theme:           model.Theme,
	}
	return shell.Render()
}

func (model *Model) panes(
	plan layout.Plan,
) (
	components.Pane,
	components.Pane,
	components.Pane,
	*components.Pane,
	error,
) {
	trace := components.Pane{
		Title: "导航",
		State: paneState(model.focus.Active() == "navigation"),
		Lines: model.navigationLines(),
	}
	mainWidth := max(1, plan.MainWidth-2)
	queueWidth := max(1, plan.CoachWidth-2)
	if plan.Mode == layout.Narrow || plan.Mode == layout.Blocked {
		mainWidth = max(1, model.Width-2)
	}

	mainLines, err := model.mainLines(mainWidth, plan.Mode == layout.Narrow)
	if err != nil {
		return components.Pane{}, components.Pane{}, components.Pane{}, nil, err
	}
	queueLines, err := model.queueLines(queueWidth)
	if err != nil {
		return components.Pane{}, components.Pane{}, components.Pane{}, nil, err
	}
	main := components.Pane{
		Title:  "训练主页",
		Status: model.homeStatus(),
		State:  paneState(model.focus.Active() == focusPrimary || model.focus.Active() == focusRecent),
		Lines:  mainLines,
	}
	queue := components.Pane{
		Title: "Practice Queue",
		State: paneState(model.focus.Active() == focusQueue),
		Lines: queueLines,
	}

	if model.helpOpen {
		help := components.Pane{
			Title: "快捷键",
			State: components.PaneOverlay,
			Lines: model.helpLines(),
		}
		if plan.Mode == layout.Narrow {
			return trace, main, queue, &help, nil
		}
		main = help
		queue.State = components.PaneInactive
	}
	return trace, main, queue, nil, nil
}

func (model *Model) mainLines(width int, includeQueue bool) ([]string, error) {
	switch model.state.Phase {
	case async.Pending:
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在加载训练记录",
		}).Render(model.Theme, width)
		return []string{line}, err
	case async.Streaming:
		status := "正在加载最近训练与练习队列"
		line, err := (components.ActivityLine{
			State: async.NewStreaming(&status),
			Label: status,
		}).Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		lines := []string{line}
		if model.state.Value != nil {
			lines = append(lines, model.succeededMainLines(
				*model.state.Value,
				width,
				includeQueue,
			)...)
		}
		return lines, nil
	case async.Failed:
		notice := components.ErrorNotice(
			model.state.Err,
			&components.KeyHint{Key: "t", Action: "重试", Enabled: true},
		)
		return notice.Render(model.Theme, width)
	case async.Succeeded:
		return model.succeededMainLines(
			*model.state.Value,
			width,
			includeQueue,
		), nil
	default:
		return nil, fmt.Errorf("unsupported training phase %q", model.state.Phase)
	}
}

func (model *Model) succeededMainLines(
	data db.TrainingHomeData,
	width int,
	includeQueue bool,
) []string {
	lines := model.primaryLines(data, width)
	lines = append(lines, "")
	lines = append(lines, components.SectionLabel{
		Text: "Recent sessions",
		Kind: components.LabelInfo,
	}.Render(model.Theme))

	recent := components.SelectableList{
		Items:        recentItems(data.Recent),
		Selected:     model.selectedRecent,
		Focused:      model.focus.Active() == focusRecent,
		EmptyMessage: "还没有训练记录",
		EmptyAction: &components.KeyHint{
			Key:     "n",
			Action:  "创建第一场模拟",
			Enabled: true,
		},
	}
	recentHeight := max(2, min(6, len(data.Recent)))
	lines = append(lines, trimBlankLines(recent.Render(
		model.Theme,
		width,
		recentHeight,
	))...)

	if includeQueue {
		lines = append(lines, "")
		lines = append(lines, components.SectionLabel{
			Text: "Practice queue",
			Kind: components.LabelCoach,
		}.Render(model.Theme))
		queue := components.SelectableList{
			Items:        practiceItems(data.PracticeQueue),
			Selected:     model.selectedQueue,
			Focused:      model.focus.Active() == focusQueue,
			EmptyMessage: "还没有练习队列",
		}
		queueHeight := max(1, min(5, len(data.PracticeQueue)))
		lines = append(lines, trimBlankLines(queue.Render(
			model.Theme,
			width,
			queueHeight,
		))...)
	}
	return lines
}

func (model *Model) queueLines(width int) ([]string, error) {
	if model.state.Phase == async.Failed {
		return []string{
			"训练记录恢复后会重新加载练习队列。",
		}, nil
	}
	if model.state.Phase == async.Pending {
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在加载练习队列",
		}).Render(model.Theme, width)
		return []string{line}, err
	}
	data := model.data()
	if data == nil {
		return []string{"正在等待训练数据。"}, nil
	}
	list := components.SelectableList{
		Items:        practiceItems(data.PracticeQueue),
		Selected:     model.selectedQueue,
		Focused:      model.focus.Active() == focusQueue,
		EmptyMessage: "还没有练习队列",
	}
	return trimBlankLines(list.Render(
		model.Theme,
		width,
		max(2, min(8, len(data.PracticeQueue))),
	)), nil
}

func (model *Model) primaryLines(
	data db.TrainingHomeData,
	width int,
) []string {
	if data.Resume == nil {
		return []string{
			model.Theme.Paint(theme.Primary, "创建一场基于个人经历的模拟面试。"),
			model.Theme.Paint(
				theme.Focus,
				layout.TruncateRight(
					model.Theme.Glyphs.Cursor+" [n] 创建第一场模拟",
					width,
					model.Theme.UseASCII,
				),
			),
		}
	}
	resume := data.Resume
	detail := templateLabel(resume.Scenario.Template)
	if resume.LastEvent != nil {
		detail += " · " + resume.LastEvent.QuestionID +
			" · 最后记录 " + resume.LastEvent.OccurredAt.Local().Format("15:04")
	} else {
		detail += " · 尚未开始作答"
	}
	if resume.Draft != nil {
		detail += " · 草稿已保存"
	}
	return []string{
		model.Theme.Paint(theme.Primary, "继续上次未完成的训练。"),
		layout.TruncateRight(detail, width, model.Theme.UseASCII),
		model.Theme.Paint(
			theme.Focus,
			layout.TruncateRight(
				model.Theme.Glyphs.Cursor+" [Enter] 继续训练",
				width,
				model.Theme.UseASCII,
			),
		),
		model.Theme.Paint(theme.Muted, "[n] 新建训练"),
	}
}

func (model *Model) navigationLines() []string {
	return []string{
		"[t] 训练",
		"[p] 画像",
		"[r] 报告",
		"[s] 设置",
		"",
		"[?] 快捷键",
	}
}

func (model *Model) helpLines() []string {
	return []string{
		"GLOBAL NAVIGATION",
		"[t] 训练主页 · [p] 画像 · [r] 报告 · [s] 设置",
		"",
		"CURRENT SCREEN",
		"[Tab] 切换区域 · [↑/↓] 选择 · [Enter] 打开",
		"[n] 新建训练 · [v] 查看报告 · [q] 请求退出",
		"",
		"[Esc] 返回之前的焦点",
	}
}

func (model *Model) commands() []components.KeyHint {
	if model.helpOpen {
		return []components.KeyHint{
			{Key: "Esc", Action: "返回", Enabled: true},
		}
	}
	commands := []components.KeyHint{}
	data := model.data()
	if data != nil && data.Resume != nil {
		commands = append(commands, components.KeyHint{
			Key: "Enter", Action: "继续训练", Enabled: true,
		})
	} else if model.state.Phase == async.Succeeded {
		commands = append(commands, components.KeyHint{
			Key: "n", Action: "新建训练", Enabled: true,
		})
	} else if model.state.Phase == async.Failed {
		commands = append(commands, components.KeyHint{
			Key: "t", Action: "重试", Enabled: true,
		})
	}
	commands = append(commands,
		components.KeyHint{Key: "Tab", Action: "下一栏", Enabled: true},
		components.KeyHint{Key: "?", Action: "快捷键", Enabled: true},
		components.KeyHint{Key: "p", Action: "画像", Enabled: true},
		components.KeyHint{Key: "r", Action: "报告", Enabled: true},
		components.KeyHint{Key: "s", Action: "设置", Enabled: true},
	)
	return commands
}

func (model *Model) screenTitle() string {
	data := model.data()
	if data == nil || len(data.Recent) == 0 {
		return "TRAINING"
	}
	return "TRAINING / " + data.Recent[0].UpdatedAt.Local().Format("01.02")
}

func (model *Model) activitySummary() string {
	switch model.state.Phase {
	case async.Pending, async.Streaming:
		return "正在加载训练记录"
	case async.Failed:
		return "SQLite 查询失败"
	case async.Succeeded:
		data := model.data()
		if data != nil && data.Resume != nil && data.Resume.LastEvent != nil {
			return "可继续 " + data.Resume.LastEvent.QuestionID
		}
	}
	return "本地记录已就绪"
}

func (model *Model) homeStatus() string {
	switch model.state.Phase {
	case async.Pending, async.Streaming:
		return "加载中"
	case async.Failed:
		return "SQLite 错误"
	case async.Succeeded:
		if data := model.data(); data != nil && data.Resume != nil {
			return "可继续"
		}
		return "新训练"
	default:
		return ""
	}
}

func recentItems(items []db.RecentTraining) []components.ListItem {
	result := make([]components.ListItem, 0, len(items))
	for _, item := range items {
		meta := sessionStatusLabel(item.Status)
		if item.Score != nil {
			meta += " · " + dimensionLabel(item.Score.Dimension) +
				fmt.Sprintf(" %d/%d", item.Score.Score, item.Score.Scale)
		} else if item.ReportID != "" {
			meta += " · 评分不可用"
		}
		result = append(result, components.ListItem{
			ID:    item.SessionID,
			Label: templateLabel(item.Template),
			Meta:  meta,
		})
	}
	return result
}

func practiceItems(items []db.PracticeItem) []components.ListItem {
	result := make([]components.ListItem, 0, len(items))
	for _, item := range items {
		parts := make([]string, 0, 2)
		if item.DurationMinutes > 0 {
			parts = append(parts, fmt.Sprintf("%d min", item.DurationMinutes))
		}
		if item.Mode != "" {
			parts = append(parts, modeLabel(item.Mode))
		}
		result = append(result, components.ListItem{
			ID:    item.ID,
			Label: item.Topic,
			Meta:  strings.Join(parts, " · "),
		})
	}
	return result
}

func templateLabel(value string) string {
	switch value {
	case "behavioral":
		return "行为面"
	case "project_deep_dive":
		return "项目深挖"
	case "technical_foundations":
		return "基础技术"
	case "algorithm_coding":
		return "算法编码"
	case "system_design":
		return "系统设计"
	case "mixed":
		return "综合面"
	default:
		value = strings.TrimSpace(value)
		if value == "" {
			return "未命名场景"
		}
		return value
	}
}

func modeLabel(value contracts.ScenarioMode) string {
	switch value {
	case contracts.ScenarioStrict:
		return "严格"
	case contracts.ScenarioStandard:
		return "常规"
	case contracts.ScenarioCoach:
		return "教练"
	default:
		return string(value)
	}
}

func sessionStatusLabel(value db.SessionStatus) string {
	switch value {
	case db.SessionActive:
		return "进行中"
	case db.SessionEvaluationPending:
		return "待评估"
	case db.SessionCompleted:
		return "已完成"
	default:
		return "状态不可用"
	}
}

func dimensionLabel(value contracts.EvaluationDimension) string {
	switch value {
	case contracts.DimensionAnswerStructure:
		return "表达结构"
	case contracts.DimensionExperienceCredibility:
		return "经历可信度"
	case contracts.DimensionTechnicalDepth:
		return "技术深度"
	case contracts.DimensionProblemClarification:
		return "问题澄清"
	case contracts.DimensionProblemSolving:
		return "解题过程"
	case contracts.DimensionCodeQuality:
		return "代码质量"
	case contracts.DimensionTimeManagement:
		return "时间管理"
	case contracts.DimensionIndependence:
		return "独立完成度"
	default:
		return "评分维度不可用"
	}
}

func paneState(focused bool) components.PaneState {
	if focused {
		return components.PaneFocused
	}
	return components.PaneInactive
}

func trimBlankLines(lines []string) []string {
	last := len(lines)
	for last > 0 && strings.TrimSpace(lines[last-1]) == "" {
		last--
	}
	return lines[:last]
}
