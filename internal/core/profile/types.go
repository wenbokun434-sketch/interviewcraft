// Package profile owns the editable, evidence-traceable CandidateProfile
// aggregate and its persistence boundary.
package profile

import (
	"context"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// SourceKind identifies how resume text entered the application.
type SourceKind string

const (
	SourcePDF   SourceKind = "pdf"
	SourceDOCX  SourceKind = "docx"
	SourceTXT   SourceKind = "txt"
	SourcePaste SourceKind = "paste"
)

// MaxSourceBytes is the shared hard limit for normalized resume input.
const MaxSourceBytes = 10 << 20

// Source is normalized resume text plus a safe display name.
type Source struct {
	Kind SourceKind
	Name string
	Text string
}

// Metadata persists the source text and field locks outside the strict Agent
// output contract.
type Metadata struct {
	Source             Source
	LockedFactIDs      []contracts.EvidenceID
	LockedInferenceIDs []string
	CreatedAt          time.Time
	UpdatedAt          time.Time
}

// Aggregate is one editable profile and its local-only metadata.
type Aggregate struct {
	ID          string
	Candidate   contracts.CandidateProfile
	Metadata    Metadata
	ConfirmedAt *time.Time
}

// Repository saves Candidate plus metadata atomically.
type Repository interface {
	SaveProfileAggregate(context.Context, Aggregate) error
	GetProfileAggregate(context.Context, string) (Aggregate, bool, error)
	DeleteProfile(context.Context, string) (bool, error)
}

// Structurer turns extracted text into the strict CandidateProfile contract.
type Structurer interface {
	Structure(
		context.Context,
		Source,
		string,
	) (contracts.CandidateProfile, error)
}

// Validate rejects unsupported, unnamed, or empty resume sources.
func (source Source) Validate() error {
	if source.Kind != SourcePDF &&
		source.Kind != SourceDOCX &&
		source.Kind != SourceTXT &&
		source.Kind != SourcePaste {
		return validationError("validate resume source", "简历来源类型无效。")
	}
	if strings.TrimSpace(source.Name) == "" {
		return validationError("validate resume source", "简历来源名称不能为空。")
	}
	if strings.TrimSpace(source.Text) == "" {
		return validationError("validate resume source", "简历文本不能为空。")
	}
	if len(source.Text) > MaxSourceBytes {
		return domainerr.New(
			domainerr.CodeValidation,
			"validate resume source",
			"简历文本不能超过 10MB。",
			"缩短文本或压缩文件后重试。",
			false,
		)
	}
	return nil
}

func validationError(operation, message string) *domainerr.Error {
	return domainerr.New(
		domainerr.CodeValidation,
		operation,
		message,
		"提供有效简历文本后重试。",
		false,
	)
}
