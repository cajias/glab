// Package gomodproxy serves the Go module proxy protocol on loopback so the
// Dependency Firewall can check module zips without a MITM certificate: go
// and git on macOS trust only the system store, and installing the firewall's
// CA there needs sudo.
package gomodproxy

import (
	"path"
	"regexp"
	"strings"

	"golang.org/x/mod/module"
)

type kind string

const (
	kindInfo   kind = "info"
	kindMod    kind = "mod"
	kindZip    kind = "zip"
	kindList   kind = "list"
	kindLatest kind = "latest"
)

// sumdbPrefix paths never parse but must reach the backend: a 404 on
// "/sumdb/<name>/supported" makes go contact the checksum database directly,
// over a connection that does not trust the MITM CA.
const sumdbPrefix = "sumdb/"

// toolchainModule is the pseudo-module go fetches toolchains through since
// Go 1.21. go verifies toolchain zips against the checksum database even
// with GOSUMDB=off, and the firewall has no policy for them.
const toolchainModule = "golang.org/toolchain"

// toolchainVersion matches the versions cmd/go requests for a toolchain:
// v0.0.1-go<release>.<goos>-<goarch>.
var toolchainVersion = regexp.MustCompile(`^v0\.0\.1-go1\.[0-9]+(\.[0-9]+|rc[0-9]+)\.[a-z0-9]+-[a-z0-9]+$`)

type request struct {
	module  string
	version string
	kind    kind
}

func parseRequest(reqPath string) (request, bool) {
	trimmed := strings.TrimPrefix(reqPath, "/")

	// '@' is illegal in module paths and '/' in versions, so cutting at the
	// first "/@" matches goproxy's routing, and a second "/@v/" lands in the
	// version, where UnescapeVersion refuses it.
	escapedModule, rest, found := strings.Cut(trimmed, "/@")
	if !found || escapedModule == "" {
		return request{}, false
	}

	modPath, err := module.UnescapePath(escapedModule)
	if err != nil {
		return request{}, false
	}

	switch rest {
	case "latest":
		return request{module: modPath, kind: kindLatest}, true
	case "v/list":
		return request{module: modPath, kind: kindList}, true
	}

	escapedFile, ok := strings.CutPrefix(rest, "v/")
	if !ok {
		return request{}, false
	}

	ext := path.Ext(escapedFile)
	k, ok := kindForExt(ext)
	if !ok {
		return request{}, false
	}

	version, err := module.UnescapeVersion(strings.TrimSuffix(escapedFile, ext))
	if err != nil || version == "" {
		return request{}, false
	}

	return request{module: modPath, version: version, kind: k}, true
}

func kindForExt(ext string) (kind, bool) {
	switch ext {
	case ".info":
		return kindInfo, true
	case ".mod":
		return kindMod, true
	case ".zip":
		return kindZip, true
	default:
		return "", false
	}
}

func isSumDB(p string) bool {
	return strings.HasPrefix(strings.TrimPrefix(p, "/"), sumdbPrefix)
}

func isToolchain(r request) bool {
	return r.module == toolchainModule && toolchainVersion.MatchString(r.version)
}
