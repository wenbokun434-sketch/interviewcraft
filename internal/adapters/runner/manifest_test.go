package runner

import (
	"strings"
	"testing"
)

func TestParseRunnerManifestStrictContract(t *testing.T) {
	valid := validRunnerManifest("1.2.3", strings.Repeat("a", 64), strings.Repeat("b", 64))
	manifest, err := ParseRunnerManifest(strings.NewReader(valid))
	if err != nil || manifest.Version != "1.2.3" || len(manifest.Images) != 2 {
		t.Fatalf("manifest=%#v err=%v", manifest, err)
	}
	image, err := manifest.ImageFor("arm64")
	if err != nil || image.Digest != "sha256:"+strings.Repeat("b", 64) {
		t.Fatalf("image=%#v err=%v", image, err)
	}

	tests := map[string]func(string) string{
		"empty":          func(string) string { return "" },
		"unknown header": func(value string) string { return strings.Replace(value, RunnerManifestHeader, "unknown", 1) },
		"unknown row": func(value string) string {
			return strings.Replace(value, "image\tlinux\tamd64", "asset\tlinux\tamd64", 1)
		},
		"duplicate platform": func(value string) string {
			return strings.Replace(value, "image\tlinux\tarm64", "image\tlinux\tamd64", 1)
		},
		"uppercase digest": func(value string) string {
			return strings.Replace(value, strings.Repeat("a", 64), strings.Repeat("A", 64), 1)
		},
		"foreign repository": func(value string) string {
			return strings.Replace(value, OfficialRepository, "ghcr.io/attacker/runner", 1)
		},
		"wrong user":     func(value string) string { return strings.Replace(value, "65532:65532", "0:0", 1) },
		"wrong protocol": func(value string) string { return strings.Replace(value, responseVersion, "runner-v0", 1) },
		"blank line":     func(value string) string { return strings.Replace(value, "\nimage", "\n\nimage", 1) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseRunnerManifest(strings.NewReader(mutate(valid))); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func validRunnerManifest(version, amd64, arm64 string) string {
	return RunnerManifestHeader + "\n" +
		"meta\t" + version + "\t" + strings.Repeat("c", 40) + "\t2026-08-11T00:00:00Z\n" +
		"image\tlinux\tamd64\t" + OfficialRepository + "\tsha256:" + amd64 + "\t" + responseVersion + "\t65532:65532\n" +
		"image\tlinux\tarm64\t" + OfficialRepository + "\tsha256:" + arm64 + "\t" + responseVersion + "\t65532:65532\n"
}
