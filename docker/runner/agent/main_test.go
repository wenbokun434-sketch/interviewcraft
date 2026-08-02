package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestDecodeRequestAcceptsStrictPairSumProtocol(t *testing.T) {
	payload := validRequestPayload(t)
	request, err := decodeRequest(bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}
	if request.Version != requestVersion || request.QuestionID != "pair_sum" ||
		request.Language != "python" || len(request.Public) != 1 || len(request.Hidden) != 1 {
		t.Fatalf("request=%#v", request)
	}
}

func TestDecodeRequestRejectsUnknownFieldsAndInvalidTests(t *testing.T) {
	tests := [][]byte{
		[]byte(`{"version":"interviewcraft-runner-request-v1","question_id":"pair_sum","language":"python","source":"pass","public_tests":[],"hidden_tests":[],"secret":"value"}`),
		[]byte(`{"version":"interviewcraft-runner-request-v1","question_id":"pair_sum","language":"ruby","source":"pass","public_tests":[{"name":"public","args":[[1,2],3],"expected":[0,1]}],"hidden_tests":[{"name":"hidden","args":[[1,2],3],"expected":[0,1]}]}`),
		[]byte(`{"version":"interviewcraft-runner-request-v1","question_id":"pair_sum","language":"python","source":"pass","public_tests":[{"name":"public","args":[1],"expected":[]}],"hidden_tests":[{"name":"hidden","args":[[1,2],3],"expected":[0,1]}]}`),
	}
	for index, payload := range tests {
		if _, err := decodeRequest(bytes.NewReader(payload)); err == nil {
			t.Fatalf("case %d unexpectedly passed", index)
		}
	}
}

func TestSafeErrorResponseNeverReturnsHiddenInputOrPath(t *testing.T) {
	public := []testCase{{Name: "public", Args: json.RawMessage(`[[1,2],3]`), Expected: json.RawMessage(`[0,1]`)}}
	hidden := []testCase{{Name: "hidden-secret", Args: json.RawMessage(`[[888123,2],3]`), Expected: json.RawMessage(`[0,1]`)}}
	response := errorResponse(public, hidden, "runtime_error", time.Now(), 12)
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	for _, forbidden := range []string{"888123", "hidden-secret", "/tmp", "expected"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("safe response leaked %q: %s", forbidden, text)
		}
	}
	if response.Result.HiddenTests.Failed != 1 || response.Result.ErrorKind != "runtime_error" {
		t.Fatalf("response=%#v", response)
	}
}

func TestLimitedBufferAndJSONComparison(t *testing.T) {
	buffer := limitedBuffer{limit: 4}
	if written, err := buffer.Write([]byte("123456")); err != nil || written != 6 {
		t.Fatalf("Write=(%d,%v)", written, err)
	}
	if buffer.buffer.String() != "1234" || !buffer.overflow {
		t.Fatalf("buffer=%q overflow=%v", buffer.buffer.String(), buffer.overflow)
	}
	if !equalJSON(json.RawMessage(`{"a":[1,2]}`), json.RawMessage(` { "a": [1, 2] } `)) ||
		equalJSON(json.RawMessage(`[0,1]`), json.RawMessage(`[1,0]`)) {
		t.Fatal("semantic JSON comparison is invalid")
	}
}

func TestChildErrorKindIsEnumerated(t *testing.T) {
	if childErrorKind(errOutOfMemory) != "out_of_memory" ||
		childErrorKind(bytes.ErrTooLarge) != "runtime_error" {
		t.Fatal("unexpected child error classification")
	}
}

func validRequestPayload(t *testing.T) []byte {
	t.Helper()
	request := requestEnvelope{
		Version: requestVersion, QuestionID: "pair_sum", Language: "python",
		Source: "def pair_sum(nums, target): return [0, 1]",
		Public: []testCase{{Name: "public", Args: json.RawMessage(`[[1,2],3]`), Expected: json.RawMessage(`[0,1]`)}},
		Hidden: []testCase{{Name: "hidden", Args: json.RawMessage(`[[2,3],5]`), Expected: json.RawMessage(`[0,1]`)}},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
