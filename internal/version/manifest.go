package version

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const ManifestHeader = "interviewcraft-release-v1"

var (
	releaseVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z][0-9A-Za-z.-]*)?(?:\+[0-9A-Za-z][0-9A-Za-z.-]*)?$`)
	commitPattern         = regexp.MustCompile(`^[0-9a-f]{7,64}$`)
	filenamePattern       = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	digestPattern         = regexp.MustCompile(`^[0-9a-f]{64}$`)
	sizePattern           = regexp.MustCompile(`^[1-9][0-9]*$`)
)

var supportedPlatforms = []Platform{
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "windows", GOARCH: "amd64"},
	{GOOS: "windows", GOARCH: "arm64"},
}

// Platform identifies one required release target.
type Platform struct {
	GOOS   string
	GOARCH string
}

// Metadata identifies the source and creation time for a release manifest.
type Metadata struct {
	Version    string
	Commit     string
	CreatedUTC time.Time
}

// File is one hash- and size-pinned release artifact.
type File struct {
	Kind     string
	GOOS     string
	GOARCH   string
	Filename string
	SHA256   string
	Size     int64
}

// Manifest is the complete strict release metadata document.
type Manifest struct {
	Metadata Metadata
	Assets   []File
	Checksum File
	SBOMs    []File
}

// ParseManifest accepts only the tab-separated v1 grammar and complete six
// platform matrix. It intentionally rejects forward-compatible unknown rows.
func ParseManifest(reader io.Reader) (Manifest, error) {
	if reader == nil {
		return Manifest{}, errors.New("manifest reader is nil")
	}
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	lineNumber := 0
	result := Manifest{}
	seenMeta := false
	seenChecksum := false
	platforms := make(map[Platform]struct{})
	filenames := make(map[string]struct{})
	for scanner.Scan() {
		lineNumber++
		line := scanner.Text()
		if lineNumber == 1 {
			if line != ManifestHeader {
				return Manifest{}, fmt.Errorf("line 1: unsupported manifest header")
			}
			continue
		}
		if line == "" || strings.ContainsRune(line, '\r') {
			return Manifest{}, fmt.Errorf("line %d: blank or CRLF lines are not allowed", lineNumber)
		}
		fields := strings.Split(line, "\t")
		switch fields[0] {
		case "meta":
			if len(fields) != 4 || seenMeta || lineNumber != 2 {
				return Manifest{}, fmt.Errorf("line %d: invalid or duplicate meta row", lineNumber)
			}
			created, err := validateMetadata(fields[1], fields[2], fields[3])
			if err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			result.Metadata = Metadata{Version: fields[1], Commit: fields[2], CreatedUTC: created}
			seenMeta = true
		case "asset":
			if !seenMeta || seenChecksum || len(fields) != 6 {
				return Manifest{}, fmt.Errorf("line %d: invalid asset row position or field count", lineNumber)
			}
			file, err := parseFile(fields)
			if err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			platform := Platform{GOOS: file.GOOS, GOARCH: file.GOARCH}
			if !isSupportedPlatform(platform) {
				return Manifest{}, fmt.Errorf("line %d: unsupported platform %s/%s", lineNumber, file.GOOS, file.GOARCH)
			}
			if _, duplicate := platforms[platform]; duplicate {
				return Manifest{}, fmt.Errorf("line %d: duplicate platform %s/%s", lineNumber, file.GOOS, file.GOARCH)
			}
			if err := addFilename(filenames, file.Filename); err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			platforms[platform] = struct{}{}
			result.Assets = append(result.Assets, file)
		case "checksum":
			if !seenMeta || seenChecksum || len(fields) != 6 || fields[1] != "-" || fields[2] != "-" || fields[3] != "checksums.txt" {
				return Manifest{}, fmt.Errorf("line %d: invalid or duplicate checksum row", lineNumber)
			}
			file, err := parseFile(fields)
			if err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if err := addFilename(filenames, file.Filename); err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			result.Checksum, seenChecksum = file, true
		case "sbom":
			if !seenChecksum || len(fields) != 6 || fields[1] != "-" || fields[2] != "-" {
				return Manifest{}, fmt.Errorf("line %d: invalid sbom row position or field count", lineNumber)
			}
			file, err := parseFile(fields)
			if err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			if !strings.HasSuffix(file.Filename, ".spdx.json") {
				return Manifest{}, fmt.Errorf("line %d: sbom filename must end in .spdx.json", lineNumber)
			}
			if err := addFilename(filenames, file.Filename); err != nil {
				return Manifest{}, fmt.Errorf("line %d: %w", lineNumber, err)
			}
			result.SBOMs = append(result.SBOMs, file)
		default:
			return Manifest{}, fmt.Errorf("line %d: unknown row kind", lineNumber)
		}
	}
	if err := scanner.Err(); err != nil {
		return Manifest{}, err
	}
	if lineNumber == 0 || !seenMeta || !seenChecksum || len(result.Assets) != len(supportedPlatforms) || len(result.SBOMs) == 0 {
		return Manifest{}, errors.New("manifest is incomplete")
	}
	for _, platform := range supportedPlatforms {
		if _, ok := platforms[platform]; !ok {
			return Manifest{}, fmt.Errorf("manifest is missing platform %s/%s", platform.GOOS, platform.GOARCH)
		}
	}
	return result, nil
}

// EncodeManifest writes a deterministic tab-separated v1 manifest.
func EncodeManifest(writer io.Writer, manifest Manifest) error {
	if writer == nil {
		return errors.New("manifest writer is nil")
	}
	var builder strings.Builder
	fmt.Fprintln(&builder, ManifestHeader)
	fmt.Fprintf(&builder, "meta\t%s\t%s\t%s\n", manifest.Metadata.Version, manifest.Metadata.Commit, manifest.Metadata.CreatedUTC.UTC().Format(time.RFC3339))
	assets := append([]File(nil), manifest.Assets...)
	sort.Slice(assets, func(i, j int) bool {
		if assets[i].GOOS != assets[j].GOOS {
			return assets[i].GOOS < assets[j].GOOS
		}
		return assets[i].GOARCH < assets[j].GOARCH
	})
	for _, file := range assets {
		writeFileRow(&builder, file)
	}
	writeFileRow(&builder, manifest.Checksum)
	sboms := append([]File(nil), manifest.SBOMs...)
	sort.Slice(sboms, func(i, j int) bool { return sboms[i].Filename < sboms[j].Filename })
	for _, file := range sboms {
		writeFileRow(&builder, file)
	}
	validated, err := ParseManifest(strings.NewReader(builder.String()))
	if err != nil {
		return err
	}
	_ = validated
	_, err = io.WriteString(writer, builder.String())
	return err
}

// VerifyDirectory re-hashes every manifest entry from one downloaded release
// directory after strict parsing has rejected path-bearing filenames.
func (manifest Manifest) VerifyDirectory(directory string) error {
	files := append([]File(nil), manifest.Assets...)
	files = append(files, manifest.Checksum)
	files = append(files, manifest.SBOMs...)
	for _, expected := range files {
		path := filepath.Join(directory, expected.Filename)
		file, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open %s: %w", expected.Filename, err)
		}
		hash := sha256.New()
		size, copyErr := io.Copy(hash, file)
		closeErr := file.Close()
		if copyErr != nil {
			return fmt.Errorf("hash %s: %w", expected.Filename, copyErr)
		}
		if closeErr != nil {
			return fmt.Errorf("close %s: %w", expected.Filename, closeErr)
		}
		if size != expected.Size || hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
			return fmt.Errorf("release asset %s does not match manifest", expected.Filename)
		}
	}
	return nil
}

func validateMetadata(version, commit, created string) (time.Time, error) {
	if !releaseVersionPattern.MatchString(version) {
		return time.Time{}, errors.New("invalid release version")
	}
	if !commitPattern.MatchString(commit) {
		return time.Time{}, errors.New("invalid lowercase Git commit")
	}
	if !strings.HasSuffix(created, "Z") {
		return time.Time{}, errors.New("created time must be UTC with Z suffix")
	}
	parsed, err := time.Parse(time.RFC3339, created)
	if err != nil || parsed.Location() != time.UTC {
		return time.Time{}, errors.New("invalid created UTC time")
	}
	return parsed, nil
}

func parseFile(fields []string) (File, error) {
	if !validFilename(fields[3]) {
		return File{}, errors.New("invalid release filename")
	}
	if !digestPattern.MatchString(fields[4]) {
		return File{}, errors.New("SHA-256 must be 64 lowercase hexadecimal characters")
	}
	if !sizePattern.MatchString(fields[5]) {
		return File{}, errors.New("asset size must be positive base-10")
	}
	size, err := strconv.ParseInt(fields[5], 10, 64)
	if err != nil || size <= 0 {
		return File{}, errors.New("asset size is out of range")
	}
	return File{Kind: fields[0], GOOS: fields[1], GOARCH: fields[2], Filename: fields[3], SHA256: fields[4], Size: size}, nil
}

func validFilename(filename string) bool {
	return filenamePattern.MatchString(filename) && filename != "." && filename != ".." && filepath.Base(filename) == filename
}

func addFilename(seen map[string]struct{}, filename string) error {
	if _, duplicate := seen[filename]; duplicate {
		return fmt.Errorf("duplicate filename %q", filename)
	}
	seen[filename] = struct{}{}
	return nil
}

func isSupportedPlatform(platform Platform) bool {
	for _, candidate := range supportedPlatforms {
		if candidate == platform {
			return true
		}
	}
	return false
}

func writeFileRow(builder *strings.Builder, file File) {
	fmt.Fprintf(builder, "%s\t%s\t%s\t%s\t%s\t%d\n", file.Kind, file.GOOS, file.GOARCH, file.Filename, file.SHA256, file.Size)
}
