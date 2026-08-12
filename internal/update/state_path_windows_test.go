//go:build windows

package update

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestResolvedPathWithinAcceptsWindowsShortPathAlias(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Long Temporary Directory Name")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	verifier := filepath.Join(root, "cosign-fixture.exe")
	if err := os.WriteFile(verifier, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	path16, err := windows.UTF16PtrFromString(verifier)
	if err != nil {
		t.Fatal(err)
	}
	buffer := make([]uint16, 32768)
	length, err := windows.GetShortPathName(path16, &buffer[0], uint32(len(buffer)))
	if err != nil {
		t.Skipf("8.3 short path aliases are unavailable: %v", err)
	}
	shortVerifier := windows.UTF16ToString(buffer[:length])
	if shortVerifier == "" || strings.EqualFold(filepath.Clean(shortVerifier), filepath.Clean(verifier)) {
		t.Skip("8.3 short path aliases are unavailable")
	}
	if !resolvedPathWithin(root, shortVerifier) {
		t.Fatalf("short path alias was rejected: %s", shortVerifier)
	}
	if !sameExistingPath(verifier, shortVerifier) {
		t.Fatalf("short path alias did not identify the same file: %s", shortVerifier)
	}
}
