package api

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	gitlab "gitlab.com/gitlab-org/api/client-go"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/oauth2"
	"gitlab.com/gitlab-org/cli/internal/utils"
)

// ClientOption represents a function that configures a Client
type ClientOption func(*Client) error

type BuildInfo struct {
	Version, Commit, Platform, Architecture string
}

func (i BuildInfo) UserAgent() string {
	return fmt.Sprintf("glab/%s (%s, %s)", i.Version, i.Platform, i.Architecture)
}

// Client represents an argument to NewClient
type Client struct {
	// gitlabClient represents GitLab API client.
	gitlabClient *gitlab.Client
	// internal http client
	httpClient *http.Client
	// custom certificate
	caFile string
	// client certificate files
	clientCertFile string
	clientKeyFile  string
	// cookie file for IdP/SSO authentication
	cookieFile string
	// ssoPrompt is called to get user consent before SSO redirects
	ssoPrompt SSOConsentFunc
	// ssoPersist is called to save approved SSO domains to config
	ssoPersist SSOPersistFunc
	// ssoAllowedDomains are pre-approved SSO domains (loaded from config)
	ssoAllowedDomains map[string]bool

	baseURL    string
	authSource gitlab.AuthSource

	allowInsecure bool

	userAgent string

	customHeaders map[string]string
}

func (c *Client) HTTPClient() *http.Client {
	return c.httpClient
}

// AuthSource returns the auth source
// TODO: clarify use cases for this.
func (c *Client) AuthSource() gitlab.AuthSource {
	return c.authSource
}

// Lab returns the initialized GitLab client.
func (c *Client) Lab() *gitlab.Client {
	return c.gitlabClient
}

var secureCipherSuites = []uint16{
	tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
	tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
	tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
}

type newAuthSource func(c *http.Client) (authSource gitlab.AuthSource, err error)

// NewClient initializes a api client for use throughout glab.
func NewClient(newAuthSource newAuthSource, options ...ClientOption) (*Client, error) {
	// 0. initialize empty Client
	client := &Client{}

	// 1. apply provided option functions to populate client
	for _, option := range options {
		if err := option(client); err != nil {
			return nil, fmt.Errorf("failed to apply client option: %w", err)
		}
	}

	// 2. initialize HTTP client used by the auth source and by the GitLab client
	if err := client.initializeHTTPClient(); err != nil {
		return nil, err
	}

	// 3. initialize the auth source
	// We need to delay this because sources like OAuth2 need a valid
	// HTTP client to refresh the token.
	authSource, err := newAuthSource(client.httpClient)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize auth source: %w", err)
	}
	client.authSource = authSource

	// 4. initialize the GitLab client
	if client.gitlabClient != nil {
		return client, nil
	}

	if client.authSource == nil {
		return nil, errors.New("unable to initialize GitLab Client because no authentication source is provided. Login first")
	}

	gitlabClient, err := gitlab.NewAuthSourceClient(
		client.authSource,
		gitlab.WithHTTPClient(client.httpClient),
		gitlab.WithBaseURL(client.baseURL),
		gitlab.WithUserAgent(client.userAgent),
		gitlab.WithRequestOptions(gitlab.WithHeaders(client.customHeaders)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize GitLab client: %v", err)
	}

	client.gitlabClient = gitlabClient
	return client, nil
}

func (c *Client) initializeHTTPClient() error {
	if c.httpClient != nil {
		return nil
	}

	// Create TLS configuration based on client settings
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: c.allowInsecure,
	}

	// Set secure cipher suites for gitlab.com
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	if !glinstance.IsSelfHosted(u.Hostname()) {
		tlsConfig.CipherSuites = secureCipherSuites
	}

	// Configure custom CA if provided
	if c.caFile != "" {
		caCert, err := os.ReadFile(c.caFile)
		if err != nil {
			return fmt.Errorf("error reading cert file: %w", err)
		}
		// use system cert pool as a baseline
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return err
		}
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	// Configure client certificates if provided
	if c.clientCertFile != "" && c.clientKeyFile != "" {
		clientCert, err := tls.LoadX509KeyPair(c.clientCertFile, c.clientKeyFile)
		if err != nil {
			return err
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	// Set appropriate timeouts based on whether custom CA is used
	dialTimeout := 5 * time.Second
	keepAlive := 5 * time.Second
	idleTimeout := 30 * time.Second
	if c.caFile != "" {
		dialTimeout = 30 * time.Second
		keepAlive = 30 * time.Second
		idleTimeout = 90 * time.Second
	}

	var rt http.RoundTripper = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}

	if enabled, found := utils.IsEnvVarEnabled("GLAB_DEBUG_HTTP"); found && enabled {
		rt = &debugTransport{rt: rt, w: os.Stderr}
	}

	c.httpClient = &http.Client{Transport: rt}

	// Configure cookie jar and SSO redirect handling if cookie file is provided
	if c.cookieFile != "" {
		jar, err := c.createCookieJar()
		if err != nil {
			return fmt.Errorf("failed to create cookie jar: %w", err)
		}
		c.httpClient.Jar = jar

		// Limit redirects to prevent infinite loops.
		// Note: SSO redirects are handled by ssoTransport at the transport level,
		// so this CheckRedirect only needs to enforce the redirect limit.
		c.httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			if len(via) >= maxRedirects {
				return fmt.Errorf("stopped after %d redirects", maxRedirects)
			}
			return nil
		}

		// Create a separate ssoClient that uses the underlying transport directly.
		// This client shares the same cookie jar but doesn't use ssoTransport,
		// avoiding infinite loops when completing the SSO flow.
		ssoClient := &http.Client{
			Transport: rt, // Use underlying transport, NOT ssoTransport
			Jar:       jar,
			Timeout:   SSOTimeout,
			// Limit redirects to prevent infinite redirect loops during SSO flow
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return fmt.Errorf("stopped after %d redirects", maxRedirects)
				}
				return nil
			},
		}

		// Wrap the transport with ssoTransport for automatic SSO handling.
		// This ensures all requests (including those from gitlab.Client library)
		// automatically complete the SSO flow when needed.
		// The ssoTransport detects redirects at the response level (before http.Client
		// processes them with CheckRedirect), allowing it to handle SSO seamlessly.
		c.httpClient.Transport = &ssoTransport{
			rt:             rt,
			ssoClient:      ssoClient,
			promptForSSO:   c.ssoPrompt,
			persistDomain:  c.ssoPersist,
			allowedDomains: c.ssoAllowedDomains,
		}
	}

	return nil
}

// createCookieJar creates a cookie jar and loads cookies from the configured cookie file.
func (c *Client) createCookieJar() (http.CookieJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create cookie jar: %w", err)
	}

	cookies, err := config.LoadCookieFile(c.cookieFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load cookies from file: %w", err)
	}

	if len(cookies) == 0 {
		return nil, fmt.Errorf("cookie file %q contains no valid cookies; ensure it is in Netscape/Mozilla format with unexpired cookies", c.cookieFile)
	}

	// Group cookies by domain and add them to the jar.
	// We load ALL cookies because the request may be redirected to an identity
	// provider (e.g., SAML/SSO) on a different domain, and those redirects
	// need the IdP cookies to authenticate.
	domainCookies := make(map[string][]*http.Cookie)
	for _, cookie := range cookies {
		// Normalize domain - remove leading dot for URL construction
		domain := strings.TrimPrefix(cookie.Domain, ".")
		domainCookies[domain] = append(domainCookies[domain], cookie)
	}

	// Add cookies to jar for each domain
	for domain, domainCookieList := range domainCookies {
		domainURL, err := url.Parse("https://" + domain + "/")
		if err != nil {
			dbg.Debugf("skipping %d cookies for invalid domain %q: %v", len(domainCookieList), domain, err)
			continue
		}
		jar.SetCookies(domainURL, domainCookieList)
	}

	return jar, nil
}

// WithCustomHeaders is a ClientOption that sets custom headers
func WithCustomHeaders(headers map[string]string) ClientOption {
	return func(c *Client) error {
		c.customHeaders = headers
		return nil
	}
}

// WithCustomCA configures the client to use a custom CA certificate
func WithCustomCA(caFile string) ClientOption {
	return func(c *Client) error {
		c.caFile = caFile
		return nil
	}
}

// WithClientCertificate configures the client to use client certificates for mTLS
func WithClientCertificate(certFile, keyFile string) ClientOption {
	return func(c *Client) error {
		c.clientCertFile = certFile
		c.clientKeyFile = keyFile
		return nil
	}
}

// WithInsecureSkipVerify configures the client to skip TLS verification
func WithInsecureSkipVerify(skip bool) ClientOption {
	return func(c *Client) error {
		c.allowInsecure = skip
		return nil
	}
}

// WithHTTPClient configures the HTTP client
func WithHTTPClient(httpClient *http.Client) ClientOption {
	return func(c *Client) error {
		c.httpClient = httpClient
		return nil
	}
}

// WithGitLabClient configures the GitLab client
func WithGitLabClient(client *gitlab.Client) ClientOption {
	return func(c *Client) error {
		c.gitlabClient = client
		return nil
	}
}

// WithBaseURL configures the base URL for the GitLab instance
func WithBaseURL(baseURL string) ClientOption {
	return func(c *Client) error {
		c.baseURL = baseURL
		return nil
	}
}

// WithUserAgent configures the user agent to use
func WithUserAgent(userAgent string) ClientOption {
	return func(c *Client) error {
		c.userAgent = userAgent
		return nil
	}
}

// WithCookieFile configures the client to use cookies from a Netscape/Mozilla format cookie file.
// This is useful for GitLab instances behind identity providers requiring browser-based SAML authentication.
func WithCookieFile(cookieFile string) ClientOption {
	return func(c *Client) error {
		c.cookieFile = cookieFile
		return nil
	}
}

// WithSSOPrompt configures a callback function to prompt for user consent before SSO redirects.
// The function receives the IdP domain and should return (allowed, error).
// If not set, SSO redirects will fail with an error requiring interactive mode.
func WithSSOPrompt(fn SSOConsentFunc) ClientOption {
	return func(c *Client) error {
		c.ssoPrompt = fn
		return nil
	}
}

// WithSSOPersist configures a callback to save approved SSO domains to persistent storage.
// This is called after the user consents to an SSO redirect.
func WithSSOPersist(fn SSOPersistFunc) ClientOption {
	return func(c *Client) error {
		c.ssoPersist = fn
		return nil
	}
}

// WithSSOAllowedDomains configures pre-approved SSO domains (typically loaded from config).
// Redirects to these domains will not prompt for consent.
func WithSSOAllowedDomains(domains map[string]bool) ClientOption {
	return func(c *Client) error {
		c.ssoAllowedDomains = domains
		return nil
	}
}

// NewClientFromConfig initializes the global api with the config data.
// Deprecated: Use NewClientFromConfigWithIO for interactive SSO consent prompts.
func NewClientFromConfig(repoHost string, cfg config.Config, isGraphQL bool, userAgent string) (*Client, error) {
	return NewClientFromConfigWithIO(repoHost, cfg, isGraphQL, userAgent, nil)
}

// NewClientFromConfigWithIO initializes the api client with config data and optional iostreams.
// If io is provided and a cookie file is configured, SSO redirects will prompt for user consent.
func NewClientFromConfigWithIO(repoHost string, cfg config.Config, isGraphQL bool, userAgent string, io *iostreams.IOStreams) (*Client, error) {
	apiHost, _ := cfg.Get(repoHost, "api_host")
	if apiHost == "" {
		apiHost = repoHost
	}

	apiProtocol, _ := cfg.Get(repoHost, "api_protocol")
	if apiProtocol == "" {
		apiProtocol = glinstance.DefaultProtocol
	}

	isOAuth2Cfg, _ := cfg.Get(repoHost, "is_oauth2")

	token, _ := cfg.Get(repoHost, "token")
	jobToken, _ := cfg.Get(repoHost, "job_token")
	tlsVerify, _ := cfg.Get(repoHost, "skip_tls_verify")
	skipTlsVerify := tlsVerify == "true" || tlsVerify == "1"
	caCert, _ := cfg.Get(repoHost, "ca_cert")
	clientCert, _ := cfg.Get(repoHost, "client_cert")
	keyFile, _ := cfg.Get(repoHost, "client_key")
	cookieFile, _ := cfg.Get(repoHost, "cookie_file")

	// Build options based on configuration
	options := []ClientOption{
		WithUserAgent(userAgent),
	}

	// Resolve custom headers from config
	headers, err := config.ResolveCustomHeaders(cfg, repoHost)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve custom headers: %w", err)
	}
	if len(headers) > 0 {
		options = append(options, WithCustomHeaders(headers))
	}

	// determine auth source
	var newAuthSource newAuthSource
	switch {
	case isOAuth2Cfg == "true":
		if v, _ := cfg.Get(repoHost, "oauth2_refresh_token"); v == "" {
			if token == "" {
				return nil, errors.New("with OAuth2 is enabled and when no Refresh Token is available an OAuth2 Access Token is required")
			}

			newAuthSource = func(client *http.Client) (gitlab.AuthSource, error) {
				return oauth2AccessTokenOnlyAuthSource{token: token}, nil
			}
			break
		}

		newAuthSource = func(client *http.Client) (gitlab.AuthSource, error) {
			ts, err := oauth2.NewConfigTokenSource(cfg, client, glinstance.DefaultProtocol, repoHost)
			if err != nil {
				return nil, err
			}
			return gitlab.OAuthTokenSource{TokenSource: ts}, nil
		}
	case jobToken != "":
		newAuthSource = func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.JobTokenAuthSource{Token: jobToken}, nil
		}
	default:
		newAuthSource = func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: token}, nil
		}
	}

	var baseURL string
	if isGraphQL {
		baseURL = glinstance.GraphQLEndpoint(apiHost, apiProtocol)
	} else {
		baseURL = glinstance.APIEndpoint(apiHost, apiProtocol, "")
	}
	options = append(options, WithBaseURL(baseURL))

	if caCert != "" {
		options = append(options, WithCustomCA(caCert))
	}

	if clientCert != "" && keyFile != "" {
		options = append(options, WithClientCertificate(clientCert, keyFile))
	}

	if skipTlsVerify {
		options = append(options, WithInsecureSkipVerify(skipTlsVerify))
	}

	if cookieFile != "" {
		options = append(options, WithCookieFile(cookieFile))

		// Load pre-approved SSO domain from config
		ssoDomain, _ := cfg.Get(repoHost, "sso_domain")
		if ssoDomain != "" {
			options = append(options, WithSSOAllowedDomains(map[string]bool{ssoDomain: true}))
		}

		// Add SSO consent prompt if iostreams is available
		if io != nil {
			options = append(options, WithSSOPrompt(NewSSOPrompt(io)))

			// Add persist callback to save approved domains to config
			options = append(options, WithSSOPersist(func(domain string) error {
				return cfg.Set(repoHost, "sso_domain", domain)
			}))
		}
	}

	return NewClient(newAuthSource, options...)
}

func NewHTTPRequest(ctx context.Context, c *Client, method string, baseURL *url.URL, body io.Reader, headers []string, bodyIsJSON bool) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, baseURL.String(), body)
	if err != nil {
		return nil, err
	}

	for name, value := range c.customHeaders {
		req.Header.Set(name, value)
	}

	// Add any headers passed directly to this function
	for _, h := range headers {
		idx := strings.IndexRune(h, ':')
		if idx == -1 {
			return nil, fmt.Errorf("header %q requires a value separated by ':'", h)
		}
		name, value := h[0:idx], strings.TrimSpace(h[idx+1:])
		if strings.EqualFold(name, "Content-Length") {
			length, err := strconv.ParseInt(value, 10, 0)
			if err != nil {
				return nil, err
			}
			req.ContentLength = length
		} else {
			req.Header.Add(name, value)
		}
	}

	if bodyIsJSON && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	if c.Lab().UserAgent != "" {
		req.Header.Set("User-Agent", c.Lab().UserAgent)
	}

	name, value, err := c.authSource.Header(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set(name, value)

	return req, nil
}

// Is404 checks if the error represents a 404 response
func Is404(err error) bool {
	// If the error is a typed response
	if errResponse, ok := err.(*gitlab.ErrorResponse); ok &&
		errResponse.Response != nil &&
		errResponse.Response.StatusCode == http.StatusNotFound {
		return true
	}

	// This can also come back as a string 404 from gitlab client-go
	if err != nil && err.Error() == "404 Not Found" {
		return true
	}

	return false
}
