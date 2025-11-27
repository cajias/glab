package api

import (
	"bytes"
	"io"
	"net/http"
	"strings"
)

// ssoTransport is an http.RoundTripper that handles SSO authentication redirects.
// When a mutating request (POST/PUT/PATCH/DELETE) receives a redirect response
// to a different host (typically an IdP for SSO), this transport completes the
// SSO flow with a GET request and retries the original request.
//
// This allows all HTTP requests (including those from the gitlab.Client library)
// to automatically handle SSO authentication without requiring special handling
// at the caller level.
type ssoTransport struct {
	// rt is the underlying RoundTripper (typically http.Transport)
	rt http.RoundTripper
	// ssoClient is used for SSO flow and retry requests.
	// It shares the same cookie jar but uses the underlying transport.
	ssoClient *http.Client
}

// isRedirectResponse returns true if the response is a redirect status code.
func isRedirectResponse(statusCode int) bool {
	return statusCode >= 300 && statusCode < 400
}

// isSSOREdirect returns true if the redirect is to a different host (IdP).
func isSSOREdirect(originalHost, locationHeader string) bool {
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
// It performs the request and handles SSO redirects for mutating methods.
func (t *ssoTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Save request body for potential retry (only for mutating methods)
	var bodyBytes []byte
	if req.Body != nil && isMutatingMethod(req.Method) {
		var err error
		bodyBytes, err = io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		req.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	}

	// Perform the request using the underlying transport
	resp, err := t.rt.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	// Check if this is a redirect to a different host for a mutating method
	if !isRedirectResponse(resp.StatusCode) || !isMutatingMethod(req.Method) {
		return resp, nil
	}

	location := resp.Header.Get("Location")
	if !isSSOREdirect(req.URL.Host, location) {
		return resp, nil
	}

	// This is an SSO redirect - we need to complete the SSO flow
	// Close the redirect response body first
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Resolve the absolute URL for the redirect location
	redirectURL, err := req.URL.Parse(location)
	if err != nil {
		return nil, err
	}

	// Complete SSO flow with a GET request (which the IdP expects)
	ctx := req.Context()
	ssoReq, err := http.NewRequestWithContext(ctx, http.MethodGet, redirectURL.String(), nil)
	if err != nil {
		return nil, err
	}

	ssoResp, err := t.ssoClient.Do(ssoReq)
	if err != nil {
		return nil, err
	}
	// Consume and close the response body
	_, _ = io.Copy(io.Discard, ssoResp.Body)
	ssoResp.Body.Close()

	// Retry the original request with fresh cookies from the jar.
	// CRITICAL: Do NOT copy the original Cookie header - let the cookie jar provide
	// the fresh session cookies that were set during the SSO flow.
	retryReq, err := http.NewRequestWithContext(ctx, req.Method, req.URL.String(), bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	// Copy headers BUT NOT the Cookie header
	for key, values := range req.Header {
		if !strings.EqualFold(key, "Cookie") {
			retryReq.Header[key] = values
		}
	}

	return t.ssoClient.Do(retryReq)
}
