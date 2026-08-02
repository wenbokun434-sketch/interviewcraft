package runner

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/core/domainerr"
)

var (
	imagePattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/@:-]{0,255}$`)
	containerNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	versionPattern       = regexp.MustCompile(`^[0-9][0-9A-Za-z.+-]{0,63}$`)
)

func normalizeConfig(config Config) Config {
	defaults := DefaultConfig()
	if strings.TrimSpace(config.DockerBinary) == "" {
		config.DockerBinary = defaults.DockerBinary
	}
	if strings.TrimSpace(config.Image) == "" {
		config.Image = defaults.Image
	}
	if config.Limits.CPUs == 0 {
		config.Limits.CPUs = defaults.Limits.CPUs
	}
	if config.Limits.MemoryMB == 0 {
		config.Limits.MemoryMB = defaults.Limits.MemoryMB
	}
	if config.Limits.PIDs == 0 {
		config.Limits.PIDs = defaults.Limits.PIDs
	}
	if config.Limits.WallTime == 0 {
		config.Limits.WallTime = defaults.Limits.WallTime
	}
	if config.Limits.TmpfsMB == 0 {
		config.Limits.TmpfsMB = defaults.Limits.TmpfsMB
	}
	if config.Limits.MaxOutputBytes == 0 {
		config.Limits.MaxOutputBytes = defaults.Limits.MaxOutputBytes
	}
	if config.Limits.CleanupTimeout == 0 {
		config.Limits.CleanupTimeout = defaults.Limits.CleanupTimeout
	}
	if config.Limits.ProgressInterval == 0 {
		config.Limits.ProgressInterval = defaults.Limits.ProgressInterval
	}
	return config
}

func validateConfig(config Config) error {
	var issues []string
	if strings.ContainsRune(config.DockerBinary, 0) {
		issues = append(issues, "docker binary contains NUL")
	}
	if !imagePattern.MatchString(config.Image) || strings.HasPrefix(config.Image, "-") {
		issues = append(issues, "image reference is invalid")
	}
	limits := config.Limits
	if limits.CPUs < 0.10 || limits.CPUs > 2 {
		issues = append(issues, "cpus must be between 0.10 and 2")
	}
	if limits.MemoryMB < 64 || limits.MemoryMB > 512 {
		issues = append(issues, "memory must be between 64MB and 512MB")
	}
	if limits.PIDs < 16 || limits.PIDs > 128 {
		issues = append(issues, "pids must be between 16 and 128")
	}
	if limits.WallTime < 100*time.Millisecond || limits.WallTime > 30*time.Second {
		issues = append(issues, "wall time must be between 100ms and 30s")
	}
	if limits.TmpfsMB < 16 || limits.TmpfsMB > 128 {
		issues = append(issues, "tmpfs must be between 16MB and 128MB")
	}
	if limits.MaxOutputBytes < 4<<10 || limits.MaxOutputBytes > 256<<10 {
		issues = append(issues, "output limit must be between 4KB and 256KB")
	}
	if limits.CleanupTimeout < time.Second || limits.CleanupTimeout > 10*time.Second {
		issues = append(issues, "cleanup timeout must be between 1s and 10s")
	}
	if limits.ProgressInterval < 10*time.Millisecond ||
		limits.ProgressInterval > time.Second {
		issues = append(issues, "progress interval must be between 10ms and 1s")
	}
	if len(issues) != 0 {
		return domainerr.Wrap(
			domainerr.CodeValidation,
			"configure Docker Runner",
			"Docker",
			"Docker Runner 安全配置无效。",
			"恢复默认隔离限制后重试。",
			false,
			errors.New(strings.Join(issues, "; ")),
		)
	}
	return nil
}

func validateContainerName(value string) error {
	if !containerNamePattern.MatchString(value) ||
		!strings.HasPrefix(value, "interviewcraft-runner-") {
		return fmt.Errorf("invalid generated container name")
	}
	return nil
}

func randomContainerName() string {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "interviewcraft-runner-" +
			hex.EncodeToString([]byte(fmt.Sprintf("%d", time.Now().UnixNano())))
	}
	return "interviewcraft-runner-" + hex.EncodeToString(buffer)
}
