package theme

import (
	"strings"
	"testing"
)

func TestResolveModesAndSemanticRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mode       Mode
		wantCanvas string
		isDefault  bool
	}{
		{name: "auto", mode: Auto, wantCanvas: "#10110E", isDefault: true},
		{name: "dark", mode: Dark, wantCanvas: "#10110E"},
		{name: "light", mode: Light, wantCanvas: "#F2F1EA"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			resolved, err := Resolve(Options{Mode: test.mode, ColorMode: NoColor})
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if resolved.Canvas.Hex != test.wantCanvas ||
				resolved.Canvas.IsDefault != test.isDefault {
				t.Fatalf("canvas=%#v", resolved.Canvas)
			}
			if resolved.Focus.Hex == "" || resolved.Error.Hex == "" ||
				resolved.Coach.Hex == "" {
				t.Fatalf("semantic palette incomplete: %#v", resolved)
			}
		})
	}
}

func TestParseOptionsSwitchesAccessibilityCapabilities(t *testing.T) {
	t.Parallel()

	options, err := ParseOptions([]string{
		"--theme", "light",
		"--ansi-16",
		"--ascii",
		"--reduce-motion",
	})
	if err != nil {
		t.Fatalf("ParseOptions: %v", err)
	}
	resolved, err := Resolve(options)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Mode != Light || resolved.ColorMode != ANSI16 ||
		!resolved.UseASCII || !resolved.ReduceMotion {
		t.Fatalf("resolved options=%#v", resolved)
	}
	if resolved.Glyphs.TopLeft != "+" || resolved.Glyphs.Cursor != ">" ||
		resolved.Glyphs.Stream != "|" {
		t.Fatalf("ASCII glyphs=%#v", resolved.Glyphs)
	}
}

func TestPaintUsesTrueColorANSI16AndNoColor(t *testing.T) {
	t.Parallel()

	trueColor, _ := Resolve(Options{Mode: Dark, ColorMode: TrueColor})
	ansi, _ := Resolve(Options{Mode: Dark, ColorMode: ANSI16})
	plain, _ := Resolve(Options{Mode: Dark, ColorMode: NoColor})

	if got := trueColor.Paint(Error, "! failed"); !strings.Contains(got, "38;2;255;107;91") {
		t.Fatalf("true-color output=%q", got)
	}
	if got := trueColor.Paint(Panel, " content "); !strings.Contains(got, "48;2;24;26;21") {
		t.Fatalf("true-color background output=%q", got)
	}
	if got := ansi.Paint(Error, "! failed"); !strings.Contains(got, "\x1b[91m") {
		t.Fatalf("ANSI-16 output=%q", got)
	}
	if got := plain.Paint(Error, "! failed"); got != "! failed" {
		t.Fatalf("no-color output=%q", got)
	}
}

func TestResolveRejectsUnsupportedOptions(t *testing.T) {
	t.Parallel()

	if _, err := Resolve(Options{Mode: "sepia"}); err == nil {
		t.Fatal("unsupported theme unexpectedly accepted")
	}
	if _, err := ParseOptions([]string{"--mouse-only"}); err == nil {
		t.Fatal("unknown option unexpectedly accepted")
	}
}
