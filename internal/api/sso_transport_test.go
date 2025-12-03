//go:build !integration

package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSSOTransport_NoRedirect(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", bytes.NewBufferString(`{"name": "test"}`))
	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestSSOTransport_WithSSOFlow(t *testing.T) {
	// Track request counts
	var gitlabRequestCount int32
	var idpRequestCount int32

	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		// IdP should only receive GET requests
		if r.Method != http.MethodGet {
			t.Errorf("IdP received %s request, expected GET", r.Method)
		}
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    gitlabServer.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Verify that ssoTransport is being used
	if _, ok := client.httpClient.Transport.(*ssoTransport); !ok {
		t.Fatal("expected ssoTransport to be used when cookie file is configured")
	}

	// Make a POST request directly through the HTTP client (simulates gitlab.Client behavior)
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify we got the success response
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, resp.StatusCode)
	}

	// Verify IdP received a GET request (SSO flow)
	if atomic.LoadInt32(&idpRequestCount) != 1 {
		t.Errorf("expected 1 IdP request, got %d", idpRequestCount)
	}

	// Verify GitLab received 2 requests (initial + retry)
	if atomic.LoadInt32(&gitlabRequestCount) != 2 {
		t.Errorf("expected 2 GitLab requests, got %d", gitlabRequestCount)
	}
}

func TestSSOTransport_PreservesRequestBody(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    gitlabServer.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request with a body
	expectedBody := `{"important": "data"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(expectedBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify the body was received correctly
	if len(receivedBodies) != 1 {
		t.Fatalf("expected 1 request, got %d", len(receivedBodies))
	}
	if receivedBodies[0] != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, receivedBodies[0])
	}
}

func TestSSOTransport_HeadersPreserved(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", nil)
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token123")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify custom headers were preserved
	if receivedHeaders.Get("X-Custom-Header") != "custom-value" {
		t.Errorf("X-Custom-Header not preserved: got %q", receivedHeaders.Get("X-Custom-Header"))
	}
	if receivedHeaders.Get("Content-Type") != "application/json" {
		t.Errorf("Content-Type not preserved: got %q", receivedHeaders.Get("Content-Type"))
	}
	if receivedHeaders.Get("Authorization") != "Bearer token123" {
		t.Errorf("Authorization not preserved: got %q", receivedHeaders.Get("Authorization"))
	}
}

func TestSSOTransport_GETRequestNotIntercepted(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a GET request
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v4/projects", nil)

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify only one request was made (no retry)
	if atomic.LoadInt32(&requestCount) != 1 {
		t.Errorf("expected 1 request for GET, got %d", requestCount)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestSSOTransport_NotSetWithoutCookieFile(t *testing.T) {
	client := &Client{
		baseURL: "https://example.com/api/v4",
		// No cookie file configured
	}

	err := client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// ssoTransport should NOT be used when no cookie file is configured
	if _, ok := client.httpClient.Transport.(*ssoTransport); ok {
		t.Error("ssoTransport should not be used when cookie file is not configured")
	}
}

func TestSSOTransport_SSOFlowFails(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    gitlabServer.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request - should complete SSO flow but IdP returns error
	// The SSO flow should still succeed (we just consume the IdP response)
	// and the retry should be attempted
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Even though IdP returned 500, the SSO flow completed and retry was attempted
	// The retry will get another redirect (since our test server always redirects)
	// But that's expected - the important thing is that errors are handled gracefully
	// and the flow doesn't crash
}

func TestSSOTransport_SSOFlowConnectionFails(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    gitlabServer.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request - SSO flow should fail because IdP is unreachable
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	_, err = client.httpClient.Do(req)
	if err == nil {
		t.Fatal("expected error when SSO flow fails, got nil")
	}

	// Verify the error message contains context about SSO flow failure
	if !strings.Contains(err.Error(), "SSO flow request failed") {
		t.Errorf("expected error to contain 'SSO flow request failed', got: %v", err)
	}
}

func TestSSOTransport_SameHostRedirect_PreservesMethod(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request that will be redirected
	body := `{"body": "Test note"}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/redirect", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify the final endpoint received POST (not GET)
	if receivedMethod != http.MethodPost {
		t.Errorf("expected method POST at final endpoint, got %s", receivedMethod)
	}

	// Verify the body was preserved
	if receivedBody != body {
		t.Errorf("expected body %q, got %q", body, receivedBody)
	}

	// Verify we got the success response
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, resp.StatusCode)
	}

	// Verify 2 requests were made (initial + follow redirect)
	if requestCount != 2 {
		t.Errorf("expected 2 requests, got %d", requestCount)
	}
}

func TestSSOTransport_SameHostRedirect_MultipleRedirects(t *testing.T) {
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
			if r.Method != http.MethodPost {
				t.Errorf("expected POST at %s, got %s", r.URL.Path, r.Method)
			}
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request that will be redirected multiple times
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/redirect1", strings.NewReader(`{"test": "data"}`))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// 4 requests: initial + 3 redirects
	if requestCount != 4 {
		t.Errorf("expected 4 requests, got %d", requestCount)
	}
}

func TestSSOTransport_SameHostRedirect_MaxRedirectsExceeded(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request that will redirect forever
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/start", strings.NewReader(`{"test": "data"}`))
	req.Header.Set("Content-Type", "application/json")

	_, err = client.httpClient.Do(req)
	if err == nil {
		t.Fatal("expected error for too many redirects, got nil")
	}

	if !strings.Contains(err.Error(), "stopped after") && !strings.Contains(err.Error(), "redirects") {
		t.Errorf("expected error about max redirects, got: %v", err)
	}
}

func TestSSOTransport_GETNotAffected(t *testing.T) {
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    server.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a GET request that will be redirected
	req, _ := http.NewRequest(http.MethodGet, server.URL+"/redirect", nil)

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// GET requests should work normally
	if receivedMethod != http.MethodGet {
		t.Errorf("expected GET at final endpoint, got %s", receivedMethod)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestSSOTransport_SameHostToSSORedirect(t *testing.T) {
	// Test the scenario where a same-host redirect leads to an SSO redirect
	var gitlabRequestCount, idpRequestCount int32

	// Create IdP server
	idpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&idpRequestCount, 1)
		// IdP should receive GET request
		if r.Method != http.MethodGet {
			t.Errorf("IdP received %s request, expected GET", r.Method)
		}
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
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    gitlabServer.URL,
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request that goes through same-host redirect then SSO redirect
	body := `{"body": "Test note"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/start", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify we got the success response
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("expected status %d, got %d", http.StatusCreated, resp.StatusCode)
	}

	// Verify IdP received exactly one GET request
	if atomic.LoadInt32(&idpRequestCount) != 1 {
		t.Errorf("expected 1 IdP request, got %d", idpRequestCount)
	}

	// Verify GitLab received 3 requests: initial, same-host redirect, and retry after SSO
	if atomic.LoadInt32(&gitlabRequestCount) != 3 {
		t.Errorf("expected 3 GitLab requests, got %d", gitlabRequestCount)
	}
}
