package transfer

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
)

// Export writes a transfer package or standalone report without overwriting an
// existing path.
func (service *Service) Export(
	ctx context.Context,
	options ExportOptions,
	observer Observer,
) (ExportResult, error) {
	notify(observer, async.NewPending[Progress]())
	format := options.Format
	if format == "" {
		format = FormatPackage
	}
	if format != FormatPackage && format != FormatJSON && format != FormatMarkdown {
		return ExportResult{}, fail(observer, domainerr.New(
			domainerr.CodeValidation,
			"export local data",
			"导出格式必须是 package、json 或 markdown。",
			"修正 `--format` 后重试。",
			false,
		))
	}
	outputPath, err := cleanOutputPath(options.OutputPath)
	if err != nil {
		return ExportResult{}, fail(observer, err)
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return ExportResult{}, fail(observer, domainerr.New(
			domainerr.CodeInvalidState,
			"export local data",
			"导出目标已存在，未覆盖原文件。",
			"指定一个新文件名后重试。",
			false,
		))
	} else if !errors.Is(err, os.ErrNotExist) {
		return ExportResult{}, fail(observer, storageFailure("inspect export target", err))
	}

	stream(observer, "reading_local_data", 1, 4, "正在读取本地训练数据")
	var payload []byte
	recordCount := 0
	switch format {
	case FormatPackage:
		bundle, count, err := service.snapshot(ctx, options.IncludeCoachContent)
		if err != nil {
			return ExportResult{}, fail(observer, err)
		}
		stream(observer, "validating_links", 2, 4, "正在校验 ID 与证据关系")
		payload, err = json.MarshalIndent(bundle, "", "  ")
		if err != nil {
			return ExportResult{}, fail(observer, storageFailure("encode transfer package", err))
		}
		recordCount = count
	case FormatJSON, FormatMarkdown:
		reportExport, err := service.loadReportExport(
			ctx,
			strings.TrimSpace(options.SessionID),
			options.IncludeCoachContent,
		)
		if err != nil {
			return ExportResult{}, fail(observer, err)
		}
		stream(observer, "validating_links", 2, 4, "正在校验报告证据链接")
		if format == FormatJSON {
			payload, err = json.MarshalIndent(reportExport, "", "  ")
		} else {
			payload = []byte(renderMarkdown(reportExport))
		}
		if err != nil {
			return ExportResult{}, fail(observer, storageFailure("encode report export", err))
		}
		recordCount = 1 + len(reportExport.CoachTranscript)
	}
	stream(observer, "writing_artifact", 3, 4, "正在原子写入导出文件")
	if err := writeNewFile(outputPath, payload); err != nil {
		return ExportResult{}, fail(observer, err)
	}
	completed := Progress{
		Stage: "completed", Current: 4, Total: 4,
		Message: "导出文件已写入",
	}
	notify(observer, async.NewSucceeded(completed))
	return ExportResult{Path: outputPath, Format: format, RecordCount: recordCount}, nil
}

// Import restores one strict transfer package into an empty initialized Lite
// database in one transaction.
func (service *Service) Import(
	ctx context.Context,
	inputPath string,
	observer Observer,
) (ImportResult, error) {
	notify(observer, async.NewPending[Progress]())
	inputPath = strings.TrimSpace(inputPath)
	if inputPath == "" {
		return ImportResult{}, fail(observer, domainerr.New(
			domainerr.CodeValidation,
			"import transfer package",
			"迁移包路径不能为空。",
			"使用 `--input` 指定迁移包。",
			false,
		))
	}
	absolute, err := filepath.Abs(inputPath)
	if err != nil {
		return ImportResult{}, fail(observer, storageFailure("resolve transfer input", err))
	}
	stream(observer, "reading_package", 1, 6, "正在读取迁移包")
	payload, err := readBoundedFile(absolute)
	if err != nil {
		return ImportResult{}, fail(observer, err)
	}
	stream(observer, "validating_package", 2, 6, "正在校验版本、ID 与证据关系")
	bundle, err := decodeBundle(payload)
	if err != nil {
		return ImportResult{}, fail(observer, err)
	}
	result, err := service.restore(ctx, bundle, observer)
	if err != nil {
		return ImportResult{}, fail(observer, err)
	}
	completed := Progress{
		Stage: "completed", Current: 6, Total: 6,
		Message: "迁移包已完整恢复",
	}
	notify(observer, async.NewSucceeded(completed))
	return result, nil
}

// Delete removes one session graph or all training data only after exact
// phrase confirmation. Provider configuration is always retained.
func (service *Service) Delete(
	ctx context.Context,
	confirmation Confirmation,
	observer Observer,
) (int64, error) {
	notify(observer, async.NewPending[Progress]())
	if err := validateConfirmation(confirmation); err != nil {
		return 0, fail(observer, err)
	}
	database, err := service.open(ctx)
	if err != nil {
		return 0, fail(observer, err)
	}
	defer database.Close()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return 0, fail(observer, storageFailure("begin data deletion", err))
	}
	defer transaction.Rollback()
	stream(observer, "deleting_training_data", 1, 2, "正在事务删除训练数据")
	var affected int64
	switch confirmation.Scope {
	case DeleteSession:
		result, err := transaction.ExecContext(
			ctx,
			`DELETE FROM sessions WHERE id = ?`,
			confirmation.SessionID,
		)
		if err != nil {
			return 0, fail(observer, storageFailure("delete session data", err))
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return 0, fail(observer, storageFailure("read session deletion result", err))
		}
	case DeleteAll:
		var sessions int64
		if err := transaction.QueryRowContext(
			ctx,
			`SELECT COUNT(*) FROM sessions`,
		).Scan(&sessions); err != nil {
			return 0, fail(observer, storageFailure("count training data", err))
		}
		result, err := transaction.ExecContext(ctx, `DELETE FROM candidate_profiles`)
		if err != nil {
			return 0, fail(observer, storageFailure("delete all training data", err))
		}
		profiles, err := result.RowsAffected()
		if err != nil {
			return 0, fail(observer, storageFailure("read all-data deletion result", err))
		}
		affected = profiles + sessions
	}
	if affected == 0 {
		return 0, fail(observer, domainerr.New(
			domainerr.CodeInvalidState,
			"delete training data",
			"没有匹配的训练数据可删除。",
			"刷新 Data 区后重新选择删除范围。",
			false,
		))
	}
	if err := service.runBeforeCommit("delete"); err != nil {
		return 0, fail(observer, storageFailure("commit data deletion", err))
	}
	if err := transaction.Commit(); err != nil {
		return 0, fail(observer, storageFailure("commit data deletion", err))
	}
	completed := Progress{
		Stage: "completed", Current: 2, Total: 2,
		Message: "训练数据已删除",
	}
	notify(observer, async.NewSucceeded(completed))
	return affected, nil
}

func (service *Service) loadReportExport(
	ctx context.Context,
	sessionID string,
	includeCoachContent bool,
) (ReportExport, error) {
	if sessionID == "" {
		return ReportExport{}, domainerr.New(
			domainerr.CodeValidation,
			"export report",
			"导出 Markdown 或 JSON 报告时必须指定会话 ID。",
			"使用 `--session <id>` 选择报告。",
			false,
		)
	}
	database, err := service.open(ctx)
	if err != nil {
		return ReportExport{}, err
	}
	defer database.Close()
	var payload string
	err = database.QueryRowContext(
		ctx,
		`SELECT payload_json FROM reports WHERE session_id = ?`,
		sessionID,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return ReportExport{}, domainerr.New(
			domainerr.CodeInvalidState,
			"export report",
			"所选会话还没有可导出的报告。",
			"完成会话评估后再导出。",
			false,
		)
	}
	if err != nil {
		return ReportExport{}, storageFailure("read report export", err)
	}
	document, err := corereport.Decode([]byte(payload))
	if err != nil {
		return ReportExport{}, err
	}
	result := ReportExport{
		Version: ReportExportVersion, ExportedAt: service.now().UTC(),
		CoachContentIncluded: includeCoachContent,
		Report:               document, CoachTranscript: []CoachExcerpt{},
	}
	if !includeCoachContent {
		return result, nil
	}
	rows, err := database.QueryContext(ctx, `
		SELECT id, question_id, help_level, content, policy_note, occurred_at
		FROM sidebar_events
		WHERE session_id = ?
		ORDER BY occurred_at, id
	`, sessionID)
	if err != nil {
		return ReportExport{}, storageFailure("read Coach transcript export", err)
	}
	defer rows.Close()
	for rows.Next() {
		var item CoachExcerpt
		var occurredAt string
		if err := rows.Scan(
			&item.ID,
			&item.QuestionID,
			&item.HelpLevel,
			&item.Content,
			&item.PolicyNote,
			&occurredAt,
		); err != nil {
			return ReportExport{}, storageFailure("scan Coach transcript export", err)
		}
		item.OccurredAt, err = time.Parse(time.RFC3339Nano, occurredAt)
		if err != nil {
			return ReportExport{}, storageFailure("parse Coach transcript time", err)
		}
		result.CoachTranscript = append(result.CoachTranscript, item)
	}
	if err := rows.Err(); err != nil {
		return ReportExport{}, storageFailure("read Coach transcript export", err)
	}
	return result, nil
}

func validateConfirmation(confirmation Confirmation) error {
	confirmation.SessionID = strings.TrimSpace(confirmation.SessionID)
	confirmation.Phrase = strings.TrimSpace(confirmation.Phrase)
	switch confirmation.Scope {
	case DeleteSession:
		if confirmation.SessionID == "" ||
			confirmation.Phrase != SessionDeletePhrase(confirmation.SessionID) {
			return domainerr.New(
				domainerr.CodePolicyDenied,
				"confirm session deletion",
				"删除单场训练前必须再次确认。",
				"输入精确确认短语后重试。",
				false,
			)
		}
	case DeleteAll:
		if confirmation.SessionID != "" || confirmation.Phrase != AllDeletePhrase() {
			return domainerr.New(
				domainerr.CodePolicyDenied,
				"confirm all-data deletion",
				"删除全部训练数据前必须再次确认。",
				"输入精确确认短语后重试。",
				false,
			)
		}
	default:
		return domainerr.New(
			domainerr.CodeValidation,
			"confirm data deletion",
			"删除范围无效。",
			"选择单场训练或全部训练数据。",
			false,
		)
	}
	return nil
}

func readBoundedFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, storageFailure("read transfer package", err)
	}
	if info.IsDir() || info.Size() > maxPackageBytes {
		return nil, domainerr.New(
			domainerr.CodeValidation,
			"read transfer package",
			"迁移包必须是小于 64MB 的文件。",
			"选择有效的 InterviewCraft 迁移包。",
			false,
		)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, storageFailure("open transfer package", err)
	}
	defer file.Close()
	payload, err := io.ReadAll(io.LimitReader(file, maxPackageBytes+1))
	if err != nil {
		return nil, storageFailure("read transfer package", err)
	}
	if len(payload) > maxPackageBytes {
		return nil, domainerr.New(
			domainerr.CodeValidation,
			"read transfer package",
			"迁移包超过 64MB 限制。",
			"选择有效的 InterviewCraft 迁移包。",
			false,
		)
	}
	return payload, nil
}

func writeNewFile(path string, payload []byte) error {
	directory := filepath.Dir(path)
	info, err := os.Stat(directory)
	if err != nil || !info.IsDir() {
		return storageFailure("write transfer artifact", nonNilError(err, "output parent is not a directory"))
	}
	temporary, err := os.CreateTemp(directory, ".interviewcraft-export-*")
	if err != nil {
		return storageFailure("create transfer artifact", err)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return storageFailure("protect transfer artifact", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return storageFailure("write transfer artifact", err)
	}
	if err := temporary.Sync(); err != nil {
		return storageFailure("sync transfer artifact", err)
	}
	if err := temporary.Close(); err != nil {
		return storageFailure("close transfer artifact", err)
	}
	if _, err := os.Lstat(path); err == nil {
		return domainerr.New(
			domainerr.CodeInvalidState,
			"write transfer artifact",
			"导出目标已存在，未覆盖原文件。",
			"指定一个新文件名后重试。",
			false,
		)
	} else if !errors.Is(err, os.ErrNotExist) {
		return storageFailure("inspect transfer artifact target", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return storageFailure("commit transfer artifact", err)
	}
	committed = true
	return nil
}

func renderMarkdown(value ReportExport) string {
	document := value.Report
	var output bytes.Buffer
	fmt.Fprintf(&output, "# InterviewCraft report: %s\n\n", markdownText(document.Summary.Template))
	fmt.Fprintf(&output, "- Session: `%s`\n", document.Summary.SessionID)
	fmt.Fprintf(&output, "- Mode: `%s`\n", document.Summary.Mode)
	fmt.Fprintf(&output, "- Duration: %d seconds\n", document.Summary.DurationSeconds)
	fmt.Fprintf(&output, "- Questions: %d\n", document.Summary.QuestionCount)
	fmt.Fprintf(&output, "- Coach prompts: %d\n", document.Summary.CoachPromptCount)
	fmt.Fprintf(&output, "- Code runs: %d\n", document.Summary.CodeRunCount)
	fmt.Fprintf(&output, "- Generated: %s\n\n", document.GeneratedAt.Format(time.RFC3339))

	output.WriteString("## Scorecard\n\n")
	output.WriteString("| Dimension | Assessment | Evidence | Next action |\n")
	output.WriteString("|---|---|---|---|\n")
	for _, item := range document.Scorecard {
		assessment := string(item.Status)
		if item.Score != nil {
			assessment = fmt.Sprintf("%d/5", *item.Score)
		}
		fmt.Fprintf(
			&output,
			"| %s | %s | %s | %s |\n",
			markdownCell(string(item.Dimension)),
			markdownCell(assessment),
			markdownCell(evidenceList(item.EvidenceIDs)),
			markdownCell(item.NextAction),
		)
	}

	output.WriteString("\n## Question review\n\n")
	for _, item := range document.QuestionReview {
		fmt.Fprintf(&output, "### %s\n\n", markdownText(item.QuestionID))
		fmt.Fprintf(&output, "%s\n\n", markdownText(item.Prompt))
		fmt.Fprintf(&output, "- Summary: %s (%s)\n", markdownText(item.Summary.Text), evidenceList(item.Summary.EvidenceIDs))
		fmt.Fprintf(&output, "- Next action: %s (%s)\n\n", markdownText(item.NextAction.Text), evidenceList(item.NextAction.EvidenceIDs))
	}

	output.WriteString("## Learning map\n\n")
	if len(document.LearningMap) == 0 {
		output.WriteString("No Coach learning gaps recorded.\n\n")
	}
	for _, gap := range document.LearningMap {
		fmt.Fprintf(
			&output,
			"- **%s** — %d asks, highest help %s, questions %s\n",
			markdownText(gap.Topic),
			gap.AskCount,
			gap.MaxHelpLevel,
			markdownText(strings.Join(gap.QuestionIDs, ", ")),
		)
	}

	output.WriteString("\n## Practice next\n\n")
	for _, item := range document.PracticePlan {
		fmt.Fprintf(
			&output,
			"- **%s** — %s, %d minutes. %s\n",
			markdownText(item.Topic),
			item.Mode,
			item.DurationMinutes,
			markdownText(item.CompletionCriteria),
		)
	}

	output.WriteString("\n## Evidence index\n\n")
	for _, item := range document.Evidence {
		fmt.Fprintf(
			&output,
			"- `%s` — %s, %s, %s\n",
			item.ID,
			markdownText(item.Kind),
			markdownText(item.QuestionID),
			item.OccurredAt.Format(time.RFC3339),
		)
	}
	if value.CoachContentIncluded {
		output.WriteString("\n## Coach transcript (explicitly included)\n\n")
		for _, item := range value.CoachTranscript {
			fmt.Fprintf(
				&output,
				"- `%s` %s %s: %s\n",
				item.ID,
				item.QuestionID,
				item.HelpLevel,
				markdownText(item.Content),
			)
		}
	}
	return output.String()
}

func evidenceList[T ~string](values []T) string {
	if len(values) == 0 {
		return "evidence unavailable"
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, string(value))
	}
	return strings.Join(result, ", ")
}

func markdownText(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "\n", " ")
}

func markdownCell(value string) string {
	return strings.ReplaceAll(markdownText(value), "|", "\\|")
}

func fail(observer Observer, err error) error {
	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		typed = storageFailure("transfer operation", err)
	}
	notify(observer, async.NewFailed[Progress](typed))
	return typed
}
