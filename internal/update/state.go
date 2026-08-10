package update

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func saveState(path string, state State) error {
	state.Schema = StateSchema
	if err := validateState(path, state); err != nil {
		return err
	}
	return writeJSONAtomic(path, state)
}

func readState(path string) (State, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return State{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return State{}, errors.New("update state contains trailing JSON")
	}
	if err := validateState(path, state); err != nil {
		return State{}, err
	}
	return state, nil
}

func validateState(path string, state State) error {
	if state.Schema != StateSchema || !validStatePhase(state.Phase) || !versionPattern.MatchString(state.FromVersion) || !versionPattern.MatchString(state.ToVersion) || state.UpdatedAt.IsZero() {
		return errors.New("update state metadata is invalid")
	}
	dataDir, err := filepath.Abs(state.DataDir)
	if err != nil {
		return err
	}
	if err := validateScopedDirectory(dataDir); err != nil {
		return err
	}
	root := backupRootFor(dataDir)
	statePath, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if statePath != filepath.Join(root, "update-state.json") {
		return errors.New("update state path is outside the backup root")
	}
	for _, value := range []string{state.BackupDirectory, state.StagedBinary, state.DiagnosticPath} {
		if value == "" {
			continue
		}
		if !filepath.IsAbs(value) {
			return errors.New("update state artifact path is not absolute")
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return err
		}
		if !pathWithin(root, absolute) {
			return errors.New("update state artifact is outside the backup root")
		}
	}
	if state.ForwardBackup != "" {
		absolute, err := filepath.Abs(state.ForwardBackup)
		if err != nil || !pathWithin(root, absolute) {
			return errors.New("forward backup is outside the backup root")
		}
	}
	if filepath.Clean(state.DataDir) != dataDir || !filepath.IsAbs(state.BinaryPath) || !filepath.IsAbs(state.ReceiptPath) {
		return errors.New("update state paths are not canonical")
	}
	if state.Phase != PhaseCommitted && state.Phase != PhaseRolledBack && state.Phase != PhaseFailed && strings.TrimSpace(state.GuardToken) == "" {
		return errors.New("active update state has no maintenance token")
	}
	return nil
}

func validStatePhase(value StatePhase) bool {
	switch value {
	case PhasePrepared, PhaseSwitched, PhaseCommitted, PhaseFailed, PhaseRolledBack:
		return true
	default:
		return false
	}
}

func pathWithin(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
