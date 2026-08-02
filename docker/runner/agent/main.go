package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
)

const (
	requestVersion  = "interviewcraft-runner-request-v1"
	responseVersion = "interviewcraft-runner-response-v1"
	resultVersion   = "interviewcraft-code-result-v1"
	resultMarker    = "__INTERVIEWCRAFT_RESULT__"
	maxRequestBytes = 1 << 20
	maxSourceBytes  = 256 << 10
	maxChildOutput  = 32 << 10
	maxTests        = 64
)

var errOutOfMemory = errors.New("child exceeded memory limit")

type testCase struct {
	Name     string          `json:"name"`
	Args     json.RawMessage `json:"args"`
	Expected json.RawMessage `json:"expected"`
}

type requestEnvelope struct {
	Version    string     `json:"version"`
	QuestionID string     `json:"question_id"`
	Language   string     `json:"language"`
	Source     string     `json:"source"`
	Public     []testCase `json:"public_tests"`
	Hidden     []testCase `json:"hidden_tests"`
}

type publicResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type hiddenSummary struct {
	Passed int `json:"passed"`
	Failed int `json:"failed"`
}

type safeResult struct {
	Version     string         `json:"version"`
	Status      string         `json:"status"`
	PublicTests []publicResult `json:"public_tests"`
	HiddenTests hiddenSummary  `json:"hidden_tests"`
	ErrorKind   string         `json:"error_kind"`
}

type runtimeStats struct {
	DurationMilliseconds int64 `json:"duration_ms"`
	PeakMemoryKB         int64 `json:"peak_memory_kb"`
}

type responseEnvelope struct {
	Version string       `json:"version"`
	Result  safeResult   `json:"result"`
	Runtime runtimeStats `json:"runtime"`
}

type program interface {
	Run(context.Context, json.RawMessage) (json.RawMessage, int64, error)
}

type processProgram struct {
	directory string
	language  string
}

func main() {
	started := time.Now()
	request, err := decodeRequest(os.Stdin)
	if err != nil {
		writeResponse(errorResponse(nil, nil, "policy_denied", started, 0))
		return
	}
	response := execute(request, started)
	writeResponse(response)
}

func decodeRequest(reader io.Reader) (requestEnvelope, error) {
	limited := io.LimitReader(reader, maxRequestBytes+1)
	payload, err := io.ReadAll(limited)
	if err != nil || len(payload) > maxRequestBytes {
		return requestEnvelope{}, errors.New("invalid request size")
	}
	var request requestEnvelope
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return requestEnvelope{}, errors.New("invalid request")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return requestEnvelope{}, errors.New("invalid request values")
	}
	if request.Version != requestVersion || request.QuestionID != "pair_sum" ||
		len(request.Source) == 0 || len(request.Source) > maxSourceBytes ||
		len(request.Public) == 0 || len(request.Hidden) == 0 ||
		len(request.Public)+len(request.Hidden) > maxTests ||
		(request.Language != "python" && request.Language != "javascript" && request.Language != "java") {
		return requestEnvelope{}, errors.New("unsupported request")
	}
	for _, test := range append(append([]testCase(nil), request.Public...), request.Hidden...) {
		if strings.TrimSpace(test.Name) == "" || !json.Valid(test.Args) || !json.Valid(test.Expected) {
			return requestEnvelope{}, errors.New("invalid test")
		}
		if _, _, err := parsePairSumArgs(test.Args); err != nil {
			return requestEnvelope{}, errors.New("invalid test arguments")
		}
	}
	return request, nil
}

func execute(request requestEnvelope, started time.Time) responseEnvelope {
	directory, err := os.MkdirTemp("/tmp", "ic-run-")
	if err != nil {
		return errorResponse(request.Public, request.Hidden, "runner_unhealthy", started, 0)
	}
	defer os.RemoveAll(directory)
	program := &processProgram{directory: directory, language: request.Language}
	peak, compileErr := program.compile(request.Source)
	if compileErr != nil {
		return errorResponse(request.Public, request.Hidden, "compile_error", started, peak)
	}

	public := make([]publicResult, 0, len(request.Public))
	hidden := hiddenSummary{}
	failed := false
	for _, test := range request.Public {
		actual, processPeak, runErr := program.Run(context.Background(), test.Args)
		peak = max(peak, processPeak)
		if runErr != nil {
			return errorResponse(request.Public, request.Hidden, childErrorKind(runErr), started, peak)
		}
		status := "passed"
		if !equalJSON(actual, test.Expected) {
			status = "failed"
			failed = true
		}
		public = append(public, publicResult{Name: test.Name, Status: status})
	}
	for _, test := range request.Hidden {
		actual, processPeak, runErr := program.Run(context.Background(), test.Args)
		peak = max(peak, processPeak)
		if runErr != nil {
			return errorResponse(request.Public, request.Hidden, childErrorKind(runErr), started, peak)
		}
		if equalJSON(actual, test.Expected) {
			hidden.Passed++
		} else {
			hidden.Failed++
			failed = true
		}
	}
	status := "passed"
	if failed {
		status = "failed"
	}
	return responseEnvelope{
		Version: responseVersion,
		Result: safeResult{
			Version: resultVersion, Status: status, PublicTests: public,
			HiddenTests: hidden, ErrorKind: "none",
		},
		Runtime: runtime(started, peak),
	}
}

func (program *processProgram) compile(source string) (int64, error) {
	switch program.language {
	case "python":
		if err := writePrivate(filepath.Join(program.directory, "solution.py"), source); err != nil {
			return 0, err
		}
		return runProcess(context.Background(), program.directory, "python3", "-I", "-B", "-m", "py_compile", "solution.py")
	case "javascript":
		if err := writePrivate(filepath.Join(program.directory, "solution.js"), source); err != nil {
			return 0, err
		}
		return runProcess(context.Background(), program.directory, "node", "--check", "solution.js")
	case "java":
		if err := writePrivate(filepath.Join(program.directory, "Solution.java"), source); err != nil {
			return 0, err
		}
		if err := writePrivate(filepath.Join(program.directory, "Harness.java"), javaHarness); err != nil {
			return 0, err
		}
		return runProcess(context.Background(), program.directory, "javac", "-encoding", "UTF-8", "Solution.java", "Harness.java")
	default:
		return 0, errors.New("unsupported language")
	}
}

func (program *processProgram) Run(ctx context.Context, rawArgs json.RawMessage) (json.RawMessage, int64, error) {
	numbers, target, err := parsePairSumArgs(rawArgs)
	if err != nil {
		return nil, 0, err
	}
	arguments, err := json.Marshal([]any{numbers, target})
	if err != nil {
		return nil, 0, err
	}
	var command string
	var args []string
	switch program.language {
	case "python":
		command = "python3"
		args = []string{"-I", "-B", "-c", pythonHarness, string(arguments)}
	case "javascript":
		command = "node"
		args = []string{"--max-old-space-size=96", "-e", javaScriptHarness, string(arguments)}
	case "java":
		command = "java"
		args = []string{"-Xms8m", "-Xmx96m", "-XX:ActiveProcessorCount=1", "Harness", joinInts(numbers), strconv.Itoa(target)}
	default:
		return nil, 0, errors.New("unsupported language")
	}
	output, peak, err := runProcessOutput(ctx, program.directory, command, args...)
	if err != nil {
		return nil, peak, err
	}
	index := bytes.LastIndex(output, []byte(resultMarker))
	if index < 0 {
		return nil, peak, errors.New("missing result")
	}
	result := bytes.TrimSpace(output[index+len(resultMarker):])
	if !json.Valid(result) || len(result) > maxChildOutput {
		return nil, peak, errors.New("invalid result")
	}
	return append(json.RawMessage(nil), result...), peak, nil
}

func runProcess(ctx context.Context, directory, command string, args ...string) (int64, error) {
	_, peak, err := runProcessOutput(ctx, directory, command, args...)
	return peak, err
}

func runProcessOutput(ctx context.Context, directory, command string, args ...string) ([]byte, int64, error) {
	process := exec.CommandContext(ctx, command, args...)
	process.Dir = directory
	process.Env = []string{
		"HOME=/tmp", "LANG=C.UTF-8", "LC_ALL=C.UTF-8",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	var stdout limitedBuffer
	var stderr limitedBuffer
	stdout.limit = maxChildOutput
	stderr.limit = maxChildOutput
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	peak := peakMemoryKB(process.ProcessState)
	if err != nil {
		if killedForMemory(process.ProcessState) ||
			bytes.Contains(stderr.buffer.Bytes(), []byte("OutOfMemoryError")) ||
			bytes.Contains(stderr.buffer.Bytes(), []byte("heap out of memory")) {
			return nil, peak, errOutOfMemory
		}
		return nil, peak, errors.New("child failed")
	}
	if stdout.overflow || stderr.overflow {
		return nil, peak, errors.New("child failed")
	}
	return append([]byte(nil), stdout.buffer.Bytes()...), peak, nil
}

type limitedBuffer struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (buffer *limitedBuffer) Write(payload []byte) (int, error) {
	length := len(payload)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if len(payload) > remaining {
			payload = payload[:remaining]
		}
		_, _ = buffer.buffer.Write(payload)
	}
	if length > remaining {
		buffer.overflow = true
	}
	return length, nil
}

func errorResponse(publicTests, hiddenTests []testCase, kind string, started time.Time, peak int64) responseEnvelope {
	public := make([]publicResult, len(publicTests))
	for index, test := range publicTests {
		public[index] = publicResult{Name: test.Name, Status: "error"}
	}
	return responseEnvelope{
		Version: responseVersion,
		Result: safeResult{
			Version: resultVersion, Status: "error", PublicTests: public,
			HiddenTests: hiddenSummary{Failed: len(hiddenTests)}, ErrorKind: kind,
		},
		Runtime: runtime(started, peak),
	}
}

func runtime(started time.Time, peak int64) runtimeStats {
	duration := time.Since(started).Milliseconds()
	if duration < 0 {
		duration = 0
	}
	if peak < 0 {
		peak = 0
	}
	return runtimeStats{DurationMilliseconds: duration, PeakMemoryKB: peak}
}

func writeResponse(response responseEnvelope) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(true)
	if err := encoder.Encode(response); err != nil {
		os.Exit(1)
	}
}

func writePrivate(path, value string) error {
	return os.WriteFile(path, []byte(value), 0o600)
}

func parsePairSumArgs(payload json.RawMessage) ([]int, int, error) {
	var values []json.RawMessage
	if err := json.Unmarshal(payload, &values); err != nil || len(values) != 2 {
		return nil, 0, errors.New("invalid arguments")
	}
	var numbers []int
	var target int
	if err := json.Unmarshal(values[0], &numbers); err != nil || len(numbers) > 10000 {
		return nil, 0, errors.New("invalid numbers")
	}
	if err := json.Unmarshal(values[1], &target); err != nil {
		return nil, 0, errors.New("invalid target")
	}
	return numbers, target, nil
}

func equalJSON(left, right json.RawMessage) bool {
	var leftValue any
	var rightValue any
	if json.Unmarshal(left, &leftValue) != nil || json.Unmarshal(right, &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

func childErrorKind(err error) string {
	if errors.Is(err, errOutOfMemory) {
		return "out_of_memory"
	}
	return "runtime_error"
}

func joinInts(values []int) string {
	parts := make([]string, len(values))
	for index, value := range values {
		parts[index] = strconv.Itoa(value)
	}
	return strings.Join(parts, ",")
}

const pythonHarness = `
import contextlib, io, json, sys
args = json.loads(sys.argv[1])
scope = {}
sink = io.StringIO()
with contextlib.redirect_stdout(sink), contextlib.redirect_stderr(sink):
    with open("solution.py", "r", encoding="utf-8") as source_file:
        exec(compile(source_file.read(), "solution.py", "exec"), scope, scope)
    result = scope["pair_sum"](*args)
sys.stdout.write("__INTERVIEWCRAFT_RESULT__" + json.dumps(result, separators=(",", ":")))
`

const javaScriptHarness = `
const fs = require("fs");
const vm = require("vm");
const args = JSON.parse(process.argv[1]);
console.log = () => {};
console.error = () => {};
const source = fs.readFileSync("solution.js", "utf8") + "\n;globalThis.__icTarget = pairSum;";
vm.runInThisContext(source, {filename: "solution.js"});
const result = globalThis.__icTarget(...args);
process.stdout.write("__INTERVIEWCRAFT_RESULT__" + JSON.stringify(result));
`

const javaHarness = `
import java.util.Arrays;

final class Harness {
    public static void main(String[] args) {
        String[] values = args[0].isEmpty() ? new String[0] : args[0].split(",");
        int[] numbers = new int[values.length];
        for (int index = 0; index < values.length; index++) {
            numbers[index] = Integer.parseInt(values[index]);
        }
        int[] result = new Solution().pairSum(numbers, Integer.parseInt(args[1]));
        System.out.print("__INTERVIEWCRAFT_RESULT__" + Arrays.toString(result));
    }
}
`
