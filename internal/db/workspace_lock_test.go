package db

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceGuardWaitsForStoreAndBlocksOtherProcesses(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := Open(context.Background(), Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	if guard, err := AcquireMaintenance(ctx, dataDir); err == nil || guard != nil {
		t.Fatalf("maintenance acquired over active Store: guard=%#v err=%v", guard, err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	guard, err := AcquireMaintenance(context.Background(), dataDir)
	if err != nil {
		t.Fatalf("AcquireMaintenance: %v", err)
	}
	if second, secondErr := AcquireMaintenance(context.Background(), dataDir); secondErr == nil || second != nil {
		t.Fatalf("second maintenance guard=%#v err=%v", second, secondErr)
	}
	if blocked, blockedErr := Open(context.Background(), Config{DataDir: dataDir}, nil); blockedErr == nil || blocked != nil {
		if blocked != nil {
			_ = blocked.Close()
		}
		t.Fatalf("normal Store opened during maintenance: %v", blockedErr)
	}
	t.Setenv(MaintenanceTokenEnv, guard.Token())
	allowed, err := Open(context.Background(), Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("token-authorized Open: %v", err)
	}
	if err := allowed.Close(); err != nil {
		t.Fatalf("authorized Close: %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("guard Close: %v", err)
	}
	if err := guard.Close(); err != nil {
		t.Fatalf("idempotent guard Close: %v", err)
	}
	if _, err := OpenMaintenanceGuard(dataDir, "wrong"); err == nil || errors.Is(err, context.Canceled) {
		t.Fatalf("wrong token resume error=%v", err)
	}
}
