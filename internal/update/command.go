package update

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
)

type osCommands struct{}

func (osCommands) Run(ctx context.Context, environment []string, binary string, args ...string) (CommandResult, error) {
	command := exec.CommandContext(ctx, binary, args...)
	command.Env = append(os.Environ(), environment...)
	stdout := &boundedBuffer{limit: 256 << 10}
	stderr := &boundedBuffer{limit: 256 << 10}
	command.Stdout, command.Stderr = stdout, stderr
	err := command.Run()
	result := CommandResult{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}
	if err == nil {
		return result, nil
	}
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		result.ExitCode = exitError.ExitCode()
	} else {
		result.ExitCode = -1
	}
	return result, err
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *boundedBuffer) Write(payload []byte) (int, error) {
	written := len(payload)
	remaining := buffer.limit - buffer.buffer.Len()
	if remaining > 0 {
		if len(payload) > remaining {
			payload = payload[:remaining]
		}
		_, _ = buffer.buffer.Write(payload)
	}
	return written, nil
}
func (buffer *boundedBuffer) Bytes() []byte { return bytes.Clone(buffer.buffer.Bytes()) }
