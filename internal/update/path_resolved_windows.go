package update

import (
	"errors"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const finalPathNameNormalizedVolumeDOS = 0

// resolvedPathWithin obtains the final DOS paths from Windows file handles,
// which normalizes 8.3 aliases and follows reparse points before containment
// is checked.
func resolvedPathWithin(root, target string) bool {
	resolvedRoot, err := finalWindowsPath(root, windows.FILE_FLAG_BACKUP_SEMANTICS)
	if err != nil {
		return false
	}
	resolvedTarget, err := finalWindowsPath(target, 0)
	if err != nil {
		return false
	}
	return pathWithin(resolvedRoot, resolvedTarget)
}

func finalWindowsPath(path string, flags uint32) (string, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(pointer, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, flags, 0)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 32768)
	length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), finalPathNameNormalizedVolumeDOS)
	if err != nil {
		return "", err
	}
	if length == 0 || length >= uint32(len(buffer)) {
		return "", errors.New("final Windows path is invalid")
	}
	resolved := windows.UTF16ToString(buffer[:length])
	resolved = strings.TrimPrefix(resolved, `\\?\`)
	return filepath.Clean(resolved), nil
}
