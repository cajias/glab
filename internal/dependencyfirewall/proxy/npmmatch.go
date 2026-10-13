package proxy

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// NPMMatcher recognizes npm artifact tarball downloads and publish uploads
// against the public npm registry (registry.npmjs.org) or any npm-protocol
// upstream with the same URL shape.
type NPMMatcher struct {
	// limits bounds upload-body inspection. The zero value resolves to the
	// production defaults; tests set small values to exercise spill/over-limit.
	limits peekLimits
}

func (m NPMMatcher) Match(req *http.Request) Match {
	if req == nil {
		return Match{}
	}
	if req.Method == http.MethodPut {
		return npmUpload(req, m.limits.orDefault())
	}
	name, version, ok := npmTarball(req.URL.Path)
	if !ok {
		return Match{}
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "npm", Name: name, Version: version},
		Operation:  policy.Download,
	}
}

// npmTarballRegexp matches the npm registry tarball URL shape for both
// unscoped and scoped packages, anchored on the "/<name>/-/<file>.tgz" suffix
// so it also matches registries served under a path prefix:
//
//	/left-pad/-/left-pad-1.3.0.tgz
//	/@babel/core/-/core-7.24.0.tgz
//	/artifactory/api/npm/npm/left-pad/-/left-pad-1.3.0.tgz
//	/api/v4/projects/1/packages/npm/@scope/pkg/-/pkg-1.0.0.tgz
//
// The tarball shape ("/-/" separator + ".tgz") is the reliable enforcement
// signal; the leading path segments vary by registry (public npm, Artifactory,
// GitLab's own package registry). Anchoring on "^/?<name>" instead would
// silently skip every path-prefixed registry — those fetches would be
// forwarded with no policy check and no verdict, uninspected but green — so
// the leading "(?:^|/)" lets any prefix precede the coordinate.
//
// Named groups:
//
//	name    — full package name including optional @scope/
//	short   — bare package name without scope (used in the filename)
//	file    — filename base "<short>-<version>"
//
// Go's RE2 engine has no backreferences, so the regexp cannot assert that
// the filename repeats the short name; npmTarball verifies that "file"
// starts with "short-" and derives the version from the remainder.
var npmTarballRegexp = regexp.MustCompile(
	`(?:^|/)(?P<name>(?:@[^/]+/)?(?P<short>[^/]+))/-/(?P<file>[^/]+)\.tgz$`,
)

// npmTarball parses "/<name>/-/<file>-<version>.tgz" (scoped:
// "/@scope/name/-/name-<version>.tgz").
func npmTarball(path string) (string, string, bool) {
	m := npmTarballRegexp.FindStringSubmatch(path)
	if m == nil {
		return "", "", false
	}
	name := m[npmTarballRegexp.SubexpIndex("name")]
	short := m[npmTarballRegexp.SubexpIndex("short")]
	file := m[npmTarballRegexp.SubexpIndex("file")]
	if name == "" || short == "" {
		return "", "", false
	}
	version, ok := strings.CutPrefix(file, short+"-")
	if !ok || version == "" {
		return "", "", false
	}
	return name, version, true
}

// npmUpload reads a publish body to extract the name and the single version
// being published. It restores req.Body. A body too large to inspect (see
// peekLimits.max) fails closed by leaving Pass false.
func npmUpload(req *http.Request, limits peekLimits) Match {
	if req.Body == nil {
		return Match{}
	}
	peeked := peekUploadBody(req.Body, limits)
	req.Body = peeked.body
	if peeked.overLimit {
		return Match{
			Matched:    true,
			Operation:  policy.Upload,
			Coordinate: policy.Coordinate{Ecosystem: "npm"},
			Reason:     "upload body too large to inspect for dependency firewall policy",
		}
	}

	// Every PUT in the session reaches this matcher, including npm's metadata
	// writes (dist-tags, owners, stars) and non-registry uploads. A body that
	// is not a JSON object cannot be a packument, so it is forwarded.
	if !bytes.HasPrefix(bytes.TrimLeft(peeked.data, " \t\r\n"), []byte("{")) {
		return Match{}
	}
	// A tarball larger than the in-memory window cuts the packument off in
	// peeked.data, so scan the whole spilled body instead.
	src := io.Reader(bytes.NewReader(peeked.data))
	if peeked.spilled != nil {
		src = peeked.spilled
	}
	doc, err := scanPackument(src, int(limits.inMemory))
	// An object we cannot decode may be a publish: fail closed rather than
	// forwarding it uninspected.
	if err != nil {
		return unparseableMatch("npm", policy.Upload, "unparseable publish metadata")
	}
	// Only a publish carries the tarball in _attachments; metadata writes such
	// as deprecate or owner changes do not, and are forwarded.
	if !doc.attachments {
		return Match{}
	}
	// A publish carries exactly one version. With several, the one checked
	// need not be the one the registry stores.
	if doc.name == "" || len(doc.versions) != 1 {
		return unparseableMatch("npm", policy.Upload, "unparseable publish metadata")
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "npm", Name: doc.name, Version: doc.versions[0]},
		Operation:  policy.Upload,
	}
}

// bodyWithPrefix returns a ReadCloser yielding prefix then the remaining
// bytes of rest, closing rest when done. Upload matchers use it to peek at
// the leading bytes of a request body and then restore the full stream for
// the upstream round trip.
func bodyWithPrefix(prefix []byte, rest io.ReadCloser) io.ReadCloser {
	return struct {
		io.Reader
		io.Closer
	}{
		Reader: io.MultiReader(bytes.NewReader(prefix), rest),
		Closer: rest,
	}
}
