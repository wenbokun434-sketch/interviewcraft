package update

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	cosignVersion  = "v3.1.3"
	oidcIssuer     = "https://token.actions.githubusercontent.com"
	identityPrefix = "https://github.com/wenbokun434-sketch/interviewcraft/.github/workflows/release.yml@refs/tags/v"
)

var cosignMatrix = map[string]struct{ Filename, SHA256 string }{
	"darwin/amd64":  {"cosign-darwin-amd64", "2347488e5d5b25336644024dfeca5601b190e91197a71a917bda44744aff106c"},
	"darwin/arm64":  {"cosign-darwin-arm64", "5cf948c2f4dfe59687bdd0b8523709067383e03982cc543475c8a7dc70e92a76"},
	"linux/amd64":   {"cosign-linux-amd64", "4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71"},
	"linux/arm64":   {"cosign-linux-arm64", "c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a"},
	"windows/amd64": {"cosign-windows-amd64.exe", "9fe59be0eca1271873ce019061335eb1ac419b7059202e797828467ddabe33be"},
	"windows/arm64": {"cosign-windows-amd64.exe", "9fe59be0eca1271873ce019061335eb1ac419b7059202e797828467ddabe33be"},
}

type latestRelease struct {
	TagName string `json:"tag_name"`
}

func resolveLatest(ctx context.Context, client *http.Client, uri string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return "", err
	}
	response, err := client.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", fmt.Errorf("release API returned HTTP %d", response.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	var value latestRelease
	if err := decoder.Decode(&value); err != nil {
		return "", err
	}
	versionValue := strings.TrimPrefix(strings.TrimSpace(value.TagName), "v")
	if !versionPattern.MatchString(versionValue) {
		return "", errors.New("release API returned an invalid version")
	}
	return versionValue, nil
}

func downloadFile(ctx context.Context, client *http.Client, uri, target string, limit int64) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release download returned HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, limit+1))
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 || written > limit {
		return errors.New("release download is empty or oversized")
	}
	return nil
}

func prepareCosign(ctx context.Context, client *http.Client, goos, goarch, directory string) (string, error) {
	record, ok := cosignMatrix[goos+"/"+goarch]
	if !ok {
		return "", errors.New("Cosign is unavailable for this platform")
	}
	target := filepath.Join(directory, record.Filename)
	uri := "https://github.com/sigstore/cosign/releases/download/" + cosignVersion + "/" + record.Filename
	if err := downloadFile(ctx, client, uri, target, 256<<20); err != nil {
		return "", err
	}
	hash, err := fileSHA256(target)
	if err != nil || hash != record.SHA256 {
		return "", errors.New("pinned Cosign checksum mismatch")
	}
	if err := os.Chmod(target, 0o700); err != nil {
		return "", err
	}
	return target, nil
}

func prepareLocalVerifier(path, expectedHash string) (string, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("local update verifier fixture is unavailable")
	}
	hash, err := fileSHA256(path)
	if err != nil || hash != expectedHash {
		return "", errors.New("local update verifier fixture checksum mismatch")
	}
	return path, nil
}

type commandVerifier struct {
	commands CommandRunner
	binary   string
}

func (verifier commandVerifier) VerifyBlob(ctx context.Context, manifest, bundle, identity, issuer string) error {
	result, err := verifier.commands.Run(ctx, nil, verifier.binary, "verify-blob", "--bundle", bundle, "--certificate-identity", identity, "--certificate-oidc-issuer", issuer, manifest)
	if err != nil || result.ExitCode != 0 {
		return errors.New("release manifest signature verification failed")
	}
	return nil
}

func extractReleaseBinary(archivePath, filename, destination, goos string) error {
	if strings.HasSuffix(filename, ".zip") {
		return extractZipBinary(archivePath, destination, goos)
	}
	if strings.HasSuffix(filename, ".tar.gz") {
		return extractTarBinary(archivePath, destination, goos)
	}
	return errors.New("release archive format is unsupported")
}

func extractZipBinary(archivePath, destination, goos string) error {
	archive, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer archive.Close()
	found := false
	for _, entry := range archive.File {
		name := strings.TrimSuffix(entry.Name, "/")
		if entry.Name == "" || !safeArchiveEntry(entry.Name) || entry.Mode()&os.ModeSymlink != 0 {
			return errors.New("release archive contains an unsafe path or link")
		}
		if entry.FileInfo().IsDir() {
			if !allowedArchiveDirectory(name) {
				return errors.New("release archive contains an unexpected directory")
			}
			continue
		}
		if !allowedArchiveFile(name, goos) {
			return errors.New("release archive contains an unexpected file")
		}
		if isApplicationBinary(name, goos) {
			if found {
				return errors.New("release archive contains duplicate executables")
			}
			reader, err := entry.Open()
			if err != nil {
				return err
			}
			if err := copyReaderToNewFile(reader, destination, 0o700, 256<<20); err != nil {
				_ = reader.Close()
				return err
			}
			if err := reader.Close(); err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		return errors.New("release archive has no application binary")
	}
	return nil
}

func extractTarBinary(archivePath, destination, goos string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	found := false
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if !safeArchiveEntry(header.Name) {
			return errors.New("release archive contains an unsafe path")
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if !allowedArchiveDirectory(name) {
				return errors.New("release archive contains an unexpected directory")
			}
		case tar.TypeReg, tar.TypeRegA:
			if !allowedArchiveFile(name, goos) {
				return errors.New("release archive contains an unexpected file")
			}
			if isApplicationBinary(name, goos) {
				if found {
					return errors.New("release archive contains duplicate executables")
				}
				if err := copyReaderToNewFile(reader, destination, 0o700, 256<<20); err != nil {
					return err
				}
				found = true
			}
		default:
			return errors.New("release archive contains a link or special file")
		}
	}
	if !found {
		return errors.New("release archive has no application binary")
	}
	return nil
}

func copyReaderToNewFile(reader io.Reader, target string, mode os.FileMode, limit int64) error {
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(file, io.LimitReader(reader, limit+1))
	if copyErr == nil {
		copyErr = file.Sync()
	}
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	if written == 0 || written > limit {
		return errors.New("staged binary is empty or oversized")
	}
	return nil
}

func safeArchiveEntry(value string) bool {
	if value == "" || strings.Contains(value, "\\") || strings.HasPrefix(value, "/") {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	trimmed := strings.TrimSuffix(value, "/")
	return clean == trimmed && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func allowedArchiveDirectory(value string) bool { return value == "docs" || value == "scripts" }

func allowedArchiveFile(value, goos string) bool {
	if isApplicationBinary(value, goos) {
		return true
	}
	switch value {
	case "README.md", "docs/DEPLOYMENT.md", "docs/SECURITY.md", "scripts/install.ps1", "scripts/install.sh", "scripts/uninstall.ps1", "scripts/uninstall.sh", "scripts/cosign-v3.1.3-sha256.txt":
		return true
	default:
		return false
	}
}

func isApplicationBinary(value, goos string) bool {
	if goos == "windows" {
		return value == "interviewcraft.exe"
	}
	return value == "interviewcraft"
}

func compareVersions(left, right string) (int, error) {
	parse := func(value string) ([3]int, []string, error) {
		var numbers [3]int
		core := strings.SplitN(value, "+", 2)[0]
		parts := strings.SplitN(core, "-", 2)
		items := strings.Split(parts[0], ".")
		if len(items) != 3 {
			return numbers, nil, errors.New("invalid semantic version")
		}
		for index := range items {
			if len(items[index]) > 1 && items[index][0] == '0' {
				return numbers, nil, errors.New("invalid semantic version numeric identifier")
			}
			number, err := strconv.Atoi(items[index])
			if err != nil {
				return numbers, nil, err
			}
			numbers[index] = number
		}
		var prerelease []string
		if len(parts) == 2 {
			prerelease = strings.Split(parts[1], ".")
			for _, identifier := range prerelease {
				if identifier == "" || (isNumeric(identifier) && len(identifier) > 1 && identifier[0] == '0') {
					return numbers, nil, errors.New("invalid semantic version prerelease")
				}
			}
		}
		return numbers, prerelease, nil
	}
	leftNumbers, leftPre, err := parse(left)
	if err != nil {
		return 0, err
	}
	rightNumbers, rightPre, err := parse(right)
	if err != nil {
		return 0, err
	}
	for index := 0; index < 3; index++ {
		if leftNumbers[index] < rightNumbers[index] {
			return -1, nil
		}
		if leftNumbers[index] > rightNumbers[index] {
			return 1, nil
		}
	}
	if len(leftPre) == 0 && len(rightPre) == 0 {
		return 0, nil
	}
	if len(leftPre) == 0 {
		return 1, nil
	}
	if len(rightPre) == 0 {
		return -1, nil
	}
	limit := len(leftPre)
	if len(rightPre) < limit {
		limit = len(rightPre)
	}
	for index := 0; index < limit; index++ {
		leftNumeric, rightNumeric := isNumeric(leftPre[index]), isNumeric(rightPre[index])
		switch {
		case leftNumeric && rightNumeric:
			leftNumber, _ := strconv.ParseUint(leftPre[index], 10, 64)
			rightNumber, _ := strconv.ParseUint(rightPre[index], 10, 64)
			if leftNumber < rightNumber {
				return -1, nil
			}
			if leftNumber > rightNumber {
				return 1, nil
			}
		case leftNumeric:
			return -1, nil
		case rightNumeric:
			return 1, nil
		default:
			if comparison := strings.Compare(leftPre[index], rightPre[index]); comparison != 0 {
				return comparison, nil
			}
		}
	}
	if len(leftPre) < len(rightPre) {
		return -1, nil
	}
	if len(leftPre) > len(rightPre) {
		return 1, nil
	}
	return 0, nil
}

func isNumeric(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}
