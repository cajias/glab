package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"strings"
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
}

// maxRedirects is the maximum number of redirects to follow for same-host redirects.
// This matches the default limit used by Go's http.Client and provides a reasonable
// balance between following legitimate redirect chains and preventing infinite loops.
const maxRedirects = 10

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
func isSSORedirect(originalHost, locationHeader string) bool {
	if locationHeader == "" {
		return false
	}
	// Parse the location header to get the host
	// It could be a relative URL or an absolute URL
	if strings.HasPrefix(locationHeader, "http://") || strings.HasPrefix(locationHeader, "https://") {
		// Absolute URL - extract the host
		urlWithoutScheme := strings.TrimPrefix(strings.TrimPrefix(locationHeader, "https://"), "http://")
		// Get the host part (before the first /)
		redirectHost := urlWithoutScheme
		if idx := strings.Index(redirectHost, "/"); idx != -1 {
			redirectHost = redirectHost[:idx]
		}
		// Compare hosts including ports (127.0.0.1:8080 != 127.0.0.1:9090)
		return redirectHost != originalHost
	}
	// Relative URL - same host
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

	// Handle cross-host SSO redirects
	if isSSORedirect(req.URL.Host, location) {
		return t.handleSSORedirect(req, resp, location, bodyBytes)
	}

	// Handle same-host redirects - follow the redirect while preserving the method
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

	// Complete SSO flow with a GET request (which the IdP expects)
	ctx := req.Context()
	ssoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, redirectURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create SSO request: %w", err)
	}

	ssoResp, err := t.ssoClient.Do(ssoReq)
	if err != nil {
		return nil, fmt.Errorf("SSO flow request failed: %w", err)
	}
	// Consume and close the response body
	_, _ = io.Copy(io.Discard, ssoResp.Body)
	ssoResp.Body.Close()

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
	for redirectCount := 0; redirectCount < maxRedirects; redirectCount++ {
		// Resolve the redirect URL
		redirectURL, err := currentURL.Parse(location)
		if err != nil {
			return nil, fmt.Errorf("failed to parse redirect URL: %w", err)
		}

		// Check if this redirect goes to a different host (SSO)
		if isSSORedirect(req.URL.Host, redirectURL.String()) {
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
