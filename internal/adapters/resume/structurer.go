package resume

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
)

const profileSystemPrompt = `You are the InterviewCraft Profile Agent.
Return only one CandidateProfile JSON object that matches the supplied schema.
Never invent employment, education, projects, achievements, dates, skills, or responsibilities.
Every fact must be directly supported by one exact contiguous source_span from resume_text.
source_span.start and source_span.end are zero-based UTF-8 byte offsets, and resume_text[start:end] must equal source_span.text exactly.
Keep uncertain interpretations only in inferences with needs_confirmation=true and a confidence from 0 to 1.
Never copy an inference into facts. Include projects and skills only when their exact text occurs in resume_text.
Set target_role exactly to the requested target_role.`

// ProfileStructurer connects extracted resume text to the published
// CandidateProfile structured-output contract.
type ProfileStructurer struct {
	generator llm.Generator
}

// NewProfileStructurer constructs an LLM-backed profile structurer.
func NewProfileStructurer(generator llm.Generator) *ProfileStructurer {
	return &ProfileStructurer{generator: generator}
}

// Structure generates and strictly validates one CandidateProfile. Invalid
// Schema output is retried once by the shared Provider boundary.
func (structurer *ProfileStructurer) Structure(
	ctx context.Context,
	source profile.Source,
	targetRole string,
) (contracts.CandidateProfile, error) {
	if err := source.Validate(); err != nil {
		return contracts.CandidateProfile{}, err
	}
	targetRole = strings.TrimSpace(targetRole)
	if targetRole == "" {
		return contracts.CandidateProfile{}, domainerr.New(
			domainerr.CodeValidation,
			"structure CandidateProfile",
			"目标岗位不能为空。",
			"填写目标岗位后重试。",
			false,
		)
	}
	if structurer == nil {
		return contracts.CandidateProfile{}, domainerr.New(
			domainerr.CodeDependencyUnavailable,
			"structure CandidateProfile",
			"画像生成器尚未初始化。",
			"配置模型 Provider 后重试。",
			true,
		)
	}
	schema, ok := contracts.JSONSchema(contracts.SchemaCandidateProfile)
	if !ok {
		return contracts.CandidateProfile{}, domainerr.Wrap(
			domainerr.CodeDependencyUnavailable,
			"structure CandidateProfile",
			"CandidateProfile schema",
			"画像结构契约不可用。",
			"重新安装或更新 InterviewCraft 后重试。",
			false,
			errors.New("CandidateProfile schema is not published"),
		)
	}
	input, err := json.Marshal(struct {
		TargetRole string             `json:"target_role"`
		SourceKind profile.SourceKind `json:"source_kind"`
		SourceName string             `json:"source_name"`
		ResumeText string             `json:"resume_text"`
	}{
		TargetRole: targetRole,
		SourceKind: source.Kind,
		SourceName: source.Name,
		ResumeText: source.Text,
	})
	if err != nil {
		return contracts.CandidateProfile{}, domainerr.Wrap(
			domainerr.CodeValidation,
			"encode profile source",
			"profile structurer",
			"无法准备画像输入。",
			"检查简历文本后重试。",
			false,
			err,
		)
	}
	return llm.GenerateStructured(
		ctx,
		structurer.generator,
		llm.Request{
			SchemaName: string(contracts.SchemaCandidateProfile),
			Schema:     schema,
			Messages: []llm.Message{
				{Role: llm.RoleSystem, Content: profileSystemPrompt},
				{Role: llm.RoleUser, Content: string(input)},
			},
		},
		contracts.DecodeCandidateProfile,
	)
}

var _ profile.Structurer = (*ProfileStructurer)(nil)
