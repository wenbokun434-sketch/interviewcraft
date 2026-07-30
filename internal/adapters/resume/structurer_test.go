package resume

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
)

func TestProfileStructurerUsesStrictTraceableContract(t *testing.T) {
	t.Parallel()

	source := profile.Source{
		Kind: profile.SourcePaste,
		Name: "pasted-resume.txt",
		Text: "Built payment service with Go.",
	}
	response := `{
		"target_role":"Backend Engineer",
		"facts":[{
			"id":"fact-payment",
			"field":"project",
			"value":"payment service",
			"source_span":{
				"start":6,
				"end":30,
				"text":"payment service with Go."
			}
		}],
		"inferences":[],
		"projects":["payment service"],
		"skills":["Go"]
	}`
	var captured llm.Request
	generator := generatorFunc(func(
		_ context.Context,
		request llm.Request,
	) ([]byte, error) {
		captured = request
		return []byte(response), nil
	})

	candidate, err := NewProfileStructurer(generator).Structure(
		context.Background(),
		source,
		" Backend Engineer ",
	)

	if err != nil {
		t.Fatalf("Structure: %v", err)
	}
	if candidate.TargetRole != "Backend Engineer" ||
		len(candidate.Facts) != 1 ||
		candidate.Facts[0].SourceSpan.Text != "payment service with Go." {
		t.Fatalf("candidate = %#v", candidate)
	}
	if captured.SchemaName != string(contracts.SchemaCandidateProfile) ||
		len(captured.Schema) == 0 ||
		len(captured.Messages) != 2 {
		t.Fatalf("request = %#v", captured)
	}
	system := captured.Messages[0].Content
	for _, rule := range []string{
		"Never invent",
		"UTF-8 byte offsets",
		"needs_confirmation=true",
		"Never copy an inference into facts",
	} {
		if !strings.Contains(system, rule) {
			t.Errorf("system prompt does not contain %q", rule)
		}
	}
	var input map[string]any
	if err := json.Unmarshal(
		[]byte(captured.Messages[1].Content),
		&input,
	); err != nil {
		t.Fatalf("decode user input: %v", err)
	}
	if input["target_role"] != "Backend Engineer" ||
		input["resume_text"] != source.Text ||
		input["source_name"] != source.Name {
		t.Fatalf("user input = %#v", input)
	}
}

func TestProfileStructurerRetriesInvalidSchemaOnlyOnce(t *testing.T) {
	t.Parallel()

	source := profile.Source{
		Kind: profile.SourceTXT,
		Name: "resume.txt",
		Text: "Go engineer",
	}
	responses := [][]byte{
		[]byte(`{"target_role":"Backend Engineer"}`),
		[]byte(`{
			"target_role":"Backend Engineer",
			"facts":[{
				"id":"fact-go",
				"field":"skill",
				"value":"Go",
				"source_span":{"start":0,"end":11,"text":"Go engineer"}
			}],
			"inferences":[],
			"projects":[],
			"skills":["Go"]
		}`),
	}
	calls := 0
	generator := generatorFunc(func(
		_ context.Context,
		request llm.Request,
	) ([]byte, error) {
		if calls == 1 {
			if len(request.Messages) != 3 ||
				!strings.Contains(
					request.Messages[2].Content,
					"previous response",
				) {
				t.Fatalf("retry request = %#v", request.Messages)
			}
		}
		response := responses[calls]
		calls++
		return response, nil
	})

	_, err := NewProfileStructurer(generator).Structure(
		context.Background(),
		source,
		"Backend Engineer",
	)

	if err != nil {
		t.Fatalf("Structure: %v", err)
	}
	if calls != 2 {
		t.Fatalf("generator calls = %d, want 2", calls)
	}
}

func TestProfileStructurerRejectsEmptyInputAndMissingProvider(t *testing.T) {
	t.Parallel()

	_, emptyErr := NewProfileStructurer(nil).Structure(
		context.Background(),
		profile.Source{
			Kind: profile.SourcePaste,
			Name: "pasted-resume.txt",
			Text: " ",
		},
		"Backend Engineer",
	)
	if !domainerr.IsCode(emptyErr, domainerr.CodeValidation) {
		t.Fatalf("empty source error = %v", emptyErr)
	}

	_, providerErr := NewProfileStructurer(nil).Structure(
		context.Background(),
		profile.Source{
			Kind: profile.SourcePaste,
			Name: "pasted-resume.txt",
			Text: "Go engineer",
		},
		"Backend Engineer",
	)
	if !domainerr.IsCode(providerErr, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("missing provider error = %v", providerErr)
	}
}

type generatorFunc func(context.Context, llm.Request) ([]byte, error)

func (function generatorFunc) Generate(
	ctx context.Context,
	request llm.Request,
) ([]byte, error) {
	return function(ctx, request)
}
