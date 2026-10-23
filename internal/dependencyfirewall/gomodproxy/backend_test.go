//go:build !integration

package gomodproxy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goproxy/goproxy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"
)

func envValue(env []string, key string) (string, bool) {
	value, found := "", false
	for _, entry := range env {
		if k, v, ok := strings.Cut(entry, "="); ok && k == key {
			value, found = v, true
		}
	}
	return value, found
}

func TestFetcherEnvInheritsProcessEnvironment(t *testing.T) {
	t.Setenv("HOME", "/tmp/home-fixture")
	t.Setenv("GOCACHE", "/tmp/gocache-fixture")

	env := fetcherEnv(t.TempDir(), nil)
	for _, key := range []string{"PATH", "HOME", "GOCACHE"} {
		_, ok := envValue(env, key)
		assert.True(t, ok, "%s must be inherited", key)
	}
}

func TestFetcherEnvPinsFetcherSettings(t *testing.T) {
	t.Setenv("GOTOOLCHAIN", "go1.99.0")
	t.Setenv("GOWORK", "/elsewhere/go.work")
	t.Setenv("GOFLAGS", "-mod=vendor")
	t.Setenv("GOMODCACHE", "/elsewhere/modcache")
	tempDir := t.TempDir()

	env := fetcherEnv(tempDir, nil)

	toolchain, _ := envValue(env, "GOTOOLCHAIN")
	assert.Equal(t, "local", toolchain)
	work, _ := envValue(env, "GOWORK")
	assert.Equal(t, "off", work)
	flags, _ := envValue(env, "GOFLAGS")
	assert.Equal(t, "-mod=mod -modcacherw", flags)
	modcache, _ := envValue(env, "GOMODCACHE")
	assert.Equal(t, filepath.Join(tempDir, "modcache"), modcache)
}

func TestFetcherEnvOverrideReplacesDefault(t *testing.T) {
	t.Setenv("GOSUMDB", "off")

	env := fetcherEnv(t.TempDir(), map[string]string{
		"GOSUMDB": "sum.golang.org",
		"GOPROXY": "https://corp.example",
	})

	sumdb, _ := envValue(env, "GOSUMDB")
	assert.Equal(t, "sum.golang.org", sumdb)
	proxy, _ := envValue(env, "GOPROXY")
	assert.Equal(t, "https://corp.example", proxy)
	toolchain, _ := envValue(env, "GOTOOLCHAIN")
	assert.Equal(t, "local", toolchain, "keys without an override keep their default")
}

func TestFetcherEnvOverrideCannotReplaceIsolationPins(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	env := fetcherEnv(tempDir, map[string]string{
		"GOMODCACHE":  "/elsewhere/modcache",
		"GOFLAGS":     "-mod=vendor",
		"GOTOOLCHAIN": "go1.99.0",
		"GOWORK":      "/elsewhere/go.work",
	})

	modcache, _ := envValue(env, "GOMODCACHE")
	assert.Equal(t, filepath.Join(tempDir, "modcache"), modcache)
	flags, _ := envValue(env, "GOFLAGS")
	assert.Equal(t, "-mod=mod -modcacherw", flags)
	toolchain, _ := envValue(env, "GOTOOLCHAIN")
	assert.Equal(t, "local", toolchain)
	work, _ := envValue(env, "GOWORK")
	assert.Equal(t, "off", work)
}

// "|" makes go fall back on any error, which would defeat the gate.
func TestFetcherEnvGOPROXYHasNoPipeSeparator(t *testing.T) {
	t.Parallel()
	value, ok := envValue(fetcherEnv(t.TempDir(), nil), "GOPROXY")
	require.True(t, ok)
	assert.NotContains(t, value, "|")
}

func fetcherOf(t *testing.T, b *Backend) *goproxy.GoFetcher {
	t.Helper()
	gp, ok := b.handler.(*goproxy.Goproxy)
	require.True(t, ok)
	fetcher, ok := gp.Fetcher.(*goproxy.GoFetcher)
	require.True(t, ok)
	return fetcher
}

func TestNewBackendConfiguresSumDBAndLogger(t *testing.T) {
	t.Parallel()
	b, err := NewBackend("/usr/bin/go", []string{"sum.golang.org"}, map[string]string{"GOSUMDB": "sum.golang.org"})
	require.NoError(t, err)
	t.Cleanup(b.Close)

	gp, ok := b.handler.(*goproxy.Goproxy)
	require.True(t, ok)
	assert.Equal(t, []string{"sum.golang.org"}, gp.ProxiedSumDBs)
	assert.NotNil(t, gp.Cacher, "a nil Cacher refetches upstream on every request")
	require.NotNil(t, gp.Logger)
	assert.IsType(t, debugHandler{}, gp.Logger.Handler())

	fetcher := fetcherOf(t, b)
	assert.Equal(t, "/usr/bin/go", fetcher.GoBin)
	sumdb, _ := envValue(fetcher.Env, "GOSUMDB")
	assert.Equal(t, "sum.golang.org", sumdb)
}

func writeFileProxy(t *testing.T) string {
	t.Helper()
	src := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(src, "go.mod"), []byte("module example.com/m\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(src, "m.go"), []byte("package m\n"), 0o600))

	root := t.TempDir()
	dir := filepath.Join(root, "example.com", "m", "@v")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "list"), []byte("v1.0.0\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.0.0.info"), []byte(`{"Version":"v1.0.0"}`), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "v1.0.0.mod"), []byte("module example.com/m\n"), 0o600))

	zf, err := os.Create(filepath.Join(dir, "v1.0.0.zip"))
	require.NoError(t, err)
	require.NoError(t, modzip.CreateFromDir(zf, module.Version{Path: "example.com/m", Version: "v1.0.0"}, src))
	require.NoError(t, zf.Close())
	return root
}

func TestBackendCloseRemovesPopulatedModuleCache(t *testing.T) {
	t.Parallel()
	b, err := NewBackend("go", nil, map[string]string{
		"GOPROXY": "file://" + filepath.ToSlash(writeFileProxy(t)),
		"GOSUMDB": "off",
	})
	require.NoError(t, err)

	cmd := exec.Command("go", "mod", "download", "-json", "example.com/m@v1.0.0")
	cmd.Env = fetcherOf(t, b).Env
	cmd.Dir = t.TempDir()
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	_, err = os.Stat(filepath.Join(b.tempDir, "modcache", "example.com", "m@v1.0.0", "m.go"))
	require.NoError(t, err, "go mod download must populate the private module cache")

	b.Close()
	_, err = os.Stat(b.tempDir)
	assert.True(t, os.IsNotExist(err), "temp tree must not survive Close")
}
