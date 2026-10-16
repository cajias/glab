package proxy

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/dbg"
	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/policy"
)

// NuGetMatcher recognizes NuGet v3 flatcontainer artifact downloads
// (".../{id}/{version}/{id}.{version}.nupkg") and pushes: a multipart PUT
// carrying a .nupkg or .snupkg. Each server advertises its own push URL in
// its service index, so pushes are recognized by body rather than by path. A
// .nupkg download outside the flatcontainer shape, such as a nuget.org v2 CDN
// URL, fails closed.
//
// Download coordinates carry the id and version lowercased, as the
// flatcontainer path spells them; push coordinates keep the casing in the
// package's nuspec.
type NuGetMatcher struct {
	limits peekLimits
}

const nupkgSuffix = ".nupkg"

// maxNuspecSize bounds the decompressed .nuspec read from a pushed package.
// Manifests are a few KiB; the cap stops a small zip from expanding into a
// large buffer in the proxy.
const maxNuspecSize = 1 << 20

func (m NuGetMatcher) Match(req *http.Request) Match {
	if req == nil {
		return Match{}
	}
	if req.Method == http.MethodPut {
		return nugetUpload(req, m.limits.orDefault())
	}
	// req.URL.Path is already percent-decoded once by Go's parser. Decoding
	// again would let a double-encoded request present the matcher a different
	// path than the upstream NuGet server sees, and because an unmatched
	// request bypasses the firewall that is a policy-bypass vector. Match
	// against the once-decoded path the origin actually receives.
	name, version, result := nugetFlatcontainer(req.URL.Path)
	switch result {
	case nugetNotDownload:
		return Match{}
	case nugetUnparseable:
		return unparseableMatch("nuget", policy.Download, "unparseable nuget download URL")
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "nuget", Name: name, Version: version},
		Operation:  policy.Download,
	}
}

// nugetFlatcontainerResult classifies a request path relative to the NuGet
// flatcontainer download shape: not a .nupkg download at all, a .nupkg
// download whose coordinate could not be parsed (fail closed), or a parsed
// coordinate.
type nugetFlatcontainerResult int

const (
	nugetNotDownload nugetFlatcontainerResult = iota
	nugetUnparseable
	nugetParsed
)

// nugetFlatcontainer parses ".../{id}/{version}/{id}.{version}.nupkg" and
// returns the id and version. It anchors on the trailing "<id>.<version>.nupkg"
// filename matching the two path segments that precede it.
func nugetFlatcontainer(path string) (string, string, nugetFlatcontainerResult) {
	trimmed := strings.Trim(path, "/")
	// The ".nupkg" suffix is the in-scope signal; anything else is not a
	// NuGet artifact download and passes through untouched.
	if !strings.HasSuffix(strings.ToLower(trimmed), nupkgSuffix) {
		return "", "", nugetNotDownload
	}
	// A ".nupkg" request whose flatcontainer structure or filename does not
	// yield a coordinate is recognized-but-unparseable: fail closed rather
	// than forwarding an in-scope download uninspected.
	parts := strings.Split(trimmed, "/")
	if len(parts) < 3 {
		return "", "", nugetUnparseable
	}
	id := parts[len(parts)-3]
	version := parts[len(parts)-2]
	file := parts[len(parts)-1]
	if id == "" || version == "" {
		return "", "", nugetUnparseable
	}
	if !strings.EqualFold(file, id+"."+version+nupkgSuffix) {
		return "", "", nugetUnparseable
	}
	return id, version, nugetParsed
}

func nugetUpload(req *http.Request, limits peekLimits) Match {
	if req.Body == nil {
		return Match{}
	}
	mediaType, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if err != nil || !strings.HasPrefix(mediaType, "multipart/") {
		return Match{}
	}
	peeked := peekUploadBody(req.Body, limits)
	req.Body = peeked.body
	if peeked.overLimit {
		return Match{
			Matched:    true,
			Operation:  policy.Upload,
			Coordinate: policy.Coordinate{Ecosystem: "nuget"},
			Reason:     "upload body too large to inspect for dependency firewall policy",
		}
	}
	// The zip central directory sits at the end of the package, past the
	// in-memory window for a large push, so read the whole spilled body.
	src := io.Reader(bytes.NewReader(peeked.data))
	if peeked.spilled != nil {
		src = peeked.spilled
	}
	id, version, found, err := nugetPackageFromMultipart(src, params["boundary"], limits.inMemory)
	if err != nil {
		return unparseableMatch("nuget", policy.Upload, "unparseable publish metadata")
	}
	// Every PUT in the session reaches this matcher; a multipart upload with
	// no package in it, such as a test report, is not a push.
	if !found {
		return Match{}
	}
	return Match{
		Matched:    true,
		Pass:       true,
		Coordinate: policy.Coordinate{Ecosystem: "nuget", Name: id, Version: version},
		Operation:  policy.Upload,
	}
}

var errNuGetPackage = errors.New("unparseable nuget package")

// nugetPackageFromMultipart finds the package among the parts of a push body
// and reads its coordinate. found is false when no part is a zip archive.
func nugetPackageFromMultipart(body io.Reader, boundary string, inMemory int64) (string, string, bool, error) {
	if boundary == "" {
		return "", "", false, errNuGetPackage
	}
	var id, version string
	found := false
	mr := multipart.NewReader(body, boundary)
	for {
		part, err := mr.NextPart()
		if errors.Is(err, io.EOF) {
			return id, version, found, nil
		}
		if err != nil {
			return "", "", false, err
		}
		pkg, size, cleanup, err := spoolPart(part, inMemory)
		if err != nil {
			return "", "", false, err
		}
		partID, partVersion, isPackage, err := nugetPackageCoordinate(pkg, size)
		cleanup()
		if err != nil {
			return "", "", false, err
		}
		if !isPackage {
			continue
		}
		// One push carries one package; with two, the one checked need not
		// be the one the server stores.
		if found {
			return "", "", false, errNuGetPackage
		}
		id, version, found = partID, partVersion, true
	}
}

// spoolPart buffers a multipart part for the random access archive/zip needs:
// in memory up to inMemory bytes, otherwise in a temp file that cleanup
// removes.
func spoolPart(part io.Reader, inMemory int64) (io.ReaderAt, int64, func(), error) {
	mem, err := io.ReadAll(io.LimitReader(part, inMemory+1))
	if err != nil {
		return nil, 0, nil, err
	}
	if int64(len(mem)) <= inMemory {
		return bytes.NewReader(mem), int64(len(mem)), func() {}, nil
	}
	f, err := os.CreateTemp("", "glab-df-nupkg-*")
	if err != nil {
		return nil, 0, nil, err
	}
	cleanup := func() {
		_ = f.Close()
		if err := os.Remove(f.Name()); err != nil {
			dbg.Debugf("dependency firewall: failed to remove nuget spool file %s: %v", f.Name(), err)
		}
	}
	if _, err := f.Write(mem); err != nil {
		cleanup()
		return nil, 0, nil, err
	}
	n, err := io.Copy(f, part)
	if err != nil {
		cleanup()
		return nil, 0, nil, err
	}
	return f, int64(len(mem)) + n, cleanup, nil
}

var zipLocalHeader = []byte("PK\x03\x04")

// nugetPackageCoordinate reads the coordinate of the package in r. isPackage
// is false when r is not a zip archive at all.
func nugetPackageCoordinate(r io.ReaderAt, size int64) (string, string, bool, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		// A part that starts like a zip but does not open is a corrupt
		// package, not unrelated traffic.
		head := make([]byte, len(zipLocalHeader))
		if _, readErr := r.ReadAt(head, 0); readErr == nil && bytes.Equal(head, zipLocalHeader) {
			return "", "", true, errNuGetPackage
		}
		return "", "", false, nil
	}
	id, version, err := nuspecCoordinate(zr)
	return id, version, true, err
}

// nuspecCoordinate reads <id> and <version> from the package manifest the way
// NuGet resolves it: the single .nuspec at the archive root (NuGet.Client
// PackageHelper.IsManifest), and the one id and version in its metadata.
func nuspecCoordinate(zr *zip.Reader) (string, string, error) {
	var manifest *zip.File
	for _, f := range zr.File {
		if strings.ContainsAny(f.Name, `/\`) || !strings.HasSuffix(strings.ToLower(f.Name), ".nuspec") {
			continue
		}
		if manifest != nil {
			return "", "", errNuGetPackage
		}
		manifest = f
	}
	if manifest == nil {
		return "", "", errNuGetPackage
	}
	rc, err := manifest.Open()
	if err != nil {
		return "", "", err
	}
	raw, err := io.ReadAll(io.LimitReader(rc, maxNuspecSize+1))
	if err := errors.Join(err, rc.Close()); err != nil {
		return "", "", err
	}
	if len(raw) > maxNuspecSize {
		return "", "", errNuGetPackage
	}
	var doc struct {
		Metadata []struct {
			ID      []string `xml:"id"`
			Version []string `xml:"version"`
		} `xml:"metadata"`
	}
	if err := xml.Unmarshal(raw, &doc); err != nil {
		return "", "", err
	}
	// NuGet reads the first id and version where encoding/xml keeps the last,
	// so a repeated element could name a coordinate NuGet never uses.
	if len(doc.Metadata) != 1 || len(doc.Metadata[0].ID) != 1 || len(doc.Metadata[0].Version) != 1 {
		return "", "", errNuGetPackage
	}
	id, version := doc.Metadata[0].ID[0], doc.Metadata[0].Version[0]
	if id == "" || version == "" {
		return "", "", errNuGetPackage
	}
	return id, version, nil
}
