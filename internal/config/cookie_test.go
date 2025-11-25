package config

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadCookieFile(t *testing.T) {
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
				if cookies[0].Name != "session_id" {
					t.Errorf("expected cookie name 'session_id', got '%s'", cookies[0].Name)
				}
				if cookies[0].Value != "abc123" {
					t.Errorf("expected cookie value 'abc123', got '%s'", cookies[0].Value)
				}
				if cookies[0].Domain != ".example.com" {
					t.Errorf("expected domain '.example.com', got '%s'", cookies[0].Domain)
				}
				if !cookies[0].Secure {
					t.Error("expected secure to be true")
				}
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
				if cookies[0].Name != "_gitlab_session" {
					t.Errorf("expected first cookie name '_gitlab_session', got '%s'", cookies[0].Name)
				}
				if cookies[1].Name != "known_sign_in" {
					t.Errorf("expected second cookie name 'known_sign_in', got '%s'", cookies[1].Name)
				}
				if cookies[2].Name != "api_token" {
					t.Errorf("expected third cookie name 'api_token', got '%s'", cookies[2].Name)
				}
				if cookies[2].Path != "/api" {
					t.Errorf("expected third cookie path '/api', got '%s'", cookies[2].Path)
				}
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
				if !cookies[0].Expires.IsZero() {
					t.Error("expected session cookie with zero expiration")
				}
			},
		},
		{
			name: "cookie value with special characters",
			content: `.example.com	TRUE	/	TRUE	1735689600	encoded	val%20ue%3D%26test
`,
			expectedLen: 1,
			checkCookie: func(t *testing.T, cookies []*http.Cookie) {
				t.Helper()
				expected := "val%20ue%3D%26test"
				if cookies[0].Value != expected {
					t.Errorf("expected value '%s', got '%s'", expected, cookies[0].Value)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temp file
			cookieFile := filepath.Join(tmpDir, tt.name+".txt")
			err := os.WriteFile(cookieFile, []byte(tt.content), 0o600)
			if err != nil {
				t.Fatalf("failed to create test file: %v", err)
			}

			cookies, err := LoadCookieFile(cookieFile)
			if (err != nil) != tt.wantErr {
				t.Errorf("LoadCookieFile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}

			if len(cookies) != tt.expectedLen {
				t.Errorf("expected %d cookies, got %d", tt.expectedLen, len(cookies))
			}

			if tt.checkCookie != nil && len(cookies) > 0 {
				tt.checkCookie(t, cookies)
			}
		})
	}
}

func TestLoadCookieFile_NonExistent(t *testing.T) {
	_, err := LoadCookieFile("/nonexistent/path/cookies.txt")
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

func TestExpandPath(t *testing.T) {
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
			result, err := ExpandPath(tt.input)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestGetCookiesForURL(t *testing.T) {
	cookies := []*http.Cookie{
		{Name: "session", Value: "123", Domain: ".example.com", Path: "/", Secure: true},
		{Name: "insecure", Value: "456", Domain: ".example.com", Path: "/", Secure: false},
		{Name: "other", Value: "789", Domain: ".other.com", Path: "/", Secure: false},
		{Name: "api", Value: "abc", Domain: "api.example.com", Path: "/v1", Secure: true},
		{Name: "expired", Value: "old", Domain: ".example.com", Path: "/", Expires: time.Now().Add(-24 * time.Hour)},
	}

	tests := []struct {
		name        string
		targetURL   string
		expectedLen int
		checkNames  []string
	}{
		{
			name:        "match https subdomain",
			targetURL:   "https://sub.example.com/path",
			expectedLen: 2,
			checkNames:  []string{"session", "insecure"},
		},
		{
			name:        "match http (no secure cookies)",
			targetURL:   "http://example.com/",
			expectedLen: 1,
			checkNames:  []string{"insecure"},
		},
		{
			name:        "different domain",
			targetURL:   "https://different.com/",
			expectedLen: 0,
		},
		{
			name:        "exact domain match",
			targetURL:   "https://api.example.com/v1/users",
			expectedLen: 3, // api, session, insecure
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u, _ := url.Parse(tt.targetURL)
			matching := GetCookiesForURL(cookies, u)

			if len(matching) != tt.expectedLen {
				t.Errorf("expected %d cookies, got %d", tt.expectedLen, len(matching))
				for _, c := range matching {
					t.Logf("  matched: %s", c.Name)
				}
			}

			if tt.checkNames != nil {
				for _, name := range tt.checkNames {
					found := false
					for _, c := range matching {
						if c.Name == name {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected cookie '%s' to be in matching set", name)
					}
				}
			}
		})
	}
}

func TestParseCookieLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		want    *http.Cookie
		wantErr bool
	}{
		{
			name: "standard cookie",
			line: ".example.com\tTRUE\t/\tTRUE\t1735689600\tsession\tvalue",
			want: &http.Cookie{
				Name:    "session",
				Value:   "value",
				Domain:  ".example.com",
				Path:    "/",
				Secure:  true,
				Expires: time.Unix(1735689600, 0),
			},
		},
		{
			name: "insecure cookie",
			line: "example.com\tFALSE\t/path\tFALSE\t1735689600\tname\tval",
			want: &http.Cookie{
				Name:    "name",
				Value:   "val",
				Domain:  "example.com",
				Path:    "/path",
				Secure:  false,
				Expires: time.Unix(1735689600, 0),
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
			got, err := parseCookieLine(tt.line)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseCookieLine() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}

			if got.Name != tt.want.Name {
				t.Errorf("Name = %s, want %s", got.Name, tt.want.Name)
			}
			if got.Value != tt.want.Value {
				t.Errorf("Value = %s, want %s", got.Value, tt.want.Value)
			}
			if got.Domain != tt.want.Domain {
				t.Errorf("Domain = %s, want %s", got.Domain, tt.want.Domain)
			}
			if got.Path != tt.want.Path {
				t.Errorf("Path = %s, want %s", got.Path, tt.want.Path)
			}
			if got.Secure != tt.want.Secure {
				t.Errorf("Secure = %v, want %v", got.Secure, tt.want.Secure)
			}
		})
	}
}

func TestCheckCookieFilePermissions(t *testing.T) {
	tmpDir := t.TempDir()

	t.Run("secure permissions", func(t *testing.T) {
		secureFile := filepath.Join(tmpDir, "secure_cookies.txt")
		err := os.WriteFile(secureFile, []byte("test"), 0o600)
		if err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		err = CheckCookieFilePermissions(secureFile)
		if err != nil {
			t.Errorf("expected no error for secure file, got: %v", err)
		}
	})

	t.Run("insecure permissions", func(t *testing.T) {
		insecureFile := filepath.Join(tmpDir, "insecure_cookies.txt")
		err := os.WriteFile(insecureFile, []byte("test"), 0o644)
		if err != nil {
			t.Fatalf("failed to create file: %v", err)
		}

		err = CheckCookieFilePermissions(insecureFile)
		if err == nil {
			t.Error("expected error for insecure file permissions")
		}
	})
}
