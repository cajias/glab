package gomodproxy

import (
	"context"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/goproxy/goproxy"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

const upstreamGOPROXY = "https://proxy.golang.org,direct"

// Backend serves allowed module proxy requests through goproxy and owns the
// temp tree goproxy fetches into.
type Backend struct {
	handler http.Handler
	tempDir string
}

// NewBackend builds a goproxy handler that shells out to goBin for direct
// fetches. proxiedSumDBs names the checksum databases served under /sumdb/.
// envOverrides replaces the fetcher environment's value for each key it
// sets, except the isolation pins, which always win.
func NewBackend(goBin string, proxiedSumDBs []string, envOverrides map[string]string) (*Backend, error) {
	tempDir, err := os.MkdirTemp("", "glab-df-gomodproxy-*")
	if err != nil {
		return nil, err
	}

	return &Backend{
		tempDir: tempDir,
		handler: &goproxy.Goproxy{
			Fetcher: &goproxy.GoFetcher{
				Env:     fetcherEnv(tempDir, envOverrides),
				GoBin:   goBin,
				TempDir: tempDir,
			},
			Cacher:        goproxy.DirCacher(filepath.Join(tempDir, "cache")),
			TempDir:       tempDir,
			ProxiedSumDBs: proxiedSumDBs,
			Logger:        slog.New(debugHandler{}),
		},
	}, nil
}

func (b *Backend) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b.handler.ServeHTTP(w, r)
}

// Close removes the temp tree, including modules fetched while resolving a
// request that was later blocked.
func (b *Backend) Close() {
	if err := os.RemoveAll(b.tempDir); err != nil {
		dbg.Debugf("gomodproxy: removing %s: %v\n", b.tempDir, err)
	}
}

// fetcherEnv starts from os.Environ because goproxy treats a non-nil Env as
// the complete environment of its go subprocess, which needs PATH for git and
// HOME for SSH keys, .netrc, and gitconfig. Settings are appended last
// because goproxy uses the last value per key. The isolation pins are
// applied after the overrides:
//   - a private GOMODCACHE, because sharing the child's deadlocks on the
//     module-cache lock and would let the child skip the zip request;
//   - -modcacherw, so Close can delete what go extracts read-only;
//   - GOTOOLCHAIN=local, so the fetcher never downloads a toolchain;
//   - GOWORK=off, so an absolute GOWORK cannot apply workspace replaces.
func fetcherEnv(tempDir string, overrides map[string]string) []string {
	settings := map[string]string{"GOPROXY": upstreamGOPROXY}
	maps.Copy(settings, overrides)
	maps.Copy(settings, map[string]string{
		"GOMODCACHE":  filepath.Join(tempDir, "modcache"),
		"GOFLAGS":     "-mod=mod -modcacherw",
		"GOTOOLCHAIN": "local",
		"GOWORK":      "off",
	})

	env := os.Environ()
	for _, k := range slices.Sorted(maps.Keys(settings)) {
		env = append(env, k+"="+settings[k])
	}
	return env
}

// debugHandler routes goproxy's slog output to glab's debug log; on stderr
// it would corrupt the firewall summary.
type debugHandler struct{}

func (debugHandler) Enabled(context.Context, slog.Level) bool { return true }

func (debugHandler) Handle(_ context.Context, r slog.Record) error {
	var b strings.Builder
	b.WriteString(r.Message)
	r.Attrs(func(a slog.Attr) bool {
		b.WriteString(" " + a.String())
		return true
	})
	dbg.Debugf("gomodproxy: %s\n", b.String())
	return nil
}

func (h debugHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h debugHandler) WithGroup(string) slog.Handler { return h }
