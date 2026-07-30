package profile

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/async"
	"github.com/interviewcraft/interviewcraft/internal/core/contracts"
	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestCreateValidatesTraceAndSavesCompleteAggregate(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 9, 0, 0, 0, time.UTC)
	source := profileSource()
	candidate := profileCandidate(source.Text)
	repository := newMemoryRepository()
	service := NewService(
		repository,
		structurerFunc(func(
			_ context.Context,
			gotSource Source,
			targetRole string,
		) (contracts.CandidateProfile, error) {
			if !reflect.DeepEqual(gotSource, source) {
				t.Fatalf("structurer source = %#v, want %#v", gotSource, source)
			}
			if targetRole != "Backend Engineer" {
				t.Fatalf("target role = %q", targetRole)
			}
			return candidate, nil
		}),
		func() time.Time { return now },
	)
	var states []async.State[Progress]

	aggregate, err := service.Create(
		context.Background(),
		"profile-1",
		source,
		"Backend Engineer",
		func(state async.State[Progress]) {
			states = append(states, state)
		},
	)

	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if repository.saveCalls != 1 {
		t.Fatalf("save calls = %d, want 1", repository.saveCalls)
	}
	if aggregate.ID != "profile-1" ||
		!reflect.DeepEqual(aggregate.Candidate, candidate) ||
		!reflect.DeepEqual(aggregate.Metadata.Source, source) ||
		!aggregate.Metadata.CreatedAt.Equal(now) ||
		!aggregate.Metadata.UpdatedAt.Equal(now) {
		t.Fatalf("aggregate = %#v", aggregate)
	}
	if aggregate.Metadata.LockedFactIDs == nil ||
		aggregate.Metadata.LockedInferenceIDs == nil {
		t.Fatalf("lock collections must be explicit empty arrays: %#v", aggregate.Metadata)
	}
	assertProfileLifecycle(t, states, async.Succeeded)
}

func TestCreateRejectsEmptyAndUnsupportedEvidenceWithoutSaving(t *testing.T) {
	t.Parallel()

	source := profileSource()
	tests := []struct {
		name      string
		source    Source
		candidate contracts.CandidateProfile
	}{
		{
			name:      "empty resume",
			source:    Source{Kind: SourcePaste, Name: "pasted-resume.txt", Text: " "},
			candidate: profileCandidate(source.Text),
		},
		{
			name: "oversized resume",
			source: Source{
				Kind: SourcePaste,
				Name: "pasted-resume.txt",
				Text: string(make([]byte, MaxSourceBytes+1)),
			},
			candidate: profileCandidate(source.Text),
		},
		{
			name:   "invented fact",
			source: source,
			candidate: func() contracts.CandidateProfile {
				value := profileCandidate(source.Text)
				value.Facts[0].SourceSpan.Text = "not in the resume"
				return value
			}(),
		},
		{
			name:   "invented project",
			source: source,
			candidate: func() contracts.CandidateProfile {
				value := profileCandidate(source.Text)
				value.Projects = append(value.Projects, "Imaginary Project")
				return value
			}(),
		},
		{
			name:   "confirmed inference",
			source: source,
			candidate: func() contracts.CandidateProfile {
				value := profileCandidate(source.Text)
				value.Inferences[0].NeedsConfirmation = false
				return value
			}(),
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			repository := newMemoryRepository()
			service := NewService(
				repository,
				structurerFunc(func(
					context.Context,
					Source,
					string,
				) (contracts.CandidateProfile, error) {
					return test.candidate, nil
				}),
				nil,
			)
			var states []async.State[Progress]

			_, err := service.Create(
				context.Background(),
				"profile-1",
				test.source,
				"Backend Engineer",
				func(state async.State[Progress]) {
					states = append(states, state)
				},
			)

			if !domainerr.IsCode(err, domainerr.CodeValidation) {
				t.Fatalf("Create error = %v, want validation", err)
			}
			if repository.saveCalls != 0 || len(repository.items) != 0 {
				t.Fatalf(
					"invalid profile was partially saved: calls=%d items=%#v",
					repository.saveCalls,
					repository.items,
				)
			}
			assertProfileLifecycle(t, states, async.Failed)
		})
	}
}

func TestCreateCancellationAndFailuresLeaveNoPartialProfile(t *testing.T) {
	t.Parallel()

	source := profileSource()
	candidate := profileCandidate(source.Text)
	t.Run("cancelled before structuring", func(t *testing.T) {
		repository := newMemoryRepository()
		structurerCalls := 0
		service := NewService(
			repository,
			structurerFunc(func(
				context.Context,
				Source,
				string,
			) (contracts.CandidateProfile, error) {
				structurerCalls++
				return candidate, nil
			}),
			nil,
		)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := service.Create(
			ctx,
			"profile-1",
			source,
			"Backend Engineer",
			nil,
		)

		if !domainerr.IsCode(err, domainerr.CodeOperationCancelled) {
			t.Fatalf("Create error = %v, want cancellation", err)
		}
		if structurerCalls != 0 || repository.saveCalls != 0 {
			t.Fatalf(
				"cancelled create performed work: structurer=%d save=%d",
				structurerCalls,
				repository.saveCalls,
			)
		}
	})

	t.Run("structurer failure", func(t *testing.T) {
		repository := newMemoryRepository()
		service := NewService(
			repository,
			structurerFunc(func(
				context.Context,
				Source,
				string,
			) (contracts.CandidateProfile, error) {
				return contracts.CandidateProfile{}, errors.New("provider down")
			}),
			nil,
		)

		_, err := service.Create(
			context.Background(),
			"profile-1",
			source,
			"Backend Engineer",
			nil,
		)

		if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
			t.Fatalf("Create error = %v, want dependency failure", err)
		}
		if repository.saveCalls != 0 {
			t.Fatalf("save calls = %d, want 0", repository.saveCalls)
		}
	})

	t.Run("repository failure", func(t *testing.T) {
		repository := newMemoryRepository()
		repository.saveErr = errors.New("disk full")
		service := NewService(
			repository,
			structurerFunc(func(
				context.Context,
				Source,
				string,
			) (contracts.CandidateProfile, error) {
				return candidate, nil
			}),
			nil,
		)

		_, err := service.Create(
			context.Background(),
			"profile-1",
			source,
			"Backend Engineer",
			nil,
		)

		if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
			t.Fatalf("Create error = %v, want persistence failure", err)
		}
		if len(repository.items) != 0 {
			t.Fatalf("failed save left partial profile: %#v", repository.items)
		}
	})
}

func TestEditLockDeleteAndLoadProfile(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 7, 30, 10, 0, 0, 0, time.UTC)
	source := profileSource()
	repository := newMemoryRepository()
	repository.items["profile-1"] = Aggregate{
		ID:        "profile-1",
		Candidate: profileCandidate(source.Text),
		Metadata: Metadata{
			Source:             source,
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}
	nextTime := now
	service := NewService(repository, nil, func() time.Time {
		nextTime = nextTime.Add(time.Minute)
		return nextTime
	})

	locked, err := service.SetLocked(
		context.Background(),
		"profile-1",
		"fact-payment",
		true,
	)
	if err != nil || !reflect.DeepEqual(
		locked.Metadata.LockedFactIDs,
		[]contracts.EvidenceID{"fact-payment"},
	) {
		t.Fatalf("SetLocked fact: aggregate=%#v err=%v", locked, err)
	}
	replacement := fact(
		source.Text,
		"fact-payment",
		"technology",
		"Go",
		"Built payment service with Go.",
	)
	if _, err := service.EditFact(
		context.Background(),
		"profile-1",
		replacement,
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("locked EditFact error = %v", err)
	}
	if _, err := service.DeleteItem(
		context.Background(),
		"profile-1",
		"fact-payment",
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("locked DeleteItem error = %v", err)
	}

	if _, err := service.SetLocked(
		context.Background(),
		"profile-1",
		"fact-payment",
		false,
	); err != nil {
		t.Fatalf("unlock fact: %v", err)
	}
	edited, err := service.EditFact(
		context.Background(),
		"profile-1",
		replacement,
	)
	if err != nil || edited.Candidate.Facts[0].Value != "Go" {
		t.Fatalf("EditFact: aggregate=%#v err=%v", edited, err)
	}

	if _, err := service.SetLocked(
		context.Background(),
		"profile-1",
		"inference-lead",
		true,
	); err != nil {
		t.Fatalf("lock inference: %v", err)
	}
	if _, err := service.DeleteItem(
		context.Background(),
		"profile-1",
		"inference-lead",
	); !domainerr.IsCode(err, domainerr.CodePolicyDenied) {
		t.Fatalf("locked inference delete error = %v", err)
	}
	if _, err := service.SetLocked(
		context.Background(),
		"profile-1",
		"inference-lead",
		false,
	); err != nil {
		t.Fatalf("unlock inference: %v", err)
	}
	withoutInference, err := service.DeleteItem(
		context.Background(),
		"profile-1",
		"inference-lead",
	)
	if err != nil || len(withoutInference.Candidate.Inferences) != 0 {
		t.Fatalf("DeleteItem inference: aggregate=%#v err=%v", withoutInference, err)
	}

	restored, found, err := service.Load(context.Background(), "profile-1")
	if err != nil || !found || !reflect.DeepEqual(restored, withoutInference) {
		t.Fatalf("Load: aggregate=%#v found=%v err=%v", restored, found, err)
	}
	deleted, err := service.Delete(context.Background(), "profile-1")
	if err != nil || !deleted {
		t.Fatalf("Delete: deleted=%v err=%v", deleted, err)
	}
	if _, found, err := service.Load(
		context.Background(),
		"profile-1",
	); err != nil || found {
		t.Fatalf("Load after delete: found=%v err=%v", found, err)
	}
}

func TestFailedEditDoesNotMutateStoredProfile(t *testing.T) {
	t.Parallel()

	source := profileSource()
	now := time.Date(2026, 7, 30, 11, 0, 0, 0, time.UTC)
	repository := newMemoryRepository()
	original := Aggregate{
		ID:        "profile-1",
		Candidate: profileCandidate(source.Text),
		Metadata: Metadata{
			Source:             source,
			LockedFactIDs:      []contracts.EvidenceID{},
			LockedInferenceIDs: []string{},
			CreatedAt:          now,
			UpdatedAt:          now,
		},
	}
	repository.items[original.ID] = cloneAggregate(original)
	repository.saveErr = errors.New("disk full")
	service := NewService(repository, nil, func() time.Time {
		return now.Add(time.Minute)
	})

	_, err := service.EditFact(
		context.Background(),
		original.ID,
		fact(
			source.Text,
			"fact-payment",
			"technology",
			"Go",
			"Built payment service with Go.",
		),
	)

	if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
		t.Fatalf("EditFact error = %v", err)
	}
	if !reflect.DeepEqual(repository.items[original.ID], original) {
		t.Fatalf(
			"failed edit mutated stored profile: got=%#v want=%#v",
			repository.items[original.ID],
			original,
		)
	}
}

func profileSource() Source {
	return Source{
		Kind: SourcePaste,
		Name: "pasted-resume.txt",
		Text: "Built payment service with Go.\n" +
			"Led migration project using PostgreSQL.",
	}
}

func profileCandidate(source string) contracts.CandidateProfile {
	return contracts.CandidateProfile{
		TargetRole: "Backend Engineer",
		Facts: []contracts.ProfileFact{
			fact(
				source,
				"fact-payment",
				"project",
				"payment service",
				"Built payment service with Go.",
			),
			fact(
				source,
				"fact-migration",
				"project",
				"migration project",
				"Led migration project using PostgreSQL.",
			),
		},
		Inferences: []contracts.ProfileInference{{
			ID:                "inference-lead",
			Field:             "leadership",
			Value:             "May have led a delivery",
			Confidence:        0.6,
			NeedsConfirmation: true,
		}},
		Projects: []string{"payment service", "migration project"},
		Skills:   []string{"Go", "PostgreSQL"},
	}
}

func fact(
	source string,
	id contracts.EvidenceID,
	field string,
	value string,
	spanText string,
) contracts.ProfileFact {
	start := -1
	for index := 0; index+len(spanText) <= len(source); index++ {
		if source[index:index+len(spanText)] == spanText {
			start = index
			break
		}
	}
	return contracts.ProfileFact{
		ID:    id,
		Field: field,
		Value: value,
		SourceSpan: contracts.SourceSpan{
			Start: start,
			End:   start + len(spanText),
			Text:  spanText,
		},
	}
}

type structurerFunc func(
	context.Context,
	Source,
	string,
) (contracts.CandidateProfile, error)

func (function structurerFunc) Structure(
	ctx context.Context,
	source Source,
	targetRole string,
) (contracts.CandidateProfile, error) {
	return function(ctx, source, targetRole)
}

type memoryRepository struct {
	items     map[string]Aggregate
	saveErr   error
	deleteErr error
	saveCalls int
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{items: make(map[string]Aggregate)}
}

func (repository *memoryRepository) SaveProfileAggregate(
	_ context.Context,
	aggregate Aggregate,
) error {
	repository.saveCalls++
	if repository.saveErr != nil {
		return repository.saveErr
	}
	repository.items[aggregate.ID] = cloneAggregate(aggregate)
	return nil
}

func (repository *memoryRepository) GetProfileAggregate(
	_ context.Context,
	id string,
) (Aggregate, bool, error) {
	aggregate, found := repository.items[id]
	return cloneAggregate(aggregate), found, nil
}

func (repository *memoryRepository) DeleteProfile(
	_ context.Context,
	id string,
) (bool, error) {
	if repository.deleteErr != nil {
		return false, repository.deleteErr
	}
	if _, found := repository.items[id]; !found {
		return false, nil
	}
	delete(repository.items, id)
	return true, nil
}

func assertProfileLifecycle(
	t *testing.T,
	states []async.State[Progress],
	wantTerminal async.Phase,
) {
	t.Helper()
	if len(states) < 2 ||
		states[0].Phase != async.Pending ||
		states[len(states)-1].Phase != wantTerminal {
		t.Fatalf("profile lifecycle = %#v", states)
	}
	for index, state := range states {
		if err := state.Validate(); err != nil {
			t.Fatalf("state %d invalid: %v", index, err)
		}
	}
	if wantTerminal == async.Succeeded {
		streaming := 0
		for _, state := range states {
			if state.Phase == async.Streaming {
				streaming++
			}
		}
		if streaming < 3 {
			t.Fatalf("streaming stages = %d, want structuring/validation/save", streaming)
		}
	}
}
