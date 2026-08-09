// Package credentials resolves Provider secrets without persisting plaintext
// in InterviewCraft configuration, logs or transfer data.
package credentials

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"runtime"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

// Service is the fixed operating-system keyring service name.
const Service = "InterviewCraft"

// ErrNotFound reports an empty keyring account.
var ErrNotFound = keyring.ErrNotFound

// Store is the narrow credential-manager boundary used by setup and runtime.
type Store interface {
	Get(service, account string) (string, error)
	Set(service, account, secret string) error
	Delete(service, account string) error
}

// SystemStore uses Windows Credential Manager, macOS Keychain or Linux Secret
// Service through go-keyring.
type SystemStore struct{}

func (SystemStore) Get(service, account string) (string, error) {
	return keyring.Get(service, account)
}

func (SystemStore) Set(service, account, secret string) error {
	return keyring.Set(service, account, secret)
}

func (SystemStore) Delete(service, account string) error {
	return keyring.Delete(service, account)
}

// Account returns the SHA-256 identifier for one canonical data directory.
func Account(dataDir string) (string, error) {
	canonical, err := filepath.Abs(strings.TrimSpace(dataDir))
	if err != nil {
		return "", err
	}
	canonical = filepath.Clean(canonical)
	if resolved, resolveErr := filepath.EvalSymlinks(canonical); resolveErr == nil {
		canonical = filepath.Clean(resolved)
	}
	if runtime.GOOS == "windows" {
		canonical = strings.ToLower(canonical)
	}
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:]), nil
}

// Resolver applies environment precedence and then reads the system keyring.
type Resolver struct {
	lookup  func(string) (string, bool)
	store   Store
	account string
}

// NewResolver constructs a non-caching resolver so credential changes are
// visible without copying secrets into long-lived exported state.
func NewResolver(
	dataDir string,
	lookup func(string) (string, bool),
	store Store,
) (*Resolver, error) {
	account, err := Account(dataDir)
	if err != nil {
		return nil, err
	}
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	if store == nil {
		store = SystemStore{}
	}
	return &Resolver{lookup: lookup, store: store, account: account}, nil
}

// Resolve matches the existing LLM SecretResolver contract.
func (resolver *Resolver) Resolve(environmentName string) (string, bool) {
	secret, _, err := resolver.ResolveDetailed(environmentName)
	return secret, err == nil && strings.TrimSpace(secret) != ""
}

// ResolveDetailed distinguishes an empty keyring from an unavailable one.
func (resolver *Resolver) ResolveDetailed(environmentName string) (string, bool, error) {
	if resolver == nil {
		return "", false, errors.New("credential resolver is nil")
	}
	if secret, ok := resolver.lookup(environmentName); ok && strings.TrimSpace(secret) != "" {
		return secret, false, nil
	}
	secret, err := resolver.store.Get(Service, resolver.account)
	if errors.Is(err, ErrNotFound) {
		return "", true, nil
	}
	if err != nil {
		return "", true, err
	}
	return secret, true, nil
}

// Account returns the non-secret keyring account identifier.
func (resolver *Resolver) Account() string {
	if resolver == nil {
		return ""
	}
	return resolver.account
}
