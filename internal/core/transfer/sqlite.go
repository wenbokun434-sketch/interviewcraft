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
	"slices"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
	_ "modernc.org/sqlite"
)

type tableSpec struct {
	name       string
	columns    []string
	selectExpr []string
	orderBy    string
}

var transferTables = []tableSpec{
	{
		name:    "candidate_profiles",
		columns: []string{"id", "payload_json", "confirmed_at", "created_at", "updated_at"},
		orderBy: "id",
	},
	{
		name: "profile_sources",
		columns: []string{
			"profile_id", "source_kind", "source_name", "source_text",
			"locked_fact_ids_json", "locked_inference_ids_json",
			"created_at", "updated_at",
		},
		orderBy: "profile_id",
	},
	{
		name: "scenarios",
		columns: []string{
			"id", "profile_id", "payload_json", "prompt_version",
			"confirmed_at", "created_at", "updated_at",
		},
		orderBy: "id",
	},
	{
		name:    "sessions",
		columns: []string{"id", "scenario_id", "status", "started_at", "updated_at"},
		orderBy: "id",
	},
	{
		name: "session_events",
		columns: []string{
			"sequence", "event_id", "session_id", "speaker", "question_id",
			"content", "occurred_at", "evidence_refs_json",
		},
		orderBy: "session_id, occurred_at, sequence",
	},
	{
		name:    "drafts",
		columns: []string{"session_id", "question_id", "kind", "content", "updated_at"},
		orderBy: "session_id, question_id, kind",
	},
	{
		name: "sidebar_events",
		columns: []string{
			"id", "session_id", "question_id", "intent", "help_level",
			"tags_json", "outcome", "paused_timer", "occurred_at",
			"content", "policy_note",
		},
		orderBy: "session_id, occurred_at, id",
	},
	{
		name:    "coach_usage",
		columns: []string{"event_id", "session_id", "question_id", "occurred_at"},
		orderBy: "session_id, occurred_at, event_id",
	},
	{
		name: "code_submissions",
		columns: []string{
			"id", "session_id", "question_id", "language", "source",
			"test_result_json", "runtime_stats_json", "snapshot_id", "created_at",
		},
		orderBy: "session_id, created_at, id",
	},
	{
		name:    "reports",
		columns: []string{"id", "session_id", "payload_json", "created_at", "updated_at"},
		orderBy: "session_id, id",
	},
}

// Options injects deterministic time and an optional transaction fault hook.
type Options struct {
	Now          func() time.Time
	BeforeCommit func(operation string) error
}

// Service owns transfer operations against one initialized Lite database.
type Service struct {
	databasePath string
	now          func() time.Time
	beforeCommit func(string) error
}

// NewService creates a transfer service. It does not create or migrate a DB.
func NewService(databasePath string, options Options) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{
		databasePath: strings.TrimSpace(databasePath),
		now:          now,
		beforeCommit: options.BeforeCommit,
	}
}

// Inventory returns only counts and session IDs, never content or credentials.
func (service *Service) Inventory(ctx context.Context) (Inventory, error) {
	database, err := service.open(ctx)
	if err != nil {
		return Inventory{}, err
	}
	defer database.Close()
	result := Inventory{SessionIDs: []string{}}
	counts := []struct {
		table  string
		target *int
	}{
		{"candidate_profiles", &result.Profiles},
		{"scenarios", &result.Scenarios},
		{"sessions", &result.Sessions},
		{"reports", &result.Reports},
		{"sidebar_events", &result.CoachItems},
	}
	for _, item := range counts {
		if err := database.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+item.table,
		).Scan(item.target); err != nil {
			return Inventory{}, storageFailure("read data inventory", err)
		}
	}
	rows, err := database.QueryContext(ctx, `
		SELECT id FROM sessions ORDER BY updated_at DESC, id
	`)
	if err != nil {
		return Inventory{}, storageFailure("list data inventory sessions", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return Inventory{}, storageFailure("read data inventory session", err)
		}
		result.SessionIDs = append(result.SessionIDs, id)
	}
	if err := rows.Err(); err != nil {
		return Inventory{}, storageFailure("list data inventory sessions", err)
	}
	return result, nil
}

func (service *Service) open(ctx context.Context) (*sql.DB, error) {
	if service == nil || service.databasePath == "" {
		return nil, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"open transfer storage",
			"本地迁移存储不可用。",
			"先运行 `interviewcraft init`，再重试数据操作。",
			true,
		)
	}
	info, err := os.Stat(service.databasePath)
	if err != nil || info.IsDir() {
		return nil, storageFailure("open transfer storage", nonNilError(err, "database path is a directory"))
	}
	database, err := sql.Open("sqlite", service.databasePath)
	if err != nil {
		return nil, storageFailure("open transfer storage", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	for _, statement := range []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
	} {
		if _, err := database.ExecContext(ctx, statement); err != nil {
			_ = database.Close()
			return nil, storageFailure("configure transfer storage", err)
		}
	}
	if err := database.PingContext(ctx); err != nil {
		_ = database.Close()
		return nil, storageFailure("connect transfer storage", err)
	}
	return database, nil
}

func (service *Service) snapshot(
	ctx context.Context,
	includeCoachContent bool,
) (Bundle, int, error) {
	database, err := service.open(ctx)
	if err != nil {
		return Bundle{}, 0, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Bundle{}, 0, storageFailure("begin transfer snapshot", err)
	}
	defer transaction.Rollback()
	bundle := Bundle{
		Version:              BundleVersion,
		ExportedAt:           service.now().UTC(),
		CoachContentIncluded: includeCoachContent,
		Tables:               make([]Table, 0, len(transferTables)),
	}
	recordCount := 0
	for _, spec := range transferTables {
		table, err := readTable(ctx, transaction, spec, includeCoachContent)
		if err != nil {
			return Bundle{}, 0, err
		}
		recordCount += len(table.Rows)
		bundle.Tables = append(bundle.Tables, table)
	}
	if err := transaction.Commit(); err != nil {
		return Bundle{}, 0, storageFailure("finish transfer snapshot", err)
	}
	if recordCount == 0 {
		return Bundle{}, 0, domainerr.New(
			domainerr.CodeInvalidState,
			"export transfer package",
			"还没有可导出的训练数据。",
			"完成或保存一场训练后再导出。",
			false,
		)
	}
	if err := validateBundle(bundle); err != nil {
		return Bundle{}, 0, err
	}
	return bundle, recordCount, nil
}

func readTable(
	ctx context.Context,
	transaction *sql.Tx,
	spec tableSpec,
	includeCoachContent bool,
) (Table, error) {
	selectExpr := slices.Clone(spec.columns)
	if len(spec.selectExpr) > 0 {
		selectExpr = slices.Clone(spec.selectExpr)
	}
	if spec.name == "sidebar_events" && !includeCoachContent {
		selectExpr[len(selectExpr)-2] = `'' AS content`
		selectExpr[len(selectExpr)-1] = `'' AS policy_note`
	}
	statement := "SELECT " + strings.Join(selectExpr, ", ") +
		" FROM " + spec.name + " ORDER BY " + spec.orderBy
	rows, err := transaction.QueryContext(ctx, statement)
	if err != nil {
		return Table{}, storageFailure("read transfer table "+spec.name, err)
	}
	defer rows.Close()
	table := Table{
		Name: spec.name, Columns: slices.Clone(spec.columns),
		Rows: make([][]json.RawMessage, 0),
	}
	for rows.Next() {
		values := make([]any, len(spec.columns))
		targets := make([]any, len(values))
		for index := range values {
			targets[index] = &values[index]
		}
		if err := rows.Scan(targets...); err != nil {
			return Table{}, storageFailure("scan transfer table "+spec.name, err)
		}
		encoded := make([]json.RawMessage, len(values))
		for index, value := range values {
			cell, err := encodeCell(value)
			if err != nil {
				return Table{}, storageFailure("encode transfer table "+spec.name, err)
			}
			encoded[index] = cell
		}
		table.Rows = append(table.Rows, encoded)
	}
	if err := rows.Err(); err != nil {
		return Table{}, storageFailure("read transfer table "+spec.name, err)
	}
	return table, nil
}

func encodeCell(value any) (json.RawMessage, error) {
	switch typed := value.(type) {
	case nil:
		return json.RawMessage("null"), nil
	case []byte:
		encoded, err := json.Marshal(string(typed))
		return json.RawMessage(encoded), err
	case string:
		encoded, err := json.Marshal(typed)
		return json.RawMessage(encoded), err
	case int64, float64, bool:
		encoded, err := json.Marshal(typed)
		return json.RawMessage(encoded), err
	default:
		return nil, fmt.Errorf("unsupported SQLite value %T", value)
	}
}

func (service *Service) restore(
	ctx context.Context,
	bundle Bundle,
	observer Observer,
) (ImportResult, error) {
	database, err := service.open(ctx)
	if err != nil {
		return ImportResult{}, err
	}
	defer database.Close()
	transaction, err := database.BeginTx(ctx, nil)
	if err != nil {
		return ImportResult{}, storageFailure("begin transfer import", err)
	}
	defer transaction.Rollback()
	if err := ensureEmptyTarget(ctx, transaction); err != nil {
		return ImportResult{}, err
	}
	stream(observer, "restoring_profiles", 3, 6, "正在恢复画像与场景")
	for index, table := range bundle.Tables {
		if index == 3 {
			stream(observer, "restoring_sessions", 4, 6, "正在恢复会话与证据")
		}
		if index == len(bundle.Tables)-1 {
			stream(observer, "restoring_reports", 5, 6, "正在恢复报告与训练计划")
		}
		if err := insertTable(ctx, transaction, transferTables[index], table); err != nil {
			return ImportResult{}, err
		}
	}
	if service.beforeCommit != nil {
		if err := service.beforeCommit("import"); err != nil {
			return ImportResult{}, storageFailure("commit transfer import", err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return ImportResult{}, storageFailure("commit transfer import", err)
	}
	return ImportResult{
		Profiles: len(bundle.Tables[0].Rows),
		Sessions: len(bundle.Tables[3].Rows),
		Reports:  len(bundle.Tables[len(bundle.Tables)-1].Rows),
	}, nil
}

func ensureEmptyTarget(ctx context.Context, transaction *sql.Tx) error {
	for _, spec := range transferTables {
		var count int
		if err := transaction.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+spec.name,
		).Scan(&count); err != nil {
			return storageFailure("check import target", err)
		}
		if count != 0 {
			return domainerr.New(
				domainerr.CodeInvalidState,
				"import transfer package",
				"目标 Lite 实例已有训练数据，导入已停止。",
				"使用空实例导入，或先显式导出并删除现有数据。",
				false,
			)
		}
	}
	return nil
}

func insertTable(
	ctx context.Context,
	transaction *sql.Tx,
	spec tableSpec,
	table Table,
) error {
	if len(table.Rows) == 0 {
		return nil
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(spec.columns)), ",")
	statement := "INSERT INTO " + spec.name + " (" +
		strings.Join(spec.columns, ",") + ") VALUES (" + placeholders + ")"
	for rowIndex, row := range table.Rows {
		values := make([]any, len(row))
		for index, cell := range row {
			value, err := decodeCell(cell)
			if err != nil {
				return invalidPackage(fmt.Sprintf("%s row %d contains an invalid cell", spec.name, rowIndex+1), err)
			}
			values[index] = value
		}
		if _, err := transaction.ExecContext(ctx, statement, values...); err != nil {
			return storageFailure("restore transfer table "+spec.name, err)
		}
	}
	return nil
}

func decodeCell(cell json.RawMessage) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(cell))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	switch typed := value.(type) {
	case nil, string, bool:
		return typed, nil
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer, nil
		}
		return typed.Float64()
	default:
		return nil, fmt.Errorf("transfer cell must be a JSON scalar")
	}
}

func validateBundle(bundle Bundle) error {
	if bundle.Version != BundleVersion {
		return domainerr.New(
			domainerr.CodeValidation,
			"validate transfer package",
			"迁移包版本不兼容。",
			"使用与该迁移包版本兼容的 InterviewCraft 导入。",
			false,
		)
	}
	if bundle.ExportedAt.IsZero() || len(bundle.Tables) != len(transferTables) {
		return invalidPackage("迁移包元数据或固定表清单不完整。", nil)
	}
	recordCount := 0
	for index, spec := range transferTables {
		table := bundle.Tables[index]
		if table.Name != spec.name || !slices.Equal(table.Columns, spec.columns) || table.Rows == nil {
			return invalidPackage("迁移包表结构与当前版本不一致。", nil)
		}
		for _, row := range table.Rows {
			if len(row) != len(spec.columns) {
				return invalidPackage("迁移包记录字段数量无效。", nil)
			}
			for _, cell := range row {
				if _, err := decodeCell(cell); err != nil {
					return invalidPackage("迁移包包含无效字段。", err)
				}
			}
		}
		recordCount += len(table.Rows)
	}
	if recordCount == 0 {
		return invalidPackage("迁移包不包含任何训练数据。", nil)
	}
	if err := validateGraph(bundle); err != nil {
		return err
	}
	if !bundle.CoachContentIncluded {
		for _, row := range bundle.Tables[6].Rows {
			content, _ := stringCell(row, 9)
			policy, _ := stringCell(row, 10)
			if content != "" || policy != "" {
				return invalidPackage("迁移包声明不含 Coach 原文但仍携带正文。", nil)
			}
		}
	}
	return nil
}

func validateGraph(bundle Bundle) error {
	profiles := make(map[string]contracts.CandidateProfile)
	for _, row := range bundle.Tables[0].Rows {
		id, err := requiredStringCell(row, 0, "profile id")
		if err != nil {
			return err
		}
		if _, duplicate := profiles[id]; duplicate {
			return invalidPackage("迁移包包含重复画像 ID。", nil)
		}
		payload, err := requiredStringCell(row, 1, "profile payload")
		if err != nil {
			return err
		}
		profile, err := contracts.DecodeCandidateProfile([]byte(payload))
		if err != nil {
			return invalidPackage("迁移包画像载荷无效。", err)
		}
		profiles[id] = profile
	}
	for _, row := range bundle.Tables[1].Rows {
		profileID, err := requiredStringCell(row, 0, "profile source profile id")
		if err != nil {
			return err
		}
		if _, found := profiles[profileID]; !found {
			return invalidPackage("画像来源引用了不存在的画像。", nil)
		}
		for _, index := range []int{4, 5} {
			value, err := requiredStringCell(row, index, "profile source JSON")
			if err != nil || !json.Valid([]byte(value)) {
				return invalidPackage("画像来源包含无效 JSON。", err)
			}
		}
	}

	scenarios := make(map[string]string)
	for _, row := range bundle.Tables[2].Rows {
		id, err := requiredStringCell(row, 0, "scenario id")
		if err != nil {
			return err
		}
		profileID, err := requiredStringCell(row, 1, "scenario profile id")
		if err != nil {
			return err
		}
		profile, found := profiles[profileID]
		if !found {
			return invalidPackage("场景引用了不存在的画像。", nil)
		}
		if _, duplicate := scenarios[id]; duplicate {
			return invalidPackage("迁移包包含重复场景 ID。", nil)
		}
		payload, err := requiredStringCell(row, 2, "scenario payload")
		if err != nil {
			return err
		}
		scenario, err := contracts.DecodeScenario([]byte(payload))
		if err != nil {
			return invalidPackage("迁移包场景载荷无效。", err)
		}
		facts := make(map[contracts.EvidenceID]struct{}, len(profile.Facts))
		for _, fact := range profile.Facts {
			facts[fact.ID] = struct{}{}
		}
		for _, question := range scenario.Questions {
			for _, evidenceID := range question.EvidenceIDs {
				if _, exists := facts[evidenceID]; !exists {
					return invalidPackage("场景证据未解析到对应画像事实。", nil)
				}
			}
		}
		scenarios[id] = profileID
	}

	sessions := make(map[string]struct{})
	for _, row := range bundle.Tables[3].Rows {
		id, err := requiredStringCell(row, 0, "session id")
		if err != nil {
			return err
		}
		scenarioID, err := requiredStringCell(row, 1, "session scenario id")
		if err != nil {
			return err
		}
		if _, found := scenarios[scenarioID]; !found {
			return invalidPackage("会话引用了不存在的场景。", nil)
		}
		if _, duplicate := sessions[id]; duplicate {
			return invalidPackage("迁移包包含重复会话 ID。", nil)
		}
		sessions[id] = struct{}{}
	}
	childSessionColumns := map[int]int{4: 2, 5: 0, 6: 1, 7: 1, 8: 1, 9: 1}
	for tableIndex, sessionColumn := range childSessionColumns {
		for _, row := range bundle.Tables[tableIndex].Rows {
			sessionID, err := requiredStringCell(row, sessionColumn, "child session id")
			if err != nil {
				return err
			}
			if _, found := sessions[sessionID]; !found {
				return invalidPackage("会话子记录引用了不存在的会话。", nil)
			}
		}
	}
	for _, row := range bundle.Tables[4].Rows {
		payload, err := requiredStringCell(row, 7, "event evidence refs")
		if err != nil || !json.Valid([]byte(payload)) {
			return invalidPackage("会话事件证据引用 JSON 无效。", err)
		}
	}
	for _, row := range bundle.Tables[6].Rows {
		payload, err := requiredStringCell(row, 5, "Coach tags")
		if err != nil || !json.Valid([]byte(payload)) {
			return invalidPackage("Coach 标签 JSON 无效。", err)
		}
	}
	for _, row := range bundle.Tables[8].Rows {
		for _, index := range []int{5, 6} {
			payload, err := requiredStringCell(row, index, "code result JSON")
			if err != nil || !json.Valid([]byte(payload)) {
				return invalidPackage("代码结果 JSON 无效。", err)
			}
		}
	}
	for _, row := range bundle.Tables[9].Rows {
		id, err := requiredStringCell(row, 0, "report id")
		if err != nil {
			return err
		}
		sessionID, err := requiredStringCell(row, 1, "report session id")
		if err != nil {
			return err
		}
		payload, err := requiredStringCell(row, 2, "report payload")
		if err != nil {
			return err
		}
		document, err := corereport.Decode([]byte(payload))
		if err != nil || document.ID != id || document.Summary.SessionID != sessionID {
			return invalidPackage("报告 ID、会话或证据关系无效。", err)
		}
	}
	return nil
}

func stringCell(row []json.RawMessage, index int) (string, error) {
	if index < 0 || index >= len(row) {
		return "", fmt.Errorf("cell index out of range")
	}
	var value string
	if err := json.Unmarshal(row[index], &value); err != nil {
		return "", err
	}
	return value, nil
}

func requiredStringCell(
	row []json.RawMessage,
	index int,
	field string,
) (string, error) {
	value, err := stringCell(row, index)
	if err != nil || strings.TrimSpace(value) == "" {
		return "", invalidPackage(field+" 不能为空。", err)
	}
	return value, nil
}

func decodeBundle(payload []byte) (Bundle, error) {
	var bundle Bundle
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&bundle); err != nil {
		return Bundle{}, invalidPackage("迁移包不是有效的严格 JSON。", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return Bundle{}, invalidPackage("迁移包包含多个 JSON 值。", nil)
		}
		return Bundle{}, invalidPackage("迁移包尾部包含无效内容。", err)
	}
	if err := validateBundle(bundle); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func notify(observer Observer, state async.State[Progress]) {
	if observer != nil {
		observer(state)
	}
}

func stream(observer Observer, stage string, current, total int, message string) {
	progress := Progress{Stage: stage, Current: current, Total: total, Message: message}
	notify(observer, async.NewStreaming(&progress))
}

func invalidPackage(message string, cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"validate transfer package",
		"transfer package",
		message,
		"使用未修改且版本兼容的迁移包重试。",
		false,
		cause,
	)
}

func storageFailure(operation string, cause error) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"local transfer storage",
		"无法完成本地数据操作。",
		"检查数据库与目标路径权限后重试；原数据保持不变。",
		true,
		cause,
	)
}

func nonNilError(err error, fallback string) error {
	if err != nil {
		return err
	}
	return errors.New(fallback)
}

func (service *Service) runBeforeCommit(operation string) error {
	if service.beforeCommit == nil {
		return nil
	}
	return service.beforeCommit(operation)
}

func cleanOutputPath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", domainerr.New(
			domainerr.CodeValidation,
			"resolve transfer output",
			"导出路径不能为空。",
			"使用 `--output` 指定新文件路径。",
			false,
		)
	}
	absolute, err := filepath.Abs(value)
	if err != nil {
		return "", storageFailure("resolve transfer output", err)
	}
	return absolute, nil
}
