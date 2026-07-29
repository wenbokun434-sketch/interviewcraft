package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// SchemaRetryLimit is the maximum number of automatic retries after an invalid
// structured model response. The initial attempt is not counted as a retry.
const SchemaRetryLimit = 1

// Decoder validates one strict JSON contract.
type Decoder[T any] func([]byte) (T, error)

// DecodeCandidateProfile strictly decodes and validates a profile.
func DecodeCandidateProfile(data []byte) (CandidateProfile, error) {
	return decodeStrict(data, "CandidateProfile", CandidateProfile.Validate)
}

// DecodeScenario strictly decodes and validates a scenario.
func DecodeScenario(data []byte) (Scenario, error) {
	return decodeStrict(data, "Scenario", Scenario.Validate)
}

// DecodeInterviewerAction strictly decodes and validates an action.
func DecodeInterviewerAction(data []byte) (InterviewerAction, error) {
	return decodeStrict(data, "InterviewerAction", InterviewerAction.Validate)
}

// DecodeCoachResponse strictly decodes and validates a Coach response.
func DecodeCoachResponse(data []byte) (CoachResponse, error) {
	return decodeStrict(data, "CoachResponse", CoachResponse.Validate)
}

// DecodeEvaluationFinding strictly decodes and validates a finding.
func DecodeEvaluationFinding(data []byte) (EvaluationFinding, error) {
	return decodeStrict(data, "EvaluationFinding", EvaluationFinding.Validate)
}

// DecodeWithSchemaRetry retries exactly once when decoding or validation fails.
// Source failures are returned immediately because repeating dependency calls
// is owned by the adapter's transport policy.
func DecodeWithSchemaRetry[T any](
	source func(attempt int) ([]byte, error),
	decode Decoder[T],
) (T, error) {
	var zero T
	var lastValidation error

	for attempt := 0; attempt <= SchemaRetryLimit; attempt++ {
		data, err := source(attempt)
		if err != nil {
			var typed *domainerr.Error
			if errors.As(err, &typed) {
				return zero, typed
			}
			return zero, domainerr.Wrap(
				domainerr.CodeDependencyUnavailable,
				"generate structured output",
				"model provider",
				"模型服务没有返回结构化数据。",
				"检查 Provider 后重试。",
				true,
				err,
			)
		}

		value, err := decode(data)
		if err == nil {
			return value, nil
		}
		lastValidation = err
	}

	return zero, domainerr.Wrap(
		domainerr.CodeInvalidModelOutput,
		"decode structured output",
		"model provider",
		"模型输出不符合约定格式，自动重试后仍无法使用。",
		"重试当前操作或安全结束本题。",
		true,
		lastValidation,
	)
}

func decodeStrict[T any](
	data []byte,
	contract string,
	validate func(T) error,
) (T, error) {
	var value T
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(&value); err != nil {
		return value, decodeFailure(contract, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return value, decodeFailure(contract, err)
	}
	if err := validate(value); err != nil {
		return value, err
	}
	return value, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func decodeFailure(contract string, cause error) error {
	violation := &Violation{
		Contract: contract,
		Issues: []ValidationIssue{{
			Field:  "$",
			Reason: fmt.Sprintf("must be valid strict JSON: %v", cause),
		}},
	}
	return domainerr.Wrap(
		domainerr.CodeValidation,
		"decode "+contract,
		"",
		"结构化数据不是有效的 "+contract+" JSON。",
		"修正 JSON 字段后重试。",
		false,
		violation,
	)
}
