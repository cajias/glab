package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

// ssoTransport handles SSO authentication redirects for all HTTP methods and preserves
// HTTP methods for mutating requests during same-host redirects (301/302/303).
//
// For cross-host redirects (SSO): completes SSO flow with GET, then retries original request.
// For same-host redirects on mutating methods: follows redirect preserving method and body.
type ssoTransport struct {
	rt             http.RoundTripper
	ssoClient      *http.Client
	allowedDomains map[string]struct{}
}

const maxRedirects = 10
const ssoTimeout = 30 * time.Second
const maxBodySize = 10 * 1024 * 1024 // 10 MB

// requiresMethodPreservation returns true for status codes where Go's http.Client
// converts POST to GET (301/302/303). 307/308 already preserve the method.
func requiresMethodPreservation(statusCode int) bool {
	return statusCode == http.StatusMovedPermanently || // 301
		statusCode == http.StatusFound || // 302
		statusCode == http.StatusSeeOther // 303
}

// isSSORedirect returns true if the redirect goes to a different host (IdP).
func isSSORedirect(originalHost, originalScheme, locationHeader string) bool {
	if locationHeader == "" {
		return false
	}
	redirectURL, err := url.Parse(locationHeader)
	if err != nil || redirectURL.Host == "" {
		return false
	}
	return normalizeHost(redirectURL.Host, redirectURL.Scheme) != normalizeHost(originalHost, originalScheme)
}

// normalizeHost removes default ports for comparison (e.g., :443 for HTTPS).
func normalizeHost(host, scheme string) string {
	hostname, port, err := net.SplitHostPort(host)
	if err != nil {
		return strings.ToLower(host)
	}
	if (scheme == "https" && port == "443") || (scheme == "http" && port == "80") {
		return strings.ToLower(hostname)
	}
	return strings.ToLower(host)
}

// isLocalhost returns true for literal localhost/loopback addresses.
// DNS resolution is not performed to prevent DNS rebinding attacks.
func isLocalhost(hostname string) bool {
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

// storeCookies stores Set-Cookie headers from resp into the cookie jar.
func (t *ssoTransport) storeCookies(resp *http.Response, u *url.URL) {
	if t.ssoClient == nil || t.ssoClient.Jar == nil {
		return
	}
	if cookies := resp.Cookies(); len(cookies) > 0 {
		t.ssoClient.Jar.SetCookies(u, cookies)
	}
}

func drainAndClose(resp *http.Response) {
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// copyHeaders copies headers from src to dst, excluding Cookie, Content-Length,
// and auth headers (Authorization, Private-Token) to prevent credential leakage
// on cross-host redirects. Use copyAuthHeaders for same-host requests.
func copyHeaders(src, dst *http.Request) {
	for key, values := range src.Header {
		switch strings.ToLower(key) {
		case "cookie", "content-length", "authorization", "private-token":
			continue
		}
		dst.Header[key] = values
	}
}

// copyAuthHeaders copies authentication headers from src to dst.
// Only call for same-host requests where credentials should be preserved.
func copyAuthHeaders(src, dst *http.Request) {
	for _, key := range []string{"Authorization", "Private-Token"} {
		if values, ok := src.Header[key]; ok {
			dst.Header[key] = values
		}
	}
}

func isMutatingMethod(method string) bool {
	return method == http.MethodPost || method == http.MethodPut ||
		method == http.MethodPatch || method == http.MethodDelete
}

func (t *ssoTransport) isDomainAllowed(domain string) bool {
	_, ok := t.allowedDomains[domain]
	return ok
}

// RoundTrip performs the request and handles SSO/same-host redirects for 301/302/303.
func (t *ssoTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Buffer the body upfront for mutating methods so it can be replayed after redirects.
	// This must happen before rt.RoundTrip, which consumes the body.
	var bodyBytes []byte
	if req.Body != nil && isMutatingMethod(req.Method) {
		var err error
		bodyBytes, err = io.ReadAll(io.LimitReader(req.Body, maxBodySize+1))
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
		if len(bodyBytes) > maxBodySize {
			return nil, fmt.Errorf("request body exceeds maximum size of %d bytes for SSO redirect replay", maxBodySize)
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	resp, err := t.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	if !requiresMethodPreservation(resp.StatusCode) {
		return resp, nil
	}

	// Store cookies from redirect response (RoundTrip doesn't store cookies in jar)
	t.storeCookies(resp, req.URL)

	location := resp.Header.Get("Location")
	dbg.Debugf("ssoTransport: %s %s -> %d %s", req.Method, req.URL, resp.StatusCode, location)

	if isSSORedirect(req.URL.Host, req.URL.Scheme, location) {
		return t.handleSSORedirect(req, resp, location, bodyBytes)
	}

	// GET/HEAD same-host redirects are handled correctly by http.Client
	if !isMutatingMethod(req.Method) {
		return resp, nil
	}

	drainAndClose(resp)
	return t.followRedirects(req.Context(), req, req.URL, location, bodyBytes, true)
}

// handleSSORedirect completes the SSO flow with a GET and retries the original request.
func (t *ssoTransport) handleSSORedirect(req *http.Request, resp *http.Response, location string, bodyBytes []byte) (*http.Response, error) {
	if resp != nil {
		drainAndClose(resp)
	}

	redirectURL, err := req.URL.Parse(location)
	if err != nil {
		return nil, fmt.Errorf("failed to parse redirect URL: %w", err)
	}

	// Enforce HTTPS (except localhost for tests/development)
	if redirectURL.Scheme != "https" && !isLocalhost(redirectURL.Hostname()) {
		return nil, fmt.Errorf("SSO redirect rejected: refusing non-HTTPS redirect to %s://%s (HTTPS required for security; HTTP is only allowed for localhost)", redirectURL.Scheme, redirectURL.Host)
	}

	redirectHost := redirectURL.Hostname()
	if !t.isDomainAllowed(redirectHost) {
		return nil, fmt.Errorf("SSO redirect to %s requires consent; configure sso_domain in your glab config: glab config set sso_domain %s -h <hostname>", redirectHost, redirectHost)
	}

	ctx := req.Context()
	ssoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, redirectURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSO request: %w", err)
	}

	// Follow SSO redirects until redirected back to the original API path
	originalHost := normalizeHost(req.URL.Host, req.URL.Scheme)
	originalPath := req.URL.Path
	ssoFlowClient := &http.Client{
		Transport: t.ssoClient.Transport,
		Jar:       t.ssoClient.Jar,
		Timeout:   t.ssoClient.Timeout,
		CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if normalizeHost(r.URL.Host, r.URL.Scheme) == originalHost && r.URL.Path == originalPath {
				return http.ErrUseLastResponse
			}
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		},
	}

	dbg.Debugf("ssoTransport: SSO flow GET %s", redirectURL)
	ssoResp, err := ssoFlowClient.Do(ssoReq)
	if err != nil {
		return nil, fmt.Errorf("SSO flow request failed: %w", err)
	}
	defer drainAndClose(ssoResp)

	// Store cookies from final redirect (not auto-stored with ErrUseLastResponse)
	cookieURL := req.URL
	if loc := ssoResp.Header.Get("Location"); loc != "" {
		if parsed, err := url.Parse(loc); err == nil && parsed.Host != "" {
			cookieURL = parsed
		}
	}
	t.storeCookies(ssoResp, cookieURL)

	if ssoResp.StatusCode >= http.StatusBadRequest {
		return nil, fmt.Errorf("SSO authentication failed: IdP returned status %d; cookies may be expired or invalid", ssoResp.StatusCode)
	}

	dbg.Debugf("ssoTransport: SSO complete (%d), retrying %s %s", ssoResp.StatusCode, req.Method, req.URL)

	// Retry original request with fresh cookies from the jar
	retryReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create retry request: %w", err)
	}
	copyHeaders(req, retryReq)
	copyAuthHeaders(req, retryReq) // same-host retry: preserve auth credentials

	// Use a no-redirect client so we can handle redirects with method preservation
	retryClient := &http.Client{
		Transport:     t.ssoClient.Transport,
		Jar:           t.ssoClient.Jar,
		Timeout:       t.ssoClient.Timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	retryResp, err := retryClient.Do(retryReq)
	if err != nil {
		return nil, fmt.Errorf("retry %s %s failed after SSO: %w", req.Method, req.URL, err)
	}

	// If not a method-changing redirect (301/302/303), return directly
	if !requiresMethodPreservation(retryResp.StatusCode) {
		return retryResp, nil
	}

	retryLocation := retryResp.Header.Get("Location")
	if retryLocation == "" {
		return retryResp, nil
	}

	t.storeCookies(retryResp, retryReq.URL)
	drainAndClose(retryResp)

	// Follow post-retry redirects (disallow SSO to prevent infinite loops)
	return t.followRedirects(ctx, req, retryReq.URL, retryLocation, bodyBytes, false)
}

// followRedirects follows same-host redirects while preserving the HTTP method.
// If allowSSO is true, cross-host redirects trigger SSO handling; otherwise they error.
func (t *ssoTransport) followRedirects(ctx context.Context, origReq *http.Request, currentURL *url.URL, location string, bodyBytes []byte, allowSSO bool) (*http.Response, error) {
	for redirectCount := range maxRedirects {
		redirectURL, err := currentURL.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redirect URL: %w", err)
		}

		if isSSORedirect(origReq.URL.Host, origReq.URL.Scheme, redirectURL.String()) {
			if allowSSO {
				return t.handleSSORedirect(origReq, nil, redirectURL.String(), bodyBytes)
			}
			return nil, fmt.Errorf("unexpected SSO redirect from %s to %s during retry; try 'glab auth login' to re-authenticate", currentURL.Host, redirectURL.Host)
		}

		redirectReq, err := http.NewRequestWithContext(ctx, origReq.Method, redirectURL.String(), bytes.NewReader(bodyBytes))
		if err != nil {
			return nil, fmt.Errorf("failed to create redirect request: %w", err)
		}
		copyHeaders(origReq, redirectReq)
		copyAuthHeaders(origReq, redirectReq) // same-host redirect: preserve auth credentials

		// Add cookies from jar (RoundTrip doesn't consult the jar)
		if t.ssoClient != nil && t.ssoClient.Jar != nil {
			for _, cookie := range t.ssoClient.Jar.Cookies(redirectReq.URL) {
				redirectReq.AddCookie(cookie)
			}
		}

		dbg.Debugf("ssoTransport: redirect #%d: %s %s", redirectCount+1, origReq.Method, redirectURL)
		resp, err := t.rt.RoundTrip(redirectReq)
		if err != nil {
			return nil, fmt.Errorf("redirect to %s failed: %w", redirectURL, err)
		}

		t.storeCookies(resp, redirectReq.URL)

		if !requiresMethodPreservation(resp.StatusCode) {
			return resp, nil
		}

		location = resp.Header.Get("Location")
		if location == "" {
			return resp, nil
		}

		drainAndClose(resp)
		currentURL = redirectURL
	}

	return nil, fmt.Errorf("stopped after %d redirects", maxRedirects)
}
