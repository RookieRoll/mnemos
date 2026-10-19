//go:build !windows

package continuity

import "path/filepath"

func canonicalFilesystemPath(path string) (string, error) { return filepath.EvalSymlinks(path) }
