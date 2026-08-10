package update

import (
	"bufio"
	"errors"
	"io"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const releaseManifestHeader = "interviewcraft-release-v1"

var (
	versionPattern  = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?(?:\+[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
	commitPattern   = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	hashPattern     = regexp.MustCompile(`^[0-9a-f]{64}$`)
	filenamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

func ParseReleaseManifest(reader io.Reader) (ReleaseManifest, error) {
	scanner := bufio.NewScanner(io.LimitReader(reader, 1<<20))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	lines := make([]string, 0, 16)
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if line == "" {
			return ReleaseManifest{}, errors.New("release manifest contains a blank line")
		}
		lines = append(lines, line)
	}
	if err := scanner.Err(); err != nil {
		return ReleaseManifest{}, err
	}
	if len(lines) < 9 || lines[0] != releaseManifestHeader {
		return ReleaseManifest{}, errors.New("release manifest header is invalid")
	}
	meta := strings.Split(lines[1], "\t")
	if len(meta) != 4 || meta[0] != "meta" || !versionPattern.MatchString(meta[1]) || !commitPattern.MatchString(meta[2]) {
		return ReleaseManifest{}, errors.New("release manifest metadata is invalid")
	}
	created, err := time.Parse(time.RFC3339, meta[3])
	if err != nil {
		return ReleaseManifest{}, errors.New("release manifest timestamp is invalid")
	}
	manifest := ReleaseManifest{Version: meta[1], Commit: meta[2], Created: created}
	seenPlatform := map[string]bool{}
	seenFile := map[string]bool{}
	seenChecksum := false
	sbomCount := 0
	for _, line := range lines[2:] {
		fields := strings.Split(line, "\t")
		if len(fields) != 6 {
			return ReleaseManifest{}, errors.New("release manifest row has invalid field count")
		}
		kind := fields[0]
		if !validReleaseFilename(fields[3]) || !hashPattern.MatchString(fields[4]) {
			return ReleaseManifest{}, errors.New("release manifest file metadata is invalid")
		}
		size, err := strconv.ParseInt(fields[5], 10, 64)
		if err != nil || size <= 0 {
			return ReleaseManifest{}, errors.New("release manifest file size is invalid")
		}
		if seenFile[fields[3]] {
			return ReleaseManifest{}, errors.New("release manifest filename is duplicated")
		}
		seenFile[fields[3]] = true
		switch kind {
		case "asset":
			if seenChecksum || !validPlatform(fields[1], fields[2]) {
				return ReleaseManifest{}, errors.New("release manifest platform is invalid")
			}
			extension := ".tar.gz"
			if fields[1] == "windows" {
				extension = ".zip"
			}
			if fields[3] != "interviewcraft_"+manifest.Version+"_"+fields[1]+"_"+fields[2]+extension {
				return ReleaseManifest{}, errors.New("release asset filename does not match its platform")
			}
			platform := fields[1] + "/" + fields[2]
			if seenPlatform[platform] {
				return ReleaseManifest{}, errors.New("release manifest platform is duplicated")
			}
			seenPlatform[platform] = true
			manifest.Assets = append(manifest.Assets, ReleaseAsset{GOOS: fields[1], GOARCH: fields[2], Filename: fields[3], SHA256: fields[4], Size: size})
		case "checksum":
			if seenChecksum || fields[1] != "-" || fields[2] != "-" || fields[3] != "checksums.txt" {
				return ReleaseManifest{}, errors.New("release checksum row is invalid")
			}
			seenChecksum = true
		case "sbom":
			if !seenChecksum || fields[1] != "-" || fields[2] != "-" || !strings.HasSuffix(fields[3], ".spdx.json") {
				return ReleaseManifest{}, errors.New("release SBOM row is invalid")
			}
			sbomCount++
		default:
			return ReleaseManifest{}, errors.New("release manifest row kind is unknown")
		}
	}
	for _, platform := range []string{"windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		if !seenPlatform[platform] {
			return ReleaseManifest{}, errors.New("release manifest is missing a platform")
		}
	}
	if !seenChecksum || sbomCount == 0 || len(manifest.Assets) != 6 {
		return ReleaseManifest{}, errors.New("release manifest is incomplete")
	}
	return manifest, nil
}

func (manifest ReleaseManifest) AssetFor(goos, goarch string) (ReleaseAsset, error) {
	for _, asset := range manifest.Assets {
		if asset.GOOS == goos && asset.GOARCH == goarch {
			return asset, nil
		}
	}
	return ReleaseAsset{}, errors.New("release manifest has no compatible asset")
}

func validReleaseFilename(value string) bool {
	return filenamePattern.MatchString(value) && value != "." && value != ".." && filepath.Base(value) == value && !strings.ContainsAny(value, `/\\`)
}

func validPlatform(goos, goarch string) bool {
	return (goos == "windows" || goos == "linux" || goos == "darwin") && (goarch == "amd64" || goarch == "arm64")
}
