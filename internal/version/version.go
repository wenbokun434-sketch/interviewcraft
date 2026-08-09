// Package version exposes immutable build and release metadata.
package version

import "runtime"

const SchemaVersion = "interviewcraft-version-v1"

// These values are replaced by GoReleaser ldflags for tagged builds.
var (
	ApplicationVersion = "dev"
	GitCommit          = "unknown"
	BuildTime          = "unknown"
)

// Info is the stable machine-readable version contract.
type Info struct {
	SchemaVersion string `json:"schema_version"`
	Version       string `json:"version"`
	GitCommit     string `json:"git_commit"`
	BuildTime     string `json:"build_time"`
	GOOS          string `json:"goos"`
	GOARCH        string `json:"goarch"`
}

// Current returns build metadata for the running binary.
func Current() Info {
	return Info{
		SchemaVersion: SchemaVersion,
		Version:       fallback(ApplicationVersion, "dev"),
		GitCommit:     fallback(GitCommit, "unknown"),
		BuildTime:     fallback(BuildTime, "unknown"),
		GOOS:          runtime.GOOS,
		GOARCH:        runtime.GOARCH,
	}
}

func fallback(value, replacement string) string {
	if value == "" {
		return replacement
	}
	return value
}
