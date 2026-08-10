//go:build windows

package update

import "golang.org/x/sys/windows"

func diskAvailable(path string) (uint64, error) {
	pointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available uint64
	err = windows.GetDiskFreeSpaceEx(pointer, &available, nil, nil)
	return available, err
}
