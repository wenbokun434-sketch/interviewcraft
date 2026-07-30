package report

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
	"github.com/interviewcraft/interviewcraft/internal/db"
)

// Repository is the narrow durable report boundary.
type Repository interface {
	SaveReport(context.Context, db.Report) error
	GetReport(context.Context, string) (db.Report, bool, error)
}

// Options injects deterministic report timestamps.
type Options struct {
	Now func() time.Time
}

// Service validates every evidence link before saving or returning a report.
type Service struct {
	repository Repository
	now        func() time.Time
}

// NewService constructs the durable report service.
func NewService(repository Repository, options Options) *Service {
	now := options.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repository: repository, now: now}
}

// Save validates and persists a complete report payload.
func (service *Service) Save(
	ctx context.Context,
	document Document,
) error {
	if service == nil || service.repository == nil {
		return unavailableStorage()
	}
	if err := document.Validate(); err != nil {
		return err
	}
	payload, err := json.Marshal(document)
	if err != nil {
		return reportFailure("encode report", err)
	}
	now := service.now().UTC()
	existing, found, err := service.repository.GetReport(
		ctx,
		document.Summary.SessionID,
	)
	if err != nil {
		return reportFailure("read existing report", err)
	}
	createdAt := now
	if found {
		createdAt = existing.CreatedAt
	}
	if err := service.repository.SaveReport(ctx, db.Report{
		ID:        document.ID,
		SessionID: document.Summary.SessionID,
		Payload:   payload,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}); err != nil {
		return reportFailure("save report", err)
	}
	return nil
}

// Get decodes and revalidates one persisted report.
func (service *Service) Get(
	ctx context.Context,
	sessionID string,
) (Document, bool, error) {
	if service == nil || service.repository == nil {
		return Document{}, false, unavailableStorage()
	}
	stored, found, err := service.repository.GetReport(ctx, sessionID)
	if err != nil {
		return Document{}, false, reportFailure("read report", err)
	}
	if !found {
		return Document{}, false, nil
	}
	document, err := Decode(stored.Payload)
	if err != nil {
		return Document{}, false, err
	}
	if document.Summary.SessionID != sessionID {
		return Document{}, false, reportFailure(
			"decode report",
			errors.New("report session id does not match storage key"),
		)
	}
	return document, true, nil
}

// Decode strictly decodes and validates a report payload.
func Decode(payload []byte) (Document, error) {
	var document Document
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil {
		return Document{}, reportFailure("decode report", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			err = errors.New("multiple JSON values are not allowed")
		}
		return Document{}, reportFailure("decode report", err)
	}
	if err := document.Validate(); err != nil {
		return Document{}, err
	}
	return document, nil
}

func unavailableStorage() *domainerr.Error {
	return domainerr.New(
		domainerr.CodeDependencyUnavailable,
		"access report storage",
		"报告存储不可用。",
		"重新启动 InterviewCraft 后重试。",
		true,
	)
}

func reportFailure(operation string, err error) *domainerr.Error {
	var typed *domainerr.Error
	if errors.As(err, &typed) {
		return typed
	}
	return domainerr.Wrap(
		domainerr.CodePersistenceFailed,
		operation,
		"report storage",
		"无法保存或恢复证据化报告。",
		"会话证据已保留；检查本地数据库后重试。",
		true,
		err,
	)
}
