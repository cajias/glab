package api

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"
)

// transportKey holds every input newTransport reads, so two clients with equal
// keys can share one connection pool.
type transportKey struct {
	// proxyURL is empty when the proxy comes from the environment.
	proxyURL       string
	caFile         string
	clientCertFile string
	clientKeyFile  string
	insecure       bool
	gitlabCom      bool
}

// TransportCache lets API clients with identical transport settings share a
// connection pool, so requests to the same host reuse a warm connection
// instead of paying for a new TLS handshake.
// Safe for concurrent use.
type TransportCache struct {
	mu    sync.Mutex
	byKey map[transportKey]*http.Transport
}

func NewTransportCache() *TransportCache {
	return &TransportCache{
		byKey: make(map[transportKey]*http.Transport),
	}
}

func (tc *TransportCache) transport(key transportKey) (*http.Transport, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if t, ok := tc.byKey[key]; ok {
		return t, nil
	}

	t, err := newTransport(key)
	if err != nil {
		return nil, err
	}

	tc.byKey[key] = t
	return t, nil
}

func newTransport(key transportKey) (*http.Transport, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: key.insecure,
	}

	if key.gitlabCom {
		tlsConfig.CipherSuites = secureCipherSuites
	}

	if key.caFile != "" {
		caCert, err := os.ReadFile(key.caFile)
		if err != nil {
			return nil, fmt.Errorf("error reading cert file: %w", err)
		}
		// use system cert pool as a baseline
		caCertPool, err := x509.SystemCertPool()
		if err != nil {
			return nil, err
		}
		caCertPool.AppendCertsFromPEM(caCert)
		tlsConfig.RootCAs = caCertPool
	}

	if key.clientCertFile != "" && key.clientKeyFile != "" {
		clientCert, err := tls.LoadX509KeyPair(key.clientCertFile, key.clientKeyFile)
		if err != nil {
			return nil, err
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	// Set appropriate timeouts based on whether custom CA is used
	dialTimeout := 5 * time.Second
	keepAlive := 5 * time.Second
	idleTimeout := 30 * time.Second
	if key.caFile != "" {
		dialTimeout = 30 * time.Second
		keepAlive = 30 * time.Second
		idleTimeout = 90 * time.Second
	}

	proxy := http.ProxyFromEnvironment
	if key.proxyURL != "" {
		u, err := url.Parse(key.proxyURL)
		if err != nil {
			return nil, err
		}
		proxy = http.ProxyURL(u)
	}

	return &http.Transport{
		Proxy: proxy,
		DialContext: (&net.Dialer{
			Timeout:   dialTimeout,
			KeepAlive: keepAlive,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   25,
		IdleConnTimeout:       idleTimeout,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       tlsConfig,
	}, nil
}

// pooledTransport hides CloseIdleConnections, which go-retryablehttp calls
// after every failed request, so that one client's failure does not drop the
// connections other clients share.
type pooledTransport struct {
	rt http.RoundTripper
}

func (t *pooledTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return t.rt.RoundTrip(req)
}
