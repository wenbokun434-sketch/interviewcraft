package version

import (
	"bytes"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestRoundTripAndDirectoryVerification(t *testing.T) {
	directory := t.TempDir()
	manifest := fixtureManifest(t, directory)
	var encoded bytes.Buffer
	if err := EncodeManifest(&encoded, manifest); err != nil {
		t.Fatalf("EncodeManifest: %v", err)
	}
	parsed, err := ParseManifest(strings.NewReader(encoded.String()))
	if err != nil {
		t.Fatalf("ParseManifest: %v\n%s", err, encoded.String())
	}
	if parsed.Metadata.Version != "1.2.3" || len(parsed.Assets) != 6 || len(parsed.SBOMs) != 1 {
		t.Fatalf("parsed=%#v", parsed)
	}
	if err := parsed.VerifyDirectory(directory); err != nil {
		t.Fatalf("VerifyDirectory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(directory, parsed.Assets[0].Filename), []byte("tampered"), 0o600); err != nil {
		t.Fatalf("tamper: %v", err)
	}
	if err := parsed.VerifyDirectory(directory); err == nil {
		t.Fatal("tampered asset passed verification")
	}
}

func TestManifestRejectsMalformedAndIncompleteDocuments(t *testing.T) {
	directory := t.TempDir()
	var encoded bytes.Buffer
	if err := EncodeManifest(&encoded, fixtureManifest(t, directory)); err != nil {
		t.Fatalf("EncodeManifest: %v", err)
	}
	valid := encoded.String()
	lines := strings.Split(strings.TrimSuffix(valid, "\n"), "\n")
	assetFields := strings.Split(lines[2], "\t")
	tests := map[string]string{
		"empty":              "",
		"unknown header":     strings.Replace(valid, ManifestHeader, "other-release-v1", 1),
		"unknown row":        valid + "future\t-\t-\tthing\t" + strings.Repeat("a", 64) + "\t1\n",
		"duplicate platform": strings.Join(append(lines, lines[2]), "\n") + "\n",
		"missing platform":   strings.Join(append(lines[:2], lines[3:]...), "\n") + "\n",
		"missing sbom":       strings.Join(lines[:len(lines)-1], "\n") + "\n",
		"path separator":     strings.Replace(valid, assetFields[3], "../archive.zip", 1),
		"uppercase sha":      strings.Replace(valid, assetFields[4], strings.ToUpper(assetFields[4]), 1),
		"zero size":          strings.Replace(valid, "\t"+assetFields[5]+"\n", "\t0\n", 1),
		"space fields":       strings.Replace(valid, "asset\t", "asset ", 1),
		"duplicate filename": strings.Replace(valid, strings.Split(lines[3], "\t")[3], assetFields[3], 1),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest(strings.NewReader(document)); err == nil {
				t.Fatalf("malformed manifest accepted:\n%s", document)
			}
		})
	}
}

func TestManifestRejectsTrailingPartialLineAndOversizeInput(t *testing.T) {
	directory := t.TempDir()
	var encoded bytes.Buffer
	if err := EncodeManifest(&encoded, fixtureManifest(t, directory)); err != nil {
		t.Fatalf("EncodeManifest: %v", err)
	}
	tooLong := encoded.String() + "sbom\t-\t-\t" + strings.Repeat("a", 1024*1024) + "\t" + strings.Repeat("b", 64) + "\t1"
	if _, err := ParseManifest(strings.NewReader(tooLong)); err == nil {
		t.Fatal("oversize input accepted")
	}
}

func TestManifestTemporaryKeySignatureRejectsTamper(t *testing.T) {
	var encoded bytes.Buffer
	if err := EncodeManifest(&encoded, fixtureManifest(t, t.TempDir())); err != nil {
		t.Fatalf("EncodeManifest: %v", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	digest := sha256.Sum256(encoded.Bytes())
	signature, err := rsa.SignPSS(rand.Reader, key, cryptoHashSHA256, digest[:], nil)
	if err != nil {
		t.Fatalf("SignPSS: %v", err)
	}
	if err := rsa.VerifyPSS(&key.PublicKey, cryptoHashSHA256, digest[:], signature, nil); err != nil {
		t.Fatalf("VerifyPSS: %v", err)
	}
	tampered := sha256.Sum256(append(encoded.Bytes(), '!'))
	if err := rsa.VerifyPSS(&key.PublicKey, cryptoHashSHA256, tampered[:], signature, nil); err == nil {
		t.Fatal("tampered manifest signature verified")
	}
	if _, err := x509.MarshalPKIXPublicKey(&key.PublicKey); err != nil {
		t.Fatalf("MarshalPKIXPublicKey: %v", err)
	}
}

const cryptoHashSHA256 = crypto.SHA256

func fixtureManifest(t *testing.T, directory string) Manifest {
	t.Helper()
	assets := make([]File, 0, len(supportedPlatforms))
	for _, platform := range supportedPlatforms {
		extension := ".tar.gz"
		if platform.GOOS == "windows" {
			extension = ".zip"
		}
		filename := "interviewcraft_1.2.3_" + platform.GOOS + "_" + platform.GOARCH + extension
		assets = append(assets, writeFixtureFile(t, directory, "asset", platform.GOOS, platform.GOARCH, filename, []byte(platform.GOOS+"/"+platform.GOARCH)))
	}
	checksum := writeFixtureFile(t, directory, "checksum", "-", "-", "checksums.txt", []byte("fixture checksums\n"))
	sbom := writeFixtureFile(t, directory, "sbom", "-", "-", "interviewcraft_1.2.3.spdx.json", []byte(`{"spdxVersion":"SPDX-2.3"}`))
	return Manifest{
		Metadata: Metadata{Version: "1.2.3", Commit: "0123456789abcdef", CreatedUTC: time.Date(2026, 8, 10, 12, 0, 0, 0, time.UTC)},
		Assets:   assets, Checksum: checksum, SBOMs: []File{sbom},
	}
}

func writeFixtureFile(t *testing.T, directory, kind, goos, goarch, filename string, payload []byte) File {
	t.Helper()
	if err := os.WriteFile(filepath.Join(directory, filename), payload, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	digest := sha256.Sum256(payload)
	return File{Kind: kind, GOOS: goos, GOARCH: goarch, Filename: filename, SHA256: hex.EncodeToString(digest[:]), Size: int64(len(payload))}
}
