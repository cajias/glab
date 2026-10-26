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
		via[i], _ = http.NewRequest(http.MethodGet, fmt.Sprintf("https://example.com/redirect%d", i), http.NoBody)
	}

	redirectReq, _ := http.NewRequest(http.MethodGet, "https://example.com/redirect10", http.NoBody)
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

// TestSSOClient_DoesNotInheritInstanceTLSExceptions covers the finding that the
// SSO legs reused the instance's transport, so skip_tls_verify disabled
// certificate validation on the hop carrying the IdP session cookie and the
// client certificate was offered to the IdP.
func TestSSOClient_DoesNotInheritInstanceTLSExceptions(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()

	cookieFile := filepath.Join(tmpDir, "cookies.txt")
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()
	cookieContent := fmt.Sprintf(".example.com\tTRUE\t/\tTRUE\t%d\tsession\tvalue1\n", futureTimestamp)
	require.NoError(t, os.WriteFile(cookieFile, []byte(cookieContent), 0o600))

	// Content is irrelevant: newTransport only needs the file to be readable to
	// build a RootCAs pool from it.
	caFile := filepath.Join(tmpDir, "ca.pem")
	require.NoError(t, os.WriteFile(caFile, []byte("-----BEGIN CERTIFICATE-----\n-----END CERTIFICATE-----\n"), 0o600))

	client := &Client{
		baseURL:       "https://gitlab.example.com/api/v4",
		cookieFile:    cookieFile,
		caFile:        caFile,
		allowInsecure: true,
	}
	require.NoError(t, client.initializeHTTPClient())

	transport, ok := client.httpClient.Transport.(*ssoTransport)
	require.True(t, ok, "expected httpClient.Transport to be *ssoTransport, got %T", client.httpClient.Transport)

	instanceRT, ok := transport.rt.(*http.Transport)
	require.True(t, ok, "expected the instance transport to be *http.Transport, got %T", transport.rt)
	ssoRT, ok := transport.ssoClient.Transport.(*http.Transport)
	require.True(t, ok, "expected the SSO transport to be *http.Transport, got %T", transport.ssoClient.Transport)

	require.NotSame(t, instanceRT, ssoRT, "the SSO legs must not share the instance's transport")

	assert.True(t, instanceRT.TLSClientConfig.InsecureSkipVerify,
		"the instance keeps the skip_tls_verify the user asked for")
	assert.False(t, ssoRT.TLSClientConfig.InsecureSkipVerify,
		"the SSO legs must validate certificates even when the instance skips verification")
	assert.Empty(t, ssoRT.TLSClientConfig.Certificates,
		"the client certificate must not be offered to the identity provider")
	assert.NotNil(t, ssoRT.TLSClientConfig.RootCAs,
		"the configured CA is preserved: a corporate CA is often what the IdP chain needs")
}

func TestParseSSODomains(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		value   string
		want    map[string]struct{}
		wantErr string
	}{
		{
			name:  "empty value yields no allowlist",
			value: "",
		},
		{
			name:  "a single domain stays a one-element list",
			value: "idp.example.com",
			want:  map[string]struct{}{"idp.example.com": {}},
		},
		{
			name:  "a comma-separated list covers a multi-hop flow",
			value: "midway-auth.amazon.com,idp.federate.amazon.com",
			want: map[string]struct{}{
				"midway-auth.amazon.com":  {},
				"idp.federate.amazon.com": {},
			},
		},
		{
			name:  "surrounding whitespace and empty entries are dropped",
			value: " idp-a.example.com , , idp-b.example.com ,",
			want: map[string]struct{}{
				"idp-a.example.com": {},
				"idp-b.example.com": {},
			},
		},
		{
			name:  "entries are lowercased to match the hostname comparison",
			value: "IDP.Example.COM",
			want:  map[string]struct{}{"idp.example.com": {}},
		},
		{
			name:    "a scheme is rejected rather than silently never matching",
			value:   "https://idp.example.com",
			wantErr: `invalid sso_domain entry "https://idp.example.com"`,
		},
		{
			name:    "a port is rejected",
			value:   "idp.example.com:443",
			wantErr: `invalid sso_domain entry "idp.example.com:443"`,
		},
		{
			name:    "a path is rejected",
			value:   "idp.example.com/saml",
			wantErr: `invalid sso_domain entry "idp.example.com/saml"`,
		},
		{
			name:    "a wildcard is rejected",
			value:   "*.amazon.com",
			wantErr: `invalid sso_domain entry "*.amazon.com"`,
		},
		{
			name:    "one bad entry rejects the whole list",
			value:   "idp-a.example.com,*.example.com",
			wantErr: `invalid sso_domain entry "*.example.com"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseSSODomains(tt.value)

			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Contains(t, err.Error(), "bare hostname", "the error must say what a good value looks like")
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}
