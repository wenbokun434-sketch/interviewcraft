package runner

import (
	"context"
	"errors"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

// Check implements the existing doctor.RunnerProbe boundary.
func (runner *DockerRunner) Check(ctx context.Context) error {
	_, err := runner.Diagnose(ctx)
	return err
}

// Diagnose verifies Docker connectivity and the expected labeled image without
// starting a user-code container.
func (runner *DockerRunner) Diagnose(ctx context.Context) (Diagnostic, error) {
	if runner == nil || runner.command == nil {
		return Diagnostic{}, unavailableRunner("diagnose Docker Runner", nil)
	}
	version, err := runner.command.Run(
		ctx,
		nil,
		"version",
		"--format",
		"{{.Server.Version}}",
	)
	if err != nil || version.ExitCode != 0 {
		return Diagnostic{}, unavailableRunner("diagnose Docker Runner", err)
	}
	serverVersion := strings.TrimSpace(string(version.Stdout))
	if !versionPattern.MatchString(serverVersion) {
		return Diagnostic{}, protocolFailure(errors.New("invalid Docker version response"))
	}
	image, err := runner.command.Run(
		ctx,
		nil,
		"image",
		"inspect",
		"--format",
		`{{index .Config.Labels "io.interviewcraft.runner"}}`,
		runner.config.Image,
	)
	if err != nil || image.ExitCode != 0 || strings.TrimSpace(string(image.Stdout)) != "true" {
		return Diagnostic{}, domainerr.Wrap(
			domainerr.CodeDependencyUnavailable,
			"diagnose Docker Runner image",
			"Docker Runner",
			"Docker Runner 镜像不可用或标签无效。",
			"运行 `docker build -t interviewcraft-runner:local docker/runner`。",
			true,
			err,
		)
	}
	limits := runner.config.Limits
	return Diagnostic{
		DockerVersion: serverVersion,
		Image:         runner.config.Image, ImageReady: true,
		NetworkDisabled: true, ReadOnlyRoot: true, NonRootUser: true,
		CapabilitiesOff: true, NoNewPrivileges: true,
		CPUs: limits.CPUs, MemoryMB: limits.MemoryMB,
		PIDs: limits.PIDs, WallTime: limits.WallTime,
	}, nil
}
