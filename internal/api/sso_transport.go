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
	allowedDomains map[string]bool
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
		return nil, fmt.Errorf("SSO redirect rejected: refusing non-HTTPS redirect to %s://%s (HTTPS required for security)", redirectURL.Scheme, redirectURL.Host)
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

	dbg.Debugf("completing SSO flow: GET %s", redirectURL.String())
	ssoResp, err := t.ssoClient.Do(ssoReq)
	if err != nil {
		return nil, fmt.Errorf("SSO flow request failed: %w", err)
	}
	// Ensure response body is always consumed and closed
	defer func() {
		_, _ = io.Copy(io.Discard, ssoResp.Body)
		ssoResp.Body.Close()
	}()

	// Validate SSO response - if IdP returned an error, the SSO flow failed
	if ssoResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("SSO authentication failed: IdP returned status %d; cookies may be expired or invalid", ssoResp.StatusCode)
	}

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

	return t.ssoClient.Do(retryReq)
}

// handleSameHostRedirect handles same-host redirects while preserving the HTTP method and body.
// This prevents the default HTTP client behavior of converting POST to GET for 302/303 redirects.
func (t *ssoTransport) handleSameHostRedirect(req *http.Request, resp *http.Response, location string, bodyBytes []byte) (*http.Response, error) {
	// Close the redirect response body first
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	ctx := req.Context()
	currentURL := req.URL

	// Follow redirects, preserving the method
	for range maxRedirects {
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

		// Copy headers, excluding those that should be managed by the HTTP client:
		// - Cookie: let the cookie jar handle it
		// - Content-Length: will be recalculated based on the body
		for key, values := range req.Header {
			if !shouldExcludeHeader(key) {
				redirectReq.Header[key] = values
			}
		}

		// Perform the redirect request
		resp, err = t.rt.RoundTrip(redirectReq)
		if err != nil {
			return nil, err
		}

		// Check if we need to follow another redirect (only for 301/302/303)
		if !requiresMethodPreservation(resp.StatusCode) {
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
	return t.allowedDomains[domain]
}
