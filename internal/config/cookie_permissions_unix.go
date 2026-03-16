//go:build !windows

package config

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// validateCookieFilePermissions checks that the cookie file has permissions 0600
// (owner read/write only) and is owned by the current user, inspired by how ssh
// validates identity files.
func validateCookieFilePermissions(path string, info fs.FileInfo) error {
	// Check permissions are exactly 0600
	perm := info.Mode().Perm()
	if perm != 0o600 {
		return fmt.Errorf("cookie file %s has permissions %04o, expected 0600; run: chmod 600 %s", path, perm, path)
	}

	// Check file is owned by the current user
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cookie file %s: unable to determine file ownership", path)
	}

	currentUID := uint32(os.Getuid())
	if stat.Uid != currentUID {
		return fmt.Errorf("cookie file %s is not owned by the current user (file uid=%d, current uid=%d)", path, stat.Uid, currentUID)
	}

	return nil
}
