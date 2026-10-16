//go:build !integration

package authutils

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_isOurCredentialHelper(t *testing.T) {
	tests := []struct {
		name string
		arg  string
		want bool
	}{
		{
			name: "looks like glab but isn't",
			arg:  "glab auth",
			want: false,
		},
		{
			name: "ours",
			arg:  "!/path/to/glab auth",
			want: true,
		},
		{
			name: "blank",
			arg:  "",
			want: false,
		},
		{
			name: "invalid",
			arg:  "!",
			want: false,
		},
		{
			name: "osxkeychain",
			arg:  "osxkeychain",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isOurCredentialHelper(tt.arg); got != tt.want {
				t.Errorf("isOurCredentialHelper() = %v, want %v", got, tt.want)
			}
		})
	}
}

// Runs real Git against a throwaway global configuration: the failure this
// guards against is Git refusing to overwrite a key that holds several values.
func TestGitCredentialFlow_SetupHelper_runsAgain(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Chdir(t.TempDir())

	// isOurCredentialHelper does not recognize this name, so the second run
	// rewrites the key instead of skipping it.
	gc := &GitCredentialFlow{Executable: filepath.Join(t.TempDir(), "glab4071")}
	require.NoError(t, gc.SetupHelper("gitlab.example.com", "https"))
	require.NoError(t, gc.SetupHelper("gitlab.example.com", "https"))

	out, err := exec.Command("git", "config", "--global", "--get-all", "credential.https://gitlab.example.com.helper").Output()
	require.NoError(t, err)

	values := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	require.Len(t, values, 2)
	assert.Empty(t, values[0])
	assert.Equal(t, "!"+shellQuote(gc.Executable)+" auth git-credential", values[1])
}
