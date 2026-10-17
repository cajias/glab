//go:build !integration

package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestNewClientFromConfigProxy(t *testing.T) {
	t.Run("configured proxy", func(t *testing.T) {
		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("gitlab.example.com", "token", "glpat-test"))
		require.NoError(t, cfg.Set("gitlab.example.com", "proxy", "https://proxy.local:8443"))

		client, err := NewClientFromConfig("gitlab.example.com", cfg, false, "glab-test")
		require.NoError(t, err)
		transport, ok := client.HTTPClient().Transport.(*http.Transport)
		require.True(t, ok)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://gitlab.example.com/api/v4", http.NoBody)
		require.NoError(t, err)

		proxyURL, err := transport.Proxy(req)
		require.NoError(t, err)
		require.NotNil(t, proxyURL)
		assert.Equal(t, "https://proxy.local:8443", proxyURL.String())
	})

	t.Run("missing host uses the environment", func(t *testing.T) {
		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("missing.example.com", "token", "glpat-test"))

		client, err := NewClientFromConfig("missing.example.com", cfg, false, "glab-test")
		require.NoError(t, err)
		assert.Nil(t, client.proxyURL)
	})

	t.Run("invalid configured proxy", func(t *testing.T) {
		cfg := config.NewBlankConfig()
		require.NoError(t, cfg.Set("bad.example.com", "token", "glpat-test"))
		require.NoError(t, cfg.Set("bad.example.com", "proxy", "://bad-url"))

		_, err := NewClientFromConfig("bad.example.com", cfg, false, "glab-test")
		require.ErrorContains(t, err, "failed to resolve proxy")
	})
}
