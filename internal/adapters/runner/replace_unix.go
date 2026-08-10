//go:build !windows

package runner

import "os"

func replaceVerifierFile(source, target string) error {
	return os.Rename(source, target)
}
