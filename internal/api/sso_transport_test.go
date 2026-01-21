//go:build !integration

package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSSOTransport_NoRedirect(t *testing.T) {
	t.Parallel()
	// Setup test server that returns success immediately
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Create a simple client without cookie file (transport should pass through)
	client := &Client{
		baseURL: server.URL,
	}
	err := client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", bytes.NewBufferString(`{"name": "test"}`))
	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSSOTransport_WithSSOFlow(t *testing.T) {
	t.Parallel()
	// Track request counts
	var gitlabRequestCount int32
	var idpRequestCount int32

	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		// IdP should only receive GET requests
		assert.Equal(t, http.MethodGet, r.Method, "IdP should only receive GET requests")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SSO complete"))
	}))
	defer idpServer.Close()

	// Create GitLab server
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&gitlabRequestCount, 1)
		if count == 1 {
			// First request: redirect to IdP
			http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
			return
		}
		// Second request: success (after SSO flow)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	// Create cookie file with cookies for both servers
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{"127.0.0.1": {}},
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Verify that ssoTransport is being used
	_, ok := client.httpClient.Transport.(*ssoTransport)
	require.True(t, ok, "expected ssoTransport to be used when cookie file is configured")

	// Make a POST request directly through the HTTP client (simulates gitlab.Client behavior)
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify we got the success response
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Verify IdP received a GET request (SSO flow)
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount), "expected 1 IdP request")

	// Verify GitLab received 2 requests (initial + retry)
	assert.Equal(t, int32(2), atomic.LoadInt32(&gitlabRequestCount), "expected 2 GitLab requests")
}

func TestSSOTransport_PreservesRequestBody(t *testing.T) {
	t.Parallel()
	var receivedBodies []string

	// Create GitLab server that captures request bodies
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBodies = append(receivedBodies, string(bodyBytes))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    gitlabServer.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request with a body
	expectedBody := `{"important": "data"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(expectedBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify the body was received correctly
	require.Len(t, receivedBodies, 1, "expected 1 request")
	assert.Equal(t, expectedBody, receivedBodies[0])
}

func TestSSOTransport_HeadersPreserved(t *testing.T) {
	t.Parallel()
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", nil)
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token123")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify custom headers were preserved
	assert.Equal(t, "custom-value", receivedHeaders.Get("X-Custom-Header"), "X-Custom-Header not preserved")
	assert.Equal(t, "application/json", receivedHeaders.Get("Content-Type"), "Content-Type not preserved")
	assert.Equal(t, "Bearer token123", receivedHeaders.Get("Authorization"), "Authorization not preserved")
}

func TestSSOTransport_GETRequestNotIntercepted(t *testing.T) {
	t.Parallel()
	// Track request counts
	var requestCount int32

	// Create server that would trigger SSO redirect for POST but not GET
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requestCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a GET request
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v4/projects", nil)

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify only one request was made (no retry)
	assert.Equal(t, int32(1), atomic.LoadInt32(&requestCount), "expected 1 request for GET")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSSOTransport_NotSetWithoutCookieFile(t *testing.T) {
	t.Parallel()
	client := &Client{
		baseURL: "https://example.com/api/v4",
		// No cookie file configured
	}

	err := client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// ssoTransport should NOT be used when no cookie file is configured
	_, ok := client.httpClient.Transport.(*ssoTransport)
	assert.False(t, ok, "ssoTransport should not be used when cookie file is not configured")
}

func TestSSOTransport_SSOFlowFails(t *testing.T) {
	t.Parallel()
	// Create IdP server that returns an error
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("IdP error"))
	}))
	defer idpServer.Close()

	// Create GitLab server that redirects to IdP
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Always redirect to IdP
		http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{"127.0.0.1": {}},
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request - IdP returns 500, SSO flow should fail with clear error
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	_, err = client.httpClient.Do(req)
	require.Error(t, err, "expected error when IdP returns 500")

	// Verify the error message indicates SSO authentication failure
	assert.Contains(t, err.Error(), "SSO authentication failed")
	assert.Contains(t, err.Error(), "500")
}

func TestSSOTransport_SSOFlowConnectionFails(t *testing.T) {
	t.Parallel()
	// Create GitLab server that redirects to a non-existent IdP
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Redirect to non-existent server
		http.Redirect(w, r, "http://127.0.0.1:1/oauth/authorize", http.StatusFound)
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{"127.0.0.1": {}},
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request - SSO flow should fail because IdP is unreachable
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	_, err = client.httpClient.Do(req)
	require.Error(t, err, "expected error when SSO flow fails")

	// Verify the error message contains context about SSO flow failure
	assert.Contains(t, err.Error(), "SSO flow request failed")
}

func TestSSOTransport_SameHostRedirect_PreservesMethod(t *testing.T) {
	t.Parallel()
	// Track the method received at the final endpoint
	var receivedMethod string
	var receivedBody string
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path == "/redirect" {
			// Redirect to the same host (relative URL)
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		// Final endpoint - capture method and body
		receivedMethod = r.Method
		if r.Body != nil {
			bodyBytes, _ := io.ReadAll(r.Body)
			receivedBody = string(bodyBytes)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123, "body": "Note created"}`))
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that will be redirected
	body := `{"body": "Test note"}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/redirect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify the final endpoint received POST (not GET)
	assert.Equal(t, http.MethodPost, receivedMethod, "expected method POST at final endpoint")

	// Verify the body was preserved
	assert.Equal(t, body, receivedBody)

	// Verify we got the success response
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Verify 2 requests were made (initial + follow redirect)
	assert.Equal(t, 2, requestCount, "expected 2 requests")
}

func TestSSOTransport_SameHostRedirect_MultipleRedirects(t *testing.T) {
	t.Parallel()
	// Track requests
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		switch r.URL.Path {
		case "/redirect1":
			http.Redirect(w, r, "/redirect2", http.StatusFound)
		case "/redirect2":
			http.Redirect(w, r, "/redirect3", http.StatusMovedPermanently)
		case "/redirect3":
			http.Redirect(w, r, "/final", http.StatusSeeOther)
		default:
			// Verify POST method is preserved
			assert.Equal(t, http.MethodPost, r.Method, "expected POST at %s", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"status": "ok"}`))
		}
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that will be redirected multiple times
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/redirect1", strings.NewReader(`{"test": "data"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// 4 requests: initial + 3 redirects
	assert.Equal(t, 4, requestCount, "expected 4 requests")
}

func TestSSOTransport_SameHostRedirect_MaxRedirectsExceeded(t *testing.T) {
	t.Parallel()
	// Create a server that always redirects
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that will redirect forever
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/start", strings.NewReader(`{"test": "data"}`))
	req.Header.Set("Content-Type", "application/json")

	_, err = client.httpClient.Do(req)
	require.Error(t, err, "expected error for too many redirects")

	assert.True(t, strings.Contains(err.Error(), "stopped after") || strings.Contains(err.Error(), "redirects"),
		"expected error about max redirects, got: %v", err)
}

func TestSSOTransport_GETNotAffected(t *testing.T) {
	t.Parallel()
	// Verify that GET requests still work normally with redirects
	var receivedMethod string
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`[{"id": 1}, {"id": 2}]`))
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a GET request that will be redirected
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/redirect", nil)

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// GET requests should work normally
	assert.Equal(t, http.MethodGet, receivedMethod, "expected GET at final endpoint")

	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestSSOTransport_SameHostToSSORedirect(t *testing.T) {
	t.Parallel()
	// Test the scenario where a same-host redirect leads to an SSO redirect
	var gitlabRequestCount, idpRequestCount int32

	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		// IdP should receive GET request
		assert.Equal(t, http.MethodGet, r.Method, "IdP should receive GET request")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SSO complete"))
	}))
	defer idpServer.Close()

	// Create GitLab server that first redirects locally, then to IdP
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&gitlabRequestCount, 1)
		switch count {
		case 1:
			// First request: redirect to another path on same host
			http.Redirect(w, r, "/step2", http.StatusFound)
		case 2:
			// Second request: redirect to IdP
			http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
		default:
			// Third request (after SSO): success
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 456}`))
		}
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{"127.0.0.1": {}},
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that goes through same-host redirect then SSO redirect
	body := `{"body": "Test note"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify we got the success response
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Verify IdP received exactly one GET request
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount), "expected 1 IdP request")

	// Verify GitLab received 3 requests: initial, same-host redirect, and retry after SSO
	assert.Equal(t, int32(3), atomic.LoadInt32(&gitlabRequestCount), "expected 3 GitLab requests")
}

func TestSSOTransport_307Redirect_NotIntercepted(t *testing.T) {
	t.Parallel()
	// 307 Temporary Redirect already preserves method per HTTP spec,
	// so we should NOT intercept it - let the standard client handle it.
	requestCount := 0

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		// Return a 307 redirect - this should be handled by the standard client
		if r.URL.Path == "/start" {
			w.Header().Set("Location", "/final")
			w.WriteHeader(http.StatusTemporaryRedirect) // 307
			return
		}
		// The redirect should preserve the method
		assert.Equal(t, http.MethodPost, r.Method, "expected POST at %s", r.URL.Path)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that gets a 307 redirect
	body := `{"body": "Test note"}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify we got the success response
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}

// TestRegression_MergeRequestNotesEndpoint_PreservesPostMethod is a regression test
// for issue #14: POST to merge_requests/notes was returning GET response (array)
// instead of creating a note. The root cause was that same-host 302 redirects
// were being handled by Go's default HTTP client which converts POST to GET.
func TestRegression_MergeRequestNotesEndpoint_PreservesPostMethod(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate GitLab redirecting the notes endpoint (common pattern)
		if r.URL.Path == "/api/v4/projects/456/merge_requests/332/notes" && r.URL.RawQuery == "" {
			http.Redirect(w, r, "/api/v4/projects/456/merge_requests/332/notes?internal=1", http.StatusFound)
			return
		}

		// After redirect, verify we still have POST method
		if r.Method == http.MethodGet {
			// This is the bug behavior - return array (GET response)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`[{"id": 1, "body": "existing note"}]`))
			return
		}

		// Correct behavior - POST creates a new note
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id": 12345, "body": "Test comment", "author": {"name": "user"}}`))
			return
		}

		w.WriteHeader(http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// POST to notes endpoint - this is the exact scenario from issue #14
	body := `{"body": "Test comment"}`
	req, _ := http.NewRequest(http.MethodPost,
		server.URL+"/api/v4/projects/456/merge_requests/332/notes",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// CRITICAL: Verify we get 201 Created (not 200 OK with array)
	assert.Equal(t, http.StatusCreated, resp.StatusCode,
		"regression: POST may have been converted to GET")

	// Verify response is a single object, not an array
	respBody, _ := io.ReadAll(resp.Body)
	assert.False(t, bytes.HasPrefix(respBody, []byte("[")),
		"regression: received array response, POST was likely converted to GET")

	// Verify we got the expected note response
	assert.Contains(t, string(respBody), `"id": 12345`,
		"regression: unexpected response body")
}

func TestSSOTransport_ConsentRequired(t *testing.T) {
	t.Parallel()
	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SSO complete"))
	}))
	defer idpServer.Close()

	// Create GitLab server that redirects to IdP
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	// Client without ssoPrompt - should fail with consent required error
	client := &Client{
		baseURL:    gitlabServer.URL,
		cookieFile: cookieFile,
		// No ssoPrompt set
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that triggers SSO redirect
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	_, err = client.httpClient.Do(req)
	require.Error(t, err, "expected error when no consent callback is set")

	assert.Contains(t, err.Error(), "requires consent")
}

func TestSSOTransport_ConsentGranted(t *testing.T) {
	t.Parallel()
	var idpRequestCount int32
	var gitlabRequestCount int32

	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SSO complete"))
	}))
	defer idpServer.Close()

	// Create GitLab server
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&gitlabRequestCount, 1)
		if count == 1 {
			// First request: redirect to IdP
			http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
			return
		}
		// Retry request: success
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	// Client with ssoPrompt that approves
	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{"127.0.0.1": {}},
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that triggers SSO redirect
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify IdP was reached (consent was granted)
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount), "expected 1 IdP request")
}

// TestSSOTransport_SameHostRedirect_IncludesCookiesFromJar tests that when following
// same-host redirects, cookies from the cookie jar are included in the redirected request.
// This is a regression test for a bug where handleSameHostRedirect() called t.rt.RoundTrip()
// directly, which bypasses the cookie jar. PUT/POST requests would fail with 401 because
// the authentication cookies were not included in the redirected request.
func TestSSOTransport_SameHostRedirect_IncludesCookiesFromJar(t *testing.T) {
	t.Parallel()
	// Track cookies received at each endpoint
	var initialCookies, redirectedCookies []*http.Cookie

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect" {
			// Capture cookies from initial request
			initialCookies = r.Cookies()
			// Redirect to final endpoint (same host)
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		// Final endpoint - capture cookies from redirected request
		redirectedCookies = r.Cookies()

		// If no session cookie, return 401 (simulating authentication failure)
		hasCookie := false
		for _, c := range r.Cookies() {
			if c.Name == "session" {
				hasCookie = true
				break
			}
		}
		if !hasCookie {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error": "Unauthorized - missing session cookie"}`))
			return
		}

		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer server.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	// Create cookie file with session cookie for 127.0.0.1 (httptest server address)
	// Using 127.0.0.1 instead of localhost because httptest servers bind to 127.0.0.1
	// and Go's cookiejar matches by exact hostname
	cookieContent := fmt.Sprintf(`127.0.0.1	FALSE	/	FALSE	%d	session	secret-session-value
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a PUT request (mutating method) that will be redirected
	body := `{"body": "Test update"}`
	req, _ := http.NewRequest(http.MethodPut, server.URL+"/redirect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify initial request had cookies
	assert.NotEmpty(t, initialCookies, "initial request should have cookies from jar")

	// CRITICAL: Verify redirected request ALSO has cookies from the jar
	// This is the bug: handleSameHostRedirect() uses t.rt.RoundTrip() which
	// doesn't consult the cookie jar, so redirected requests lose cookies.
	assert.NotEmpty(t, redirectedCookies, "redirected request should include cookies from jar")

	// Find the session cookie in redirected request
	var foundSessionCookie bool
	for _, c := range redirectedCookies {
		if c.Name == "session" && c.Value == "secret-session-value" {
			foundSessionCookie = true
			break
		}
	}
	assert.True(t, foundSessionCookie,
		"redirected request should include session cookie from jar; got cookies: %v", redirectedCookies)

	// CRITICAL: Verify we get 201 Created, not 401 Unauthorized
	// If the bug is present, we'll get 401 because the cookie is missing
	if resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		assert.Equal(t, http.StatusCreated, resp.StatusCode,
			"cookies may not have been included in redirect; body: %s", string(bodyBytes))
	}
}

// TestSSOTransport_StoresCookiesFromRedirectResponse verifies that Set-Cookie headers
// from a 302 redirect response are stored in the cookie jar. This is critical because
// RoundTrip() doesn't automatically store cookies - only http.Client.Do() does that.
// Without this fix, GitLab's session/OAuth state cookies would be lost during SSO flows.
func TestSSOTransport_StoresCookiesFromRedirectResponse(t *testing.T) {
	t.Parallel()
	var gitlabRequestCount int32
	var idpRequestCount int32

	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SSO complete"))
	}))
	defer idpServer.Close()

	// Create GitLab server that sets cookies in the redirect response
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&gitlabRequestCount, 1)
		if count == 1 {
			// First request: redirect to IdP with Set-Cookie header
			// These cookies simulate OAuth state cookies that GitLab sets
			http.SetCookie(w, &http.Cookie{
				Name:  "_gitlab_session",
				Value: "session-token-abc123",
				Path:  "/",
			})
			http.SetCookie(w, &http.Cookie{
				Name:  "oauth_state",
				Value: "state-xyz789",
				Path:  "/",
			})
			http.Redirect(w, r, idpServer.URL+"/oauth/authorize", http.StatusFound)
			return
		}
		// Second request (retry after SSO): success
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id": 123}`))
	}))
	defer gitlabServer.Close()

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`127.0.0.1	FALSE	/	FALSE	%d	existing_cookie	existing_value
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{"127.0.0.1": {}},
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that will trigger SSO redirect
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	defer resp.Body.Close()

	// Verify we got success
	assert.Equal(t, http.StatusCreated, resp.StatusCode)

	// Verify the SSO flow completed
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount), "expected 1 IdP request")

	// CRITICAL: Verify the cookies from the redirect response are now in the jar
	// Get the ssoTransport to access the cookie jar
	transport, ok := client.httpClient.Transport.(*ssoTransport)
	require.True(t, ok, "expected ssoTransport")

	// Parse the GitLab server URL to check cookies
	gitlabURL, _ := url.Parse(gitlabServer.URL)
	jarCookies := transport.ssoClient.Jar.Cookies(gitlabURL)

	// Find the cookies that were set by the redirect response
	var foundSession, foundOAuthState bool
	for _, c := range jarCookies {
		if c.Name == "_gitlab_session" && c.Value == "session-token-abc123" {
			foundSession = true
		}
		if c.Name == "oauth_state" && c.Value == "state-xyz789" {
			foundOAuthState = true
		}
	}

	assert.True(t, foundSession,
		"_gitlab_session cookie from redirect response not stored in jar; cookies in jar: %v", jarCookies)
	assert.True(t, foundOAuthState,
		"oauth_state cookie from redirect response not stored in jar; cookies in jar: %v", jarCookies)
}

func TestSSOTransport_PreApprovedDomain(t *testing.T) {
	t.Parallel()
	var idpRequestCount int32

	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SSO complete"))
	}))
	defer idpServer.Close()

	// Extract the IdP hostname for pre-approval (without port)
	// The consent check uses url.Hostname() which excludes the port
	idpHost := "127.0.0.1" // httptest servers always use 127.0.0.1

	// Create GitLab server
	var gitlabRequestCount int32
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

	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(`localhost	FALSE	/	FALSE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	// Client with pre-approved domain
	client := &Client{
		baseURL:           gitlabServer.URL,
		cookieFile:        cookieFile,
		ssoAllowedDomains: map[string]struct{}{idpHost: {}},
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err, "failed to initialize HTTP client")

	// Make a POST request that triggers SSO redirect
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	require.NoError(t, err, "request failed")
	resp.Body.Close()

	// Verify SSO flow completed (IdP was reached)
	assert.Equal(t, int32(1), atomic.LoadInt32(&idpRequestCount), "expected 1 IdP request")
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
}
