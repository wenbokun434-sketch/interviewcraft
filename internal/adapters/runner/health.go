package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

type imageInspection struct {
	RepoDigests  []string `json:"RepoDigests"`
	OS           string   `json:"Os"`
	Architecture string   `json:"Architecture"`
	Config       struct {
		User   string            `json:"User"`
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

// Check implements doctor.RunnerProbe.
func (runner *DockerRunner) Check(ctx context.Context) error {
	_, err := runner.Diagnose(ctx)
	return err
}

// Diagnose fails closed for released images: daemon, Sigstore identity,
// immutable digest, architecture, labels, protocol and non-root user must all
// match before a code execution service is exposed.
func (runner *DockerRunner) Diagnose(ctx context.Context) (Diagnostic, error) {
	if runner == nil || runner.command == nil {
		return Diagnostic{}, unavailableRunner("diagnose Docker Runner", nil)
	}
	version, err := runner.command.Run(ctx, nil, "version", "--format", "{{.Server.Version}}")
	if err != nil || version.ExitCode != 0 {
		return Diagnostic{}, unavailableRunner("diagnose Docker Runner daemon", err)
	}
	serverVersion := strings.TrimSpace(string(version.Stdout))
	if !versionPattern.MatchString(serverVersion) {
		return Diagnostic{}, protocolFailure(errors.New("invalid Docker version response"))
	}

	signatureVerified := false
	if runner.config.ExpectedDigest != "" {
		if runner.verifier == nil || runner.verifier.VerifyImage(
			ctx, runner.config.Image, runner.config.CertificateIdentity, runner.config.OIDCIssuer,
		) != nil {
			return Diagnostic{}, unavailableRunner("verify Docker Runner signature", errors.New("signature policy rejected image"))
		}
		signatureVerified = true
	}

	result, err := runner.command.Run(ctx, nil, "image", "inspect", runner.config.Image)
	if err != nil || result.ExitCode != 0 {
		return Diagnostic{}, unavailableRunner("inspect Docker Runner image", err)
	}
	var values []imageInspection
	if json.Unmarshal(result.Stdout, &values) != nil || len(values) != 1 {
		return Diagnostic{}, protocolFailure(errors.New("invalid Docker image inspection response"))
	}
	inspection := values[0]
	if err := validateInspection(runner.config, inspection); err != nil {
		return Diagnostic{}, domainerr.Wrap(
			domainerr.CodeDependencyUnavailable,
			"validate Docker Runner image",
			"Docker Runner",
			"Docker Runner 镜像未通过完整性与兼容性检查。",
			"重新运行 `interviewcraft setup --profile full --restart` 拉取并验证正式镜像。",
			true,
			err,
		)
	}

	limits := runner.config.Limits
	diagnostic := Diagnostic{
		DockerVersion:     serverVersion,
		Image:             runner.config.Image,
		ImageReady:        true,
		SignatureVerified: signatureVerified,
		NetworkDisabled:   true,
		ReadOnlyRoot:      true,
		NonRootUser:       true,
		CapabilitiesOff:   true,
		NoNewPrivileges:   true,
		CPUs:              limits.CPUs,
		MemoryMB:          limits.MemoryMB,
		PIDs:              limits.PIDs,
		WallTime:          limits.WallTime,
	}
	if runner.config.ExpectedDigest != "" {
		diagnostic.Digest = runner.config.ExpectedDigest
		diagnostic.Version = runner.config.ExpectedVersion
		diagnostic.Protocol = runner.config.ExpectedProtocol
		diagnostic.Architecture = runner.config.ExpectedArchitecture
	}
	return diagnostic, nil
}

func validateInspection(config Config, inspection imageInspection) error {
	labels := inspection.Config.Labels
	if labels["io.interviewcraft.runner"] != "true" {
		return errors.New("runner label is invalid")
	}
	if inspection.Config.User != "65532:65532" {
		return fmt.Errorf("runner user is invalid")
	}
	if config.ExpectedDigest == "" {
		return nil
	}
	if inspection.OS != "linux" || inspection.Architecture != config.ExpectedArchitecture {
		return errors.New("runner platform is incompatible")
	}
	if labels["io.interviewcraft.version"] != config.ExpectedVersion {
		return errors.New("runner version label is incompatible")
	}
	if labels["io.interviewcraft.protocol"] != config.ExpectedProtocol {
		return errors.New("runner protocol label is incompatible")
	}
	want := OfficialRepository + "@" + config.ExpectedDigest
	found := false
	for _, value := range inspection.RepoDigests {
		if value == want {
			found = true
			break
		}
	}
	if !found {
		return errors.New("runner repository digest does not match")
	}
	return nil
}
