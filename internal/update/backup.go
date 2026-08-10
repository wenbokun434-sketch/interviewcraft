package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/interviewcraft/interviewcraft/internal/db"
)

type BackupEntry struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Mode   uint32 `json:"mode"`
}

type BackupDirectory struct {
	Path string `json:"path"`
	Mode uint32 `json:"mode"`
}

type BackupManifest struct {
	Schema      string            `json:"schema"`
	Version     string            `json:"version"`
	CreatedUTC  time.Time         `json:"created_utc"`
	DataDirName string            `json:"data_dir_name"`
	Directories []BackupDirectory `json:"directories"`
	Entries     []BackupEntry     `json:"entries"`
}

func backupRootFor(dataDir string) string {
	clean := filepath.Clean(dataDir)
	base := strings.TrimLeft(filepath.Base(clean), ".")
	if base == "" {
		base = "interviewcraft"
	}
	return filepath.Join(filepath.Dir(clean), "."+base+"-backups")
}

func statePathFor(dataDir string) string {
	return filepath.Join(backupRootFor(dataDir), "update-state.json")
}

func createBackup(ctx context.Context, dataDir, binaryPath, versionValue string, now time.Time, available func(string) (uint64, error)) (string, error) {
	if !versionPattern.MatchString(versionValue) {
		return "", errors.New("backup version is invalid")
	}
	dataDir, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	binaryPath, err = filepath.Abs(binaryPath)
	if err != nil {
		return "", err
	}
	if err := validateScopedDirectory(dataDir); err != nil {
		return "", err
	}
	total, err := treeSize(dataDir)
	if err != nil {
		return "", err
	}
	binaryInfo, err := os.Stat(binaryPath)
	if err != nil || !binaryInfo.Mode().IsRegular() {
		return "", errors.New("installed binary is unavailable")
	}
	total += binaryInfo.Size()
	if available != nil {
		free, err := available(filepath.Dir(dataDir))
		if err != nil {
			return "", err
		}
		if uint64(total)*2 > free {
			return "", errors.New("insufficient disk space for verified backup")
		}
	}
	root := backupRootFor(dataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	id := now.UTC().Format("20060102T150405.000000000Z") + "-" + sanitizeVersion(versionValue)
	final := filepath.Join(root, id)
	temporary := final + ".tmp"
	if _, err := os.Stat(final); err == nil {
		return "", errors.New("backup identifier already exists")
	}
	if err := os.Mkdir(temporary, 0o700); err != nil {
		return "", err
	}
	committed := false
	defer func() {
		if !committed {
			_ = os.RemoveAll(temporary)
		}
	}()
	manifest := BackupManifest{Schema: BackupSchema, Version: versionValue, CreatedUTC: now.UTC(), DataDirName: filepath.Base(dataDir)}
	if err := copyBackupTree(ctx, dataDir, filepath.Join(temporary, "data"), "data", &manifest); err != nil {
		return "", err
	}
	binaryName := filepath.Base(binaryPath)
	binaryTarget := filepath.Join(temporary, "binary", binaryName)
	if err := copyFile(binaryPath, binaryTarget, binaryInfo.Mode().Perm()); err != nil {
		return "", err
	}
	hash, err := fileSHA256(binaryTarget)
	if err != nil {
		return "", err
	}
	manifest.Entries = append(manifest.Entries, BackupEntry{Path: filepath.ToSlash(filepath.Join("binary", binaryName)), SHA256: hash, Size: binaryInfo.Size(), Mode: uint32(binaryInfo.Mode().Perm())})
	sort.Slice(manifest.Entries, func(left, right int) bool { return manifest.Entries[left].Path < manifest.Entries[right].Path })
	sort.Slice(manifest.Directories, func(left, right int) bool { return manifest.Directories[left].Path < manifest.Directories[right].Path })
	if len(manifest.Entries) == 0 {
		return "", errors.New("backup would be empty")
	}
	if err := writeJSONAtomic(filepath.Join(temporary, "backup-manifest.json"), manifest); err != nil {
		return "", err
	}
	if _, err := verifyBackup(temporary); err != nil {
		return "", err
	}
	if err := os.Rename(temporary, final); err != nil {
		return "", err
	}
	committed = true
	return final, nil
}

func copyBackupTree(ctx context.Context, source, target, prefix string, manifest *BackupManifest) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(target, 0o700)
		}
		if relative == filepath.Base(db.MaintenanceGuardPath(source)) || relative == filepath.Base(db.WorkspaceLockPath(source)) {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("backup refuses symbolic links")
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			if err := os.MkdirAll(destination, info.Mode().Perm()); err != nil {
				return err
			}
			manifest.Directories = append(manifest.Directories, BackupDirectory{Path: filepath.ToSlash(filepath.Join(prefix, relative)), Mode: uint32(info.Mode().Perm())})
			return nil
		}
		if !info.Mode().IsRegular() {
			return errors.New("backup refuses non-regular files")
		}
		if err := copyFile(path, destination, info.Mode().Perm()); err != nil {
			return err
		}
		hash, err := fileSHA256(destination)
		if err != nil {
			return err
		}
		manifest.Entries = append(manifest.Entries, BackupEntry{Path: filepath.ToSlash(filepath.Join(prefix, relative)), SHA256: hash, Size: info.Size(), Mode: uint32(info.Mode().Perm())})
		return nil
	})
}

func verifyBackup(directory string) (BackupManifest, error) {
	payload, err := os.ReadFile(filepath.Join(directory, "backup-manifest.json"))
	if err != nil {
		return BackupManifest{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	var manifest BackupManifest
	if err := decoder.Decode(&manifest); err != nil {
		return BackupManifest{}, err
	}
	if manifest.Schema != BackupSchema || !versionPattern.MatchString(manifest.Version) || manifest.CreatedUTC.IsZero() || len(manifest.Entries) == 0 {
		return BackupManifest{}, errors.New("backup manifest metadata is invalid")
	}
	seen := map[string]bool{}
	seenDirectories := map[string]bool{}
	for _, recordedDirectory := range manifest.Directories {
		if !safeRelativePath(recordedDirectory.Path) || !strings.HasPrefix(recordedDirectory.Path, "data/") || seenDirectories[recordedDirectory.Path] {
			return BackupManifest{}, errors.New("backup directory entry is invalid")
		}
		seenDirectories[recordedDirectory.Path] = true
		info, err := os.Lstat(filepath.Join(directory, filepath.FromSlash(recordedDirectory.Path)))
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return BackupManifest{}, errors.New("backup directory metadata does not match")
		}
	}
	for _, entry := range manifest.Entries {
		if !safeRelativePath(entry.Path) || !hashPattern.MatchString(entry.SHA256) || entry.Size < 0 || seen[entry.Path] {
			return BackupManifest{}, errors.New("backup manifest entry is invalid")
		}
		seen[entry.Path] = true
		path := filepath.Join(directory, filepath.FromSlash(entry.Path))
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() != entry.Size {
			return BackupManifest{}, errors.New("backup file metadata does not match")
		}
		hash, err := fileSHA256(path)
		if err != nil || hash != entry.SHA256 {
			return BackupManifest{}, errors.New("backup file hash does not match")
		}
	}
	actual := map[string]bool{}
	actualDirectories := map[string]bool{}
	for _, root := range []string{"data", "binary"} {
		err := filepath.WalkDir(filepath.Join(directory, root), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if path != filepath.Join(directory, root) && root == "data" {
					relative, err := filepath.Rel(directory, path)
					if err != nil {
						return err
					}
					actualDirectories[filepath.ToSlash(relative)] = true
				}
				return nil
			}
			relative, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			actual[filepath.ToSlash(relative)] = true
			return nil
		})
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return BackupManifest{}, err
		}
	}
	if len(actual) != len(seen) {
		return BackupManifest{}, errors.New("backup contains unrecorded files")
	}
	for path := range actual {
		if !seen[path] {
			return BackupManifest{}, errors.New("backup contains an unrecorded file")
		}
	}
	if len(actualDirectories) != len(seenDirectories) {
		return BackupManifest{}, errors.New("backup contains unrecorded directories")
	}
	for path := range actualDirectories {
		if !seenDirectories[path] {
			return BackupManifest{}, errors.New("backup contains an unrecorded directory")
		}
	}
	return manifest, nil
}

func restoreBackup(ctx context.Context, backupDir, dataDir, binaryPath, guardToken, diagnosticDir string) error {
	manifest, err := verifyBackup(backupDir)
	if err != nil {
		return err
	}
	if err := validateScopedDirectory(dataDir); err != nil {
		return err
	}
	stage := filepath.Join(filepath.Dir(dataDir), "."+filepath.Base(dataDir)+"-restore-"+fmt.Sprint(time.Now().UnixNano()))
	if err := os.Mkdir(stage, 0o700); err != nil {
		return err
	}
	staged := false
	defer func() {
		if !staged {
			_ = os.RemoveAll(stage)
		}
	}()
	for _, entry := range manifest.Entries {
		if !strings.HasPrefix(entry.Path, "data/") {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative := strings.TrimPrefix(entry.Path, "data/")
		if err := copyFile(filepath.Join(backupDir, filepath.FromSlash(entry.Path)), filepath.Join(stage, filepath.FromSlash(relative)), os.FileMode(entry.Mode)); err != nil {
			return err
		}
	}
	for _, recordedDirectory := range manifest.Directories {
		relative := strings.TrimPrefix(recordedDirectory.Path, "data/")
		if err := os.MkdirAll(filepath.Join(stage, filepath.FromSlash(relative)), os.FileMode(recordedDirectory.Mode)); err != nil {
			return err
		}
	}
	if err := os.WriteFile(db.MaintenanceGuardPath(stage), []byte(guardToken+"\n"), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(db.WorkspaceLockPath(stage), nil, 0o600); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(diagnosticDir), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(diagnosticDir); err == nil {
		return errors.New("diagnostic directory already exists")
	}
	if err := os.Rename(dataDir, diagnosticDir); err != nil {
		return err
	}
	if err := os.Rename(stage, dataDir); err != nil {
		_ = os.Rename(diagnosticDir, dataDir)
		return err
	}
	staged = true
	binaryName := filepath.Base(binaryPath)
	if err := installBinary(filepath.Join(backupDir, "binary", binaryName), binaryPath); err != nil {
		return err
	}
	return nil
}

func installBinary(source, target string) error {
	info, err := os.Stat(source)
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("staged binary is unavailable")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(target), ".interviewcraft-binary-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	_ = temporary.Close()
	_ = os.Remove(temporaryPath)
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := copyFile(source, temporaryPath, info.Mode().Perm()); err != nil {
		return err
	}
	if err := replaceFile(temporaryPath, target); err != nil {
		return err
	}
	committed = true
	return nil
}

func treeSize(root string) (int64, error) {
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("data directory contains an unsupported file")
		}
		relative, relativeErr := filepath.Rel(root, path)
		if relativeErr != nil {
			return relativeErr
		}
		if relative != filepath.Base(db.MaintenanceGuardPath(root)) && relative != filepath.Base(db.WorkspaceLockPath(root)) {
			total += info.Size()
		}
		return nil
	})
	return total, err
}

func validateScopedDirectory(path string) error {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	rest = strings.Trim(rest, string(filepath.Separator))
	if clean == "" || rest == "" || filepath.Dir(clean) == clean {
		return errors.New("data directory is too broad")
	}
	return nil
}

func safeRelativePath(path string) bool {
	if path == "" || strings.Contains(path, "\\") || strings.HasPrefix(path, "/") || filepath.IsAbs(path) {
		return false
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(path)))
	return clean == path && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

func sanitizeVersion(value string) string {
	return strings.NewReplacer("+", "_", "/", "_", "\\", "_").Replace(value)
}
