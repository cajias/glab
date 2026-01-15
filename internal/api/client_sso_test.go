//go:build !integration

package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

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
	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i], _ = http.NewRequest(http.MethodGet, fmt.Sprintf("https://example.com/redirect%d", i), nil)
	}

	redirectReq, _ := http.NewRequest(http.MethodGet, "https://example.com/redirect10", nil)
	err = client.httpClient.CheckRedirect(redirectReq, via)

	if err == nil {
		t.Error("expected error for too many redirects, got nil")
	}
	expectedErr := fmt.Sprintf("stopped after %d redirects", maxRedirects)
	if err != nil && err.Error() != expectedErr {
		t.Errorf("unexpected error: got %q, want %q", err.Error(), expectedErr)
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

func TestCreateCookieJar_EmptyCookieFile(t *testing.T) {
	// Create an empty cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Write an empty file (or file with only comments)
	err := os.WriteFile(cookieFile, []byte("# This is a comment\n"), 0o600)
	if err != nil {
		t.Fatalf("failed to create test cookie file: %v", err)
	}

	client := &Client{
		baseURL:    "https://gitlab.example.com/api/v4",
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	if err == nil {
		t.Fatal("expected error for empty cookie file, got nil")
	}

	// Verify error message is helpful
	if !strings.Contains(err.Error(), "no valid cookies") {
		t.Errorf("expected error to mention 'no valid cookies', got: %v", err)
	}
	if !strings.Contains(err.Error(), cookieFile) {
		t.Errorf("expected error to include cookie file path, got: %v", err)
	}
}

func TestRequiresMethodPreservation(t *testing.T) {
	tests := []struct {
		statusCode int
		want       bool
	}{
		{http.StatusOK, false},
		{http.StatusMovedPermanently, true},   // 301
		{http.StatusFound, true},              // 302
		{http.StatusSeeOther, true},           // 303
		{http.StatusTemporaryRedirect, false}, // 307 - already preserves method
		{http.StatusPermanentRedirect, false}, // 308 - already preserves method
		{http.StatusNotFound, false},
		{http.StatusInternalServerError, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.statusCode), func(t *testing.T) {
			if got := requiresMethodPreservation(tt.statusCode); got != tt.want {
				t.Errorf("requiresMethodPreservation(%d) = %v, want %v", tt.statusCode, got, tt.want)
			}
		})
	}
}

func TestIsSSORedirect(t *testing.T) {
	tests := []struct {
		name           string
		originalHost   string
		locationHeader string
		want           bool
	}{
		// Empty location
		{"empty location", "gitlab.example.com", "", false},

		// Relative URLs - same host
		{"relative path", "gitlab.example.com", "/api/v4/projects", false},
		{"relative path with query", "gitlab.example.com", "/oauth/callback?code=123", false},

		// Absolute URLs - same host
		{"same host https", "gitlab.example.com", "https://gitlab.example.com/callback", false},
		{"same host http", "gitlab.example.com", "http://gitlab.example.com/callback", false},
		{"same host with path", "gitlab.example.com", "https://gitlab.example.com/api/v4/projects", false},

		// Absolute URLs - different host (SSO)
		{"different host", "gitlab.example.com", "https://idp.example.com/saml", true},
		{"different subdomain", "gitlab.example.com", "https://sso.example.com/auth", true},
		{"completely different domain", "gitlab.example.com", "https://okta.com/login", true},

		// Port handling
		{"same host different port", "127.0.0.1:8080", "http://127.0.0.1:9090/callback", true},
		{"same host same port", "127.0.0.1:8080", "http://127.0.0.1:8080/callback", false},
		{"host with port to host without", "gitlab.example.com:443", "https://gitlab.example.com/callback", true},

		// Edge cases
		{"location with fragment", "gitlab.example.com", "https://idp.example.com/auth#state", true},
		{"location with query and fragment", "gitlab.example.com", "https://idp.example.com/auth?a=1#state", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isSSORedirect(tt.originalHost, tt.locationHeader); got != tt.want {
				t.Errorf("isSSORedirect(%q, %q) = %v, want %v", tt.originalHost, tt.locationHeader, got, tt.want)
			}
		})
	}
}

func TestIsLocalhost(t *testing.T) {
	tests := []struct {
		hostname string
		want     bool
	}{
		// Standard localhost
		{"localhost", true},

		// IPv4 loopback
		{"127.0.0.1", true},
		{"127.0.0.2", true}, // Any 127.x.x.x is loopback
		{"127.255.255.255", true},

		// IPv6 loopback
		{"::1", true},

		// Non-loopback addresses
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"0.0.0.0", false},
		{"example.com", false},
		{"gitlab.example.com", false},

		// Edge cases
		{"", false},
		{"localhost.localdomain", false}, // Not the same as "localhost"
		{"::2", false},                   // Not loopback
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			if got := isLocalhost(tt.hostname); got != tt.want {
				t.Errorf("isLocalhost(%q) = %v, want %v", tt.hostname, got, tt.want)
			}
		})
	}
}

func TestSSOTransport_HTTPSEnforcement(t *testing.T) {
	// Create a test server that will act as the "GitLab" server
	gitlabServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// This should not be reached in the HTTP rejection test
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}))
	defer gitlabServer.Close()

	jar, _ := cookiejar.New(nil)
	ssoClient := &http.Client{Jar: jar, Timeout: ssoTimeout}

	transport := &ssoTransport{
		rt:        http.DefaultTransport,
		ssoClient: ssoClient,
	}

	t.Run("rejects HTTP redirect to non-localhost", func(t *testing.T) {
		// Create a request to a hypothetical GitLab endpoint
		req, _ := http.NewRequest(http.MethodPost, gitlabServer.URL+"/api/v4/projects", bytes.NewReader([]byte("{}")))

		// Simulate an HTTP redirect to a non-localhost IdP (should be rejected)
		resp := &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://idp.example.com/saml/auth"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}

		_, err := transport.handleSSORedirect(req, resp, "http://idp.example.com/saml/auth", []byte("{}"))
		if err == nil {
			t.Fatal("expected error for HTTP redirect to non-localhost, got nil")
		}
		if !strings.Contains(err.Error(), "SSO redirect rejected") {
			t.Errorf("expected 'SSO redirect rejected' error, got: %v", err)
		}
		if !strings.Contains(err.Error(), "expected HTTPS") {
			t.Errorf("expected 'expected HTTPS' in error, got: %v", err)
		}
	})

	t.Run("allows HTTPS redirect", func(t *testing.T) {
		// Create a mock HTTPS IdP server
		idpServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer idpServer.Close()

		// Use the TLS client from the test server
		transport.ssoClient = idpServer.Client()
		transport.ssoClient.Jar = jar

		req, _ := http.NewRequest(http.MethodPost, "https://gitlab.example.com/api/v4/projects", bytes.NewReader([]byte("{}")))

		resp := &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{idpServer.URL + "/saml/auth"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}

		// This will fail at the retry stage (no real GitLab server), but should NOT fail at HTTPS check
		_, err := transport.handleSSORedirect(req, resp, idpServer.URL+"/saml/auth", []byte("{}"))
		// We expect an error, but NOT about HTTPS rejection
		if err != nil && strings.Contains(err.Error(), "SSO redirect rejected") {
			t.Errorf("HTTPS redirect should not be rejected: %v", err)
		}
	})

	t.Run("allows HTTP redirect to localhost", func(t *testing.T) {
		// Create a local test server (will be on 127.0.0.1)
		localServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer localServer.Close()

		transport.ssoClient = &http.Client{Jar: jar, Timeout: ssoTimeout}

		req, _ := http.NewRequest(http.MethodPost, "https://gitlab.example.com/api/v4/projects", bytes.NewReader([]byte("{}")))

		resp := &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{localServer.URL + "/callback"}},
			Body:       io.NopCloser(strings.NewReader("")),
		}

		// This will fail at the retry stage (no real GitLab server), but should NOT fail at HTTPS check
		_, err := transport.handleSSORedirect(req, resp, localServer.URL+"/callback", []byte("{}"))
		// We expect an error, but NOT about HTTPS rejection
		if err != nil && strings.Contains(err.Error(), "SSO redirect rejected") {
			t.Errorf("localhost HTTP redirect should not be rejected: %v", err)
		}
	})
}
