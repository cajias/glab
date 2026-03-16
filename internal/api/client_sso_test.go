//go:build !integration

package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsMutatingMethod(t *testing.T) {
	t.Parallel()
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
			t.Parallel()
			assert.Equal(t, tt.want, isMutatingMethod(tt.method))
		})
	}
}

func TestCheckRedirect_TooManyRedirects(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(".example.com\tTRUE\t/\tTRUE\t%d\tsession\tvalue1\n", futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err)

	client := &Client{
		baseURL:    "https://example.com/api/v4",
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.NoError(t, err)

	via := make([]*http.Request, maxRedirects)
	for i := range via {
		via[i], _ = http.NewRequest(http.MethodGet, fmt.Sprintf("https://example.com/redirect%d", i), nil)
	}

	redirectReq, _ := http.NewRequest(http.MethodGet, "https://example.com/redirect10", nil)
	err = client.httpClient.CheckRedirect(redirectReq, via)

	require.Error(t, err)
	assert.Equal(t, fmt.Sprintf("stopped after %d redirects", maxRedirects), err.Error())
}

func TestCreateCookieJar_EmptyCookieFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	err := os.WriteFile(cookieFile, []byte("# This is a comment\n"), 0o600)
	require.NoError(t, err)

	client := &Client{
		baseURL:    "https://gitlab.example.com/api/v4",
		cookieFile: cookieFile,
	}

	err = client.initializeHTTPClient()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no valid cookies")
	assert.Contains(t, err.Error(), cookieFile)
}

func TestRequiresMethodPreservation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		statusCode int
		want       bool
	}{
		{http.StatusOK, false},
		{http.StatusMovedPermanently, true},   // 301
		{http.StatusFound, true},              // 302
		{http.StatusSeeOther, true},           // 303
		{http.StatusTemporaryRedirect, false}, // 307
		{http.StatusPermanentRedirect, false}, // 308
		{http.StatusNotFound, false},
		{http.StatusInternalServerError, false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.statusCode), func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, requiresMethodPreservation(tt.statusCode))
		})
	}
}

func TestIsSSORedirect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		originalHost   string
		originalScheme string
		locationHeader string
		want           bool
	}{
		{"empty location", "gitlab.example.com", "https", "", false},
		{"relative path", "gitlab.example.com", "https", "/api/v4/projects", false},
		{"relative path with query", "gitlab.example.com", "https", "/oauth/callback?code=123", false},
		{"same host https", "gitlab.example.com", "https", "https://gitlab.example.com/callback", false},
		{"same host http", "gitlab.example.com", "http", "http://gitlab.example.com/callback", false},
		{"same host with path", "gitlab.example.com", "https", "https://gitlab.example.com/api/v4/projects", false},
		{"different host", "gitlab.example.com", "https", "https://idp.example.com/saml", true},
		{"different subdomain", "gitlab.example.com", "https", "https://sso.example.com/auth", true},
		{"completely different domain", "gitlab.example.com", "https", "https://okta.com/login", true},
		{"same host different port", "127.0.0.1:8080", "http", "http://127.0.0.1:9090/callback", true},
		{"same host same port", "127.0.0.1:8080", "http", "http://127.0.0.1:8080/callback", false},
		{"https host with 443 to host without port", "gitlab.example.com:443", "https", "https://gitlab.example.com/callback", false},
		{"http host with 80 to host without port", "gitlab.example.com:80", "http", "http://gitlab.example.com/callback", false},
		{"host without port to host with default port", "gitlab.example.com", "https", "https://gitlab.example.com:443/callback", false},
		{"different port non-default", "gitlab.example.com:8443", "https", "https://gitlab.example.com/callback", true},
		{"location with fragment", "gitlab.example.com", "https", "https://idp.example.com/auth#state", true},
		{"location with query and fragment", "gitlab.example.com", "https", "https://idp.example.com/auth?a=1#state", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isSSORedirect(tt.originalHost, tt.originalScheme, tt.locationHeader))
		})
	}
}

func TestIsLocalhost(t *testing.T) {
	t.Parallel()
	tests := []struct {
		hostname string
		want     bool
	}{
		{"localhost", true},
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"127.255.255.255", true},
		{"::1", true},
		{"192.168.1.1", false},
		{"10.0.0.1", false},
		{"0.0.0.0", false},
		{"example.com", false},
		{"gitlab.example.com", false},
		{"", false},
		{"::2", false},
	}

	for _, tt := range tests {
		t.Run(tt.hostname, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isLocalhost(tt.hostname))
		})
	}
}

