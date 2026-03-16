//go:build windows

package config

import (
	"io/fs"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// validateCookieFilePermissions on Windows is a no-op.
// Windows uses ACLs rather than Unix-style file permissions, so permission
// and ownership validation is not performed.
func validateCookieFilePermissions(_ string, _ fs.FileInfo) error {
	dbg.Debugf("cookie file permission checks are not performed on Windows (ACLs are used instead of Unix-style permissions)\n")
	return nil
}
