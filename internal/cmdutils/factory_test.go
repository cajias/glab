//go:build !integration

package cmdutils

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/config"
)

func TestFactory_ResolveHostNameFromConfig(t *testing.T) {
	// GIVEN
	cfg := config.NewFromString(heredoc.Doc(`
		host: gitlab.example.com
	`))

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.example.com", f.defaultHostname)
}

func TestFactory_ResolveHostNameFromEnv(t *testing.T) {
	// GIVEN
	t.Setenv("GITLAB_HOST", "gitlab.example.com")
	cfg := config.NewFromString(heredoc.Doc(`
		host: another.gitlab.example.com
	`))

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.example.com", f.defaultHostname)
}

func TestFactory_ResolveToGitLabComByDefault(t *testing.T) {
	// GIVEN
	cfg := config.NewBlankConfig()

	// WHEN
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// THEN
	assert.Equal(t, "gitlab.com", f.defaultHostname)
}

func TestFactory_GitLabClientUsesCorrectHost(t *testing.T) {
	// GIVEN
	tests := []struct {
		name                         string
		cfg                          config.Config
		env                          map[string]string
		expectedGitLabClientHostname *url.URL
	}{
		{
			name:                         "default",
			cfg:                          config.NewBlankConfig(),
			expectedGitLabClientHostname: mustURL(t, "https://gitlab.com/api/v4/"),
		},
		{
			name: "host from config",
			cfg: config.NewFromString(heredoc.Doc(`
				host: gitlab.example.com
			`)),
			expectedGitLabClientHostname: mustURL(t, "https://gitlab.example.com/api/v4/"),
		},
		{
			name: "host from env",
			env:  map[string]string{"GITLAB_HOST": "gitlab.example.com"},
			cfg: config.NewFromString(heredoc.Doc(`
				host: another.gitlab.example.com
			`)),
			expectedGitLabClientHostname: mustURL(t, "https://gitlab.example.com/api/v4/"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// GIVEN
			if tt.env != nil {
				for k, v := range tt.env {
					t.Setenv(k, v)
				}
			}

			// WHEN
			f := NewFactory(nil, false, tt.cfg, api.BuildInfo{})
			c, err := f.GitLabClient()
			require.NoError(t, err)

			// THEN
			assert.Equal(t, tt.expectedGitLabClientHostname, c.BaseURL())
		})
	}
}

func TestFactory_ApiClientsReuseConnection(t *testing.T) {
	// GIVEN
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.Config.ConnState = func(_ net.Conn, state http.ConnState) {
		if state == http.StateNew {
			conns.Add(1)
		}
	}
	srv.StartTLS()
	defer srv.Close()

	host := strings.TrimPrefix(srv.URL, "https://")
	cfg := config.NewFromString(fmt.Sprintf(`hosts:
  %q:
    api_host: %q
    token: glpat-test
    skip_tls_verify: true
`, host, host))
	f := NewFactory(nil, false, cfg, api.BuildInfo{})

	// WHEN
	for range 2 {
		c, err := f.ApiClient(host)
		require.NoError(t, err)
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
		require.NoError(t, err)
		resp, err := c.HTTPClient().Do(req)
		require.NoError(t, err)
		_, err = io.Copy(io.Discard, resp.Body)
		require.NoError(t, err)
		require.NoError(t, resp.Body.Close())
	}

	// THEN
	assert.Equal(t, int64(1), conns.Load())
}

func mustURL(t *testing.T, s string) *url.URL {
	t.Helper()

	u, err := url.Parse(s)
	require.NoError(t, err)

	return u
}
