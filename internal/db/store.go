// Package db owns InterviewCraft's local SQLite layout, migrations, and
// persistence boundary.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	_ "modernc.org/sqlite"
)

const defaultDatabaseName = "interviewcraft.db"

// Config selects the local data directory and optional database file name.
type Config struct {
	DataDir      string
	DatabaseName string
}

// Paths are the local resources created before SQLite is opened.
type Paths struct {
	DataDir  string
	Database string
	Uploads  string
	Exports  string
	Logs     string
}

// MigrationProgress is emitted while deterministic migrations run.
type MigrationProgress struct {
	Current int
	Total   int
	Name    string
}

// MigrationObserver receives typed lifecycle states. It must return quickly.
type MigrationObserver func(async.State[MigrationProgress])

// Store is a single-process SQLite persistence boundary.
type Store struct {
	sql   *sql.DB
	paths Paths
}

// Open creates the local layout, opens SQLite, and applies pending migrations.
func Open(ctx context.Context, config Config, observer MigrationObserver) (*Store, error) {
	return openWithMigrations(ctx, config, observer, defaultMigrations)
}

func openWithMigrations(
	ctx context.Context,
	config Config,
	observer MigrationObserver,
	migrations []migration,
) (*Store, error) {
	notify(observer, async.NewPending[MigrationProgress]())

	paths, typedErr := ensureLayout(config)
	if typedErr != nil {
		notify(observer, async.NewFailed[MigrationProgress](typedErr))
		return nil, typedErr
	}

	database, err := sql.Open("sqlite", paths.Database)
	if err != nil {
		typedErr = storageError(
			"open SQLite",
			paths.Database,
			"检查数据库路径和文件权限后重试。",
			err,
		)
		notify(observer, async.NewFailed[MigrationProgress](typedErr))
		return nil, typedErr
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)

	store := &Store{sql: database, paths: paths}
	if typedErr = store.configure(ctx); typedErr != nil {
		_ = database.Close()
		notify(observer, async.NewFailed[MigrationProgress](typedErr))
		return nil, typedErr
	}
	if typedErr = store.applyMigrations(ctx, migrations, observer); typedErr != nil {
		_ = database.Close()
		notify(observer, async.NewFailed[MigrationProgress](typedErr))
		return nil, typedErr
	}

	progress := MigrationProgress{
		Current: len(migrations),
		Total:   len(migrations),
		Name:    "ready",
	}
	notify(observer, async.NewSucceeded(progress))
	return store, nil
}

// Paths returns a copy of the initialized local layout.
func (s *Store) Paths() Paths {
	if s == nil {
		return Paths{}
	}
	return s.paths
}

// Close releases the SQLite connection.
func (s *Store) Close() error {
	if s == nil || s.sql == nil {
		return nil
	}
	return s.sql.Close()
}

// SchemaVersion returns the latest applied migration version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var version int
	err := s.sql.QueryRowContext(
		ctx,
		`SELECT COALESCE(MAX(version), 0) FROM _schema_migrations`,
	).Scan(&version)
	if err != nil {
		return 0, storageError(
			"read schema version",
			s.paths.Database,
			"检查数据库文件后重试。",
			err,
		)
	}
	return version, nil
}

func ensureLayout(config Config) (Paths, *domainerr.Error) {
	dataDir := strings.TrimSpace(config.DataDir)
	if dataDir == "" {
		return Paths{}, domainerr.New(
			domainerr.CodeValidation,
			"initialize local data",
			"本地数据目录不能为空。",
			"提供明确的数据目录后重试。",
			false,
		)
	}

	databaseName := strings.TrimSpace(config.DatabaseName)
	if databaseName == "" {
		databaseName = defaultDatabaseName
	}
	if filepath.Base(databaseName) != databaseName || databaseName == "." {
		return Paths{}, domainerr.New(
			domainerr.CodeValidation,
			"initialize local data",
			"数据库文件名不能包含目录。",
			"只提供数据库文件名后重试。",
			false,
		)
	}

	absoluteDir, err := filepath.Abs(dataDir)
	if err != nil {
		return Paths{}, storageError(
			"resolve data directory",
			dataDir,
			"改用有效的本地目录后重试。",
			err,
		)
	}
	paths := Paths{
		DataDir:  absoluteDir,
		Database: filepath.Join(absoluteDir, databaseName),
		Uploads:  filepath.Join(absoluteDir, "uploads"),
		Exports:  filepath.Join(absoluteDir, "exports"),
		Logs:     filepath.Join(absoluteDir, "logs"),
	}

	for _, path := range []string{paths.DataDir, paths.Uploads, paths.Exports, paths.Logs} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return Paths{}, storageError(
				"create local data directory",
				path,
				"检查该路径是否为目录且当前用户具有写入权限。",
				err,
			)
		}
	}
	return paths, nil
}

func (s *Store) configure(ctx context.Context) *domainerr.Error {
	pragmas := []string{
		`PRAGMA foreign_keys = ON`,
		`PRAGMA busy_timeout = 5000`,
		`PRAGMA journal_mode = WAL`,
	}
	for _, statement := range pragmas {
		if _, err := s.sql.ExecContext(ctx, statement); err != nil {
			return storageError(
				"configure SQLite",
				s.paths.Database,
				"检查数据库是否可写后重试。",
				err,
			)
		}
	}
	if err := s.sql.PingContext(ctx); err != nil {
		return storageError(
			"connect SQLite",
			s.paths.Database,
			"检查数据库路径后重试。",
			err,
		)
	}
	return nil
}

func (s *Store) applyMigrations(
	ctx context.Context,
	migrations []migration,
	observer MigrationObserver,
) *domainerr.Error {
	if _, err := s.sql.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS _schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		return storageError(
			"create migration ledger",
			s.paths.Database,
			"检查数据库是否可写后重试。",
			err,
		)
	}

	applied, typedErr := s.appliedMigrations(ctx)
	if typedErr != nil {
		return typedErr
	}
	if typedErr = validateMigrationPlan(migrations, applied, s.paths.Database); typedErr != nil {
		return typedErr
	}

	appliedCount := len(applied)
	for _, item := range migrations {
		if _, ok := applied[item.version]; ok {
			continue
		}

		progress := MigrationProgress{
			Current: appliedCount + 1,
			Total:   len(migrations),
			Name:    item.name,
		}
		notify(observer, async.NewStreaming(&progress))

		transaction, err := s.sql.BeginTx(ctx, nil)
		if err != nil {
			return migrationError(s.paths.Database, item, err)
		}
		if _, err = transaction.ExecContext(ctx, item.sql); err != nil {
			_ = transaction.Rollback()
			return migrationError(s.paths.Database, item, err)
		}
		if _, err = transaction.ExecContext(
			ctx,
			`INSERT INTO _schema_migrations(version, name, applied_at) VALUES (?, ?, ?)`,
			item.version,
			item.name,
			nowText(),
		); err != nil {
			_ = transaction.Rollback()
			return migrationError(s.paths.Database, item, err)
		}
		if err = transaction.Commit(); err != nil {
			return migrationError(s.paths.Database, item, err)
		}
		appliedCount++
	}
	return nil
}

func (s *Store) appliedMigrations(ctx context.Context) (map[int]string, *domainerr.Error) {
	rows, err := s.sql.QueryContext(
		ctx,
		`SELECT version, name FROM _schema_migrations ORDER BY version`,
	)
	if err != nil {
		return nil, storageError(
			"read migration ledger",
			s.paths.Database,
			"检查数据库文件后重试。",
			err,
		)
	}

	applied := make(map[int]string)
	for rows.Next() {
		var version int
		var name string
		if err := rows.Scan(&version, &name); err != nil {
			_ = rows.Close()
			return nil, storageError(
				"read migration ledger",
				s.paths.Database,
				"检查数据库文件后重试。",
				err,
			)
		}
		applied[version] = name
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, storageError(
			"read migration ledger",
			s.paths.Database,
			"检查数据库文件后重试。",
			err,
		)
	}
	if err := rows.Close(); err != nil {
		return nil, storageError(
			"close migration ledger",
			s.paths.Database,
			"重试数据库初始化。",
			err,
		)
	}
	return applied, nil
}

func validateMigrationPlan(
	migrations []migration,
	applied map[int]string,
	databasePath string,
) *domainerr.Error {
	seen := make(map[int]string, len(migrations))
	lastVersion := 0
	for _, item := range migrations {
		if item.version <= lastVersion || item.version <= 0 ||
			strings.TrimSpace(item.name) == "" || strings.TrimSpace(item.sql) == "" {
			return storageError(
				"validate migration plan",
				databasePath,
				"修正迁移版本顺序和内容后重试。",
				fmt.Errorf("invalid migration %d %q", item.version, item.name),
			)
		}
		if previous, exists := seen[item.version]; exists {
			return storageError(
				"validate migration plan",
				databasePath,
				"移除重复迁移版本后重试。",
				fmt.Errorf("duplicate migration %d: %q and %q", item.version, previous, item.name),
			)
		}
		seen[item.version] = item.name
		lastVersion = item.version
		if appliedName, exists := applied[item.version]; exists && appliedName != item.name {
			return storageError(
				"validate migration ledger",
				databasePath,
				"恢复匹配的迁移文件或使用兼容版本。",
				fmt.Errorf(
					"migration %d name changed from %q to %q",
					item.version,
					appliedName,
					item.name,
				),
			)
		}
	}
	for version := range applied {
		if _, exists := seen[version]; !exists {
			return storageError(
				"validate migration ledger",
				databasePath,
				"使用不低于当前数据库版本的应用程序。",
				fmt.Errorf("database contains unknown migration version %d", version),
			)
		}
	}
	return nil
}

func migrationError(path string, item migration, cause error) *domainerr.Error {
	return storageError(
		"apply migration "+item.name,
		path,
		"保留数据库文件并查看日志，修正迁移后重试。",
		cause,
	)
}

func storageError(
	operation string,
	path string,
	recovery string,
	cause error,
) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"SQLite",
		fmt.Sprintf("无法完成本地存储操作，路径：%s。", path),
		recovery,
		true,
		cause,
	)
}

func notify(observer MigrationObserver, state async.State[MigrationProgress]) {
	if observer != nil {
		observer(state)
	}
}
