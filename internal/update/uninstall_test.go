package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestUninstallPreservesDataAndCredential(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".interviewcraft")
	installDir := filepath.Join(root, "bin")
	binary := filepath.Join(installDir, applicationName("windows"))
	pathFile := filepath.Join(root, "profile")
	receiptPath := filepath.Join(dataDir, "install-receipt.txt")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "interviewcraft.db"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathFile, []byte("before\n"+pathBegin+"\nmanaged\n"+pathEnd+"\nafter\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(receiptPath, Receipt{Version: "1.0.0", InstallDir: installDir, BinaryPath: binary, DataDir: dataDir, PathTarget: pathFile, PathFiles: []string{pathFile}}); err != nil {
		t.Fatal(err)
	}
	credentialCalled := false
	result, err := Uninstall(context.Background(), UninstallOptions{
		ReceiptPath: receiptPath, ExecutablePath: binary, DataDir: dataDir, GOOS: "windows", ForceDirect: true,
		RemoveCredential: func(string) error { credentialCalled = true; return nil },
	})
	if err != nil || result.Purged || credentialCalled {
		t.Fatalf("default uninstall = %+v, %v", result, err)
	}
	if _, err := os.Stat(binary); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("binary was not removed: %v", err)
	}
	if payload, err := os.ReadFile(filepath.Join(dataDir, "interviewcraft.db")); err != nil || string(payload) != "data" {
		t.Fatalf("data was not preserved: %q, %v", payload, err)
	}
	payload, err := os.ReadFile(pathFile)
	if err != nil || string(payload) != "before\nafter\n" {
		t.Fatalf("managed PATH block removal = %q, %v", payload, err)
	}
}

func TestUninstallPurgeRequiresReceiptBoundExactSecondConfirmation(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".interviewcraft")
	installDir := filepath.Join(root, "bin")
	binary := filepath.Join(installDir, applicationName("windows"))
	receiptPath := filepath.Join(dataDir, "install-receipt.txt")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(receiptPath, Receipt{Version: "1.0.0", InstallDir: installDir, BinaryPath: binary, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	base := UninstallOptions{ReceiptPath: receiptPath, ExecutablePath: binary, DataDir: dataDir, GOOS: "windows", ForceDirect: true, PurgeData: true, RemoveCredential: func(string) error { return nil }}
	if _, err := Uninstall(context.Background(), base); err == nil {
		t.Fatal("purge without second confirmation succeeded")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal("rejected purge modified installation")
	}
	base.Confirmation = dataDir
	credentialTarget := ""
	base.RemoveCredential = func(target string) error { credentialTarget = target; return nil }
	result, err := Uninstall(context.Background(), base)
	if err != nil || !result.Purged || credentialTarget != dataDir {
		t.Fatalf("purge = %+v, %q, %v", result, credentialTarget, err)
	}
	if _, err := os.Stat(dataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("data directory still exists: %v", err)
	}
}

func TestUninstallPurgeRefusesBroadAndOverlappingTargets(t *testing.T) {
	home, _ := os.UserHomeDir()
	if err := validatePurgeTarget(home, filepath.Join(home, "bin"), home, "windows"); err == nil {
		t.Fatal("home directory accepted as purge target")
	}
	root := t.TempDir()
	if err := validatePurgeTarget(root, filepath.Join(root, "bin"), root, "windows"); err == nil {
		t.Fatal("purge target containing installation was accepted")
	}
}

func TestUninstallCredentialFailureLeavesBinaryAndData(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".interviewcraft")
	installDir := filepath.Join(root, "bin")
	binary := filepath.Join(installDir, applicationName("windows"))
	receiptPath := filepath.Join(dataDir, "install-receipt.txt")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(receiptPath, Receipt{Version: "1.0.0", InstallDir: installDir, BinaryPath: binary, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	_, err := Uninstall(context.Background(), UninstallOptions{
		ReceiptPath: receiptPath, ExecutablePath: binary, DataDir: dataDir, GOOS: "windows", ForceDirect: true,
		PurgeData: true, Confirmation: dataDir, RemoveCredential: func(string) error { return errors.New("keyring unavailable") },
	})
	if err == nil {
		t.Fatal("credential failure was ignored")
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal("credential failure removed the binary")
	}
	if _, err := os.Stat(dataDir); err != nil {
		t.Fatal("credential failure removed data")
	}
}

func TestUninstallSchedulesWindowsHelperBeforeMutation(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".interviewcraft")
	installDir := filepath.Join(root, "bin")
	binary := filepath.Join(installDir, applicationName("windows"))
	receiptPath := filepath.Join(dataDir, "install-receipt.txt")
	if err := os.MkdirAll(installDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := WriteReceipt(receiptPath, Receipt{Version: "1.0.0", InstallDir: installDir, BinaryPath: binary, DataDir: dataDir}); err != nil {
		t.Fatal(err)
	}
	called := false
	result, err := Uninstall(context.Background(), UninstallOptions{
		ReceiptPath: receiptPath, ExecutablePath: binary, DataDir: dataDir, GOOS: "windows",
		ScheduleHelper: func(receipt Receipt, options UninstallOptions) error {
			called = receipt.BinaryPath == binary && options.DataDir == dataDir
			return nil
		},
	})
	if err != nil || !result.Scheduled || !called {
		t.Fatalf("scheduled uninstall = %+v, %v", result, err)
	}
	if _, err := os.Stat(binary); err != nil {
		t.Fatal("scheduler modified installed binary")
	}
}

func TestStripManagedBlockRejectsMalformedMarkers(t *testing.T) {
	if _, err := stripManagedBlock(pathBegin + "\nmissing end\n"); err == nil {
		t.Fatal("incomplete block accepted")
	}
	if _, err := stripManagedBlock(pathEnd + "\n"); err == nil {
		t.Fatal("orphan end marker accepted")
	}
}
