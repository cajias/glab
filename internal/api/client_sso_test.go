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
	"sync/atomic"
	"testing"
	"time"
)

func TestSSORedirectError_Error(t *testing.T) {
	err := &SSORedirectError{
		RedirectURL: "https://idp.example.com/oauth/authorize",
		Method:      "POST",
	}
	expected := "SSO redirect detected for POST request to https://idp.example.com/oauth/authorize"
	if err.Error() != expected {
		t.Errorf("SSORedirectError.Error() = %q, want %q", err.Error(), expected)
	}
}

func TestIsMutatingMethod(t *testing.T) {
	tests := []struct {
		method string
		want   bool
	}{
		{http.MethodGet, false},
		{http.MethodHead, false},
		{http.MethodOptions, false},
		{http.MethodPost, true},
		{http.MethodPut, true},
		{http.MethodPatch, true},
		{http.MethodDelete, true},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			if got := isMutatingMethod(tt.method); got != tt.want {
				t.Errorf("isMutatingMethod(%q) = %v, want %v", tt.method, got, tt.want)
			}
		})
	}
}

func TestCheckRedirect_SSODetection(t *testing.T) {
	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp (now + 1 year) for cookie expiration
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	// Create a cookie file
	cookieContent := fmt.Sprintf(`.gitlab.example.com	TRUE	/	TRUE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    "https://gitlab.example.com/api/v4",
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Test that CheckRedirect is set
	if client.httpClient.CheckRedirect == nil {
		t.Fatal("CheckRedirect should be set when cookie file is configured")
	}

	tests := []struct {
		name           string
		originalMethod string
		originalHost   string
		redirectHost   string
		wantSSOError   bool
	}{
		{
			name:           "POST redirect to different host triggers SSO error",
			originalMethod: http.MethodPost,
			originalHost:   "gitlab.example.com",
			redirectHost:   "idp.example.com",
			wantSSOError:   true,
		},
		{
			name:           "PUT redirect to different host triggers SSO error",
			originalMethod: http.MethodPut,
			originalHost:   "gitlab.example.com",
			redirectHost:   "idp.example.com",
			wantSSOError:   true,
		},
		{
			name:           "PATCH redirect to different host triggers SSO error",
			originalMethod: http.MethodPatch,
			originalHost:   "gitlab.example.com",
			redirectHost:   "idp.example.com",
			wantSSOError:   true,
		},
		{
			name:           "DELETE redirect to different host triggers SSO error",
			originalMethod: http.MethodDelete,
			originalHost:   "gitlab.example.com",
			redirectHost:   "idp.example.com",
			wantSSOError:   true,
		},
		{
			name:           "GET redirect to different host does not trigger SSO error",
			originalMethod: http.MethodGet,
			originalHost:   "gitlab.example.com",
			redirectHost:   "idp.example.com",
			wantSSOError:   false,
		},
		{
			name:           "POST redirect to same host does not trigger SSO error",
			originalMethod: http.MethodPost,
			originalHost:   "gitlab.example.com",
			redirectHost:   "gitlab.example.com",
			wantSSOError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalReq, _ := http.NewRequest(tt.originalMethod, "https://"+tt.originalHost+"/api/v4/projects", nil)
			redirectReq, _ := http.NewRequest(http.MethodGet, "https://"+tt.redirectHost+"/oauth/authorize", nil)

			via := []*http.Request{originalReq}
			err := client.httpClient.CheckRedirect(redirectReq, via)

			if tt.wantSSOError {
				if err == nil {
					t.Error("expected SSORedirectError, got nil")
					return
				}
				ssoErr, ok := err.(*SSORedirectError)
				if !ok {
					t.Errorf("expected *SSORedirectError, got %T", err)
					return
				}
				if ssoErr.Method != tt.originalMethod {
					t.Errorf("SSORedirectError.Method = %q, want %q", ssoErr.Method, tt.originalMethod)
				}
				if ssoErr.RedirectURL != redirectReq.URL.String() {
					t.Errorf("SSORedirectError.RedirectURL = %q, want %q", ssoErr.RedirectURL, redirectReq.URL.String())
				}
			} else {
				if err != nil {
					t.Errorf("expected no error, got %v", err)
				}
			}
		})
	}
}

func TestCheckRedirect_TooManyRedirects(t *testing.T) {
	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	cookieContent := fmt.Sprintf(`.example.com	TRUE	/	TRUE	%d	session	value1
`, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    "https://example.com/api/v4",
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Create 10 requests in the via slice (simulating 10 redirects already followed)
	via := make([]*http.Request, 10)
	for i := range via {
		via[i], _ = http.NewRequest(http.MethodGet, fmt.Sprintf("https://example.com/redirect%d", i), nil)
	}

	redirectReq, _ := http.NewRequest(http.MethodGet, "https://example.com/redirect10", nil)
	err = client.httpClient.CheckRedirect(redirectReq, via)

	if err == nil {
		t.Error("expected error for too many redirects, got nil")
	}
	if err != nil && err.Error() != "stopped after 10 redirects" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDoWithSSORetry_NoRedirect(t *testing.T) {
	// Setup test server that returns success immediately
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status": "success"}`))
	}))
	defer server.Close()

	// Create a simple client without cookie file
	client := &Client{
		baseURL: server.URL,
	}
	err := client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", bytes.NewBufferString(`{"name": "test"}`))
	resp, err := client.DoWithSSORetry(req)
	if err != nil {
		t.Fatalf("DoWithSSORetry failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestDoWithSSORetry_WithSSOFlow(t *testing.T) {
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

	// Create cookie file with cookies for both servers (simulating real IdP scenario)
	// Note: We need to use localhost since test servers use it
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

	// Make a POST request
	body := `{"name": "test-project"}`
	req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.DoWithSSORetry(req)
	if err != nil {
		t.Fatalf("DoWithSSORetry failed: %v", err)
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

func TestDoWithSSORetry_PreservesRequestBody(t *testing.T) {
	var receivedBody string

	// Create test server that captures the request body
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bodyBytes, _ := io.ReadAll(r.Body)
		receivedBody = string(bodyBytes)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		baseURL: server.URL,
	}

	err := client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// Make a POST request with a body
	expectedBody := `{"important": "data"}`
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", bytes.NewBufferString(expectedBody))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.DoWithSSORetry(req)
	if err != nil {
		t.Fatalf("DoWithSSORetry failed: %v", err)
	}
	defer resp.Body.Close()

	// Verify the body was received correctly
	if receivedBody != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, receivedBody)
	}

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}
}

func TestDoWithSSORetry_HeadersPreserved(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := &Client{
		baseURL: server.URL,
	}
	err := client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v4/projects", nil)
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer token123")

	resp, err := client.DoWithSSORetry(req)
	if err != nil {
		t.Fatalf("DoWithSSORetry failed: %v", err)
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

func TestCheckRedirect_NotSetWithoutCookieFile(t *testing.T) {
	client := &Client{
		baseURL: "https://example.com/api/v4",
		// No cookie file configured
	}

	err := client.initializeHTTPClient()
	if err != nil {
		t.Fatalf("failed to initialize HTTP client: %v", err)
	}

	// CheckRedirect should NOT be set when no cookie file is configured
	// because SSO redirect handling is only needed for cookie-based auth
	if client.httpClient.CheckRedirect != nil {
		t.Error("CheckRedirect should not be set when cookie file is not configured")
	}
}
