package config

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// LoadCookieFile parses a Netscape/Mozilla format cookie file and returns cookies.
// The file format follows the specification at https://curl.se/docs/http-cookies.html:
// domain	flag	path	secure	expiration	name	value
//
// Lines starting with # are comments and are ignored.
// Lines with fewer than 7 fields or empty lines are skipped.
func LoadCookieFile(path string) ([]*http.Cookie, error) {
	// Expand ~ to home directory
	expandedPath, err := expandPath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to expand cookie file path: %w", err)
	}

	file, err := os.Open(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cookie file: %w", err)
	}
	defer file.Close()

	var cookies []*http.Cookie
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Skip empty lines
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Handle #HttpOnly_ prefix - this is a valid cookie with HttpOnly flag
		// See https://curl.se/docs/http-cookies.html
		httpOnly := false
		if trimmed, found := strings.CutPrefix(line, "#HttpOnly_"); found {
			line = trimmed
			httpOnly = true
		} else if strings.HasPrefix(line, "#") {
			// Skip regular comments
			continue
		}

		cookie, err := parseCookieLine(line, httpOnly)
		if err != nil {
			// Log malformed entries for debugging but continue processing
			dbg.Debugf("cookie file %s:%d: skipping malformed entry: %v", expandedPath, lineNum, err)
			continue
		}

		cookies = append(cookies, cookie)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading cookie file: %w", err)
	}

	return cookies, nil
}

// parseCookieLine parses a single line from a Netscape format cookie file.
// Format: domain	flag	path	secure	expiration	name	value
// Fields are tab-separated.
func parseCookieLine(line string, httpOnly bool) (*http.Cookie, error) {
	// Split by tabs
	fields := strings.Split(line, "\t")
	if len(fields) < 7 {
		return nil, fmt.Errorf("invalid cookie line: expected 7 fields, got %d", len(fields))
	}

	domain := fields[0]
	// fields[1] is the "include subdomains" flag (TRUE/FALSE) - not used in http.Cookie directly
	path := fields[2]
	secure := strings.EqualFold(fields[3], "TRUE")

	expirationTS, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid expiration timestamp: %w", err)
	}

	// Parse expiration: 0 means session cookie (no expiration)
	var expires time.Time
	if expirationTS != 0 {
		expires = time.Unix(expirationTS, 0)
	}

	name := fields[5]
	value := fields[6]

	// Handle value that may contain tabs (rejoin any remaining fields)
	if len(fields) > 7 {
		value = strings.Join(fields[6:], "\t")
	}

	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		Domain:   domain,
		Secure:   secure,
		HttpOnly: httpOnly,
		Expires:  expires,
	}, nil
}

// expandPath expands ~ to home directory and environment variables.
func expandPath(path string) (string, error) {
	if path == "" {
		return "", nil
	}

	// Expand environment variables
	path = os.ExpandEnv(path)

	// Expand ~ to home directory
	if strings.HasPrefix(path, "~") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("failed to get home directory: %w", err)
		}
		path = home + path[1:]
	}

	return path, nil
}
