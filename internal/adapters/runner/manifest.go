package runner

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

const RunnerManifestHeader = "interviewcraft-runner-release-v1"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}(?:[0-9a-f]{24})?$`)

// ManifestImage is one immutable platform entry from runner-manifest.txt.
type ManifestImage struct {
	OS           string
	Architecture string
	Repository   string
	Digest       string
	Protocol     string
}

// RunnerManifest is the strict, tool-independent Runner release contract.
type RunnerManifest struct {
	Version    string
	Commit     string
	CreatedUTC time.Time
	Images     []ManifestImage
}

// ParseRunnerManifest rejects unknown rows, ambiguous platform entries and
// mutable/foreign image references.
func ParseRunnerManifest(reader io.Reader) (RunnerManifest, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 64<<10))
	scanner.Buffer(make([]byte, 1024), 64<<10)
	lines := make([]string, 0, 4)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			return RunnerManifest{}, errors.New("runner manifest contains a blank line")
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return RunnerManifest{}, err
	}
	if len(lines) != 4 || lines[0] != RunnerManifestHeader {
		return RunnerManifest{}, errors.New("runner manifest header or row count is invalid")
	}
	meta := strings.Split(lines[1], "\t")
	if len(meta) != 4 || meta[0] != "meta" || !releaseVersionPattern.MatchString(meta[1]) || !commitPattern.MatchString(meta[2]) {
		return RunnerManifest{}, errors.New("runner manifest metadata is invalid")
	}
	created, err := time.Parse(time.RFC3339, meta[3])
	if err != nil || created.Location() != time.UTC {
		return RunnerManifest{}, errors.New("runner manifest creation time is invalid")
	}
	manifest := RunnerManifest{Version: meta[1], Commit: meta[2], CreatedUTC: created}
	seen := map[string]bool{}
	for _, line := range lines[2:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 7 || fields[0] != "image" || fields[1] != "linux" ||
			(fields[2] != "amd64" && fields[2] != "arm64") || fields[3] != OfficialRepository ||
			!digestPattern.MatchString(fields[4]) || fields[5] != responseVersion || fields[6] != "65532:65532" {
			return RunnerManifest{}, fmt.Errorf("runner manifest image row is invalid")
		}
		if seen[fields[2]] {
			return RunnerManifest{}, errors.New("runner manifest contains a duplicate platform")
		}
		seen[fields[2]] = true
		manifest.Images = append(manifest.Images, ManifestImage{
			OS: fields[1], Architecture: fields[2], Repository: fields[3],
			Digest: fields[4], Protocol: fields[5],
		})
	}
	if !seen["amd64"] || !seen["arm64"] {
		return RunnerManifest{}, errors.New("runner manifest is missing a required platform")
	}
	return manifest, nil
}

func (manifest RunnerManifest) ImageFor(architecture string) (ManifestImage, error) {
	for _, image := range manifest.Images {
		if image.Architecture == architecture {
			return image, nil
		}
	}
	return ManifestImage{}, errors.New("runner manifest has no compatible image")
}
