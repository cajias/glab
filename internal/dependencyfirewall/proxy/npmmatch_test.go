//go:build !integration

package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

func get(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "https://registry.npmjs.org"+path, http.NoBody)
}

func TestNPMMatchTarballDownload(t *testing.T) {
	t.Parallel()
	m := NPMMatcher{}.Match(get("/left-pad/-/left-pad-1.3.0.tgz"))
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestNPMMatchScopedTarball(t *testing.T) {
	t.Parallel()
	m := NPMMatcher{}.Match(get("/@babel/core/-/core-7.24.0.tgz"))
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, "@babel/core", m.Coordinate.Name)
	assert.Equal(t, "7.24.0", m.Coordinate.Version)
}

func TestNPMMatchPathPrefixedRegistryTarball(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		path    string
		name    string
		version string
	}{
		"artifactory":            {"/artifactory/api/npm/npm/left-pad/-/left-pad-1.3.0.tgz", "left-pad", "1.3.0"},
		"gitlab packages":        {"/api/v4/projects/1/packages/npm/left-pad/-/left-pad-1.3.0.tgz", "left-pad", "1.3.0"},
		"scoped under prefix":    {"/artifactory/api/npm/npm/@babel/core/-/core-7.24.0.tgz", "@babel/core", "7.24.0"},
		"gitlab scoped packages": {"/api/v4/projects/1/packages/npm/@scope/pkg/-/pkg-1.0.0.tgz", "@scope/pkg", "1.0.0"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := NPMMatcher{}.Match(get(c.path))
			assert.True(t, m.Matched, "path-prefixed registry tarball must be inspected, not forwarded uninspected")
			assert.True(t, m.Pass)
			assert.Equal(t, policy.Coordinate{Ecosystem: "npm", Name: c.name, Version: c.version}, m.Coordinate)
			assert.Equal(t, policy.Download, m.Operation)
		})
	}
}

func TestNPMMatchMetadataIsNoMatch(t *testing.T) {
	t.Parallel()
	m := NPMMatcher{}.Match(get("/left-pad"))
	assert.False(t, m.Matched)
}

func TestNPMMatchUploadPut(t *testing.T) {
	t.Parallel()
	body := `{"name":"left-pad","versions":{"9.9.9":{}},"_attachments":{"left-pad-9.9.9.tgz":{}}}`
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))
	m := NPMMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, "left-pad", m.Coordinate.Name)
	assert.Equal(t, "9.9.9", m.Coordinate.Version)
	assert.Equal(t, policy.Upload, m.Operation)

	rest, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, body, string(rest))
}

func TestNPMMatchUploadSpillsButStillMatches(t *testing.T) {
	t.Parallel()
	// The packument JSON sits at the front and fits within the in-memory
	// window, but trailing attachment bytes push the total body past it so
	// it spills to a temp file. The coordinate must still be extracted and
	// the full body restored for the upstream round trip.
	json := `{"name":"left-pad","versions":{"9.9.9":{}},"_attachments":{"left-pad-9.9.9.tgz":{}}}`
	body := json + strings.Repeat(" ", 300) // total > in-memory window below
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))

	m := NPMMatcher{limits: peekLimits{inMemory: int64(len(json) + 8), max: 1 << 20}}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, "left-pad", m.Coordinate.Name)
	assert.Equal(t, "9.9.9", m.Coordinate.Version)

	rest, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.NoError(t, req.Body.Close())
	assert.Equal(t, body, string(rest), "full body must reach upstream even when spilled")
}

func TestNPMMatchUploadOverLimitFailsClosed(t *testing.T) {
	t.Parallel()
	body := strings.Repeat("x", 200) // exceeds the 64-byte hard limit
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))

	m := NPMMatcher{limits: peekLimits{inMemory: 16, max: 64}}.Match(req)
	assert.True(t, m.Matched)
	assert.False(t, m.Pass, "an over-limit upload must fail closed (Pass = false)")
	assert.Equal(t, "npm", m.Coordinate.Ecosystem)
	assert.Equal(t, policy.Upload, m.Operation)
}

func TestNPMMatchUploadUnparseableFailsClosed(t *testing.T) {
	t.Parallel()
	// A PUT is the npm publish endpoint; a body whose packument cannot be
	// decoded (bad JSON, missing name, no version) is a recognized-but-
	// unparseable upload and must fail closed rather than being forwarded
	// uninspected.
	cases := map[string]string{
		"bad json":          `{not json`,
		"truncated publish": `{"name":"left-pad","versions":{"1.0.0":{}},"_attachments":{"left-pad-1.0.0.tgz":{"data":"H4sI`,
		"missing name":      `{"versions":{"1.0.0":{}},"_attachments":{"x-1.0.0.tgz":{}}}`,
		"missing version":   `{"name":"left-pad","versions":{},"_attachments":{"left-pad-1.0.0.tgz":{}}}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))
			m := NPMMatcher{}.Match(req)
			assert.True(t, m.Matched)
			assert.False(t, m.Pass, "an unparseable publish body must fail closed (Pass = false)")
			assert.Equal(t, "npm", m.Coordinate.Ecosystem)
			assert.Equal(t, policy.Upload, m.Operation)
		})
	}
}

func TestNPMMatchNonPublishPutIsForwarded(t *testing.T) {
	t.Parallel()
	// npm issues PUTs that carry no package: dist-tags, ownership, stars, teams,
	// legacy login, deprecation. Only a publish carries _attachments, so these
	// are forwarded untouched, as is a non-JSON PUT from a lifecycle script.
	cases := map[string]struct{ path, body string }{
		"dist-tag add":     {"/-/package/left-pad/dist-tags/beta", `"1.3.0"`},
		"owner add":        {"/left-pad/-rev/3-abc", `{"_id":"left-pad","_rev":"3-abc","maintainers":[{"name":"u"}]}`},
		"star":             {"/left-pad", `{"_id":"left-pad","_rev":"3-abc","users":{"u":true}}`},
		"team add":         {"/-/team/org/devs/user", `{"user":"u"}`},
		"legacy adduser":   {"/-/user/org.couchdb.user:u", `{"_id":"org.couchdb.user:u","name":"u","password":"p"}`},
		"deprecate":        {"/left-pad", `{"name":"left-pad","versions":{"1.3.0":{"deprecated":"use pad-left"}}}`},
		"binary to bucket": {"/videos/run-1.mp4", "\x00\x00\x00\x18ftypmp42"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org"+c.path, strings.NewReader(c.body))
			m := NPMMatcher{}.Match(req)
			assert.False(t, m.Matched)

			rest, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			assert.Equal(t, c.body, string(rest), "a forwarded body must reach upstream intact")
		})
	}
}

func TestNPMMatchUploadLargerThanWindowIsChecked(t *testing.T) {
	t.Parallel()
	// A tarball bigger than the in-memory window cuts the packument off mid
	// JSON in peeked.data; the matcher must read the spilled body instead of
	// blocking a legitimate publish.
	body := `{"name":"left-pad","versions":{"1.3.0":{}},"_attachments":{"left-pad-1.3.0.tgz":{"data":"` +
		strings.Repeat("A", 4096) + `"}}}`
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))

	m := NPMMatcher{limits: peekLimits{inMemory: 64, max: 1 << 20}}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}, m.Coordinate)

	rest, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.NoError(t, req.Body.Close())
	assert.Equal(t, body, string(rest), "full body must reach upstream")
}

func TestNPMMatchUploadChecksCoordinateDeclaredAfterWindow(t *testing.T) {
	t.Parallel()
	// A second name/versions pair after the tarball overrides the first for
	// the registry, so it is the coordinate that must be checked.
	body := `{"name":"benign","versions":{"1.0.0":{}},"_attachments":{"x.tgz":{"data":"` +
		strings.Repeat("A", 4096) + `"}},"name":"evil","versions":{"6.6.6":{}}}`
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/evil", strings.NewReader(body))

	m := NPMMatcher{limits: peekLimits{inMemory: 64, max: 1 << 20}}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "npm", Name: "evil", Version: "6.6.6"}, m.Coordinate)
}

func TestNPMMatchUploadMultipleVersionsFailsClosed(t *testing.T) {
	t.Parallel()
	// A publish carries exactly one version; with several, any one picked for
	// the check may not be the one the registry stores.
	body := `{"name":"left-pad","versions":{"1.3.0":{},"6.6.6":{}},"_attachments":{"left-pad-1.3.0.tgz":{}}}`
	req := httptest.NewRequest(http.MethodPut, "https://registry.npmjs.org/left-pad", strings.NewReader(body))

	m := NPMMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.False(t, m.Pass)
	assert.Equal(t, policy.Upload, m.Operation)
}
