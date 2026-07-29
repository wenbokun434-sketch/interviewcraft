// Package domainerr defines errors that can cross core module boundaries
// without requiring renderers to invent user-facing recovery guidance.
package domainerr

import (
	"errors"
	"fmt"
)

// Code identifies a stable class of domain failure.
type Code string

const (
	CodeValidation            Code = "validation_failed"
	CodeInvalidModelOutput    Code = "invalid_model_output"
	CodeDependencyUnavailable Code = "dependency_unavailable"
	CodePersistenceFailed     Code = "persistence_failed"
	CodePolicyDenied          Code = "policy_denied"
	CodeOperationCancelled    Code = "operation_cancelled"
	CodeInvalidState          Code = "invalid_state"
)

// Error carries stable classification, calm copy, and one recovery action.
// Cause is intentionally omitted from Error() so low-level details are not
// accidentally rendered to users.
type Error struct {
	Code           Code
	Operation      string
	Dependency     string
	Message        string
	RecoveryAction string
	Retryable      bool
	Cause          error
}

// New creates a domain error without an underlying cause.
func New(
	code Code,
	operation string,
	message string,
	recoveryAction string,
	retryable bool,
) *Error {
	return &Error{
		Code:           code,
		Operation:      operation,
		Message:        message,
		RecoveryAction: recoveryAction,
		Retryable:      retryable,
	}
}

// Wrap creates a domain error while retaining a cause for logs and tests.
func Wrap(
	code Code,
	operation string,
	dependency string,
	message string,
	recoveryAction string,
	retryable bool,
	cause error,
) *Error {
	return &Error{
		Code:           code,
		Operation:      operation,
		Dependency:     dependency,
		Message:        message,
		RecoveryAction: recoveryAction,
		Retryable:      retryable,
		Cause:          cause,
	}
}

// Error returns safe, concrete copy without exposing the wrapped cause.
func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("operation %q failed with code %q", e.Operation, e.Code)
}

// Unwrap exposes the cause to errors.Is/errors.As without rendering it.
func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

// IsCode reports whether err contains a domain error with the requested code.
func IsCode(err error, code Code) bool {
	var target *Error
	return errors.As(err, &target) && target.Code == code
}
