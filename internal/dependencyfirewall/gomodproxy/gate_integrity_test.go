//go:build !integration

package gomodproxy

import (
	"context"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/goproxy/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/verdict"
)

type coordChecker struct {
	mu      sync.Mutex
	asked   []policy.Request
	blocked policy.Coordinate
}

func (c *coordChecker) Check(_ context.Context, req policy.Request) (policy.Result, error) {
	c.mu.Lock()
	c.asked = append(c.asked, req)
	c.mu.Unlock()
	if req.Coordinate == c.blocked {
		return policy.Result{Verdict: verdict.Blocked, Reason: "blocked by policy"}, nil
	}
	return policy.Result{}, nil
}

func (c *coordChecker) requests() []policy.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.asked)
}

func (c *coordChecker) coordinates() []policy.Coordinate {
	var coords []policy.Coordinate
	for _, r := range c.requests() {
		coords = append(coords, r.Coordinate)
	}
	return coords
}

func TestZipCheckSendsFullPolicyRequest(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{}
	s := startServer(t, checker, &backendStub{})

	res := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")

	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, []policy.Request{{
		Coordinate: policy.Coordinate{Ecosystem: "golang", Name: "github.com/pkg/errors", Version: "v0.9.1"},
		ProjectID:  "group/proj",
		Operation:  policy.Download,
	}}, checker.requests())
}

func TestPercentEncodedVersionCannotBypassGate(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{blocked: policy.Coordinate{
		Ecosystem: "golang", Name: "github.com/pkg/errors", Version: "v0.9.1",
	}}
	backend := &backendStub{body: "BLOCKED-MODULE-BYTES"}
	s := startServer(t, checker, backend)

	plain := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")
	require.Equal(t, http.StatusForbidden, plain.status, "the unencoded form must be blocked")

	encoded := get(t, s, "/github.com/pkg/errors/@v/v0%2e9%2e1.zip")
	assert.Equal(t, http.StatusNotFound, encoded.status)
	assert.NotContains(t, encoded.body, "BLOCKED-MODULE-BYTES")
	assert.Equal(t, 0, backend.count(), "a percent-encoded path must never reach the backend")
}

func TestPercentEncodedPathsAreRefused(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	for _, p := range []string{
		"/sumdb/%2e%2e/evil.com/@v/v1.0.0.zip",
		"/%2e%2e/evil.com/@v/v1.0.0.zip",
		"/evil.com/@v/..%2fv1.0.0.zip",
		"/evil.com/@v/v1%2e0%2e0.zip",
	} {
		res := get(t, s, p)
		assert.Equal(t, http.StatusNotFound, res.status, p)
	}
	assert.Zero(t, backend.count(), "no encoded path may reach the backend")
	assert.Empty(t, checker.coordinates(), "no encoded path may reach the firewall")
}

func TestCaseEncodedModulePathIsGated(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{blocked: policy.Coordinate{
		Ecosystem: "golang", Name: "github.com/BurntSushi/toml", Version: "v1.5.0",
	}}
	backend := &backendStub{body: "MODULE-BYTES"}
	s := startServer(t, checker, backend)

	info := get(t, s, "/github.com/%21burnt%21sushi/toml/@v/v1.6.0.info")
	assert.Equal(t, http.StatusOK, info.status)

	zip := get(t, s, "/github.com/%21burnt%21sushi/toml/@v/v1.6.0.zip")
	assert.Equal(t, http.StatusOK, zip.status)
	assert.Equal(t, "MODULE-BYTES", zip.body)

	blocked := get(t, s, "/github.com/%21burnt%21sushi/toml/@v/v1.5.0.zip")
	assert.Equal(t, http.StatusForbidden, blocked.status)
	assert.NotContains(t, blocked.body, "MODULE-BYTES")

	assert.Equal(t, []policy.Coordinate{
		{Ecosystem: "golang", Name: "github.com/BurntSushi/toml", Version: "v1.6.0"},
		{Ecosystem: "golang", Name: "github.com/BurntSushi/toml", Version: "v1.5.0"},
	}, checker.coordinates())
	assert.Equal(t, []string{
		"/github.com/!burnt!sushi/toml/@v/v1.6.0.info",
		"/github.com/!burnt!sushi/toml/@v/v1.6.0.zip",
	}, backend.paths, "the backend must read the '!' form the gate checked")
}

func TestCaseEncodingDoesNotReopenPercentBypass(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	for _, p := range []string{
		"/github.com/%21burnt%21sushi/toml/@v/v1%2e6%2e0.zip",
		"/github.com/%21burnt%21sushi/%2e%2e/evil.com/@v/v1.0.0.zip",
		"/github.com/%2521burnt/toml/@v/v1.6.0.zip",
	} {
		res := get(t, s, p)
		assert.Equal(t, http.StatusNotFound, res.status, p)
	}
	assert.Zero(t, backend.count(), "no other encoding may reach the backend")
	assert.Empty(t, checker.coordinates(), "no other encoding may reach the firewall")
}

type nopSeekCloser struct{ *strings.Reader }

func (nopSeekCloser) Close() error { return nil }

type fixtureFetcher struct{ zip string }

func (f fixtureFetcher) Query(_ context.Context, _, _ string) (string, time.Time, error) {
	return "v0.9.1", time.Unix(0, 0), nil
}

func (f fixtureFetcher) List(_ context.Context, _ string) ([]string, error) {
	return []string{"v0.9.1"}, nil
}

func (f fixtureFetcher) Download(_ context.Context, _, version string) (io.ReadSeekCloser, io.ReadSeekCloser, io.ReadSeekCloser, error) {
	return nopSeekCloser{strings.NewReader(`{"Version":"` + version + `","Time":"1970-01-01T00:00:00Z"}`)},
		nopSeekCloser{strings.NewReader("module github.com/pkg/errors\n")},
		nopSeekCloser{strings.NewReader(f.zip)},
		nil
}

func TestGateAgreesWithRealBackend(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{blocked: policy.Coordinate{
		Ecosystem: "golang", Name: "github.com/pkg/errors", Version: "v0.9.1",
	}}
	backend := &goproxy.Goproxy{Fetcher: fixtureFetcher{zip: "REAL-ZIP"}}
	s := startServer(t, checker, backend)

	blocked := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")
	assert.Equal(t, http.StatusForbidden, blocked.status)
	assert.NotContains(t, blocked.body, "REAL-ZIP")

	encoded := get(t, s, "/github.com/pkg/errors/@v/v0%2e9%2e1.zip")
	assert.Equal(t, http.StatusNotFound, encoded.status)
	assert.NotContains(t, encoded.body, "REAL-ZIP")

	assert.Equal(t, []policy.Coordinate{
		{Ecosystem: "golang", Name: "github.com/pkg/errors", Version: "v0.9.1"},
	}, checker.coordinates(), "gate and backend must agree on the coordinate")
}

func TestAllowedZipFlowsThroughRealBackend(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{}
	backend := &goproxy.Goproxy{Fetcher: fixtureFetcher{zip: "REAL-ZIP"}}
	s := startServer(t, checker, backend)

	res := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")
	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, "REAL-ZIP", res.body)

	info := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.info")
	assert.Equal(t, http.StatusOK, info.status)
	assert.Contains(t, info.body, "v0.9.1")
}

type countingHandler struct {
	next http.Handler
	hits atomic.Int64
}

func (c *countingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.hits.Add(1)
	c.next.ServeHTTP(w, r)
}

// Serving the .mod makes goproxy fetch and cache the zip too, so the gate,
// not the cache, must refuse the zip.
func TestBlockedZipRefusedAfterBackendCachedModule(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{blocked: policy.Coordinate{
		Ecosystem: "golang", Name: "github.com/pkg/errors", Version: "v0.9.1",
	}}
	backend := &countingHandler{next: &goproxy.Goproxy{
		Fetcher: fixtureFetcher{zip: "REAL-ZIP"},
		Cacher:  goproxy.DirCacher(t.TempDir()),
	}}
	s := startServer(t, checker, backend)

	mod := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.mod")
	require.Equal(t, http.StatusOK, mod.status)

	zip := get(t, s, "/github.com/pkg/errors/@v/v0.9.1.zip")
	assert.Equal(t, http.StatusForbidden, zip.status)
	assert.NotContains(t, zip.body, "REAL-ZIP")
	assert.Equal(t, int64(1), backend.hits.Load(), "only the .mod reaches the backend")
}

func TestGateChecksUnescapedVersion(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{}
	s := startServer(t, checker, &backendStub{})

	res := get(t, s, "/example.com/m/@v/v1.0.0-!r!c1.zip")

	assert.Equal(t, http.StatusOK, res.status)
	assert.Equal(t, []policy.Coordinate{
		{Ecosystem: "golang", Name: "example.com/m", Version: "v1.0.0-RC1"},
	}, checker.coordinates())
}

func assertRefused(t *testing.T, suffix string) {
	t.Helper()
	checker := &coordChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := get(t, s, suffix)

	assert.Equal(t, http.StatusNotFound, res.status)
	assert.Zero(t, backend.count(), "a refused path must never be delegated")
	assert.Empty(t, checker.coordinates())
}

func TestGateRefusesDotSegments(t *testing.T) {
	t.Parallel()
	assertRefused(t, "/github.com/zzz/../foo/bar/@v/v1.0.0.zip")
	assertRefused(t, "/sumdb/sum.golang.org/../../github.com/pkg/errors/@v/v0.9.1.zip")
}

func TestGateRefusesTrailingSlash(t *testing.T) {
	t.Parallel()
	assertRefused(t, "/github.com/pkg/errors/@v/v0.9.1.zip/")
}

func TestGateRefusesEmptyModulePath(t *testing.T) {
	t.Parallel()
	assertRefused(t, "/@v/v1.0.0.zip")
}

func TestGateRefusesSecondAtV(t *testing.T) {
	t.Parallel()
	t.Run("empty first module", func(t *testing.T) {
		t.Parallel()
		assertRefused(t, "/@v/x/github.com/foo/bar/@v/v1.0.0.zip")
	})
	t.Run("second segment lands in the version", func(t *testing.T) {
		t.Parallel()
		assertRefused(t, "/github.com/foo/@v/x/github.com/foo/bar/@v/v1.0.0.zip")
	})
}

func TestGateRefusesMalformedBangEscapes(t *testing.T) {
	t.Parallel()
	for _, elem := range []string{"!!a", "a!", "!A", "Foo"} {
		t.Run(elem, func(t *testing.T) {
			t.Parallel()
			assertRefused(t, "/github.com/"+elem+"/m/@v/v1.0.0.zip")
		})
	}
}

func TestHeadRequestIsAllowed(t *testing.T) {
	t.Parallel()
	checker := &coordChecker{}
	backend := &backendStub{}
	s := startServer(t, checker, backend)

	res := do(t, http.MethodHead, s.BaseURL()+"/github.com/pkg/errors/@v/v0.9.1.info")
	assert.NotEqual(t, http.StatusMethodNotAllowed, res.status)
	assert.Equal(t, 1, backend.count())
}

// disconnectChecker blocks until released and reports whether its context
// was cancelled first.
type disconnectChecker struct {
	entered   chan struct{}
	release   chan struct{}
	cancelled chan struct{}
}

func (c *disconnectChecker) Check(ctx context.Context, _ policy.Request) (policy.Result, error) {
	close(c.entered)
	select {
	case <-ctx.Done():
		close(c.cancelled)
		return policy.Result{}, ctx.Err()
	case <-c.release:
		return policy.Result{}, nil
	}
}

func TestClientDisconnectDoesNotRecordBlocked(t *testing.T) {
	t.Parallel()
	checker := &disconnectChecker{
		entered:   make(chan struct{}),
		release:   make(chan struct{}),
		cancelled: make(chan struct{}),
	}
	s := startServer(t, checker, &backendStub{})

	ctx, cancel := context.WithCancel(t.Context())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		s.BaseURL()+"/github.com/pkg/errors/@v/v0.9.1.zip", http.NoBody)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		defer close(done)
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = res.Body.Close()
		}
	}()

	<-checker.entered
	cancel()
	<-done
	// Give the server time to notice the disconnect before releasing.
	select {
	case <-checker.cancelled:
	case <-time.After(200 * time.Millisecond):
	}
	close(checker.release)
	s.Stop()

	select {
	case <-checker.cancelled:
		assert.Fail(t, "a client disconnect cancelled the policy check")
	default:
	}
	assert.Empty(t, s.Verdicts(), "a client disconnect must not be recorded as a firewall block")
}
