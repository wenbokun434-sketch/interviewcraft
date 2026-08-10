package update

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestBackupIsCompleteVerifiedAndImmutable(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, ".interviewcraft")
	binary := filepath.Join(root, "bin", applicationName(runtime.GOOS))
	if err := os.MkdirAll(filepath.Join(dataDir, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dataDir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "nested", "data.json"), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	backup, err := createBackup(context.Background(), dataDir, binary, "1.0.0", time.Now(), func(string) (uint64, error) { return 1 << 30, nil })
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := verifyBackup(backup)
	if err != nil || len(manifest.Entries) != 2 || len(manifest.Directories) != 2 {
		t.Fatalf("verifyBackup = %+v, %v", manifest, err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "nested", "data.json"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(backup, "data", "nested", "data.json")); err != nil || string(got) != "original" {
		t.Fatalf("backup changed with source: %q, %v", got, err)
	}
	if err := os.WriteFile(filepath.Join(backup, "data", "nested", "data.json"), []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := verifyBackup(backup); err == nil {
		t.Fatal("tampered backup was accepted")
	}
}

func TestBackupRejectsUnsafeInputs(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	binary := filepath.Join(root, applicationName(runtime.GOOS))
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "data"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("x"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := createBackup(context.Background(), dataDir, binary, "1.0.0", time.Now(), func(string) (uint64, error) { return 1, nil }); err == nil {
		t.Fatal("insufficient space was ignored")
	}
	if _, err := createBackup(context.Background(), filepath.VolumeName(root)+string(filepath.Separator), binary, "1.0.0", time.Now(), nil); err == nil {
		t.Fatal("broad data directory was accepted")
	}
	link := filepath.Join(dataDir, "link")
	if err := os.Symlink(filepath.Join(dataDir, "data"), link); err == nil {
		if _, err := createBackup(context.Background(), dataDir, binary, "1.0.0", time.Now(), nil); err == nil {
			t.Fatal("symbolic link was copied into backup")
		}
	}
}

func TestArchiveExtractionRejectsTraversalLinksAndExtraExecutables(t *testing.T) {
	tests := []struct {
		name    string
		entries []zipEntry
	}{
		{name: "traversal", entries: []zipEntry{{name: "../interviewcraft.exe", payload: "bad"}}},
		{name: "absolute", entries: []zipEntry{{name: "/interviewcraft.exe", payload: "bad"}}},
		{name: "extra executable", entries: []zipEntry{{name: "interviewcraft.exe", payload: "ok"}, {name: "other.exe", payload: "bad"}}},
		{name: "duplicate", entries: []zipEntry{{name: "interviewcraft.exe", payload: "one"}, {name: "interviewcraft.exe", payload: "two"}}},
		{name: "symlink", entries: []zipEntry{{name: "interviewcraft.exe", payload: "target", mode: os.ModeSymlink | 0o777}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			archive := filepath.Join(t.TempDir(), "release.zip")
			if err := os.WriteFile(archive, makeZip(t, test.entries), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := extractReleaseBinary(archive, "release.zip", filepath.Join(t.TempDir(), "interviewcraft.exe"), "windows"); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestManifestStrictFailureMatrix(t *testing.T) {
	archive := zipPayload(t, "interviewcraft.exe", []byte("binary"))
	valid := fixtureManifest(testToVersion, testCommit, archive, false)
	if _, err := ParseReleaseManifest(strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	tests := map[string]string{
		"unknown row":        strings.Replace(valid, "sbom\t", "unknown\t", 1),
		"uppercase hash":     strings.Replace(valid, strings.Repeat("b", 64), strings.Repeat("B", 64), 1),
		"zero size":          strings.Replace(valid, "checksums.txt\t"+strings.Repeat("b", 64)+"\t1", "checksums.txt\t"+strings.Repeat("b", 64)+"\t0", 1),
		"path separator":     strings.Replace(valid, "checksums.txt", "bad/checksums.txt", 1),
		"duplicate platform": strings.Replace(valid, "windows\tarm64", "windows\tamd64", 1),
		"missing platform":   strings.Replace(valid, lineContaining(valid, "darwin\tarm64"), "", 1),
		"wrong asset name":   strings.Replace(valid, "interviewcraft_1.1.0_windows_amd64.zip", "interviewcraft_1.1.0_linux_amd64.zip", 1),
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseReleaseManifest(strings.NewReader(manifest)); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestSafeRelativePath(t *testing.T) {
	for _, value := range []string{"", ".", "..", "../x", "/x", `x\y`} {
		if safeRelativePath(value) {
			t.Fatalf("unsafe relative path accepted: %q", value)
		}
	}
	if !safeRelativePath("nested/data.json") {
		t.Fatal("safe path rejected")
	}
}

type zipEntry struct {
	name    string
	payload string
	mode    os.FileMode
}

func makeZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		}
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.payload)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func lineContaining(value, fragment string) string {
	for _, line := range strings.SplitAfter(value, "\n") {
		if strings.Contains(line, fragment) {
			return line
		}
	}
	return ""
}

func TestValidateScopedDirectory(t *testing.T) {
	if err := validateScopedDirectory(filepath.Join(t.TempDir(), "data")); err != nil {
		t.Fatal(err)
	}
	if err := validateScopedDirectory(filepath.VolumeName(t.TempDir()) + string(filepath.Separator)); err == nil {
		t.Fatal("volume root accepted")
	}
}
