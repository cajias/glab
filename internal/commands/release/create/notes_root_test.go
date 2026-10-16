//go:build !integration

package create

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveNotesFileOrText_ReleasesDirectory(t *testing.T) {
	for _, tt := range []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "file", input: "notes.md", want: "release notes"},
		{name: "missing file", input: "missing.md", want: "missing.md"},
		{name: "directory read error", input: "directory", wantErr: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original, err := os.Getwd()
			require.NoError(t, err)
			directory := t.TempDir()
			t.Chdir(directory)
			require.NoError(t, os.WriteFile("notes.md", []byte("release notes"), 0o600))
			require.NoError(t, os.Mkdir("directory", 0o700))

			got, err := resolveNotesFileOrText(&options{experimentalNotesTextOrFile: tt.input})

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
			if runtime.GOOS == "linux" {
				assert.Empty(t, openDescriptors(t, directory))
			}
			require.NoError(t, os.Chdir(original)) //nolint:usetesting // t.Chdir keeps the current directory open until cleanup, preventing this removal on Windows.
			require.NoError(t, os.RemoveAll(directory))
		})
	}
}

func openDescriptors(t *testing.T, path string) []string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	entries, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)
	var open []string
	for _, entry := range entries {
		target, err := os.Readlink("/proc/self/fd/" + entry.Name())
		if err == nil && target == resolved {
			open = append(open, entry.Name())
		}
	}
	return open
}
