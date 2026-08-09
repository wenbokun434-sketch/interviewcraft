package version

import (
	"encoding/json"
	"testing"
)

func TestCurrentUsesSourceDefaultsAndStableJSON(t *testing.T) {
	originalVersion, originalCommit, originalBuildTime := ApplicationVersion, GitCommit, BuildTime
	t.Cleanup(func() {
		ApplicationVersion, GitCommit, BuildTime = originalVersion, originalCommit, originalBuildTime
	})
	ApplicationVersion, GitCommit, BuildTime = "", "", ""
	info := Current()
	if info.SchemaVersion != SchemaVersion || info.Version != "dev" || info.GitCommit != "unknown" || info.BuildTime != "unknown" || info.GOOS == "" || info.GOARCH == "" {
		t.Fatalf("Current=%#v", info)
	}
	payload, err := json.Marshal(info)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(payload, &fields); err != nil || len(fields) != 6 {
		t.Fatalf("fields=%v err=%v", fields, err)
	}
}

func TestCurrentUsesInjectedBuildValues(t *testing.T) {
	originalVersion, originalCommit, originalBuildTime := ApplicationVersion, GitCommit, BuildTime
	t.Cleanup(func() {
		ApplicationVersion, GitCommit, BuildTime = originalVersion, originalCommit, originalBuildTime
	})
	ApplicationVersion = "1.2.3"
	GitCommit = "0123456789abcdef"
	BuildTime = "2026-08-10T12:00:00Z"
	info := Current()
	if info.Version != ApplicationVersion || info.GitCommit != GitCommit || info.BuildTime != BuildTime {
		t.Fatalf("Current=%#v", info)
	}
}
