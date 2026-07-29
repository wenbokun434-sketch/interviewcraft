package async

import (
	"testing"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

func TestLifecycleSupportsPendingStreamingSuccess(t *testing.T) {
	t.Parallel()

	pending := NewPending[string]()
	partial := "first chunk"
	streaming := NewStreaming(&partial)
	complete := NewSucceeded("complete response")

	current, err := pending.Transition(streaming)
	if err != nil {
		t.Fatalf("pending -> streaming: %v", err)
	}
	current, err = current.Transition(complete)
	if err != nil {
		t.Fatalf("streaming -> succeeded: %v", err)
	}
	if current.Phase != Succeeded || current.Value == nil || *current.Value != "complete response" {
		t.Fatalf("unexpected succeeded state: %#v", current)
	}
}

func TestLifecycleSupportsTypedFailure(t *testing.T) {
	t.Parallel()

	failure := domainerr.New(
		domainerr.CodeDependencyUnavailable,
		"parse resume",
		"无法读取简历。",
		"粘贴纯文本后重试。",
		true,
	)

	current, err := NewPending[string]().Transition(NewFailed[string](failure))
	if err != nil {
		t.Fatalf("pending -> failed: %v", err)
	}
	if current.Err != failure {
		t.Fatal("failed state did not retain the typed error")
	}
}

func TestTerminalStateRejectsFurtherTransition(t *testing.T) {
	t.Parallel()

	complete := NewSucceeded("done")
	_, err := complete.Transition(NewPending[string]())

	if err == nil {
		t.Fatal("succeeded -> pending unexpectedly succeeded")
	}
	if !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("transition error = %v, want invalid state code", err)
	}
}

func TestStateRejectsInvalidPayload(t *testing.T) {
	t.Parallel()

	value := "not allowed"
	state := State[string]{Phase: Pending, Value: &value}

	err := state.Validate()
	if err == nil {
		t.Fatal("pending state with value unexpectedly validated")
	}
	if !domainerr.IsCode(err, domainerr.CodeInvalidState) {
		t.Fatalf("validation error = %v, want invalid state code", err)
	}
}
