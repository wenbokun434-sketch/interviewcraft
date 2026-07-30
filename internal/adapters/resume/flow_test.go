package resume_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/adapters/llm"
	"github.com/interviewcraft/interviewcraft/internal/adapters/resume"
	"github.com/interviewcraft/interviewcraft/internal/core/profile"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestPasteToStructuredProfilePersistsAndRestores(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := filepath.Join(t.TempDir(), "data")
	store, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	source, err := (resume.Extractor{}).Extract(
		ctx,
		resume.Input{
			Kind: profile.SourcePaste,
			Text: "Built payment service with Go.",
		},
		nil,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("Extract: %v", err)
	}
	generator := flowGenerator(func(
		context.Context,
		llm.Request,
	) ([]byte, error) {
		return []byte(`{
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
		}`), nil
	})
	now := time.Date(2026, 7, 30, 13, 0, 0, 0, time.UTC)
	service := profile.NewService(
		store,
		resume.NewProfileStructurer(generator),
		func() time.Time { return now },
	)

	created, err := service.Create(
		ctx,
		"profile-flow",
		source,
		"Backend Engineer",
		nil,
	)
	if err != nil {
		_ = store.Close()
		t.Fatalf("Create: %v", err)
	}
	if created.Candidate.Facts[0].SourceSpan.Text !=
		source.Text[6:30] {
		_ = store.Close()
		t.Fatalf("created fact is not traceable: %#v", created.Candidate.Facts[0])
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened, err := db.Open(ctx, db.Config{DataDir: dataDir}, nil)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	restoredService := profile.NewService(reopened, nil, nil)
	restored, found, err := restoredService.Load(ctx, created.ID)
	if err != nil || !found {
		t.Fatalf("Load after restart: found=%v err=%v", found, err)
	}
	if restored.Metadata.Source.Text != source.Text ||
		restored.Candidate.TargetRole != "Backend Engineer" {
		t.Fatalf("restored aggregate = %#v", restored)
	}
}

type flowGenerator func(context.Context, llm.Request) ([]byte, error)

func (function flowGenerator) Generate(
	ctx context.Context,
	request llm.Request,
) ([]byte, error) {
	return function(ctx, request)
}
