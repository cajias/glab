package config

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadCookieFile(t *testing.T) {
	t.Parallel()
	// Create a temporary directory
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		content     string
		expectedLen int
		checkCookie func(t *testing.T, cookies []*http.Cookie)
		wantErr     bool
	}{
		{
			name: "valid cookie file",
			content: `# Netscape HTTP Cookie File
# https://curl.se/docs/http-cookies.html
.example.com	TRUE	/	TRUE	1735689600	session_id	abc123
`,
			expectedLen: 1,
			checkCookie: func(t *testing.T, cookies []*http.Cookie) {
				t.Helper()
				assert.Equal(t, "session_id", cookies[0].Name)
				assert.Equal(t, "abc123", cookies[0].Value)
				assert.Equal(t, ".example.com", cookies[0].Domain)
				assert.True(t, cookies[0].Secure)
			},
		},
		{
			name: "multiple cookies",
			content: `# Comment line
.gitlab.com	TRUE	/	TRUE	1735689600	_gitlab_session	sess123
.gitlab.com	TRUE	/	FALSE	1735689600	known_sign_in	true
gitlab.com	FALSE	/api	TRUE	0	api_token	token456
`,
			expectedLen: 3,
			checkCookie: func(t *testing.T, cookies []*http.Cookie) {
				t.Helper()
				assert.Equal(t, "_gitlab_session", cookies[0].Name)
				assert.Equal(t, "known_sign_in", cookies[1].Name)
				assert.Equal(t, "api_token", cookies[2].Name)
				assert.Equal(t, "/api", cookies[2].Path)
			},
		},
		{
			name: "empty file",
			content: `# Netscape HTTP Cookie File
# Only comments
`,
			expectedLen: 0,
		},
		{
			name:        "file with empty lines",
			content:     "\n\n.example.com\tTRUE\t/\tTRUE\t1735689600\ttest\tvalue\n\n",
			expectedLen: 1,
		},
		{
			name: "skip malformed lines",
			content: `# Header
.example.com	TRUE	/	TRUE	1735689600	valid	cookie
malformed line without enough fields
.example.com	TRUE	/	TRUE	1735689600	another	valid
`,
			expectedLen: 2,
		},
		{
			name: "session cookie (expiration 0)",
			content: `.example.com	TRUE	/	TRUE	0	session	value
`,
			expectedLen: 1,
			checkCookie: func(t *testing.T, cookies []*http.Cookie) {
				t.Helper()
				assert.True(t, cookies[0].Expires.IsZero(), "expected session cookie with zero expiration")
			},
		},
		{
			name: "cookie value with special characters",
			content: `.example.com	TRUE	/	TRUE	1735689600	encoded	val%20ue%3D%26test
`,
			expectedLen: 1,
			checkCookie: func(t *testing.T, cookies []*http.Cookie) {
				t.Helper()
				assert.Equal(t, "val%20ue%3D%26test", cookies[0].Value)
			},
		},
		{
			name: "httponly cookie with #HttpOnly_ prefix",
			content: `# Netscape HTTP Cookie File
#HttpOnly_.example.com	TRUE	/	TRUE	1735689600	session_id	abc123
.example.com	TRUE	/	TRUE	1735689600	regular_cookie	def456
`,
			expectedLen: 2,
			checkCookie: func(t *testing.T, cookies []*http.Cookie) {
				t.Helper()
				// First cookie should have HttpOnly flag set
				assert.Equal(t, "session_id", cookies[0].Name)
				assert.True(t, cookies[0].HttpOnly, "expected first cookie to have HttpOnly flag set")
				assert.Equal(t, ".example.com", cookies[0].Domain)
				// Second cookie should NOT have HttpOnly flag
				assert.Equal(t, "regular_cookie", cookies[1].Name)
				assert.False(t, cookies[1].HttpOnly, "expected second cookie to NOT have HttpOnly flag set")
			},
		},
		{
			name: "httponly cookies for multiple domains",
			content: `# Netscape HTTP Cookie File
#HttpOnly_.gitlab.example.com	TRUE	/	TRUE	1735689600	_gitlab_session	sess123
#HttpOnly_.idp.example.com	TRUE	/	TRUE	1735689600	sso_token	idp456
.gitlab.example.com	TRUE	/	TRUE	1735689600	known_sign_in	true
`,
			expectedLen: 3,
			checkCookie: func(t *testing.T, cookies []*http.Cookie) {
				t.Helper()
				// First and second cookies should have HttpOnly flag
				assert.True(t, cookies[0].HttpOnly, "expected first cookie to have HttpOnly flag set")
				assert.True(t, cookies[1].HttpOnly, "expected second cookie to have HttpOnly flag set")
				// Third cookie should NOT have HttpOnly flag
				assert.False(t, cookies[2].HttpOnly, "expected third cookie to NOT have HttpOnly flag set")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Create temp file
			cookieFile := filepath.Join(tmpDir, tt.name+".txt")
			err := os.WriteFile(cookieFile, []byte(tt.content), 0o600)
			require.NoError(t, err, "failed to create test file")

			cookies, err := LoadCookieFile(cookieFile)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			assert.Len(t, cookies, tt.expectedLen)

			if tt.checkCookie != nil && len(cookies) > 0 {
				tt.checkCookie(t, cookies)
			}
		})
	}
}

func TestLoadCookieFile_NonExistent(t *testing.T) {
	t.Parallel()
	_, err := LoadCookieFile("/nonexistent/path/cookies.txt")
	assert.Error(t, err, "expected error for non-existent file")
}

func Test_expandPath(t *testing.T) {
	// Use t.Setenv which automatically restores the original value
	t.Setenv("HOME", "/home/testuser")
	t.Setenv("TEST_VAR", "testvalue")

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "expand tilde",
			input:    "~/.config/cookies",
			expected: "/home/testuser/.config/cookies",
		},
		{
			name:     "expand env var",
			input:    "$TEST_VAR/cookies",
			expected: "testvalue/cookies",
		},
		{
			name:     "no expansion needed",
			input:    "/absolute/path/cookies",
			expected: "/absolute/path/cookies",
		},
		{
			name:     "empty path",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := expandPath(tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestParseCookieLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		line     string
		httpOnly bool
		want     *http.Cookie
		wantErr  bool
	}{
		{
			name:     "standard cookie",
			line:     ".example.com\tTRUE\t/\tTRUE\t1735689600\tsession\tvalue",
			httpOnly: false,
			want: &http.Cookie{
				Name:     "session",
				Value:    "value",
				Domain:   ".example.com",
				Path:     "/",
				Secure:   true,
				HttpOnly: false,
				Expires:  time.Unix(1735689600, 0),
			},
		},
		{
			name:     "httponly cookie",
			line:     ".example.com\tTRUE\t/\tTRUE\t1735689600\tsession\tvalue",
			httpOnly: true,
			want: &http.Cookie{
				Name:     "session",
				Value:    "value",
				Domain:   ".example.com",
				Path:     "/",
				Secure:   true,
				HttpOnly: true,
				Expires:  time.Unix(1735689600, 0),
			},
		},
		{
			name:     "insecure cookie",
			line:     "example.com\tFALSE\t/path\tFALSE\t1735689600\tname\tval",
			httpOnly: false,
			want: &http.Cookie{
				Name:     "name",
				Value:    "val",
				Domain:   "example.com",
				Path:     "/path",
				Secure:   false,
				HttpOnly: false,
				Expires:  time.Unix(1735689600, 0),
			},
		},
		{
			name:    "too few fields",
			line:    "example.com\tTRUE\t/\tTRUE\t1735689600",
			wantErr: true,
		},
		{
			name:    "invalid expiration",
			line:    "example.com\tTRUE\t/\tTRUE\tnotanumber\tname\tval",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseCookieLine(tt.line, tt.httpOnly)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)

			assert.Equal(t, tt.want.Name, got.Name)
			assert.Equal(t, tt.want.Value, got.Value)
			assert.Equal(t, tt.want.Domain, got.Domain)
			assert.Equal(t, tt.want.Path, got.Path)
			assert.Equal(t, tt.want.Secure, got.Secure)
			assert.Equal(t, tt.want.HttpOnly, got.HttpOnly)
		})
	}
}
