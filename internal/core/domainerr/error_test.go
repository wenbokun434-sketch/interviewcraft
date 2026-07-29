package domainerr

import (
	"errors"
	"strings"
	"testing"
)

func TestErrorKeepsCauseOutOfSafeMessage(t *testing.T) {
	t.Parallel()

	cause := errors.New("secret-bearing low-level detail")
	err := Wrap(
		CodeDependencyUnavailable,
		"generate scenario",
		"model provider",
		"无法连接模型服务。",
		"检查 Provider 设置后重试。",
		true,
		cause,
	)

	if strings.Contains(err.Error(), cause.Error()) {
		t.Fatalf("safe error %q exposes cause %q", err.Error(), cause.Error())
	}
	if !errors.Is(err, cause) {
		t.Fatal("wrapped cause is not available through errors.Is")
	}
	if !IsCode(err, CodeDependencyUnavailable) {
		t.Fatal("IsCode did not identify the wrapped domain code")
	}
}

func TestNewCreatesActionableError(t *testing.T) {
	t.Parallel()

	err := New(
		CodeInvalidState,
		"advance interview",
		"当前状态不能进入下一题。",
		"返回当前题后重试。",
		false,
	)

	if err.Operation == "" || err.Message == "" || err.RecoveryAction == "" {
		t.Fatalf("domain error is not actionable: %#v", err)
	}
	if err.Retryable {
		t.Fatal("invalid state should not be marked retryable")
	}
}
