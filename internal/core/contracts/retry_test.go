package contracts

import (
	"errors"
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestDecodeWithSchemaRetryRecoversAfterOneInvalidOutput(t *testing.T) {
	t.Parallel()

	attempts := 0
	source := func(attempt int) ([]byte, error) {
		attempts++
		if attempt == 0 {
			return []byte(`{"action":"unsupported"}`), nil
		}
		return []byte(validInterviewerActionJSON), nil
	}

	action, err := DecodeWithSchemaRetry(source, DecodeInterviewerAction)

	if err != nil {
		t.Fatalf("DecodeWithSchemaRetry: %v", err)
	}
	if attempts != SchemaRetryLimit+1 {
		t.Fatalf("source attempts = %d, want %d", attempts, SchemaRetryLimit+1)
	}
	if action.Action != ActionFollowUp {
		t.Fatalf("decoded action = %q, want %q", action.Action, ActionFollowUp)
	}
}

func TestDecodeWithSchemaRetryReturnsTypedFallback(t *testing.T) {
	t.Parallel()

	attempts := 0
	source := func(int) ([]byte, error) {
		attempts++
		return []byte(`{"action":"unsupported"}`), nil
	}

	_, err := DecodeWithSchemaRetry(source, DecodeInterviewerAction)

	if err == nil {
		t.Fatal("repeated invalid output unexpectedly succeeded")
	}
	if attempts != SchemaRetryLimit+1 {
		t.Fatalf("source attempts = %d, want %d", attempts, SchemaRetryLimit+1)
	}
	if !domainerr.IsCode(err, domainerr.CodeInvalidModelOutput) {
		t.Fatalf("fallback error = %v, want invalid model output code", err)
	}

	var typed *domainerr.Error
	if !errors.As(err, &typed) {
		t.Fatalf("fallback error type = %T, want *domainerr.Error", err)
	}
	if !typed.Retryable || typed.RecoveryAction == "" {
		t.Fatalf("fallback is not actionable: %#v", typed)
	}
	if typed.Cause == nil || !domainerr.IsCode(typed.Cause, domainerr.CodeValidation) {
		t.Fatalf("fallback cause = %v, want retained validation error", typed.Cause)
	}
}

func TestDecodeWithSchemaRetryDoesNotRetryDependencyFailure(t *testing.T) {
	t.Parallel()

	attempts := 0
	source := func(int) ([]byte, error) {
		attempts++
		return nil, errors.New("provider unavailable")
	}

	_, err := DecodeWithSchemaRetry(source, DecodeCoachResponse)

	if attempts != 1 {
		t.Fatalf("dependency attempts = %d, want 1", attempts)
	}
	if !domainerr.IsCode(err, domainerr.CodeDependencyUnavailable) {
		t.Fatalf("dependency error = %v, want dependency unavailable code", err)
	}
}
