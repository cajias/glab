package config

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// LoadCookieFile parses a Netscape/Mozilla format cookie file and returns cookies.
// The file format follows the specification at https://curl.se/docs/http-cookies.html:
// domain	flag	path	secure	expiration	name	value
//
// Lines starting with # are comments and are ignored.
// Lines with fewer than 7 fields or empty lines are skipped.
func LoadCookieFile(path string) ([]*http.Cookie, error) {
	// Expand ~ to home directory
	expandedPath, err := ExpandPath(path)
	if err != nil {
		return nil, fmt.Errorf("failed to expand cookie file path: %w", err)
	}

	file, err := os.Open(expandedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open cookie file: %w", err)
	}
	defer file.Close()

	// Note: File permissions can be checked separately via CheckCookieFilePermissions() if desired.
	// We don't log here since we don't have access to the IO streams.

	var cookies []*http.Cookie
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		// Skip empty lines and comments
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		cookie, err := parseCookieLine(line)
		if err != nil {
			// Skip malformed entries but continue processing
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
func parseCookieLine(line string) (*http.Cookie, error) {
	// Split by tabs
	fields := strings.Split(line, "\t")
	if len(fields) < 7 {
		return nil, fmt.Errorf("invalid cookie line: expected 7 fields, got %d", len(fields))
	}

	domain := fields[0]
	// fields[1] is the "include subdomains" flag (TRUE/FALSE) - not used in http.Cookie directly
	path := fields[2]
	secure := strings.EqualFold(fields[3], "TRUE")

	expiration, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid expiration timestamp: %w", err)
	}

	name := fields[5]
	value := fields[6]

	// Handle value that may contain tabs (rejoin any remaining fields)
	if len(fields) > 7 {
		value = strings.Join(fields[6:], "\t")
	}

	cookie := &http.Cookie{
		Name:   name,
		Value:  value,
		Path:   path,
		Domain: domain,
		Secure: secure,
	}

	// Only set expiration if it's not 0 (session cookie)
	if expiration != 0 {
		cookie.Expires = time.Unix(expiration, 0)
	}

	return cookie, nil
}

// ExpandPath expands ~ to home directory and environment variables.
func ExpandPath(path string) (string, error) {
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

// GetCookiesForURL returns cookies that are valid for the given URL.
func GetCookiesForURL(cookies []*http.Cookie, targetURL *url.URL) []*http.Cookie {
	var matching []*http.Cookie

	for _, cookie := range cookies {
		if cookieMatchesURL(cookie, targetURL) {
			matching = append(matching, cookie)
		}
	}

	return matching
}

// cookieMatchesURL checks if a cookie should be sent to the given URL.
func cookieMatchesURL(cookie *http.Cookie, targetURL *url.URL) bool {
	// Check domain
	domain := cookie.Domain
	host := targetURL.Hostname()

	// Handle domain matching (with leading dot for subdomain matching)
	if strings.HasPrefix(domain, ".") {
		// Cookie applies to domain and all subdomains
		if !strings.HasSuffix(host, domain) && host != domain[1:] {
			return false
		}
	} else {
		// Exact domain match
		if host != domain {
			return false
		}
	}

	// Check path
	path := cookie.Path
	if path == "" {
		path = "/"
	}
	targetPath := targetURL.Path
	if targetPath == "" {
		targetPath = "/"
	}
	if !strings.HasPrefix(targetPath, path) {
		return false
	}

	// Check secure flag
	if cookie.Secure && targetURL.Scheme != "https" {
		return false
	}

	// Check expiration
	if !cookie.Expires.IsZero() && cookie.Expires.Before(time.Now()) {
		return false
	}

	return true
}

// CheckCookieFilePermissions checks if the cookie file has secure permissions.
// Returns an error if the file is readable by group or others.
func CheckCookieFilePermissions(path string) error {
	expandedPath, err := ExpandPath(path)
	if err != nil {
		return fmt.Errorf("failed to expand path: %w", err)
	}

	info, err := os.Stat(expandedPath)
	if err != nil {
		return fmt.Errorf("failed to stat file: %w", err)
	}

	mode := info.Mode().Perm()
	if mode&0o077 != 0 {
		return fmt.Errorf("cookie file %s has insecure permissions %o, should be 0600 or more restrictive", path, mode)
	}

	return nil
}
