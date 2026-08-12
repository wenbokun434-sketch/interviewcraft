package update

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvedPathWithinUsesExistingDirectoryIdentity(t *testing.T) {
	root := t.TempDir()
	verifier := filepath.Join(root, "cosign-fixture")
	if err := os.WriteFile(verifier, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if !resolvedPathWithin(root, verifier) {
		resolvedRoot, rootErr := filepath.EvalSymlinks(root)
		resolvedVerifier, verifierErr := filepath.EvalSymlinks(verifier)
		t.Fatalf("existing child was rejected: root=%q resolved_root=%q root_err=%v verifier=%q resolved_verifier=%q verifier_err=%v", root, resolvedRoot, rootErr, verifier, resolvedVerifier, verifierErr)
	}

	outside := filepath.Join(t.TempDir(), "cosign-fixture")
	if err := os.WriteFile(outside, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if resolvedPathWithin(root, outside) {
		t.Fatal("outside verifier was accepted")
	}
}

func TestResolvedPathWithinRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outsideRoot := t.TempDir()
	outside := filepath.Join(outsideRoot, "cosign-fixture")
	if err := os.WriteFile(outside, []byte("fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-outside")
	if err := os.Symlink(outsideRoot, link); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	if resolvedPathWithin(root, filepath.Join(link, filepath.Base(outside))) {
		t.Fatal("symlink escape was accepted")
	}
}
