package resume

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
)

func TestExtractSupportsPDFDOCXTXTAndPaste(t *testing.T) {
	t.Parallel()

	docxPath := writeDOCX(t, "Jane Doe", "Inventory Platform", "Go PostgreSQL")
	tests := []struct {
		name       string
		input      Input
		wantKind   profile.SourceKind
		wantText   []string
		wantStream bool
	}{
		{
			name:       "PDF",
			input:      Input{Path: filepath.Join("testdata", "sample.pdf")},
			wantKind:   profile.SourcePDF,
			wantText:   []string{"Jane Doe", "Payment Platform", "PostgreSQL"},
			wantStream: true,
		},
		{
			name:       "DOCX",
			input:      Input{Path: docxPath},
			wantKind:   profile.SourceDOCX,
			wantText:   []string{"Jane Doe", "Inventory Platform", "Go PostgreSQL"},
			wantStream: true,
		},
		{
			name:       "TXT",
			input:      Input{Path: filepath.Join("testdata", "sample.txt")},
			wantKind:   profile.SourceTXT,
			wantText:   []string{"Jane Doe", "Backend Engineer", "Redis"},
			wantStream: true,
		},
		{
			name:       "paste",
			input:      Input{Kind: profile.SourcePaste, Text: "Jane Doe\r\n\r\nGo Engineer"},
			wantKind:   profile.SourcePaste,
			wantText:   []string{"Jane Doe", "Go Engineer"},
			wantStream: false,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var states []async.State[Progress]

			source, err := (Extractor{}).Extract(
				context.Background(),
				test.input,
				func(state async.State[Progress]) {
					states = append(states, state)
				},
			)

			if err != nil {
				t.Fatalf("Extract: %v", err)
			}
			if source.Kind != test.wantKind || source.Name == "" {
				t.Fatalf("source = %#v", source)
			}
			for _, expected := range test.wantText {
				if !strings.Contains(source.Text, expected) {
					t.Errorf("text %q does not contain %q", source.Text, expected)
				}
			}
			assertExtractionLifecycle(t, states, test.wantStream, async.Succeeded)
		})
	}
}

func TestExtractCancellationReportsFailedWithoutResult(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var states []async.State[Progress]

	source, err := (Extractor{}).Extract(
		ctx,
		Input{Path: filepath.Join("testdata", "sample.txt")},
		func(state async.State[Progress]) {
			states = append(states, state)
			if state.Phase == async.Streaming {
				cancel()
			}
		},
	)

	if !reflect.DeepEqual(source, profile.Source{}) {
		t.Fatalf("cancelled source = %#v, want zero value", source)
	}
	if !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
		t.Fatalf("cancel error = %v", err)
	}
	assertExtractionLifecycle(t, states, true, async.Failed)
}

func TestExtractEmptyInputAndOversizedFileAreRejected(t *testing.T) {
	t.Parallel()

	_, emptyErr := (Extractor{}).Extract(
		context.Background(),
		Input{Kind: profile.SourcePaste, Text: " \r\n\t "},
		nil,
	)
	if !domainerr.IsCode(emptyErr, domainerr.CodeValidation) {
		t.Fatalf("empty paste error = %v", emptyErr)
	}

	path := filepath.Join(t.TempDir(), "too-large.txt")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create oversized fixture: %v", err)
	}
	if err := file.Truncate(MaxFileBytes + 1); err != nil {
		_ = file.Close()
		t.Fatalf("truncate oversized fixture: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close oversized fixture: %v", err)
	}
	_, oversizedErr := (Extractor{}).Extract(
		context.Background(),
		Input{Path: path},
		nil,
	)
	if !domainerr.IsCode(oversizedErr, domainerr.CodeValidation) {
		t.Fatalf("oversized file error = %v", oversizedErr)
	}
	if !strings.Contains(oversizedErr.Error(), "10MB") {
		t.Fatalf("oversized error is not actionable: %v", oversizedErr)
	}
}

func TestExtractFailuresRetainSourceAndPasteFallback(t *testing.T) {
	t.Parallel()

	missing := filepath.Join(t.TempDir(), "missing-resume.pdf")
	_, pathErr := (Extractor{}).Extract(
		context.Background(),
		Input{Path: missing},
		nil,
	)
	assertActionableResumeError(t, pathErr, missing)

	brokenPath := filepath.Join(t.TempDir(), "broken-resume.pdf")
	if err := os.WriteFile(brokenPath, []byte("not a PDF"), 0o600); err != nil {
		t.Fatalf("write broken PDF: %v", err)
	}
	_, formatErr := (Extractor{}).Extract(
		context.Background(),
		Input{Path: brokenPath},
		nil,
	)
	assertActionableResumeError(t, formatErr, filepath.Base(brokenPath))
}

func assertActionableResumeError(
	t *testing.T,
	err error,
	expectedSource string,
) {
	t.Helper()
	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T, want *domainerr.Error", err)
	}
	if !strings.Contains(typed.Message, expectedSource) {
		t.Fatalf("message %q does not retain %q", typed.Message, expectedSource)
	}
	if !strings.Contains(typed.RecoveryAction, "[p]") {
		t.Fatalf("recovery %q does not provide paste fallback", typed.RecoveryAction)
	}
}

func assertExtractionLifecycle(
	t *testing.T,
	states []async.State[Progress],
	wantStreaming bool,
	wantTerminal async.Phase,
) {
	t.Helper()
	if len(states) < 2 {
		t.Fatalf("states = %#v", states)
	}
	if states[0].Phase != async.Pending {
		t.Fatalf("first phase = %s, want pending", states[0].Phase)
	}
	if states[len(states)-1].Phase != wantTerminal {
		t.Fatalf("last phase = %s, want %s", states[len(states)-1].Phase, wantTerminal)
	}
	streaming := false
	for index, state := range states {
		if err := state.Validate(); err != nil {
			t.Fatalf("state %d invalid: %v", index, err)
		}
		if state.Phase == async.Streaming {
			streaming = true
			if state.Value == nil || state.Value.SourceName == "" {
				t.Fatalf("streaming state %d has no source identity: %#v", index, state)
			}
		}
	}
	if streaming != wantStreaming {
		t.Fatalf("streaming = %v, want %v; states=%#v", streaming, wantStreaming, states)
	}
}

func writeDOCX(t *testing.T, paragraphs ...string) string {
	t.Helper()
	var payload bytes.Buffer
	archive := zip.NewWriter(&payload)
	document, err := archive.Create("word/document.xml")
	if err != nil {
		t.Fatalf("create DOCX entry: %v", err)
	}
	var xml strings.Builder
	xml.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`)
	xml.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, paragraph := range paragraphs {
		xml.WriteString("<w:p><w:r><w:t>")
		xml.WriteString(paragraph)
		xml.WriteString("</w:t></w:r></w:p>")
	}
	xml.WriteString("</w:body></w:document>")
	if _, err := document.Write([]byte(xml.String())); err != nil {
		t.Fatalf("write DOCX XML: %v", err)
	}
	if err := archive.Close(); err != nil {
		t.Fatalf("close DOCX archive: %v", err)
	}
	path := filepath.Join(t.TempDir(), "resume.docx")
	if err := os.WriteFile(path, payload.Bytes(), 0o600); err != nil {
		t.Fatalf("write DOCX fixture: %v", err)
	}
	return path
}
