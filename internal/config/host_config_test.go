//go:build !integration

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestHostUsesKeyring(t *testing.T) {
	t.Setenv("GLAB_USE_KEYRING", "")
	t.Setenv("USE_KEYRING", "")

	t.Run("the host entry sets use_keyring", func(t *testing.T) {
		cfg := NewFromString("---\nhosts:\n  gitlab.example.com:\n    use_keyring: \"true\"\n")

		assert.True(t, HostUsesKeyring(cfg, "gitlab.example.com"))
		assert.False(t, HostUsesKeyring(cfg, "other.example.com"))
	})

	t.Run("a top-level use_keyring does not count", func(t *testing.T) {
		cfg := NewFromString("---\nuse_keyring: \"true\"\nhosts:\n  gitlab.example.com:\n    user: someone\n")

		assert.False(t, HostUsesKeyring(cfg, "gitlab.example.com"))
	})

	t.Run("GLAB_USE_KEYRING does not count", func(t *testing.T) {
		t.Setenv("GLAB_USE_KEYRING", "true")
		cfg := NewFromString("---\nhosts:\n  gitlab.example.com:\n    user: someone\n")

		assert.False(t, HostUsesKeyring(cfg, "gitlab.example.com"))
	})
}
