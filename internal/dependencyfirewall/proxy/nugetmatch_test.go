//go:build !integration

package proxy

import (
	"archive/zip"
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

func nugetReq(path string) *http.Request {
	return httptest.NewRequest(http.MethodGet, "https://api.nuget.org/v3-flatcontainer"+path, http.NoBody)
}

func TestNuGetMatchDownload(t *testing.T) {
	t.Parallel()
	m := NuGetMatcher{}.Match(nugetReq("/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg"))
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "nuget", Name: "newtonsoft.json", Version: "13.0.1"}, m.Coordinate)
	assert.Equal(t, policy.Download, m.Operation)
}

func TestNuGetMetadataIsNoMatch(t *testing.T) {
	t.Parallel()
	m := NuGetMatcher{}.Match(nugetReq("/newtonsoft.json/index.json"))
	assert.False(t, m.Matched)
}

func TestNuGetDoesNotDoubleDecodePath(t *testing.T) {
	t.Parallel()
	// req.URL.Path is already decoded once by Go's parser. The matcher must
	// evaluate that once-decoded path and not decode it a second time. Simulate
	// a parser output where the file segment still contains a literal "%2e":
	// upstream sees "...newtonsoft.json.13.0.1%2enupkg" (not a ".nupkg"), so it
	// must not match. A second PathUnescape would turn "%2enupkg" into ".nupkg"
	// and wrongly match.
	req := nugetReq("/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1.nupkg")
	req.URL.Path = "/newtonsoft.json/13.0.1/newtonsoft.json.13.0.1%2enupkg"
	m := NuGetMatcher{}.Match(req)
	assert.False(t, m.Matched, "matcher must not perform a second percent-decode of req.URL.Path")
}

func TestNuGetDownloadUnparseableFailsClosed(t *testing.T) {
	t.Parallel()
	// A ".nupkg" request whose filename does not match the id/version path
	// segments is a recognized-but-unparseable download and must fail closed
	// rather than being forwarded uninspected.
	m := NuGetMatcher{}.Match(nugetReq("/newtonsoft.json/13.0.1/mismatch.nupkg"))
	assert.True(t, m.Matched)
	assert.False(t, m.Pass, "an unparseable nupkg download must fail closed (Pass = false)")
	assert.Equal(t, "nuget", m.Coordinate.Ecosystem)
	assert.Equal(t, policy.Download, m.Operation)
}

type zipEntry struct{ name, body string }

func nupkgZip(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e.name)
		require.NoError(t, err)
		_, err = w.Write([]byte(e.body))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func nuspecXML(id, version string) string {
	return `<?xml version="1.0"?><package><metadata><id>` + id + `</id><version>` + version + `</version></metadata></package>`
}

// nugetPush builds a push request with one multipart file part per entry of
// parts, the shape dotnet nuget push sends.
func nugetPush(t *testing.T, parts ...[]byte) (*http.Request, []byte) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	for _, part := range parts {
		fw, err := mw.CreateFormFile("package", "package.nupkg")
		require.NoError(t, err)
		_, err = fw.Write(part)
		require.NoError(t, err)
	}
	require.NoError(t, mw.Close())
	req := httptest.NewRequest(http.MethodPut, "https://www.nuget.org/api/v2/package", bytes.NewReader(buf.Bytes()))
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req, buf.Bytes()
}

func TestNuGetMatchUpload(t *testing.T) {
	t.Parallel()
	req, body := nugetPush(t, nupkgZip(t, zipEntry{"Newtonsoft.Json.nuspec", nuspecXML("Newtonsoft.Json", "13.0.1")}))

	m := NuGetMatcher{}.Match(req)
	assert.True(t, m.Matched)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Upload, m.Operation)
	assert.Equal(t, "Newtonsoft.Json", m.Coordinate.Name)
	assert.Equal(t, "13.0.1", m.Coordinate.Version)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, body, restored)
}

func TestNuGetUploadReadsPackagedNuspec(t *testing.T) {
	t.Parallel()
	// Packed .nupkg files put the nuspec after other entries, start it with a
	// byte order mark and declare the nuspec XML namespace.
	nuspec := "\ufeff" + `<?xml version="1.0" encoding="utf-8"?>` +
		`<package xmlns="http://schemas.microsoft.com/packaging/2013/05/nuspec.xsd"><metadata minClientVersion="2.12">` +
		`<id>Newtonsoft.Json</id><version>13.0.1</version><authors>James Newton-King</authors></metadata></package>`
	req, _ := nugetPush(t, nupkgZip(t,
		zipEntry{"LICENSE.md", "MIT"},
		zipEntry{"Newtonsoft.Json.nuspec", nuspec},
		zipEntry{"lib/net6.0/Newtonsoft.Json.dll", "MZ"},
	))

	m := NuGetMatcher{}.Match(req)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "nuget", Name: "Newtonsoft.Json", Version: "13.0.1"}, m.Coordinate)
}

func TestNuGetSymbolPackagePushIsChecked(t *testing.T) {
	t.Parallel()
	// A .snupkg pushed to the symbol endpoint carries a root nuspec too, so it
	// is checked under the coordinate it names rather than blocked.
	req, _ := nugetPush(t, nupkgZip(t,
		zipEntry{"_rels/.rels", "<Relationships/>"},
		zipEntry{"Serilog.nuspec", nuspecXML("Serilog", "3.1.1")},
		zipEntry{"lib/net6.0/Serilog.pdb", "BSJB"},
	))

	m := NuGetMatcher{}.Match(req)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "nuget", Name: "Serilog", Version: "3.1.1"}, m.Coordinate)
}

func TestNuGetUploadUsesOnlyRootNuspec(t *testing.T) {
	t.Parallel()
	// NuGet reads the single .nuspec at the archive root; a nuspec in a
	// subfolder is package content and must not supply the coordinate.
	req, _ := nugetPush(t, nupkgZip(t,
		zipEntry{"content/decoy.nuspec", nuspecXML("Benign", "1.0.0")},
		zipEntry{"Evil.NUSPEC", nuspecXML("Evil", "6.6.6")},
	))

	m := NuGetMatcher{}.Match(req)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "nuget", Name: "Evil", Version: "6.6.6"}, m.Coordinate)
}

func TestNuGetUploadAmbiguousPackageFailsClosed(t *testing.T) {
	t.Parallel()
	// Each package names a coordinate NuGet would not use, or none at all.
	repeated := `<package><metadata><id>Benign</id><id>Evil</id><version>1.0.0</version></metadata></package>`
	cases := map[string][][]byte{
		"two root nuspecs":  {nupkgZip(t, zipEntry{"A.nuspec", nuspecXML("A", "1.0.0")}, zipEntry{"B.nuspec", nuspecXML("B", "2.0.0")})},
		"only nested":       {nupkgZip(t, zipEntry{"content/A.nuspec", nuspecXML("A", "1.0.0")})},
		"repeated id":       {nupkgZip(t, zipEntry{"A.nuspec", repeated})},
		"missing version":   {nupkgZip(t, zipEntry{"A.nuspec", `<package><metadata><id>A</id></metadata></package>`})},
		"oversized nuspec":  {nupkgZip(t, zipEntry{"A.nuspec", nuspecXML("A", "1.0.0") + strings.Repeat(" ", maxNuspecSize)})},
		"corrupt zip":       {[]byte("PK\x03\x04 not really a zip")},
		"two packages":      {nupkgZip(t, zipEntry{"A.nuspec", nuspecXML("A", "1.0.0")}), nupkgZip(t, zipEntry{"B.nuspec", nuspecXML("B", "2.0.0")})},
		"malformed nuspec":  {nupkgZip(t, zipEntry{"A.nuspec", "<package><metadata><id>A"})},
		"no nuspec entries": {nupkgZip(t, zipEntry{"lib/a.dll", "MZ"})},
	}
	for name, parts := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			req, _ := nugetPush(t, parts...)
			m := NuGetMatcher{}.Match(req)
			assert.True(t, m.Matched)
			assert.False(t, m.Pass, "an ambiguous or unreadable package must fail closed")
			assert.Equal(t, "nuget", m.Coordinate.Ecosystem)
			assert.Equal(t, policy.Upload, m.Operation)
		})
	}
}

func TestNuGetUploadWithPrependedBytesIsChecked(t *testing.T) {
	t.Parallel()
	// Zip readers locate the archive from its end, so bytes in front of a
	// package do not stop NuGet from reading it; they must not stop the
	// matcher either.
	pkg := append([]byte("prefix bytes "), nupkgZip(t, zipEntry{"A.nuspec", nuspecXML("A", "1.0.0")})...)
	req, _ := nugetPush(t, pkg)

	m := NuGetMatcher{}.Match(req)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "nuget", Name: "A", Version: "1.0.0"}, m.Coordinate)
}

func TestNuGetNonPackageMultipartPutIsForwarded(t *testing.T) {
	t.Parallel()
	// Every PUT in the session reaches the matcher. A multipart upload with no
	// package in it, such as a test report, is forwarded untouched.
	req, body := nugetPush(t, []byte("<testsuite tests=\"1\"/>"))

	m := NuGetMatcher{}.Match(req)
	assert.False(t, m.Matched)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	assert.Equal(t, body, restored)
}

func TestNuGetUploadLargerThanWindowIsChecked(t *testing.T) {
	t.Parallel()
	// The zip central directory sits at the end of the package, past the
	// in-memory window, so the matcher must read the spilled body.
	pkg := nupkgZip(t, zipEntry{"A.nuspec", nuspecXML("A", "1.0.0")}, zipEntry{"lib/a.dll", strings.Repeat("x", 4096)})
	req, body := nugetPush(t, pkg)

	m := NuGetMatcher{limits: peekLimits{inMemory: 64, max: 1 << 20}}.Match(req)
	assert.True(t, m.Pass)
	assert.Equal(t, policy.Coordinate{Ecosystem: "nuget", Name: "A", Version: "1.0.0"}, m.Coordinate)

	restored, err := io.ReadAll(req.Body)
	require.NoError(t, err)
	require.NoError(t, req.Body.Close())
	assert.Equal(t, body, restored, "full body must reach upstream")
}

func TestNuGetUploadOverLimitFailsClosed(t *testing.T) {
	t.Parallel()
	req, _ := nugetPush(t, nupkgZip(t, zipEntry{"Big.Package.nuspec", nuspecXML("Big.Package", "1.0.0")}))

	m := NuGetMatcher{limits: peekLimits{inMemory: 8, max: 16}}.Match(req)
	assert.True(t, m.Matched)
	assert.False(t, m.Pass, "an over-limit upload must fail closed (Pass = false)")
	assert.Equal(t, policy.Upload, m.Operation)
}
