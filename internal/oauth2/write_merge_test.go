//go:build !integration

package oauth2

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zalando/go-keyring"
	"go.yaml.in/yaml/v3"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func reloadHostValue(t *testing.T, dir, host, key string) string {
	t.Helper()
	cfg, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	v, _ := cfg.Get(host, key)
	return v
}

// rawHostEntry reads a host's entry straight from config.yml. Unlike Get, it
// never resolves a value from the keyring or the environment.
func rawHostEntry(t *testing.T, dir, host string) (map[string]any, bool) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	var doc struct {
		Hosts map[string]map[string]any `yaml:"hosts"`
	}
	require.NoError(t, yaml.Unmarshal(data, &doc))
	entry, ok := doc.Hosts[host]
	return entry, ok
}

// TestWrite_AdoptsFresherCredentialsFromDisk reproduces the #8390 clobber: a
// process holding a stale in-memory copy writes for an unrelated reason and must
// not roll back credentials another process rotated on disk in the meantime.
func TestWrite_AdoptsFresherCredentialsFromDisk(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	// Seed plaintext credentials on disk (no use_keyring => stored in the file).
	seed := config.NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_refresh_token", "refresh-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", past))
	require.NoError(t, seed.Write())

	// A stale reader loads the config...
	stale, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	// ...then another process rotates the token on disk.
	winner, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, winner.Set("gitlab.com", "token", "access-new"))
	require.NoError(t, winner.Set("gitlab.com", "oauth2_refresh_token", "refresh-new"))
	require.NoError(t, winner.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, winner.Write())

	// The stale reader now writes for an unrelated reason.
	require.NoError(t, stale.Set("", "last_update_check_timestamp", future))
	require.NoError(t, stale.Write())

	// The rotated credentials survive, and the unrelated change is persisted.
	assert.Equal(t, "access-new", reloadHostValue(t, dir, "gitlab.com", "token"))
	assert.Equal(t, "refresh-new", reloadHostValue(t, dir, "gitlab.com", "oauth2_refresh_token"))
	assert.Equal(t, future, reloadHostValue(t, dir, "gitlab.com", "oauth2_expiry_date"))
	assert.Equal(t, future, reloadHostValue(t, dir, "", "last_update_check_timestamp"))
}

// TestWrite_KeepsNewerInMemoryCredentials is the converse: when this process
// holds the fresher credentials (it just rotated), Write must not regress to
// older on-disk values.
func TestWrite_KeepsNewerInMemoryCredentials(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	seed := config.NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", past))
	require.NoError(t, seed.Write())

	fresh, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, fresh.Set("gitlab.com", "token", "access-new"))
	require.NoError(t, fresh.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, fresh.Write())

	assert.Equal(t, "access-new", reloadHostValue(t, dir, "gitlab.com", "token"))
	assert.Equal(t, future, reloadHostValue(t, dir, "gitlab.com", "oauth2_expiry_date"))
}

// TestWrite_KeyringHostAdoptsFresherExpiryWithoutPlaintextToken checks that for
// a keyring-backed host (token/refresh live in the keyring, absent from the
// file) the merge adopts only the fresher expiry and never writes a plaintext
// token into config.yml.
func TestWrite_KeyringHostAdoptsFresherExpiryWithoutPlaintextToken(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).Format(time.RFC3339)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	seed := config.NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "use_keyring", "true"))
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old")) // -> keyring
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", past))
	require.NoError(t, seed.Write())

	stale, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	winner, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, winner.Set("gitlab.com", "token", "access-new")) // -> keyring
	require.NoError(t, winner.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, winner.Write())

	require.NoError(t, stale.Set("", "last_update_check_timestamp", future))
	require.NoError(t, stale.Write())

	// Fresher expiry adopted; the keyring token is current; the file never gains a
	// plaintext token node (Get resolves it from the keyring, so assert the raw
	// document instead).
	assert.Equal(t, future, reloadHostValue(t, dir, "gitlab.com", "oauth2_expiry_date"))
	storedToken, err := keyring.Get("glab:gitlab.com:token", "")
	require.NoError(t, err)
	assert.Equal(t, "access-new", storedToken)

	hostEntry, ok := rawHostEntry(t, dir, "gitlab.com")
	require.True(t, ok)
	assert.NotContains(t, hostEntry, "token", "config.yml should not contain a plaintext token node for a keyring host")
}

// TestWrite_DoesNotReadoptClearedCredentials guards the logout / credential-clear
// path: when a process clears the OAuth fields in memory but keeps the host
// entry (as `glab auth logout` does), the merge must not treat the now-empty
// in-memory expiry as "stale" and re-adopt the still-present on-disk credentials,
// which would silently undo the clear.
func TestWrite_DoesNotReadoptClearedCredentials(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	// A file-backed host with credentials on disk.
	seed := config.NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "is_oauth2", "true"))
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_refresh_token", "refresh-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, seed.Write())

	// Clear the credential fields in memory (mirrors authutils.ClearAuthFields),
	// keeping the host entry, then persist.
	cfg, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	for _, key := range []string{"token", "oauth2_refresh_token", "oauth2_expiry_date"} {
		require.NoError(t, cfg.Set("gitlab.com", key, ""))
	}
	require.NoError(t, cfg.Write())

	// The credentials must be gone from disk, not re-adopted by the merge.
	hostEntry, ok := rawHostEntry(t, dir, "gitlab.com")
	require.True(t, ok, "the host entry itself should be preserved")
	for _, key := range []string{"token", "oauth2_refresh_token", "oauth2_expiry_date"} {
		assert.NotContains(t, hostEntry, key, "%s should stay cleared, not be re-adopted from disk", key)
	}
}

// TestWrite_AdoptsFresherCredentialsOverLegacyExpiry covers a stale reader whose
// in-memory expiry is still in the RFC822 format older glab versions wrote.
func TestWrite_AdoptsFresherCredentialsOverLegacyExpiry(t *testing.T) {
	keyring.MockInit()
	t.Cleanup(keyring.MockInit)

	dir := t.TempDir()
	past := time.Now().Add(-time.Hour).Format(time.RFC822)
	future := time.Now().Add(time.Hour).Format(time.RFC3339)

	seed := config.NewBlankConfigInDir(dir)
	require.NoError(t, seed.Set("gitlab.com", "token", "access-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_refresh_token", "refresh-old"))
	require.NoError(t, seed.Set("gitlab.com", "oauth2_expiry_date", past))
	require.NoError(t, seed.Write())

	stale, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)

	winner, err := config.ParseConfig(filepath.Join(dir, "config.yml"))
	require.NoError(t, err)
	require.NoError(t, winner.Set("gitlab.com", "token", "access-new"))
	require.NoError(t, winner.Set("gitlab.com", "oauth2_refresh_token", "refresh-new"))
	require.NoError(t, winner.Set("gitlab.com", "oauth2_expiry_date", future))
	require.NoError(t, winner.Write())

	require.NoError(t, stale.Set("", "last_update_check_timestamp", future))
	require.NoError(t, stale.Write())

	assert.Equal(t, "access-new", reloadHostValue(t, dir, "gitlab.com", "token"))
	assert.Equal(t, "refresh-new", reloadHostValue(t, dir, "gitlab.com", "oauth2_refresh_token"))
	assert.Equal(t, future, reloadHostValue(t, dir, "gitlab.com", "oauth2_expiry_date"))
}

func TestExpiryNewer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		disk string
		mem  string
		want bool
	}{
		{
			name: "disk newer",
			disk: "2026-01-02T10:00:00Z",
			mem:  "2026-01-02T09:00:00Z",
			want: true,
		},
		{
			name: "equal",
			disk: "2026-01-02T10:00:00Z",
			mem:  "2026-01-02T10:00:00Z",
			want: false,
		},
		{
			name: "memory newer",
			disk: "2026-01-02T09:00:00Z",
			mem:  "2026-01-02T10:00:00Z",
			want: false,
		},
		{
			name: "unparseable disk",
			disk: "not-a-date",
			mem:  "2026-01-02T09:00:00Z",
			want: false,
		},
		{
			name: "empty disk",
			disk: "",
			mem:  "2026-01-02T09:00:00Z",
			want: false,
		},
		{
			name: "empty memory",
			disk: "2026-01-02T10:00:00Z",
			mem:  "",
			want: false,
		},
		{
			name: "unparseable memory",
			disk: "2026-01-02T10:00:00Z",
			mem:  "not-a-date",
			want: false,
		},
		{
			name: "RFC3339 disk newer than RFC822 memory",
			disk: "2026-01-02T10:00:00Z",
			mem:  "02 Jan 26 09:00 UTC",
			want: true,
		},
		{
			name: "RFC822Z disk newer than RFC3339 memory",
			disk: "02 Jan 26 10:00 +0000",
			mem:  "2026-01-02T09:00:00Z",
			want: true,
		},
		{
			name: "RFC3339 disk older than RFC822 memory",
			disk: "2026-01-02T09:00:00Z",
			mem:  "02 Jan 26 10:00 UTC",
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, expiryNewer(tc.disk, tc.mem))
		})
	}
}
