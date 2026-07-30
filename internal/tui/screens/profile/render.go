package profile

import (
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/tui/components"
	"github.com/interviewcraft/interviewcraft/internal/tui/layout"
	"github.com/interviewcraft/interviewcraft/internal/tui/theme"
)

// Render draws P-02 at the current terminal size.
func (model *Model) Render() (string, error) {
	if model == nil {
		return "", fmt.Errorf("profile model is nil")
	}
	if err := model.operation.Validate(); err != nil {
		return "", err
	}
	plan := layout.Calculate(model.Width, model.Height)
	if err := plan.Validate(); err != nil {
		return "", err
	}

	mainWidth := max(1, plan.MainWidth-2)
	profileWidth := max(1, plan.CoachWidth-2)
	if plan.Mode == layout.Narrow || plan.Mode == layout.Blocked {
		mainWidth = max(1, model.Width-2)
	}
	mainLines, err := model.formLines(mainWidth)
	if err != nil {
		return "", err
	}
	profileLines := model.profileLines(profileWidth)
	if plan.Mode == layout.Narrow {
		mainLines = append(mainLines, "")
		mainLines = append(mainLines, model.profileLines(mainWidth)...)
	}

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
		Title:  "Profile / input",
		Status: model.status(),
		State:  paneState(model.focus.Active() != focusProfile),
		Lines:  mainLines,
	}
	profilePane := components.Pane{
		Title:  "Profile / editable",
		Status: model.profileStatus(),
		State:  paneState(model.focus.Active() == focusProfile),
		Lines:  profileLines,
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
			profilePane.State = components.PaneInactive
		}
	}
	shell := components.AppShell{
		Width:           model.Width,
		Height:          model.Height,
		Screen:          "PROFILE / input",
		Provider:        model.badge(),
		ActivitySummary: model.activitySummary(),
		Trace:           trace,
		Main:            main,
		Coach:           profilePane,
		Overlay:         overlay,
		Commands:        model.commands(),
		Theme:           model.Theme,
	}
	return shell.Render()
}

func (model *Model) formLines(width int) ([]string, error) {
	lines := []string{
		components.SectionLabel{
			Text: "Resume input",
			Kind: components.LabelInfo,
		}.Render(model.Theme),
		model.fieldLine(
			focusFile,
			"file",
			model.form.FilePath,
			model.filePlaceholder(),
			width,
		),
		model.fieldLine(
			focusPaste,
			"paste",
			oneLine(model.form.Paste),
			"粘贴简历文本",
			width,
		),
		"",
		components.SectionLabel{
			Text: "Target",
			Kind: components.LabelCoach,
		}.Render(model.Theme),
		model.fieldLine(focusRole, "role", model.form.Role, "目标岗位", width),
		model.fieldLine(focusLevel, "level", model.form.Level, "Junior", width),
		model.fieldLine(focusJD, "JD", oneLine(model.form.JD), "可选", width),
		model.fieldLine(
			focusLanguage,
			"language",
			model.form.Language,
			"中文",
			width,
		),
		"",
	}
	stateLines, err := model.stateLines(width)
	if err != nil {
		return nil, err
	}
	return append(lines, stateLines...), nil
}

func (model *Model) stateLines(width int) ([]string, error) {
	switch model.operation.Phase {
	case async.Pending:
		line, err := (components.ActivityLine{
			State: async.NewPending[string](),
			Label: "正在准备画像操作",
		}).Render(model.Theme, width)
		return []string{line}, err
	case async.Streaming:
		message := "正在处理简历"
		if model.operation.Value != nil &&
			strings.TrimSpace(model.operation.Value.Message) != "" {
			message = model.operation.Value.Message
		}
		line, err := (components.ActivityLine{
			State: async.NewStreaming(&message),
			Label: message,
		}).Render(model.Theme, width)
		if err != nil {
			return nil, err
		}
		lines := []string{line}
		if value := model.operation.Value; value != nil &&
			value.Total > 0 {
			lines = append(lines, fmt.Sprintf(
				"%d%% · %s",
				min(100, int(value.Current*100/value.Total)),
				value.SourceName,
			))
		}
		return lines, nil
	case async.Failed:
		notice := components.ErrorNotice(
			model.operation.Err,
			&components.KeyHint{Key: "x", Action: "重试", Enabled: true},
		)
		return notice.Render(model.Theme, width)
	case async.Succeeded:
		if model.aggregate == nil {
			return []string{
				model.Theme.Paint(theme.Muted, "还没有加载简历"),
				model.Theme.Paint(
					theme.Focus,
					"[x] 解析文件或粘贴文本",
				),
			}, nil
		}
		message := "画像已生成，请确认后保存"
		if model.operation.Value != nil &&
			strings.TrimSpace(model.operation.Value.Message) != "" {
			message = model.operation.Value.Message
		}
		return []string{
			model.Theme.Paint(
				theme.Success,
				model.Theme.Glyphs.Success+" "+message,
			),
		}, nil
	default:
		return nil, fmt.Errorf(
			"unsupported Profile operation phase %q",
			model.operation.Phase,
		)
	}
}

func (model *Model) profileLines(width int) []string {
	if model.aggregate == nil {
		return []string{
			"还没有加载简历",
			"",
			"解析成功后，事实和待验证推断会显示在这里。",
		}
	}
	candidate := model.aggregate.Candidate
	lines := []string{
		components.SectionLabel{
			Text: "Projects",
			Kind: components.LabelInfo,
		}.Render(model.Theme),
	}
	if len(candidate.Projects) == 0 {
		lines = append(lines, "-- 无原文支持的项目 --")
	} else {
		lines = append(lines, compactValues(
			candidate.Projects,
			width,
			model.Theme.UseASCII,
		))
	}
	lines = append(lines, components.SectionLabel{
		Text: "Skills",
		Kind: components.LabelInfo,
	}.Render(model.Theme))
	if len(candidate.Skills) == 0 {
		lines = append(lines, "-- 无原文支持的技能 --")
	} else {
		lines = append(lines, compactValues(
			candidate.Skills,
			width,
			model.Theme.UseASCII,
		))
	}
	lines = append(lines, components.SectionLabel{
		Text: "Facts / inferences",
		Kind: components.LabelCoach,
	}.Render(model.Theme))

	itemIndex := 0
	for _, fact := range candidate.Facts {
		label := model.confirmedLabel()
		if containsEvidenceID(model.aggregate.Metadata.LockedFactIDs, fact.ID) {
			label += " · locked"
		}
		lines = append(lines, model.profileRow(
			itemIndex,
			label,
			fact.Field,
			fact.Value,
			width,
		))
		itemIndex++
	}
	for _, inference := range candidate.Inferences {
		label := fmt.Sprintf(
			"? verify %.0f%%",
			inference.Confidence*100,
		)
		if containsString(
			model.aggregate.Metadata.LockedInferenceIDs,
			inference.ID,
		) {
			label += " · locked"
		}
		lines = append(lines, model.profileRow(
			itemIndex,
			label,
			inference.Field,
			inference.Value,
			width,
		))
		itemIndex++
	}
	if model.editID != "" {
		lines = append(lines, "")
		lines = append(lines, layout.TruncateRight(
			"editing "+model.editID+": "+oneLine(model.editBuffer),
			width,
			model.Theme.UseASCII,
		))
		lines = append(lines, "[Enter] 应用 · [Esc] 取消")
	}
	return lines
}

func (model *Model) fieldLine(
	focusID string,
	label string,
	value string,
	placeholder string,
	width int,
) string {
	marker := " "
	if model.focus.Active() == focusID {
		marker = model.Theme.Glyphs.Cursor
	}
	value = strings.TrimSpace(value)
	if value == "" {
		value = model.Theme.Paint(theme.Muted, placeholder)
	}
	line := fmt.Sprintf("%s %-8s [%s]", marker, label, value)
	return layout.TruncateRight(line, width, model.Theme.UseASCII)
}

func (model *Model) profileRow(
	index int,
	label string,
	field string,
	value string,
	width int,
) string {
	marker := " "
	role := theme.Primary
	if index == model.selected {
		marker = model.Theme.Glyphs.Cursor
		if model.focus.Active() == focusProfile {
			role = theme.Focus
		}
	}
	line := fmt.Sprintf(
		"%s %s · %s · %s",
		marker,
		label,
		oneLine(value),
		field,
	)
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
	if model.editID != "" {
		return []components.KeyHint{
			{Key: "Enter", Action: "应用编辑", Enabled: true},
			{Key: "Esc", Action: "取消编辑", Enabled: true},
		}
	}
	if model.isBusy() {
		return []components.KeyHint{
			{Key: "c", Action: "取消", Enabled: true},
			{Key: "Tab", Action: "下一字段", Enabled: true},
			{Key: "h", Action: "训练", Enabled: true},
		}
	}
	return []components.KeyHint{
		{Key: "x", Action: "解析", Enabled: model.hasInput()},
		{
			Key:     "Ctrl+Enter",
			Action:  "保存并继续",
			Enabled: model.aggregate != nil,
		},
		{Key: "e", Action: "编辑", Enabled: model.itemCount() > 0},
		{Key: "l", Action: "锁定", Enabled: model.itemCount() > 0},
		{Key: "d", Action: "删除", Enabled: model.itemCount() > 0},
		{Key: "Tab", Action: "下一字段", Enabled: true},
		{Key: "?", Action: "快捷键", Enabled: true},
	}
}

func (model *Model) helpLines() []string {
	return []string{
		"PROFILE INPUT",
		"[Tab/Shift+Tab] 文件、粘贴、角色、级别、JD、语言、画像",
		"[↑/↓] 修改级别、语言或选择画像字段",
		"[x] 解析 · [c] 取消 · [Ctrl+Enter] 保存并继续",
		"",
		"PROFILE EDIT",
		"[e/Enter] 行内编辑 · [l] 锁定/解锁 · [d] 删除",
		"",
		"[h] 训练主页 · [s] 设置 · [Esc] 返回之前的焦点",
	}
}

func (model *Model) hasInput() bool {
	if model.sourceMode == SourcePaste {
		return strings.TrimSpace(model.form.Paste) != ""
	}
	return strings.TrimSpace(model.form.FilePath) != ""
}

func (model *Model) filePlaceholder() string {
	if model.loadedSourceName != "" && model.form.FilePath == "" {
		return model.loadedSourceName + "（已恢复）"
	}
	return "PDF / DOCX / TXT 路径"
}

func (model *Model) confirmedLabel() string {
	if model.Theme.UseASCII {
		return "ok confirmed"
	}
	return "✓ confirmed"
}

func (model *Model) status() string {
	switch model.operation.Phase {
	case async.Pending, async.Streaming:
		return "处理中"
	case async.Failed:
		return "需要处理"
	case async.Succeeded:
		if model.aggregate == nil {
			return "未加载"
		}
		if model.aggregate.ConfirmedAt != nil {
			return "已保存"
		}
		return "待确认"
	default:
		return ""
	}
}

func (model *Model) profileStatus() string {
	if model.aggregate == nil {
		return "empty"
	}
	return fmt.Sprintf(
		"%d facts · %d verify",
		len(model.aggregate.Candidate.Facts),
		len(model.aggregate.Candidate.Inferences),
	)
}

func (model *Model) badge() components.StatusBadge {
	badge := components.StatusBadge{
		State: components.BadgeWarning,
		Text:  "profile not loaded",
	}
	if model.isBusy() {
		badge.Text = "profile processing"
		return badge
	}
	if model.operation.Phase == async.Failed {
		badge.State = components.BadgeError
		badge.Text = "profile action failed"
		return badge
	}
	if model.aggregate != nil {
		badge.State = components.BadgeReady
		badge.Text = "profile local"
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
	return "画像工作台已就绪"
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

func compactValues(values []string, width int, useASCII bool) string {
	return layout.TruncateRight(
		strings.Join(values, "  "),
		width,
		useASCII,
	)
}

func containsEvidenceID(
	values []contracts.EvidenceID,
	target contracts.EvidenceID,
) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
