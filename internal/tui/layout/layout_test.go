package layout

import (
	"fmt"
	"strings"
	"testing"
)

func TestCalculateResponsivePlans(t *testing.T) {
	t.Parallel()

	tests := []struct {
		width     int
		height    int
		wantMode  Mode
		wantTrace int
		wantCoach int
	}{
		{width: 160, height: 48, wantMode: Wide, wantTrace: 20, wantCoach: 38},
		{width: 120, height: 36, wantMode: Split, wantCoach: 38},
		{width: 80, height: 24, wantMode: Narrow},
		{width: 79, height: 24, wantMode: Blocked},
		{width: 80, height: 23, wantMode: Blocked},
	}
	for _, test := range tests {
		test := test
		t.Run(fmt.Sprintf("%dx%d", test.width, test.height), func(t *testing.T) {
			t.Parallel()

			plan := Calculate(test.width, test.height)
			if plan.Mode != test.wantMode ||
				plan.TraceWidth != test.wantTrace ||
				plan.CoachWidth != test.wantCoach {
				t.Fatalf("Calculate(%d,%d)=%#v", test.width, test.height, plan)
			}
			if err := plan.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
		})
	}
}

func TestVisibleWidthAndClippingHandleCJKAndCombiningText(t *testing.T) {
	t.Parallel()

	value := "A面试e\u0301"
	if got := VisibleWidth(value); got != 6 {
		t.Fatalf("VisibleWidth(%q)=%d, want 6", value, got)
	}
	if got := ClipRight(value, 5); got != "A面试" {
		t.Fatalf("ClipRight=%q", got)
	}
	if got := Fit("面试", 6); VisibleWidth(got) != 6 {
		t.Fatalf("Fit=%q width=%d", got, VisibleWidth(got))
	}

	colored := "\x1b[91m! failed\x1b[0m"
	if got := VisibleWidth(colored); got != len("! failed") {
		t.Fatalf("ANSI width=%d", got)
	}
}

func TestTruncateLeftPreservesUsefulTail(t *testing.T) {
	t.Parallel()

	path := `C:\very\long\workspace\candidate\resume-final.pdf`
	got := TruncateLeft(path, 24, false)
	if VisibleWidth(got) > 24 || !strings.HasSuffix(got, "resume-final.pdf") ||
		!strings.HasPrefix(got, "…") {
		t.Fatalf("TruncateLeft=%q width=%d", got, VisibleWidth(got))
	}
	gotASCII := TruncateLeft(path, 24, true)
	if !strings.HasPrefix(gotASCII, "...") || VisibleWidth(gotASCII) > 24 {
		t.Fatalf("ASCII TruncateLeft=%q", gotASCII)
	}
	right := TruncateRight("ollama/qwen3-coder-very-long-model-name", 24, false)
	if !strings.HasPrefix(right, "ollama/") || !strings.HasSuffix(right, "…") ||
		VisibleWidth(right) > 24 {
		t.Fatalf("TruncateRight=%q", right)
	}
}

func TestFocusNavigationAndOverlayRestoreExactTarget(t *testing.T) {
	t.Parallel()

	model, err := NewFocusModel("trace", "composer", "coach")
	if err != nil {
		t.Fatalf("NewFocusModel: %v", err)
	}
	model.Handle(KeyTab)
	if got := model.Active(); got != "composer" {
		t.Fatalf("active=%q", got)
	}
	if err := model.OpenOverlay("coach-overlay"); err != nil {
		t.Fatalf("OpenOverlay: %v", err)
	}
	if err := model.OpenOverlay("nested-overlay"); err == nil {
		t.Fatal("nested overlay unexpectedly accepted")
	}
	model.Handle(KeyTab)
	if got := model.Active(); got != "coach-overlay" {
		t.Fatalf("overlay focus escaped to %q", got)
	}
	model.Handle(KeyEscape)
	if got := model.Active(); got != "composer" {
		t.Fatalf("restored active=%q", got)
	}
	model.Handle(KeyShiftTab)
	if got := model.Active(); got != "trace" {
		t.Fatalf("previous active=%q", got)
	}
}
