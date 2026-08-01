package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// Render draws P-06 at the current responsive terminal size.
func (model *Model) Render() (string, error) {
	if model == nil {
		return "", fmt.Errorf("report model is nil")
	}
	if err := model.state.Validate(); err != nil {
		return "", err
	}
	plan := layout.Calculate(model.Width, model.Height)
	if err := plan.Validate(); err != nil {
		return "", err
	}
	trace, main, coach, overlay, err := model.panes(plan)
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
		Coach:           coach,
		Overlay:         overlay,
		Commands:        model.commands(),
		Theme:           model.Theme,
	}
	return shell.Render()
}

func (model *Model) panes(plan layout.Plan) (
	components.Pane,
	components.Pane,
	components.Pane,
	*components.Pane,
	error,
) {
	traceWidth := max(1, plan.TraceWidth-2)
	mainWidth := max(1, plan.MainWidth-2)
	coachWidth := max(1, plan.CoachWidth-2)
	if plan.Mode == layout.Narrow || plan.Mode == layout.Blocked {
		mainWidth = max(1, model.Width-2)
	}

	trace := components.Pane{
		Title: "Evidence rail",
		State: paneState(model.focus.Active() == focusEvidence),
		Lines: model.evidenceLines(traceWidth),
	}
	mainLines, err := model.mainLines(mainWidth, plan.Mode)
	if err != nil {
		return components.Pane{}, components.Pane{}, components.Pane{}, nil, err
	}
	coachLines, err := model.coachLines(coachWidth)
	if err != nil {
		return components.Pane{}, components.Pane{}, components.Pane{}, nil, err
	}
	main := components.Pane{
		Title:  "Report ledger",
		Status: model.reportStatus(),
		State:  paneState(model.mainFocused()),
		Lines:  mainLines,
	}
	coach := components.Pane{
		Title: "Learning / next run",
		State: paneState(
			model.focus.Active() == focusLearning ||
				model.focus.Active() == focusPractice,
		),
		Lines: coachLines,
	}

	var overlay *components.Pane
	modal := model.modalPane(mainWidth)
	if modal != nil {
		if plan.Mode == layout.Narrow {
			overlay = modal
		} else {
			main = *modal
			trace.State = components.PaneInactive
			coach.State = components.PaneInactive
		}
	}
	return trace, main, coach, overlay, nil
}

func (model *Model) mainLines(width int, mode layout.Mode) ([]string, error) {
	switch model.state.Phase {
	case async.Pending:
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在读取会话事实与报告证据",
		}).Render(model.Theme, width)
		return []string{line, "报告会按证据、逐题复盘、学习地图的顺序出现。"}, err
	case async.Failed:
		notice := components.ErrorNotice(
			model.state.Err,
			&components.KeyHint{Key: "t", Action: "返回训练主页", Enabled: true},
		)
		return notice.Render(model.Theme, width)
	case async.Streaming:
		stage := "正在生成证据化报告"
		if model.state.Value != nil && strings.TrimSpace(model.state.Value.Stage) != "" {
			stage = model.state.Value.Stage
		}
		line, err := (components.ActivityLine{
			State: async.NewStreaming(&stage),
			Label: stage,
		}).Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		lines := []string{line}
		if document := model.document(); document != nil {
			lines = append(lines, "")
			lines = append(lines, model.documentMainLines(*document, width, mode)...)
		}
		return lines, nil
	case async.Succeeded:
		document := model.document()
		if document == nil {
			return []string{
				model.Theme.Paint(theme.Info, "还没有可用报告"),
				"完成一场训练后，这里会显示可追溯到原始事件的复盘。",
				model.Theme.Paint(theme.Focus, model.Theme.Glyphs.Cursor+" [t] 开始训练"),
			}, nil
		}
		return model.documentMainLines(*document, width, mode), nil
	default:
		return nil, fmt.Errorf("unsupported report phase %q", model.state.Phase)
	}
}

func (model *Model) documentMainLines(
	document corereport.Document,
	width int,
	mode layout.Mode,
) []string {
	lines := model.factLines(document)
	if model.operationErr != nil {
		notice, err := components.ErrorNotice(
			model.operationErr,
			&components.KeyHint{Key: "d", Action: "重新删除", Enabled: true},
		).Render(model.Theme, width)
		if err == nil {
			lines = append(lines, "")
			lines = append(lines, notice...)
		}
	}
	if mode == layout.Narrow {
		lines = append(lines, "")
		return append(lines, model.narrowFocusedLines(document, width)...)
	}

	lines = append(lines, "", components.SectionLabel{
		Text: "Eight evidence-backed dimensions",
		Kind: components.LabelInfo,
	}.Render(model.Theme))
	lines = append(lines, listLines(
		components.SelectableList{
			Items:    scorecardItems(document.Scorecard),
			Selected: model.selectedScorecard,
			Focused:  model.focus.Active() == focusScorecard,
		}, model.Theme, width, 8,
	)...)

	lines = append(lines, "", components.SectionLabel{
		Text: "Question review",
	}.Render(model.Theme))
	lines = append(lines, listLines(
		components.SelectableList{
			Items:        questionItems(document.QuestionReview),
			Selected:     model.selectedReview,
			Focused:      model.focus.Active() == focusReviews,
			EmptyMessage: "还没有逐题复盘",
		}, model.Theme, width, min(3, max(2, len(document.QuestionReview))),
	)...)
	if len(document.QuestionReview) > 0 {
		review := document.QuestionReview[clampSelection(
			model.selectedReview, len(document.QuestionReview),
		)]
		lines = append(lines,
			"摘要："+review.Summary.Text,
			"下一步："+review.NextAction.Text,
		)
	}

	if mode == layout.Split {
		lines = append(lines, "", components.SectionLabel{
			Text: "Selected evidence",
			Kind: components.LabelInfo,
		}.Render(model.Theme))
		lines = append(lines, model.selectedEvidenceLine(width))
	}
	lines = append(lines, model.reviewGroupLines(document)...)
	return lines
}

func (model *Model) narrowFocusedLines(
	document corereport.Document,
	width int,
) []string {
	switch model.focus.Active() {
	case focusReviews:
		lines := []string{components.SectionLabel{
			Text: "Question review", Kind: components.LabelInfo,
		}.Render(model.Theme)}
		lines = append(lines, listLines(components.SelectableList{
			Items:        questionItems(document.QuestionReview),
			Selected:     model.selectedReview,
			Focused:      true,
			EmptyMessage: "还没有逐题复盘",
		}, model.Theme, width, 4)...)
		if len(document.QuestionReview) > 0 {
			item := document.QuestionReview[clampSelection(
				model.selectedReview, len(document.QuestionReview),
			)]
			lines = append(lines, "摘要："+item.Summary.Text, "下一步："+item.NextAction.Text)
		}
		return lines
	case focusLearning:
		return model.learningLines(document.LearningMap, width)
	case focusPractice:
		return model.practiceLines(document, width)
	case focusEvidence:
		return append([]string{components.SectionLabel{
			Text: "Evidence rail", Kind: components.LabelInfo,
		}.Render(model.Theme)}, model.evidenceLines(width)...)
	default:
		lines := []string{components.SectionLabel{
			Text: "Eight evidence-backed dimensions",
			Kind: components.LabelInfo,
		}.Render(model.Theme)}
		lines = append(lines, listLines(components.SelectableList{
			Items:    scorecardItems(document.Scorecard),
			Selected: model.selectedScorecard,
			Focused:  true,
		}, model.Theme, width, 8)...)
		lines = append(lines, model.reviewGroupLines(document)...)
		return lines
	}
}

func (model *Model) coachLines(width int) ([]string, error) {
	if model.state.Phase == async.Failed {
		return []string{"报告恢复后会重新载入学习地图与下一轮计划。"}, nil
	}
	if model.state.Phase == async.Pending {
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在聚合学习缺口",
		}).Render(model.Theme, width)
		return []string{line}, err
	}
	document := model.document()
	if document == nil {
		return []string{"完成训练后，学习缺口与下一轮计划会出现在这里。"}, nil
	}
	lines := model.learningLines(document.LearningMap, width)
	lines = append(lines, "")
	lines = append(lines, model.practiceLines(*document, width)...)
	if len(document.CrossInsights) > 0 {
		lines = append(lines, "", components.SectionLabel{
			Text: "Cross-source notes",
			Kind: components.LabelInfo,
		}.Render(model.Theme))
		for _, insight := range document.CrossInsights[:min(3, len(document.CrossInsights))] {
			lines = append(lines, "- "+insight.Text)
		}
	}
	return lines, nil
}

func (model *Model) learningLines(
	values []corereport.LearningGap,
	width int,
) []string {
	lines := []string{components.SectionLabel{
		Text: "Learning map",
		Kind: components.LabelCoach,
	}.Render(model.Theme)}
	if len(values) == 0 {
		return append(lines, "-- 本场没有 Coach 学习缺口 --")
	}
	for index, gap := range values {
		lines = append(lines, (components.LearningGapRow{
			Topic:        gap.Topic,
			AskCount:     gap.AskCount,
			MaxHelpLevel: gap.MaxHelpLevel,
			QuestionIDs:  gap.QuestionIDs,
			State:        components.LearningGapStateFor(gap),
			Focused: model.focus.Active() == focusLearning &&
				index == model.selectedLearning,
		}).Render(model.Theme, width))
	}
	return lines
}

func (model *Model) practiceLines(
	document corereport.Document,
	width int,
) []string {
	lines := []string{components.SectionLabel{
		Text: "Practice next",
		Kind: components.LabelCoach,
	}.Render(model.Theme)}
	items := document.PracticePlan[:min(3, len(document.PracticePlan))]
	lines = append(lines, listLines(components.SelectableList{
		Items:        practiceItems(items),
		Selected:     clampSelection(model.selectedPractice, len(items)),
		Focused:      model.focus.Active() == focusPractice,
		EmptyMessage: "还没有下一轮训练计划",
	}, model.Theme, width, max(2, len(items)))...)
	if len(items) > 0 {
		item := items[clampSelection(model.selectedPractice, len(items))]
		lines = append(lines, "完成标准："+item.CompletionCriteria)
	}
	return lines
}

func (model *Model) evidenceLines(width int) []string {
	document := model.document()
	if document == nil || len(document.Evidence) == 0 {
		return []string{"-- evidence unavailable --"}
	}
	lines := make([]string, 0, len(document.Evidence)+2)
	for index, item := range document.Evidence {
		lines = append(lines, (components.EvidenceLink{
			ID:         item.ID,
			Label:      item.Label,
			QuestionID: item.QuestionID,
			Timestamp:  evidenceTime(item.OccurredAt),
			Focused: model.focus.Active() == focusEvidence &&
				index == model.selectedEvidence,
		}).Render(model.Theme, width))
	}
	lines = append(lines, "", "[e] 从当前结论跳到证据")
	return lines
}

func (model *Model) modalPane(width int) *components.Pane {
	switch {
	case model.helpOpen:
		return &components.Pane{
			Title: "Report keys",
			State: components.PaneOverlay,
			Lines: []string{
				"[Tab] 切换报告区域 · [↑/↓] 选择",
				"[e/Enter] 查看当前结论的证据",
				"[n] 用当前计划创建新场景",
				"[d] 请求删除报告 · [t] 返回训练主页",
				"[Esc] 返回之前的精确焦点",
			},
		}
	case model.deleteConfirmOpen:
		prompt := components.ConfirmPrompt{
			Message: "删除报告、学习地图和派生训练计划？",
			Confirm: components.KeyHint{Key: "y", Action: "确认删除"},
			Cancel:  components.KeyHint{Key: "Esc", Action: "保留报告"},
		}
		return &components.Pane{
			Title: "Confirm report deletion",
			State: components.PaneOverlay,
			Lines: []string{
				model.Theme.Paint(theme.Warning, "该操作会删除本地报告及其派生内容。"),
				prompt.Render(model.Theme, width),
			},
		}
	case model.evidenceOpen:
		return &components.Pane{
			Title: "Evidence detail",
			State: components.PaneOverlay,
			Lines: model.evidenceDetailLines(width),
		}
	default:
		return nil
	}
}

func (model *Model) evidenceDetailLines(width int) []string {
	document := model.document()
	if model.missingEvidence || document == nil || len(document.Evidence) == 0 {
		return []string{
			(components.EvidenceLink{State: components.EvidenceMissing, Focused: true}).Render(model.Theme, width),
			"当前结论没有可解析的原始事件；报告不会把它伪装成已证实。",
			"[Esc] 返回原结论",
		}
	}
	item := document.Evidence[clampSelection(
		model.selectedEvidence, len(document.Evidence),
	)]
	return []string{
		(components.EvidenceLink{
			ID: item.ID, Label: item.Label, QuestionID: item.QuestionID,
			Timestamp: evidenceTime(item.OccurredAt), Focused: true,
		}).Render(model.Theme, width),
		"type       " + item.Kind,
		"evidence   " + string(item.ID),
		"question   " + nonBlank(item.QuestionID, "session-level"),
		"",
		"[Enter] 打开原始事件 · [Esc] 返回原结论",
	}
}

func (model *Model) factLines(document corereport.Document) []string {
	summary := document.Summary
	return []string{
		components.SectionLabel{Text: "Session facts", Kind: components.LabelInfo}.Render(model.Theme),
		fmt.Sprintf("场景 %s · 模式 %s · 用时 %s", summary.Template, modeLabel(summary.Mode), formatDuration(summary.DurationSeconds)),
		fmt.Sprintf("题目 %d · 提示 %d · 代码运行 %d", summary.QuestionCount, summary.CoachPromptCount, summary.CodeRunCount),
		fmt.Sprintf("完成 %s · 报告 %s", summary.CompletedAt.Local().Format("2006-01-02 15:04"), document.GeneratedAt.Local().Format("15:04")),
	}
}

func (model *Model) selectedEvidenceLine(width int) string {
	document := model.document()
	if document == nil {
		return (components.EvidenceLink{State: components.EvidenceMissing}).Render(model.Theme, width)
	}
	refs := model.selectedReferences()
	if len(refs) == 0 {
		return (components.EvidenceLink{State: components.EvidenceMissing}).Render(model.Theme, width)
	}
	index := evidenceIndex(document.Evidence, refs[0])
	if index < 0 {
		return (components.EvidenceLink{State: components.EvidenceMissing}).Render(model.Theme, width)
	}
	item := document.Evidence[index]
	return (components.EvidenceLink{
		ID: item.ID, Label: item.Label, QuestionID: item.QuestionID,
		Timestamp: evidenceTime(item.OccurredAt),
	}).Render(model.Theme, width)
}

type reviewGroup struct {
	Keep         []string
	Improve      []string
	PracticeNext []string
}

func groupReview(document corereport.Document) reviewGroup {
	groups := reviewGroup{
		Keep:         make([]string, 0, 3),
		Improve:      make([]string, 0, 3),
		PracticeNext: make([]string, 0, 3),
	}
	for _, item := range document.Scorecard {
		if item.Status != corereport.StatusEvidenceBacked || item.Score == nil {
			continue
		}
		label := dimensionLabel(item.Dimension)
		if *item.Score >= 4 && len(groups.Keep) < 3 {
			groups.Keep = append(groups.Keep, label+fmt.Sprintf(" %d/5", *item.Score))
		}
		if *item.Score <= 3 && len(groups.Improve) < 3 {
			groups.Improve = append(groups.Improve, label+"："+item.NextAction)
		}
	}
	for _, review := range document.QuestionReview {
		if len(groups.Keep) >= 3 {
			break
		}
		if review.Summary.Status == corereport.StatusEvidenceBacked {
			groups.Keep = append(groups.Keep, review.QuestionID+"："+review.Summary.Text)
		}
	}
	for _, item := range document.PracticePlan[:min(3, len(document.PracticePlan))] {
		groups.PracticeNext = append(groups.PracticeNext, item.Topic)
	}
	return groups
}

func (model *Model) reviewGroupLines(document corereport.Document) []string {
	groups := groupReview(document)
	lines := []string{"", components.SectionLabel{
		Text: "Keep", Kind: components.LabelInfo,
	}.Render(model.Theme)}
	lines = append(lines, bulletLines(groups.Keep, "尚无可核验的保持项")...)
	lines = append(lines, components.SectionLabel{
		Text: "Improve", Kind: components.LabelWarning,
	}.Render(model.Theme))
	lines = append(lines, bulletLines(groups.Improve, "尚无证据支持的改进项")...)
	lines = append(lines, components.SectionLabel{
		Text: "Practice next", Kind: components.LabelCoach,
	}.Render(model.Theme))
	lines = append(lines, bulletLines(groups.PracticeNext, "还没有下一轮计划")...)
	return lines
}

func scorecardItems(values []corereport.ScorecardItem) []components.ListItem {
	items := make([]components.ListItem, 0, len(values))
	for _, value := range values {
		meta := "不足以判断 · evidence unavailable"
		switch value.Status {
		case corereport.StatusNotApplicable:
			meta = "不适用 · evidence unavailable"
		case corereport.StatusEvidenceBacked:
			if value.Score != nil {
				meta = fmt.Sprintf("%d/5 · evidence %d", *value.Score, len(value.EvidenceIDs))
			} else {
				meta = fmt.Sprintf("evidence %d", len(value.EvidenceIDs))
			}
		}
		items = append(items, components.ListItem{
			ID: string(value.Dimension), Label: dimensionLabel(value.Dimension), Meta: meta,
		})
	}
	return items
}

func questionItems(values []corereport.QuestionReview) []components.ListItem {
	items := make([]components.ListItem, 0, len(values))
	for _, value := range values {
		items = append(items, components.ListItem{
			ID: value.QuestionID, Label: value.QuestionID + " · " + value.Prompt,
			Meta: assessmentLabel(value.Summary.Status),
		})
	}
	return items
}

func practiceItems(values []corereport.PracticeItem) []components.ListItem {
	items := make([]components.ListItem, 0, len(values))
	for index, value := range values {
		items = append(items, components.ListItem{
			ID: fmt.Sprintf("practice-%d", index+1), Label: value.Topic,
			Meta: fmt.Sprintf("%d min · %s", value.DurationMinutes, modeLabel(value.Mode)),
		})
	}
	return items
}

func bulletLines(values []string, empty string) []string {
	if len(values) == 0 {
		return []string{"- " + empty}
	}
	lines := make([]string, 0, min(3, len(values)))
	for _, value := range values[:min(3, len(values))] {
		lines = append(lines, "- "+value)
	}
	return lines
}

func listLines(list components.SelectableList, current theme.Theme, width, height int) []string {
	lines := list.Render(current, width, height)
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func assessmentLabel(status corereport.AssessmentStatus) string {
	switch status {
	case corereport.StatusEvidenceBacked:
		return "有证据"
	case corereport.StatusNotApplicable:
		return "不适用"
	default:
		return "不足以判断"
	}
}

func dimensionLabel(value contracts.EvaluationDimension) string {
	switch value {
	case contracts.DimensionAnswerStructure:
		return "回答结构"
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
		return string(value)
	}
}

func modeLabel(value contracts.ScenarioMode) string {
	switch value {
	case contracts.ScenarioStrict:
		return "Strict"
	case contracts.ScenarioStandard:
		return "Standard"
	case contracts.ScenarioCoach:
		return "Coach"
	default:
		return string(value)
	}
}

func formatDuration(seconds int) string {
	if seconds < 0 {
		seconds = 0
	}
	return fmt.Sprintf("%02d:%02d", seconds/60, seconds%60)
}

func evidenceTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("15:04")
}

func nonBlank(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func paneState(focused bool) components.PaneState {
	if focused {
		return components.PaneFocused
	}
	return components.PaneInactive
}

func (model *Model) mainFocused() bool {
	active := model.focus.Active()
	return active == focusScorecard || active == focusReviews
}

func (model *Model) screenTitle() string {
	if document := model.document(); document != nil {
		return "REPORT / " + document.Summary.Template
	}
	return "REPORT"
}

func (model *Model) reportStatus() string {
	switch model.state.Phase {
	case async.Pending, async.Streaming:
		return "加载中"
	case async.Failed:
		return "读取失败"
	case async.Succeeded:
		if document := model.document(); document != nil {
			if document.Degraded {
				return "部分结论降级"
			}
			return "证据已核验"
		}
		return "空"
	default:
		return ""
	}
}

func (model *Model) activitySummary() string {
	if model.state.Phase == async.Streaming && model.state.Value != nil {
		return model.state.Value.Stage
	}
	return model.reportStatus()
}

func (model *Model) commands() []components.KeyHint {
	switch {
	case model.helpOpen || model.evidenceOpen:
		return []components.KeyHint{{Key: "Esc", Action: "返回", Enabled: true}}
	case model.deleteConfirmOpen:
		return []components.KeyHint{
			{Key: "y", Action: "确认删除", Enabled: true},
			{Key: "Esc", Action: "保留报告", Enabled: true},
		}
	}
	if model.document() == nil {
		return []components.KeyHint{
			{Key: "t", Action: "开始训练", Enabled: true},
			{Key: "?", Action: "快捷键", Enabled: true},
		}
	}
	return []components.KeyHint{
		{Key: "e", Action: "查看证据", Enabled: true},
		{Key: "n", Action: "按计划训练", Enabled: true},
		{Key: "d", Action: "删除报告", Enabled: true},
		{Key: "Tab", Action: "下一栏", Enabled: true},
		{Key: "?", Action: "快捷键", Enabled: true},
	}
}
