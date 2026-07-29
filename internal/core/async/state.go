// Package async models typed lifecycle states for core operations.
package async

import (
	"fmt"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// Phase is the lifecycle phase of an asynchronous operation.
type Phase string

const (
	Pending   Phase = "pending"
	Streaming Phase = "streaming"
	Succeeded Phase = "succeeded"
	Failed    Phase = "failed"
)

// State holds exactly the payload allowed by its Phase.
// Streaming may carry a partial value; Succeeded requires a final value.
type State[T any] struct {
	Phase Phase
	Value *T
	Err   *domainerr.Error
}

// NewPending creates an operation that has started but produced no value.
func NewPending[T any]() State[T] {
	return State[T]{Phase: Pending}
}

// NewStreaming creates an operation that is emitting output. Partial may be nil
// before the first chunk arrives.
func NewStreaming[T any](partial *T) State[T] {
	return State[T]{Phase: Streaming, Value: partial}
}

// NewSucceeded creates a terminal state with its final value.
func NewSucceeded[T any](value T) State[T] {
	return State[T]{Phase: Succeeded, Value: &value}
}

// NewFailed creates a terminal state with a typed failure.
func NewFailed[T any](err *domainerr.Error) State[T] {
	return State[T]{Phase: Failed, Err: err}
}

// Validate checks the payload invariant for the current phase.
func (s State[T]) Validate() error {
	switch s.Phase {
	case Pending:
		if s.Value != nil || s.Err != nil {
			return invalidState("pending state cannot contain a value or error")
		}
	case Streaming:
		if s.Err != nil {
			return invalidState("streaming state cannot contain an error")
		}
	case Succeeded:
		if s.Value == nil || s.Err != nil {
			return invalidState("succeeded state requires a value and no error")
		}
	case Failed:
		if s.Value != nil || s.Err == nil {
			return invalidState("failed state requires an error and no value")
		}
	default:
		return invalidState(fmt.Sprintf("unknown async phase %q", s.Phase))
	}
	return nil
}

// Transition validates both states and the requested lifecycle edge.
func (s State[T]) Transition(next State[T]) (State[T], error) {
	if err := s.Validate(); err != nil {
		return s, err
	}
	if err := next.Validate(); err != nil {
		return s, err
	}

	allowed := false
	switch s.Phase {
	case Pending:
		allowed = next.Phase == Streaming || next.Phase == Succeeded || next.Phase == Failed
	case Streaming:
		allowed = next.Phase == Streaming || next.Phase == Succeeded || next.Phase == Failed
	case Succeeded, Failed:
		allowed = false
	}

	if !allowed {
		return s, invalidState(fmt.Sprintf("cannot transition from %s to %s", s.Phase, next.Phase))
	}
	return next, nil
}

func invalidState(cause string) *domainerr.Error {
	return domainerr.Wrap(
		domainerr.CodeInvalidState,
		"transition async state",
		"",
		"无法更新当前操作状态。",
		"保持当前状态并检查状态转换。",
		false,
		fmt.Errorf("%s", cause),
	)
}
