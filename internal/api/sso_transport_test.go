//go:build !integration

package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ssoTestOpts struct {
	allowedDomains map[string]struct{}
}

// setupSSOTestClient creates a Client with a temporary cookie file and initializes
// its HTTP client. Returns the initialized client for making requests.
func setupSSOTestClient(t *testing.T, baseURL string, opts *ssoTestOpts) *Client {
	t.Helper()
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf("localhost\tFALSE\t/\tFALSE\t%d\tsession\tvalue1\n", futureTimestamp)
	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err)

	client := &Client{
		baseURL:    baseURL,
		cookieFile: cookieFile,
	}
	if opts != nil && opts.allowedDomains != nil {
		client.ssoAllowedDomains = opts.allowedDomains
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err)
	return client
}

func TestSSOTransport_NoRedirect(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Without cookie file, transport should pass through
	client := &Client{baseURL: server.URL}
	err := client.initializeHTTPClient()
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", bytes.NewBufferString(`{"name": "test"}`))
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSSOTransport_WithSSOFlow(t *testing.T) {
	t.Parallel()
	var gitlabRequestCount, idpRequestCount int32

	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		assert.Equal(t, http.MethodGet, r.Method, "IdP should only receive GET requests")
		w.WriteHeader(http.StatusOK)
	}))
	defer idpServer.Close()

	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&gitlabRequestCount, 1)
		if count == 1 {
			http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer gitlabServer.Close()

	client := setupSSOTestClient(t, gitlabServer.URL, &ssoTestOpts{
		allowedDomains: map[string]struct{}{"127.0.0.1": {}},
	})

	_, ok := client.httpClient.Transport.(*ssoTransport)
	require.True(t, ok, "expected ssoTransport when cookie file is configured")

	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(`{"name": "test"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount))
	assert.Equal(t, int32(2), atomic.LoadInt32(&gitlabRequestCount))
}

func TestSSOTransport_SSOFlowFails(t *testing.T) {
	t.Parallel()
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer idpServer.Close()

	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
	}))
	defer gitlabServer.Close()

	client := setupSSOTestClient(t, gitlabServer.URL, &ssoTestOpts{
		allowedDomains: map[string]struct{}{"127.0.0.1": {}},
	})

	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(`{}`))
	_, err := client.httpClient.Do(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSO authentication failed")
}

func TestSSOTransport_SSOFlowConnectionFails(t *testing.T) {
	t.Parallel()
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/oauth/authorize", http.StatusFound)
	}))
	defer gitlabServer.Close()

	client := setupSSOTestClient(t, gitlabServer.URL, &ssoTestOpts{
		allowedDomains: map[string]struct{}{"127.0.0.1": {}},
	})

	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(`{}`))
	_, err := client.httpClient.Do(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SSO flow request failed")
}

func TestSSOTransport_SameHostRedirect_PreservesMethod(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var receivedMethod, receivedBody string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		mu.Lock()
		receivedMethod = r.Method
		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			receivedBody = string(b)
		}
		mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer server.Close()

	client := setupSSOTestClient(t, server.URL, nil)

	body := `{"body": "Test note"}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/redirect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	mu.Lock()
	assert.Equal(t, http.MethodPost, receivedMethod)
	assert.Equal(t, body, receivedBody)
	mu.Unlock()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

func TestSSOTransport_SameHostRedirect_MultipleRedirects(t *testing.T) {
	t.Parallel()
	var requestCount int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		switch r.URL.Path {
		case "/redirect1":
			http.Redirect(w, r, "/redirect2", http.StatusFound)
		case "/redirect2":
			http.Redirect(w, r, "/redirect3", http.StatusMovedPermanently)
		case "/redirect3":
			http.Redirect(w, r, "/final", http.StatusSeeOther)
		default:
			assert.Equal(t, http.MethodPost, r.Method)
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	client := setupSSOTestClient(t, server.URL, nil)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/redirect1", strings.NewReader(`{}`))
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(4), atomic.LoadInt32(&requestCount))
}

func TestSSOTransport_SameHostRedirect_MaxRedirectsExceeded(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()

	client := setupSSOTestClient(t, server.URL, nil)

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/start", strings.NewReader(`{}`))
	_, err := client.httpClient.Do(req)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "stopped after") || strings.Contains(err.Error(), "redirects"))
}

func TestSSOTransport_GETSameHostRedirect(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var receivedMethod string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		mu.Lock()
		receivedMethod = r.Method
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := setupSSOTestClient(t, server.URL, nil)

	req, _ := http.NewRequest(http.MethodGet, server.URL+"/redirect", nil)
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	mu.Lock()
	assert.Equal(t, http.MethodGet, receivedMethod)
	mu.Unlock()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSSOTransport_SameHostToSSORedirect(t *testing.T) {
	t.Parallel()
	var gitlabRequestCount, idpRequestCount int32

	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		assert.Equal(t, http.MethodGet, r.Method)
		w.WriteHeader(http.StatusOK)
	}))
	defer idpServer.Close()

	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&gitlabRequestCount, 1)
		switch count {
		case 1:
			http.Redirect(w, r, "/step2", http.StatusFound)
		case 2:
			http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
		default:
			w.WriteHeader(http.StatusCreated)
		}
	}))
	defer gitlabServer.Close()

	client := setupSSOTestClient(t, gitlabServer.URL, &ssoTestOpts{
		allowedDomains: map[string]struct{}{"127.0.0.1": {}},
	})

	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/start", strings.NewReader(`{}`))
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount))
	assert.Equal(t, int32(3), atomic.LoadInt32(&gitlabRequestCount))
}

// TestRegression_MergeRequestNotesEndpoint verifies that POST to merge_requests/notes
// does not return a GET response (array) after a same-host 302 redirect.
func TestRegression_MergeRequestNotesEndpoint(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/projects/456/merge_requests/332/notes" && r.URL.RawQuery == "" {
			http.Redirect(w, r, "/api/v4/projects/456/merge_requests/332/notes?internal=1", http.StatusFound)
			return
		}
		if r.Method == http.MethodGet {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id": 1, "body": "existing note"}]`))
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 12345, "body": "Test comment"}`))
			return
		}
		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	client := setupSSOTestClient(t, server.URL, nil)

	req, _ := http.NewRequest(http.MethodPost,
		server.URL+"/api/v4/projects/456/merge_requests/332/notes",
		strings.NewReader(`{"body": "Test comment"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode, "regression: POST may have been converted to GET")
	respBody, _ := io.ReadAll(resp.Body)
	assert.False(t, bytes.HasPrefix(respBody, []byte("[")), "regression: received array response")
}

func TestSSOTransport_ConsentRequired(t *testing.T) {
	t.Parallel()
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer idpServer.Close()

	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
	}))
	defer gitlabServer.Close()

	// No allowedDomains → should fail with consent error
	client := setupSSOTestClient(t, gitlabServer.URL, nil)

	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(`{}`))
	_, err := client.httpClient.Do(req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "requires consent")
}

// TestSSOTransport_SameHostRedirect_IncludesCookiesFromJar is a regression test verifying
// that cookies from the jar are included in redirected requests (RoundTrip bypasses the jar).
func TestSSOTransport_SameHostRedirect_IncludesCookiesFromJar(t *testing.T) {
	t.Parallel()
	var redirectedCookies []*http.Cookie

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		redirectedCookies = r.Cookies()
		hasCookie := false
		for _, c := range r.Cookies() {
			if c.Name == "session" {
				hasCookie = true
				break
			}
		}
		if !hasCookie {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	// Use 127.0.0.1 cookies (httptest binds to 127.0.0.1)
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf("127.0.0.1\tFALSE\t/\tFALSE\t%d\tsession\tsecret-session-value\n", futureTimestamp)
	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err)

	client := &Client{baseURL: server.URL, cookieFile: cookieFile}
	err = client.initializeHTTPClient()
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodPut, server.URL+"/redirect", strings.NewReader(`{}`))
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.NotEmpty(t, redirectedCookies, "redirected request should include cookies from jar")
	var foundSessionCookie bool
	for _, c := range redirectedCookies {
		if c.Name == "session" && c.Value == "secret-session-value" {
			foundSessionCookie = true
		}
	}
	assert.True(t, foundSessionCookie, "session cookie missing from redirected request")
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// TestSSOTransport_StoresCookiesFromRedirectResponse verifies Set-Cookie headers from
// 302 redirect responses are stored in the cookie jar (RoundTrip doesn't do this).
func TestSSOTransport_StoresCookiesFromRedirectResponse(t *testing.T) {
	t.Parallel()
	var gitlabRequestCount, idpRequestCount int32

	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer idpServer.Close()

	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&gitlabRequestCount, 1)
		if count == 1 {
			http.SetCookie(w, &http.Cookie{Name: "_gitlab_session", Value: "session-token-abc123", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "oauth_state", Value: "state-xyz789", Path: "/"})
			http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	defer gitlabServer.Close()

	// Use 127.0.0.1 cookies
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf("127.0.0.1\tFALSE\t/\tFALSE\t%d\texisting_cookie\texisting_value\n", futureTimestamp)
	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err)

	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{"127.0.0.1": {}},
	}
	err = client.initializeHTTPClient()
	require.NoError(t, err)

	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(`{}`))
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount))

	transport := client.httpClient.Transport.(*ssoTransport)
	gitlabURL, _ := url.Parse(gitlabServer.URL)
	jarCookies := transport.ssoClient.Jar.Cookies(gitlabURL)

	var foundSession, foundOAuthState bool
	for _, c := range jarCookies {
		if c.Name == "_gitlab_session" && c.Value == "session-token-abc123" {
			foundSession = true
		}
		if c.Name == "oauth_state" && c.Value == "state-xyz789" {
			foundOAuthState = true
		}
	}
	assert.True(t, foundSession, "_gitlab_session not stored in jar")
	assert.True(t, foundOAuthState, "oauth_state not stored in jar")
}

func TestSSOTransport_HTTPSEnforcement(t *testing.T) {
	t.Parallel()
	jar, _ := cookiejar.New(nil)
	ssoClient := &http.Client{Jar: jar, Timeout: ssoTimeout}

	transport := &ssoTransport{
		rt:        http.DefaultTransport,
		ssoClient: ssoClient,
	}

	t.Run("rejects HTTP to non-localhost", func(t *testing.T) {
		t.Parallel()
		req, _ := http.NewRequest(http.MethodPost, "http://gitlab.example.com/api/v4/projects", bytes.NewReader([]byte("{}")))
		resp := &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://idp.example.com/saml/auth"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}
		_, err := transport.handleSSORedirect(req, resp, "http://idp.example.com/saml/auth", []byte("{}"))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "HTTPS required")
	})

	t.Run("allows HTTPS", func(t *testing.T) {
		t.Parallel()
		idpServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer idpServer.Close()

		req, _ := http.NewRequest(http.MethodPost, "https://gitlab.example.com/api/v4/projects", bytes.NewReader([]byte("{}")))
		resp := &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{idpServer.URL + "/saml/auth"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}
		_, err := transport.handleSSORedirect(req, resp, idpServer.URL+"/saml/auth", []byte("{}"))
		if err != nil {
			assert.NotContains(t, err.Error(), "SSO redirect rejected")
		}
	})

	t.Run("allows HTTP to localhost", func(t *testing.T) {
		t.Parallel()
		localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer localServer.Close()

		localTransport := &ssoTransport{
			rt:        http.DefaultTransport,
			ssoClient: &http.Client{Jar: jar, Timeout: ssoTimeout},
		}
		req, _ := http.NewRequest(http.MethodPost, "https://gitlab.example.com/api/v4/projects", bytes.NewReader([]byte("{}")))
		resp := &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{localServer.URL + "/callback"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}
		_, err := localTransport.handleSSORedirect(req, resp, localServer.URL+"/callback", []byte("{}"))
		if err != nil {
			assert.NotContains(t, err.Error(), "SSO redirect rejected")
		}
	})
}
