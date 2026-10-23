package api

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

type receivedRequest struct {
	method string
	body   string
}

// redirectingServer answers every request under /moved with a redirect of the
// given status to the same path without that prefix, the way a proxy in front
// of an instance moves http to https, and records what reaches the target.
func redirectingServer(t *testing.T, status int) (*httptest.Server, func() []receivedRequest) {
	t.Helper()

	var (
		mu       sync.Mutex
		received []receivedRequest
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if target, moved := strings.CutPrefix(r.URL.RequestURI(), "/moved"); moved {
			http.Redirect(w, r, "http://"+r.Host+target, status)
			return
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("reading the request body: %v", err)
		}
		mu.Lock()
		received = append(received, receivedRequest{method: r.Method, body: string(body)})
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/releases") {
			_, _ = io.WriteString(w, `[]`)
			return
		}
		_, _ = io.WriteString(w, `{"tag_name":"v1.0.0","name":"v1.0.0"}`)
	}))
	t.Cleanup(server.Close)

	return server, func() []receivedRequest {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(received)
	}
}

func newRedirectTestClient(t *testing.T, baseURL string) *Client {
	t.Helper()

	client, err := NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
		},
		WithBaseURL(baseURL),
	)
	require.NoError(t, err)
	return client
}

func TestHTTPClient_RefusesRedirectThatTurnsCreateIntoGet(t *testing.T) {
	t.Parallel()

	server, received := redirectingServer(t, http.StatusMovedPermanently)
	client := newRedirectTestClient(t, server.URL+"/moved/api/v4")

	_, _, err := client.Lab().Releases.CreateRelease("group/project", &gitlab.CreateReleaseOptions{
		TagName: new("v1.0.0"),
	}, gitlab.WithContext(t.Context()))

	require.ErrorContains(t, err, "refusing to follow the 301 redirect from "+server.URL+"/moved/api/v4/projects/group%2Fproject/releases")
	require.ErrorContains(t, err, "would resend this POST request as a GET without its body")
	assert.Empty(t, received())
}

func TestHTTPClient_RefusesRedirectThatTurnsUpdateIntoGet(t *testing.T) {
	t.Parallel()

	server, received := redirectingServer(t, http.StatusFound)
	client := newRedirectTestClient(t, server.URL+"/moved/api/v4")

	// Without the policy the GET reaches the release, which decodes into the
	// expected type, so the update reports success without having been made.
	_, _, err := client.Lab().Releases.UpdateRelease("group/project", "v1.0.0", &gitlab.UpdateReleaseOptions{
		Name: new("renamed"),
	}, gitlab.WithContext(t.Context()))

	require.ErrorContains(t, err, "refusing to follow the 302 redirect")
	require.ErrorContains(t, err, "would resend this PUT request as a GET without its body")
	assert.Empty(t, received())
}

func TestHTTPClient_FollowsRedirectOfGet(t *testing.T) {
	t.Parallel()

	server, received := redirectingServer(t, http.StatusMovedPermanently)
	client := newRedirectTestClient(t, server.URL+"/moved/api/v4")

	release, _, err := client.Lab().Releases.GetRelease("group/project", "v1.0.0", gitlab.WithContext(t.Context()))

	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", release.TagName)
	assert.Equal(t, []receivedRequest{{method: http.MethodGet, body: ""}}, received())
}

func TestHTTPClient_FollowsRedirectThatKeepsTheMethod(t *testing.T) {
	t.Parallel()

	server, received := redirectingServer(t, http.StatusPermanentRedirect)
	client := newRedirectTestClient(t, server.URL+"/moved/api/v4")

	release, _, err := client.Lab().Releases.CreateRelease("group/project", &gitlab.CreateReleaseOptions{
		TagName: new("v1.0.0"),
	}, gitlab.WithContext(t.Context()))

	require.NoError(t, err)
	assert.Equal(t, "v1.0.0", release.TagName)
	got := received()
	require.Len(t, got, 1)
	assert.Equal(t, http.MethodPost, got[0].method)
	assert.JSONEq(t, `{"tag_name":"v1.0.0"}`, got[0].body)
}

func TestHTTPClient_StopsAfterTenRedirects(t *testing.T) {
	t.Parallel()

	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		http.Redirect(w, r, r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(server.Close)
	client := newRedirectTestClient(t, server.URL+"/api/v4")

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, server.URL+"/loop", http.NoBody)
	require.NoError(t, err)
	resp, err := client.HTTPClient().Do(req)
	if resp != nil {
		require.NoError(t, resp.Body.Close())
	}

	require.ErrorContains(t, err, "stopped after 10 redirects")
	assert.Equal(t, int32(10), hits.Load())
}

func TestHTTPClient_MethodChangingRedirectNamesTheAPIProtocol(t *testing.T) {
	const redirectsToHTTPS = "the server redirects http to https"

	sameHostHTTPS := func(serverHost string) string { return "https://" + serverHost + "/api/v4/projects" }

	// The refused location is never dialed, so the https targets need no server.
	tests := []struct {
		name      string
		autologin bool
		location  func(serverHost string) string
		wantHint  func(serverHost string) string
	}{
		{
			name:     "http to https on the same host",
			location: sameHostHTTPS,
			wantHint: func(serverHost string) string {
				return "; the server redirects http to https, so set api_protocol to https with `glab config set api_protocol https --host " + serverHost + "` or GLAB_API_PROTOCOL=https"
			},
		},
		{
			name:      "http to https on the same host with CI auto-login",
			autologin: true,
			location:  sameHostHTTPS,
			wantHint: func(string) string {
				return "; the server redirects http to https, and CI auto-login reads api_protocol from CI_SERVER_PROTOCOL, so give the instance an https external_url, or set CI_SERVER_PROTOCOL to https in the job"
			},
		},
		{
			name: "http to https on another host and the same port",
			location: func(serverHost string) string {
				_, port, _ := net.SplitHostPort(serverHost)
				return "https://" + net.JoinHostPort("gitlab.example.com", port) + "/api/v4/projects"
			},
		},
		{
			name: "http to https on the same host and another port",
			location: func(serverHost string) string {
				hostname, _, _ := net.SplitHostPort(serverHost)
				return "https://" + hostname + "/api/v4/projects"
			},
		},
		{
			name:     "same scheme and host",
			location: func(serverHost string) string { return "http://" + serverHost + "/users/sign_in" },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			autologin := ""
			if tc.autologin {
				autologin = "true"
			}
			t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", autologin)
			t.Setenv("GITLAB_CI", autologin)

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, tc.location(r.Host), http.StatusMovedPermanently)
			}))
			t.Cleanup(server.Close)
			client := newRedirectTestClient(t, server.URL+"/api/v4")

			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/v4/projects", strings.NewReader(`{}`))
			require.NoError(t, err)
			resp, err := client.HTTPClient().Do(req)
			if resp != nil {
				require.NoError(t, resp.Body.Close())
			}

			require.ErrorContains(t, err, "would resend this POST request as a GET")
			if tc.wantHint == nil {
				assert.NotContains(t, err.Error(), redirectsToHTTPS)
				return
			}
			assert.True(t, strings.HasSuffix(err.Error(), tc.wantHint(server.Listener.Addr().String())), "the error does not end with the hint: %v", err)
		})
	}
}

// A host without a port reaches 443 over https, so the hint is given whether
// the redirect leaves the port out or writes it, as Omnibus GitLab does with
// https://<host>:443.
func TestRefuseMethodChangingRedirect_NamesTheAPIProtocolForTheDefaultPort(t *testing.T) {
	t.Setenv("GLAB_ENABLE_CI_AUTOLOGIN", "")

	tests := []struct {
		name     string
		location string
	}{
		{name: "port left out", location: "https://gitlab.example.com/api/v4/projects"},
		{name: "port 443 written out", location: "https://gitlab.example.com:443/api/v4/projects"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "http://gitlab.example.com/api/v4/projects", http.NoBody)
			require.NoError(t, err)
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, tc.location, http.NoBody)
			require.NoError(t, err)
			req.Response = &http.Response{StatusCode: http.StatusMovedPermanently}

			err = refuseMethodChangingRedirect(req, []*http.Request{original})

			require.ErrorContains(t, err, "`glab config set api_protocol https --host gitlab.example.com` or GLAB_API_PROTOCOL=https")
		})
	}
}
