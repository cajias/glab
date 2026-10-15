package config

import (
	"io/fs"
	"runtime"
)

// HasSecurePerms reports whether a config file belonging to the invoking user
// is kept private. Such a file can hold a token, so anything another account
// can read is rejected.
func HasSecurePerms(m fs.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	} else {
		return m == 0o600
	}
}

// HasSafeSharedPerms reports whether a system-wide config file is not writable
// by group or others. Go reports every file on Windows as 0666, so the write
// bits mean nothing there.
func HasSafeSharedPerms(m fs.FileMode) bool {
	if runtime.GOOS == "windows" {
		return true
	}
	return m&0o022 == 0
}
