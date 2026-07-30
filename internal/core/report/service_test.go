package report

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

func TestServiceSaveGetAndEmptyState(t *testing.T) {
	t.Parallel()

	repository := &reportRepository{}
	service := NewService(repository, Options{
		Now: func() time.Time {
			return validDocument().GeneratedAt
		},
	})
	if _, found, err := service.Get(
		context.Background(),
		"missing",
	); err != nil || found {
		t.Fatalf("empty Get found=%v err=%v", found, err)
	}
	document := validDocument()
	if err := service.Save(context.Background(), document); err != nil {
		t.Fatalf("Save: %v", err)
	}
	restored, found, err := service.Get(
		context.Background(),
		document.Summary.SessionID,
	)
	if err != nil || !found || restored.ID != document.ID {
		t.Fatalf("restored=%#v found=%v err=%v", restored, found, err)
	}
}

func TestServiceReportsStorageFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		repository *reportRepository
		run        func(*Service) error
	}{
		{
			name:       "read failure",
			repository: &reportRepository{getErr: errors.New("read failed")},
			run: func(service *Service) error {
				_, _, err := service.Get(context.Background(), "session-1")
				return err
			},
		},
		{
			name:       "write failure",
			repository: &reportRepository{saveErr: errors.New("write failed")},
			run: func(service *Service) error {
				return service.Save(context.Background(), validDocument())
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.run(NewService(test.repository, Options{}))
			if !domainerr.IsCode(err, domainerr.CodePersistenceFailed) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

type reportRepository struct {
	value   db.Report
	found   bool
	getErr  error
	saveErr error
}

func (repository *reportRepository) SaveReport(
	_ context.Context,
	value db.Report,
) error {
	if repository.saveErr != nil {
		return repository.saveErr
	}
	repository.value = value
	repository.found = true
	return nil
}

func (repository *reportRepository) GetReport(
	_ context.Context,
	sessionID string,
) (db.Report, bool, error) {
	if repository.getErr != nil {
		return db.Report{}, false, repository.getErr
	}
	if !repository.found || repository.value.SessionID != sessionID {
		return db.Report{}, false, nil
	}
	return repository.value, true, nil
}
