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

func TestCreateCookieJar_MultipleDomains(t *testing.T) {
	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp (now + 1 year) for cookie expiration
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	// Create a cookie file with cookies for multiple domains (simulating IdP/SSO scenario)
	// This includes:
	// 1. GitLab instance cookies
	// 2. Identity provider cookies (for SSO redirect)
	cookieContent := fmt.Sprintf(`# Netscape HTTP Cookie File
# This test simulates an IdP/SSO protected GitLab instance
#HttpOnly_.gitlab.example.com	TRUE	/	TRUE	%d	_gitlab_session	gitlab_sess_123
.gitlab.example.com	TRUE	/	TRUE	%d	known_sign_in	true
#HttpOnly_.idp.federate.example.com	TRUE	/	TRUE	%d	sso_session	idp_sess_456
.idp.federate.example.com	TRUE	/	TRUE	%d	auth_state	authenticated
#HttpOnly_.midway-auth.example.com	TRUE	/	TRUE	%d	midway_token	midway_789
`, futureTimestamp, futureTimestamp, futureTimestamp, futureTimestamp, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	// Create a client with the cookie file and a base URL for the GitLab instance
	client := &Client{
		baseURL:    "https://gitlab.example.com/api/v4",
		cookieFile: cookieFile,
	}

	// Create the cookie jar
	jar, err := client.createCookieJar()
	require.NoError(t, err, "createCookieJar() returned error")

	// Verify that cookies are loaded for all domains, not just the base URL
	testCases := []struct {
		name           string
		url            string
		expectedCookie string
	}{
		{
			name:           "GitLab instance cookies",
			url:            "https://gitlab.example.com/api/v4/projects",
			expectedCookie: "_gitlab_session",
		},
		{
			name:           "IdP cookies for SSO redirect",
			url:            "https://idp.federate.example.com/saml/auth",
			expectedCookie: "sso_session",
		},
		{
			name:           "Midway auth cookies for redirect",
			url:            "https://midway-auth.example.com/authenticate",
			expectedCookie: "midway_token",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", tc.url, nil)
			require.NoError(t, err, "failed to create request")

			cookies := jar.Cookies(req.URL)
			found := false
			for _, cookie := range cookies {
				if cookie.Name == tc.expectedCookie {
					found = true
					break
				}
			}

			assert.True(t, found, "expected to find cookie '%s' for URL %s, but it was not found. Found cookies: %v",
				tc.expectedCookie, tc.url, cookieNames(cookies))
		})
	}
}

func TestCreateCookieJar_DomainWithLeadingDot(t *testing.T) {
	// Create a temporary directory for the test cookie file
	tmpDir := t.TempDir()
	cookieFile := filepath.Join(tmpDir, "cookies.txt")

	// Use a future timestamp (now + 1 year) for cookie expiration
	futureTimestamp := time.Now().AddDate(1, 0, 0).Unix()

	// Create a cookie file with domains that have leading dots (subdomain matching)
	cookieContent := fmt.Sprintf(`.example.com	TRUE	/	TRUE	%d	root_cookie	value1
.sub.example.com	TRUE	/	TRUE	%d	sub_cookie	value2
example.com	FALSE	/	TRUE	%d	exact_cookie	value3
`, futureTimestamp, futureTimestamp, futureTimestamp)

	err := os.WriteFile(cookieFile, []byte(cookieContent), 0o600)
	require.NoError(t, err, "failed to create test cookie file")

	client := &Client{
		baseURL:    "https://example.com/api/v4",
		cookieFile: cookieFile,
	}

	jar, err := client.createCookieJar()
	require.NoError(t, err, "createCookieJar() returned error")

	// The jar should contain cookies for all domains
	// Note: The cookie jar's Cookies() method returns cookies based on domain matching
	req, _ := http.NewRequest("GET", "https://www.example.com/path", nil)
	cookies := jar.Cookies(req.URL)

	// At least one cookie should be available (root_cookie matches subdomains)
	assert.NotEmpty(t, cookies, "expected at least one cookie to be available for www.example.com")
}

func TestCreateCookieJar_FileNotFound(t *testing.T) {
	client := &Client{
		baseURL:    "https://example.com/api/v4",
		cookieFile: "/nonexistent/path/cookies.txt",
	}

	_, err := client.createCookieJar()
	assert.Error(t, err, "expected error for non-existent cookie file")
}

// cookieNames is a helper function to extract cookie names for error messages
func cookieNames(cookies []*http.Cookie) []string {
	names := make([]string, len(cookies))
	for i, c := range cookies {
		names[i] = c.Name
	}
	return names
}
