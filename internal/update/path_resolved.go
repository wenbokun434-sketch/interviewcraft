//go:build !windows

package update

import "path/filepath"

// resolvedPathWithin resolves existing links before enforcing the local test
// verifier's temporary-directory boundary.
func resolvedPathWithin(root, target string) bool {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return false
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return false
	}
	return pathWithin(filepath.Clean(resolvedRoot), filepath.Clean(resolvedTarget))
}
