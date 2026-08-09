package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// SaveAtomic validates and atomically replaces one non-secret runtime
// configuration. The previous file remains intact on every pre-rename error.
func SaveAtomic(path string, runtime Runtime) error {
	if err := runtime.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(path) == "" {
		return validationError(
			"save runtime configuration", path,
			"配置文件路径不能为空。",
			"提供明确的数据目录后重试。",
			errors.New("configuration path is blank"),
		)
	}
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return configError(
			"create configuration directory", directory,
			"无法创建配置目录。", "检查路径和写入权限后重试。", err,
		)
	}
	payload, err := json.MarshalIndent(runtime, "", "  ")
	if err != nil {
		return configError(
			"encode runtime configuration", path,
			"无法编码本地配置。", "修正配置字段后重试。", err,
		)
	}
	payload = append(payload, '\n')
	temporary, err := os.CreateTemp(directory, ".config-*.tmp")
	if err != nil {
		return configError(
			"create temporary configuration", directory,
			"无法准备原子配置写入。", "检查目录权限和磁盘空间后重试。", err,
		)
	}
	temporaryPath := temporary.Name()
	committed := false
	defer func() {
		_ = temporary.Close()
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		return configError("protect temporary configuration", temporaryPath, "无法保护临时配置文件。", "检查文件系统权限后重试。", err)
	}
	if _, err := temporary.Write(payload); err != nil {
		return configError("write temporary configuration", temporaryPath, "无法写入临时配置文件。", "检查磁盘空间和权限后重试。", err)
	}
	if err := temporary.Sync(); err != nil {
		return configError("sync temporary configuration", temporaryPath, "无法同步临时配置文件。", "检查磁盘状态后重试。", err)
	}
	if err := temporary.Close(); err != nil {
		return configError("close temporary configuration", temporaryPath, "无法关闭临时配置文件。", "检查磁盘状态后重试。", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return configError("replace runtime configuration", path, "无法原子替换现有配置。", "旧配置已保留；检查文件锁和目录权限后重试。", err)
	}
	committed = true
	return nil
}
