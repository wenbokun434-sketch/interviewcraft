package update

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

const receiptHeader = "interviewcraft-install-receipt-v1"

type Receipt struct {
	Version     string
	InstallDir  string
	BinaryPath  string
	DataDir     string
	PathTarget  string
	PathFiles   []string
	Uninstaller string
}

func ReadReceipt(path string) (Receipt, error) {
	file, err := os.Open(path)
	if err != nil {
		return Receipt{}, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	line := 0
	seen := map[string]bool{}
	receipt := Receipt{}
	for scanner.Scan() {
		line++
		value := strings.TrimSuffix(scanner.Text(), "\r")
		if line == 1 {
			if value != receiptHeader {
				return Receipt{}, errors.New("install receipt header is invalid")
			}
			continue
		}
		fields := strings.Split(value, "\t")
		if len(fields) != 2 || fields[1] == "" {
			return Receipt{}, errors.New("install receipt row is invalid")
		}
		if fields[0] != "path_file" && seen[fields[0]] {
			return Receipt{}, errors.New("install receipt field is duplicated")
		}
		seen[fields[0]] = true
		switch fields[0] {
		case "version":
			receipt.Version = fields[1]
		case "install_dir":
			receipt.InstallDir = fields[1]
		case "binary_path":
			receipt.BinaryPath = fields[1]
		case "data_dir":
			receipt.DataDir = fields[1]
		case "path_target":
			receipt.PathTarget = fields[1]
		case "path_file":
			receipt.PathFiles = append(receipt.PathFiles, fields[1])
		case "uninstaller_path":
			receipt.Uninstaller = fields[1]
		default:
			return Receipt{}, errors.New("install receipt field is unknown")
		}
	}
	if err := scanner.Err(); err != nil {
		return Receipt{}, err
	}
	if line < 4 || !versionPattern.MatchString(receipt.Version) {
		return Receipt{}, errors.New("install receipt is incomplete")
	}
	installDir, err := filepath.Abs(receipt.InstallDir)
	if err != nil {
		return Receipt{}, err
	}
	binaryPath, err := filepath.Abs(receipt.BinaryPath)
	if err != nil {
		return Receipt{}, err
	}
	binaryName := filepath.Base(binaryPath)
	if filepath.Dir(binaryPath) != installDir || (!strings.EqualFold(binaryName, "interviewcraft") && !strings.EqualFold(binaryName, "interviewcraft.exe")) {
		return Receipt{}, errors.New("install receipt binary path is unsafe")
	}
	receipt.InstallDir, receipt.BinaryPath = installDir, binaryPath
	if receipt.DataDir != "" {
		receipt.DataDir, err = filepath.Abs(receipt.DataDir)
		if err != nil || validateScopedDirectory(receipt.DataDir) != nil {
			return Receipt{}, errors.New("install receipt data directory is unsafe")
		}
	}
	return receipt, nil
}

func WriteReceipt(path string, receipt Receipt) error {
	values := []string{receipt.Version, receipt.InstallDir, receipt.BinaryPath, receipt.DataDir, receipt.PathTarget, receipt.Uninstaller}
	for _, value := range append(values, receipt.PathFiles...) {
		if strings.ContainsAny(value, "\t\r\n") {
			return errors.New("install receipt contains a control character")
		}
	}
	lines := []string{receiptHeader, "version\t" + receipt.Version, "install_dir\t" + receipt.InstallDir, "binary_path\t" + receipt.BinaryPath}
	if receipt.DataDir != "" {
		lines = append(lines, "data_dir\t"+receipt.DataDir)
	}
	if receipt.PathTarget != "" {
		lines = append(lines, "path_target\t"+receipt.PathTarget)
	}
	for _, value := range receipt.PathFiles {
		lines = append(lines, "path_file\t"+value)
	}
	if receipt.Uninstaller != "" {
		lines = append(lines, "uninstaller_path\t"+receipt.Uninstaller)
	}
	return writeAtomic(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
}
