package db

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestBrokenMigrationRollsBackAndEmitsFailedState(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	var states []async.State[MigrationProgress]
	broken := []migration{{
		version: 1,
		name:    "broken",
		sql: `
			CREATE TABLE should_roll_back (id TEXT PRIMARY KEY);
			THIS IS NOT VALID SQL;
		`,
	}}

	store, err := openWithMigrations(
		ctx,
		Config{DataDir: dataDir},
		func(state async.State[MigrationProgress]) {
			states = append(states, state)
		},
		broken,
	)
	if store != nil {
		_ = store.Close()
		t.Fatal("broken migration returned a store")
	}
	if err == nil {
		t.Fatal("broken migration unexpectedly succeeded")
	}
	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		t.Fatalf("migration error type = %T, want *domainerr.Error", err)
	}
	if typed.Code != domainerr.CodePersistenceFailed ||
		typed.RecoveryAction == "" ||
		!strings.Contains(typed.Message, filepath.Join(dataDir, defaultDatabaseName)) {
		t.Fatalf("migration error is not actionable: %#v", typed)
	}
	assertPhases(t, states, []async.Phase{async.Pending, async.Streaming, async.Failed})

	databasePath := filepath.Join(dataDir, defaultDatabaseName)
	database, openErr := sql.Open("sqlite", databasePath)
	if openErr != nil {
		t.Fatalf("open database after rollback: %v", openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	var tableCount int
	if scanErr := database.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'should_roll_back'
	`).Scan(&tableCount); scanErr != nil {
		t.Fatalf("inspect rolled-back migration: %v", scanErr)
	}
	if tableCount != 0 {
		t.Fatalf("rolled-back table count = %d, want 0", tableCount)
	}
}

func TestMigrationPlanRejectsChangedLedgerName(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := Open(ctx, Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	changed := []migration{{
		version: 1,
		name:    "renamed_migration",
		sql:     initialSchemaSQL,
	}}
	reopened, err := openWithMigrations(
		ctx,
		Config{DataDir: dataDir},
		nil,
		changed,
	)
	if reopened != nil {
		_ = reopened.Close()
		t.Fatal("changed migration ledger returned a store")
	}
	if err == nil || !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("changed migration error = %v, want persistence code", err)
	}
}
