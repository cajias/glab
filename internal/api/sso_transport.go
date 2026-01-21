package api

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// ssoTransport is an http.RoundTripper that handles SSO authentication redirects
// and preserves HTTP methods for mutating requests during redirects.
//
// When a mutating request (POST/PUT/PATCH/DELETE) receives a redirect response
// to a different host (typically an IdP for SSO), this transport completes the
// SSO flow with a GET request and retries the original request.
//
// For same-host redirects, it follows the redirect while preserving the original
// HTTP method and request body. This is necessary because Go's http.Client
// converts POST to GET for 302/303 redirects by default (per HTTP spec), which
// breaks APIs that use redirects while expecting the method to be preserved.
//
// This allows all HTTP requests (including those from the gitlab.Client library)
// to automatically handle SSO authentication and method-preserving redirects
// without requiring special handling at the caller level.

type ssoTransport struct {
	// rt is the underlying RoundTripper (typically http.Transport)
	rt http.RoundTripper
	// ssoClient is used for SSO flow and retry requests.
	// It shares the same cookie jar but uses the underlying transport.
	ssoClient *http.Client
	// mu protects allowedDomains from concurrent access
	mu sync.RWMutex
	// allowedDomains tracks domains the user has approved (from config).
	allowedDomains map[string]struct{}
}

// maxRedirects is the maximum number of redirects to follow for same-host redirects.
// This matches the default limit used by Go's http.Client and provides a reasonable
// balance between following legitimate redirect chains and preventing infinite loops.
const maxRedirects = 10

// ssoTimeout is the timeout for SSO authentication requests.
// This provides a reasonable limit for completing the SSO flow with an external IdP
// while preventing indefinite hangs if the IdP is unresponsive.
const ssoTimeout = 30 * time.Second

// requiresMethodPreservation returns true if the status code is one that causes
// standard HTTP clients to convert POST to GET. Per HTTP spec:
// - 301, 302, 303: clients typically change POST to GET (historical behavior)
// - 307, 308: clients MUST preserve the original method
// We only need to intervene for 301/302/303; 307/308 already preserve the method.
func requiresMethodPreservation(statusCode int) bool {
	return statusCode == http.StatusMovedPermanently || // 301
		statusCode == http.StatusFound || // 302
		statusCode == http.StatusSeeOther // 303
}

// isSSORedirect returns true if the redirect is to a different host (IdP).
// It properly normalizes hosts by stripping default ports for the given scheme
// (e.g., gitlab.example.com:443 equals gitlab.example.com for HTTPS).
func isSSORedirect(originalHost, originalScheme, locationHeader string) bool {
	if locationHeader == "" {
		return false
	}

	// Parse the location header as a URL
	redirectURL, err := url.Parse(locationHeader)
	if err != nil {
		// If we can't parse it, treat it as relative (same host)
		dbg.Debugf("ssoTransport: failed to parse Location header %q: %v, treating as same-host", locationHeader, err)
		return false
	}

	// Relative URL - same host
	if redirectURL.Host == "" {
		return false
	}

	// Compare normalized hosts (hostname without default ports)
	return normalizeHost(redirectURL.Host, redirectURL.Scheme) != normalizeHost(originalHost, originalScheme)
}

// normalizeHost removes default ports from a host string for comparison.
// This ensures gitlab.example.com:443 equals gitlab.example.com for HTTPS.
// Known limitation: hostnames that resolve to loopback addresses are not detected.
func normalizeHost(host, scheme string) string {
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		// No port in the host string
		return strings.ToLower(host)
	}

	// Remove default ports for the scheme
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return strings.ToLower(hostname)
	}

	// Keep non-default ports
	return strings.ToLower(host)
}

// isLocalhost returns true if the hostname is a localhost/loopback address.
// This handles "localhost", IPv4 loopback (127.0.0.0/8), IPv6 loopback (::1),
// and hostnames that resolve to loopback addresses via DNS.
func isLocalhost(hostname string) bool {
	if hostname == "localhost" {
		return true
	}

	// Check if it's a literal IP address
	ip := net.ParseIP(hostname)
	if ip != nil {
		return ip.IsLoopback()
	}

	// Try to resolve the hostname to IP addresses
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return false
	}

	// Check if any resolved IP is a loopback address
	for _, ip := range ips {
		if ip.IsLoopback() {
			return true
		}
	}

	return false
}

// RoundTrip implements http.RoundTripper.
// It performs the request and handles redirects for mutating methods:
// - For cross-host (SSO) redirects: completes SSO flow with GET, then retries original request
// - For same-host redirects: follows redirect while preserving HTTP method and body
func (t *ssoTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Save request body for potential retry (only for mutating methods)
	var bodyBytes []byte
	if req.Body != nil && isMutatingMethod(req.Method) {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Perform the request using the underlying transport
	resp, err := t.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Check if this is a redirect that requires method preservation for a mutating method.
	// We only intervene for 301/302/303 redirects since 307/308 already preserve the method.
	if !requiresMethodPreservation(resp.StatusCode) || !isMutatingMethod(req.Method) {
		return resp, nil
	}

	// Store cookies from the redirect response.
	// We must do this manually because RoundTrip() doesn't store cookies in the jar.
	// This is critical for SSO flows where GitLab sets session/OAuth state cookies
	// in the redirect response that are needed for subsequent requests.
	if t.ssoClient != nil && t.ssoClient.Jar != nil {
		if cookies := resp.Cookies(); len(cookies) > 0 {
			t.ssoClient.Jar.SetCookies(req.URL, cookies)
		}
	}

	location := resp.Header.Get("Location")
	dbg.Debugf("ssoTransport: %s %s received %d redirect to %s", req.Method, req.URL, resp.StatusCode, location)

	// Handle cross-host SSO redirects
	if isSSORedirect(req.URL.Host, req.URL.Scheme, location) {
		dbg.Debugf("ssoTransport: detected SSO redirect (cross-host)")
		return t.handleSSORedirect(req, resp, location, bodyBytes)
	}

	// Handle same-host redirects - follow the redirect while preserving the method
	dbg.Debugf("ssoTransport: following same-host redirect while preserving %s method", req.Method)
	return t.handleSameHostRedirect(req, resp, location, bodyBytes)
}

// handleSSORedirect handles cross-host redirects to an IdP for SSO authentication.
// It completes the SSO flow with a GET request and then retries the original request.
// The resp parameter may be nil if called from handleSameHostRedirect (where the
// response body has already been closed).
func (t *ssoTransport) handleSSORedirect(req *http.Request, resp *http.Response, location string, bodyBytes []byte) (*http.Response, error) {
	// Close the redirect response body first (if present)
	if resp != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	// Resolve the absolute URL for the redirect location
	redirectURL, err := req.URL.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redirect URL: %w", err)
	}

	// Enforce HTTPS for SSO redirects to prevent cookie theft via MITM.
	// Allow HTTP only for localhost (used in tests and development).
	if redirectURL.Scheme != "https" && !isLocalhost(redirectURL.Hostname()) {
		return nil, fmt.Errorf("SSO redirect rejected: refusing non-HTTPS redirect to %s://%s (HTTPS required for security; HTTP is only allowed for localhost)", redirectURL.Scheme, redirectURL.Host)
	}

	// Check if the IdP domain is pre-approved in config
	redirectHost := redirectURL.Hostname()
	if !t.isDomainAllowed(redirectHost) {
		return nil, fmt.Errorf("SSO redirect to %s requires consent; configure sso_domain in your glab config: glab config set sso_domain %s -h <hostname>", redirectHost, redirectHost)
	}

	// Complete SSO flow with a GET request (which the IdP expects)
	ctx := req.Context()
	ssoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, redirectURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSO request: %w", err)
	}

	// Create a client that follows all SSO redirects, including the OAuth callback on GitLab.
	// We only stop when the redirect target matches the original API request URL.
	// This ensures the OAuth callback (/oauth2/idpresponse) is called and sets the session cookie.
	originalHost := normalizeHost(req.URL.Host, req.URL.Scheme)
	originalPath := req.URL.Path
	ssoFlowClient := &http.Client{
		Transport: t.ssoClient.Transport,
		Jar:       t.ssoClient.Jar,
		Timeout:   t.ssoClient.Timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			redirectHost := normalizeHost(r.URL.Host, r.URL.Scheme)

			// Stop if we're being redirected to the exact original API path on the original host.
			// This happens after the OAuth callback completes and GitLab redirects to the original URL.
			if redirectHost == originalHost && r.URL.Path == originalPath {
				dbg.Debugf("ssoTransport: SSO flow complete, stopping redirect to original API path %s", originalPath)
				return http.ErrUseLastResponse
			}

			// Continue following other redirects (OAuth callback, etc.)
			dbg.Debugf("ssoTransport: following SSO redirect to %s%s", redirectHost, r.URL.Path)

			// Follow other redirects (up to the default limit)
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}

	dbg.Debugf("completing SSO flow: GET %s", redirectURL.String())
	ssoResp, err := ssoFlowClient.Do(ssoReq)
	if err != nil {
		return nil, fmt.Errorf("SSO flow request failed: %w", err)
	}
	// Ensure response body is always consumed and closed
	defer func() {
		_, _ = io.Copy(io.Discard, ssoResp.Body)
		ssoResp.Body.Close()
	}()

	// Store cookies from the final redirect response.
	// http.Client doesn't store cookies when CheckRedirect returns ErrUseLastResponse,
	// so we need to manually store any Set-Cookie headers from the redirect response.
	// These cookies (like session tokens) are critical for the retry request.
	if t.ssoClient.Jar != nil {
		if cookies := ssoResp.Cookies(); len(cookies) > 0 {
			// Determine the correct URL for storing cookies
			cookieURL := req.URL
			if locationHeader := ssoResp.Header.Get("Location"); locationHeader != "" {
				if parsedLocation, err := url.Parse(locationHeader); err != nil {
					dbg.Debugf("ssoTransport: failed to parse Location header for cookie storage: %v, using original URL", err)
				} else if parsedLocation.Host == "" {
					dbg.Debugf("ssoTransport: Location header has no host, using original URL for cookie storage")
				} else {
					cookieURL = parsedLocation
				}
			}
			t.ssoClient.Jar.SetCookies(cookieURL, cookies)
			dbg.Debugf("ssoTransport: stored %d cookies from SSO redirect response for %s", len(cookies), cookieURL.Host)
		}
	}

	// After the SSO flow, we expect either:
	// 1. A redirect (3xx) back to the original host (we stopped following it)
	// 2. A success response (2xx) if the IdP doesn't redirect back
	// Any 4xx/5xx response indicates the SSO flow failed at the IdP.
	if ssoResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("SSO authentication failed: IdP returned status %d; cookies may be expired or invalid", ssoResp.StatusCode)
	}

	dbg.Debugf("ssoTransport: SSO flow completed with status %d, retrying original %s request", ssoResp.StatusCode, req.Method)

	// Retry the original request with fresh cookies from the jar.
	// CRITICAL: Do NOT copy the original Cookie header - let the cookie jar provide
	// the fresh session cookies that were set during the SSO flow.
	retryReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create retry request: %w", err)
	}

	// Copy headers, excluding those that should be managed by the HTTP client
	for key, values := range req.Header {
		if !shouldExcludeHeader(key) {
			retryReq.Header[key] = values
		}
	}

	// Create a retry client that doesn't follow redirects automatically.
	// This is necessary because if the retry POST gets a redirect (e.g., from GitLab
	// setting a session cookie), the default http.Client would follow it as a GET,
	// losing our POST method and body. By stopping at redirects, we can handle them
	// through our ssoTransport which preserves the HTTP method.
	retryClient := &http.Client{
		Transport: t.ssoClient.Transport,
		Jar:       t.ssoClient.Jar,
		Timeout:   t.ssoClient.Timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	dbg.Debugf("ssoTransport: retrying original request %s %s", req.Method, req.URL)
	retryResp, err := retryClient.Do(retryReq)
	if err != nil {
		return nil, fmt.Errorf("retry %s %s failed after SSO authentication: %w", req.Method, req.URL, err)
	}
	dbg.Debugf("ssoTransport: retry response status %d", retryResp.StatusCode)

	// Handle retry response, following any redirects while preserving the method.
	// This is a self-contained loop that doesn't call handleSameHostRedirect() to avoid
	// infinite recursion (since handleSameHostRedirect can detect SSO and call us back).
	currentResp := retryResp
	currentURL := retryReq.URL

	for redirectCount := range maxRedirects {
		// If we got a success or error response, return it
		if currentResp.StatusCode < 300 || currentResp.StatusCode >= 400 {
			dbg.Debugf("ssoTransport: retry completed with status %d", currentResp.StatusCode)
			return currentResp, nil
		}

		// Get redirect location
		location := currentResp.Header.Get("Location")
		if location == "" {
			return currentResp, nil
		}

		dbg.Debugf("ssoTransport: retry redirect #%d: %d to %s", redirectCount+1, currentResp.StatusCode, location)

		// Store cookies from the redirect response
		if t.ssoClient.Jar != nil {
			if cookies := currentResp.Cookies(); len(cookies) > 0 {
				t.ssoClient.Jar.SetCookies(currentURL, cookies)
			}
		}

		// Close the response body before following redirect
		_, _ = io.Copy(io.Discard, currentResp.Body)
		currentResp.Body.Close()

		// Parse redirect URL
		redirectURL, err := currentURL.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redirect URL: %w", err)
		}

		// Check if this redirect goes to a different host (would be SSO again)
		// If so, we've hit an unexpected case - return an error to avoid infinite loops
		if isSSORedirect(currentURL.Host, currentURL.Scheme, redirectURL.String()) {
			return nil, fmt.Errorf("unexpected SSO redirect from %s to %s during retry; your session may have expired, try 'glab auth login' to re-authenticate", currentURL.Host, redirectURL.Host)
		}

		// Create a new request with the same method and body
		nextReq, err := http.NewRequestWithContext(ctx, req.Method, redirectURL.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to create redirect request: %w", err)
		}

		// Copy headers, excluding Cookie (will be added from jar) and Content-Length
		for key, values := range req.Header {
			if !shouldExcludeHeader(key) {
				nextReq.Header[key] = values
			}
		}

		// Make the request (using retryClient which doesn't follow redirects)
		currentResp, err = retryClient.Do(nextReq)
		if err != nil {
			return nil, fmt.Errorf("redirect #%d to %s failed: %w", redirectCount+1, redirectURL, err)
		}

		currentURL = redirectURL
	}

	return nil, fmt.Errorf("stopped after %d redirects during retry", maxRedirects)
}

// handleSameHostRedirect handles same-host redirects while preserving the HTTP method and body.
// This prevents the default HTTP client behavior of converting POST to GET for 302/303 redirects.
func (t *ssoTransport) handleSameHostRedirect(req *http.Request, resp *http.Response, location string, bodyBytes []byte) (*http.Response, error) {
	dbg.Debugf("ssoTransport: handling same-host redirect: %s %s -> %s (status %d)", req.Method, req.URL, location, resp.StatusCode)

	// Close the redirect response body first
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ctx := req.Context()
	currentURL := req.URL

	// Follow redirects, preserving the method
	for redirectCount := range maxRedirects {
		// Resolve the redirect URL
		redirectURL, err := currentURL.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redirect URL: %w", err)
		}

		// Check if this redirect goes to a different host (SSO)
		if isSSORedirect(req.URL.Host, req.URL.Scheme, redirectURL.String()) {
			// This is now an SSO redirect - handle it
			return t.handleSSORedirect(req, nil, redirectURL.String(), bodyBytes)
		}

		// Create a new request with the same method and body.
		// We create a fresh bytes.Reader for each redirect because the reader
		// position needs to be reset to read the body again for each request.
		redirectReq, err := http.NewRequestWithContext(ctx, req.Method, redirectURL.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to create redirect request: %w", err)
		}

		// Copy headers, excluding those that should be managed separately:
		// - Cookie: added from jar below (RoundTrip doesn't consult the jar)
		// - Content-Length: will be recalculated based on the body
		for key, values := range req.Header {
			if !shouldExcludeHeader(key) {
				redirectReq.Header[key] = values
			}
		}

		// Add cookies from the jar to the request.
		// We must do this manually because RoundTrip() doesn't consult the cookie jar.
		if t.ssoClient.Jar != nil {
			for _, cookie := range t.ssoClient.Jar.Cookies(redirectReq.URL) {
				redirectReq.AddCookie(cookie)
			}
		}

		// Perform the redirect request
		dbg.Debugf("ssoTransport: same-host redirect #%d: %s %s", redirectCount+1, req.Method, redirectURL)
		resp, err = t.rt.RoundTrip(redirectReq)
		if err != nil {
			return nil, fmt.Errorf("same-host redirect to %s failed: %w", redirectReq.URL, err)
		}
		dbg.Debugf("ssoTransport: same-host redirect #%d response: %d", redirectCount+1, resp.StatusCode)

		// Store cookies from the redirect response.
		// We must do this manually because RoundTrip() doesn't store cookies in the jar.
		if t.ssoClient != nil && t.ssoClient.Jar != nil {
			if cookies := resp.Cookies(); len(cookies) > 0 {
				t.ssoClient.Jar.SetCookies(redirectReq.URL, cookies)
			}
		}

		// Check if we need to follow another redirect (only for 301/302/303)
		if !requiresMethodPreservation(resp.StatusCode) {
			dbg.Debugf("ssoTransport: same-host redirect complete, final status %d", resp.StatusCode)
			return resp, nil
		}

		// Get the next location and continue the loop
		location = resp.Header.Get("Location")
		if location == "" {
			// No location header - return the response as is
			return resp, nil
		}

		// Close the response body before following the next redirect
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()

		currentURL = redirectURL
	}

	return nil, fmt.Errorf("stopped after %d redirects", maxRedirects)
}

// shouldExcludeHeader returns true if the header should not be copied to redirect requests.
// These headers are either managed by the HTTP client or need to be recalculated.
func shouldExcludeHeader(key string) bool {
	return strings.EqualFold(key, "Cookie") || strings.EqualFold(key, "Content-Length")
}

// isMutatingMethod returns true if the HTTP method modifies data (POST, PUT, PATCH, DELETE).
func isMutatingMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut ||
		method == http.MethodPatch || method == http.MethodDelete
}

// isDomainAllowed checks if a domain has been approved for SSO redirect.
// Thread-safe.
func (t *ssoTransport) isDomainAllowed(domain string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.allowedDomains[domain]
	return ok
}
