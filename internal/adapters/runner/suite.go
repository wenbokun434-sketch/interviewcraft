package runner

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/coding"
)

type testCase struct {
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args"`
	Expected json.RawMessage `json:"expected"`
}

type testSuite struct {
	QuestionID string
	Public     []testCase
	Hidden     []testCase
}

type requestEnvelope struct {
	Version    string          `json:"version"`
	QuestionID string          `json:"question_id"`
	Language   coding.Language `json:"language"`
	Source     string          `json:"source"`
	Public     []testCase      `json:"public_tests"`
	Hidden     []testCase      `json:"hidden_tests"`
}

type responseEnvelope struct {
	Version string              `json:"version"`
	Result  coding.SafeResult   `json:"result"`
	Runtime coding.RuntimeStats `json:"runtime"`
}

var pairSumSuite = testSuite{
	QuestionID: "pair_sum",
	Public: []testCase{
		{
			Name:     "example-1",
			Args:     json.RawMessage(`[[2,7,11,15],9]`),
			Expected: json.RawMessage(`[0,1]`),
		},
		{
			Name:     "example-2",
			Args:     json.RawMessage(`[[3,2,4],6]`),
			Expected: json.RawMessage(`[1,2]`),
		},
	},
	Hidden: []testCase{
		{
			Name:     "hidden-duplicate-values",
			Args:     json.RawMessage(`[[3,3],6]`),
			Expected: json.RawMessage(`[0,1]`),
		},
		{
			Name:     "hidden-negative-values",
			Args:     json.RawMessage(`[[-3,4,3,90],0]`),
			Expected: json.RawMessage(`[0,2]`),
		},
	},
}

func suiteFor(questionID string) (testSuite, error) {
	switch strings.TrimSpace(questionID) {
	case pairSumSuite.QuestionID:
		return cloneSuite(pairSumSuite), nil
	default:
		return testSuite{}, fmt.Errorf("question has no runner suite")
	}
}

func cloneSuite(value testSuite) testSuite {
	value.Public = slices.Clone(value.Public)
	value.Hidden = slices.Clone(value.Hidden)
	for index := range value.Public {
		value.Public[index].Args = slices.Clone(value.Public[index].Args)
		value.Public[index].Expected = slices.Clone(value.Public[index].Expected)
	}
	for index := range value.Hidden {
		value.Hidden[index].Args = slices.Clone(value.Hidden[index].Args)
		value.Hidden[index].Expected = slices.Clone(value.Hidden[index].Expected)
	}
	return value
}
