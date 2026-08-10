package runner

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
)

type osCommand struct {
	binary         string
	maxOutputBytes int
}

type cosignVerifier struct {
	binary         string
	maxOutputBytes int
}

func (verifier cosignVerifier) VerifyImage(
	ctx context.Context,
	image string,
	identity string,
	issuer string,
) error {
	command := osCommand{binary: verifier.binary, maxOutputBytes: verifier.maxOutputBytes}
	result, err := command.Run(ctx, nil,
		"verify",
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", issuer,
		image,
	)
	if err != nil || result.ExitCode != 0 {
		return errors.New("runner image signature verification failed")
	}
	return nil
}

func (command osCommand) Run(
	ctx context.Context,
	stdin []byte,
	args ...string,
) (CommandResult, error) {
	executable := exec.CommandContext(ctx, command.binary, args...)
	executable.Stdin = bytes.NewReader(stdin)
	stdout := &limitedBuffer{limit: command.maxOutputBytes}
	stderr := &limitedBuffer{limit: command.maxOutputBytes}
	executable.Stdout = stdout
	executable.Stderr = stderr
	err := executable.Run()
	result := CommandResult{
		Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: 0,
	}
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

type limitedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (buffer *limitedBuffer) Write(payload []byte) (int, error) {
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

func (buffer *limitedBuffer) Bytes() []byte {
	return bytes.Clone(buffer.buffer.Bytes())
}
