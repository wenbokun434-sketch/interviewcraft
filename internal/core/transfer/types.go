// Package transfer exports, imports, and deletes local training data without
// ever reading Provider credentials.
package transfer

import (
	"encoding/json"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	corereport "github.com/interviewcraft/interviewcraft/internal/core/report"
)

const (
	// BundleVersion is the durable Lite-to-Lite transfer contract.
	BundleVersion = "interviewcraft-transfer-v1"
	// ReportExportVersion identifies standalone JSON report exports.
	ReportExportVersion = "interviewcraft-report-export-v1"
	maxPackageBytes     = 64 << 20
)

// Format selects a transfer package or one standalone report representation.
type Format string

const (
	FormatPackage  Format = "package"
	FormatJSON     Format = "json"
	FormatMarkdown Format = "markdown"
)

// Progress is emitted in deterministic stage order.
type Progress struct {
	Stage   string
	Current int
	Total   int
	Message string
}

// Observer receives typed export/import/delete lifecycle states.
type Observer func(async.State[Progress])

// Inventory is the non-sensitive data summary shown by the settings screen.
type Inventory struct {
	Profiles   int
	Scenarios  int
	Sessions   int
	Reports    int
	CoachItems int
	SessionIDs []string
}

// ExportOptions selects output without implicit privacy expansion.
type ExportOptions struct {
	Format              Format
	OutputPath          string
	SessionID           string
	IncludeCoachContent bool
}

// ExportResult reports the created artifact and included record count.
type ExportResult struct {
	Path        string
	Format      Format
	RecordCount int
}

// ImportResult reports the restored root counts.
type ImportResult struct {
	Profiles int
	Sessions int
	Reports  int
}

// Table is one fixed-schema, ordered SQLite table in a transfer bundle.
// Cells are JSON scalars; payload JSON remains a string and is revalidated.
type Table struct {
	Name    string              `json:"name"`
	Columns []string            `json:"columns"`
	Rows    [][]json.RawMessage `json:"rows"`
}

// Bundle is a complete local training graph. Provider configuration is absent
// by construction and is not a permitted table.
type Bundle struct {
	Version              string    `json:"version"`
	ExportedAt           time.Time `json:"exported_at"`
	CoachContentIncluded bool      `json:"coach_content_included"`
	Tables               []Table   `json:"tables"`
}

// CoachExcerpt is optional source text in a standalone report export.
type CoachExcerpt struct {
	ID         string    `json:"id"`
	QuestionID string    `json:"question_id"`
	HelpLevel  string    `json:"help_level"`
	Content    string    `json:"content"`
	PolicyNote string    `json:"policy_note,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// ReportExport is the standalone JSON report artifact.
type ReportExport struct {
	Version              string              `json:"version"`
	ExportedAt           time.Time           `json:"exported_at"`
	CoachContentIncluded bool                `json:"coach_content_included"`
	Report               corereport.Document `json:"report"`
	CoachTranscript      []CoachExcerpt      `json:"coach_transcript"`
}

// DeleteScope distinguishes one session from all local training data.
type DeleteScope string

const (
	DeleteSession DeleteScope = "session"
	DeleteAll     DeleteScope = "all"
)

// Confirmation is deliberately exact so callers cannot bypass confirmation
// with a boolean default.
type Confirmation struct {
	Scope     DeleteScope
	SessionID string
	Phrase    string
}

// SessionDeletePhrase returns the exact phrase required for one session.
func SessionDeletePhrase(sessionID string) string {
	return "delete session " + sessionID
}

// AllDeletePhrase returns the exact phrase required for all training data.
func AllDeletePhrase() string {
	return "delete all training data"
}
