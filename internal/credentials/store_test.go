package credentials

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolverPrefersEnvironmentOverKeyring(t *testing.T) {
	store := &memoryStore{secret: "keyring-secret"}
	resolver, err := NewResolver(t.TempDir(), func(name string) (string, bool) {
		return "environment-secret", name == "TEST_KEY"
	}, store)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	secret, fromKeyring, err := resolver.ResolveDetailed("TEST_KEY")
	if err != nil || fromKeyring || secret != "environment-secret" {
		t.Fatalf("ResolveDetailed=(%q,%v,%v)", secret, fromKeyring, err)
	}
	if store.gets != 0 {
		t.Fatalf("keyring reads=%d, want 0", store.gets)
	}
}

func TestAccountUsesCanonicalDirectoryHash(t *testing.T) {
	root := t.TempDir()
	first, err := Account(filepath.Join(root, ".", "data"))
	if err != nil {
		t.Fatalf("Account first: %v", err)
	}
	second, err := Account(filepath.Join(root, "data"))
	if err != nil {
		t.Fatalf("Account second: %v", err)
	}
	if first != second || len(first) != 64 || strings.ToLower(first) != first {
		t.Fatalf("accounts=%q/%q", first, second)
	}
}

func TestResolverReportsUnavailableKeyring(t *testing.T) {
	want := errors.New("credential manager unavailable")
	resolver, err := NewResolver(t.TempDir(), nil, &memoryStore{err: want})
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	_, fromKeyring, err := resolver.ResolveDetailed("MISSING")
	if !fromKeyring || !errors.Is(err, want) {
		t.Fatalf("fromKeyring=%v err=%v", fromKeyring, err)
	}
}

type memoryStore struct {
	secret string
	err    error
	gets   int
}

func (store *memoryStore) Get(string, string) (string, error) {
	store.gets++
	if store.err != nil {
		return "", store.err
	}
	if store.secret == "" {
		return "", ErrNotFound
	}
	return store.secret, nil
}
func (store *memoryStore) Set(string, string, string) error { return store.err }
func (store *memoryStore) Delete(string, string) error      { return store.err }
