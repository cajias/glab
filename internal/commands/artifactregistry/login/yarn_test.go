//go:build !integration

package login

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func yarnrcPath(home string) string {
	return filepath.Join(home, ".yarnrc.yml")
}

// yarnrcWithEntry is the whole file loginYarn writes into an empty home: one
// npmRegistries entry for key carrying token.
func yarnrcWithEntry(key, token string) string {
	return "npmRegistries:\n" +
		"  \"" + key + "\":\n" +
		"    npmAlwaysAuth: true\n" +
		"    npmAuthToken: " + token + "\n"
}

func TestLoginYarn_CreatesFileWhenAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	_, err := loginYarn(mustURL(t, "https://registry.example.com/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, yarnrcWithEntry("//registry.example.com", "tok1"), string(got))

	fileInfo, err := os.Stat(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())
}

// TestLoginYarn_DropsOneTrailingSlash pins the key's shape with and without a
// trailing slash: Yarn strips one from every npmRegistries key and from the
// registry it looks up, so both spellings must land on the same key.
func TestLoginYarn_DropsOneTrailingSlash(t *testing.T) {
	for _, registry := range []string{"https://registry.example.com", "https://registry.example.com/"} {
		t.Run(registry, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)

			_, err := loginYarn(mustURL(t, registry), "tok1")
			require.NoError(t, err)

			got, err := os.ReadFile(yarnrcPath(home))
			require.NoError(t, err)
			assert.Equal(t, yarnrcWithEntry("//registry.example.com", "tok1"), string(got))
		})
	}
}

func TestLoginYarn_KeepsThePath(t *testing.T) {
	for _, registry := range []string{
		"https://registry.example.com/api/v4/projects/1/packages/npm",
		"https://registry.example.com/api/v4/projects/1/packages/npm/",
	} {
		t.Run(registry, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)

			_, err := loginYarn(mustURL(t, registry), "tok1")
			require.NoError(t, err)

			got, err := os.ReadFile(yarnrcPath(home))
			require.NoError(t, err)
			assert.Equal(t, yarnrcWithEntry("//registry.example.com/api/v4/projects/1/packages/npm", "tok1"), string(got))
		})
	}
}

// TestLoginYarn_KeepsThePortAsTyped pins that, unlike --npm, the default port
// is not dropped. Yarn compares keys against the npmRegistryServer string
// without parsing it, so ":443" there only matches ":443" here.
func TestLoginYarn_KeepsThePortAsTyped(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	_, err := loginYarn(mustURL(t, "https://registry.example.com:8443/"), "tok1")
	require.NoError(t, err)
	_, err = loginYarn(mustURL(t, "https://registry.example.com:443/"), "tok2")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, "npmRegistries:\n"+
		"  \"//registry.example.com:8443\":\n"+
		"    npmAlwaysAuth: true\n"+
		"    npmAuthToken: tok1\n"+
		"  \"//registry.example.com:443\":\n"+
		"    npmAlwaysAuth: true\n"+
		"    npmAuthToken: tok2\n", string(got))
}

// TestLoginYarn_KeepsTheHostCaseAsTyped pins that, unlike --npm, the host is
// not lower-cased, for the same reason the port is kept: Yarn never
// normalizes the registry it looks up beyond one trailing slash.
func TestLoginYarn_KeepsTheHostCaseAsTyped(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	_, err := loginYarn(mustURL(t, "https://AR.Example.com/API/v4/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, yarnrcWithEntry("//AR.Example.com/API/v4", "tok1"), string(got))
}

func TestLoginYarn_KeepsThePathPercentEncoded(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	_, err := loginYarn(mustURL(t, "https://gl.example.com/api/v4/projects/group%2Fproj/packages/npm/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, yarnrcWithEntry("//gl.example.com/api/v4/projects/group%2Fproj/packages/npm", "tok1"), string(got))
}

func TestLoginYarn_UpdatesExistingEntryInPlace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	_, err := loginYarn(mustURL(t, "https://registry.example.com/"), "tok1")
	require.NoError(t, err)
	_, err = loginYarn(mustURL(t, "https://registry.example.com/"), "tok2")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, yarnrcWithEntry("//registry.example.com", "tok2"), string(got))
}

// TestLoginYarn_RefreshesEverySpellingYarnResolves pins that every key Yarn
// would resolve to the registry is refreshed, and nothing is added next to
// them. Yarn drops a trailing slash from each key, and tries the
// scheme-qualified spelling before the "//" one, so a stale token under either
// would still be the one Yarn sends. An "http://" key is a different registry
// to a lookup of the https one, and is left alone.
func TestLoginYarn_RefreshesEverySpellingYarnResolves(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "npmRegistries:\n" +
		"  \"https://ar.example.com/\":\n" +
		"    npmAuthToken: old1\n" +
		"  \"//ar.example.com/\":\n" +
		"    npmAuthToken: old2\n" +
		"  \"http://ar.example.com\":\n" +
		"    npmAuthToken: other\n"
	require.NoError(t, os.WriteFile(yarnrcPath(home), []byte(fixture), 0o600))

	_, err := loginYarn(mustURL(t, "https://ar.example.com"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, "npmRegistries:\n"+
		"  \"https://ar.example.com/\":\n"+
		"    npmAuthToken: tok1\n"+
		"    npmAlwaysAuth: true\n"+
		"  \"//ar.example.com/\":\n"+
		"    npmAuthToken: tok1\n"+
		"    npmAlwaysAuth: true\n"+
		"  \"http://ar.example.com\":\n"+
		"    npmAuthToken: other\n", string(got))
}

// TestLoginYarn_PreservesUnrelatedContent pins that the round trip keeps every
// key, value and comment it was not asked to change, including the other
// fields of the entry it refreshes, and documents the formatting it does not
// keep: blank lines and four-space indentation.
func TestLoginYarn_PreservesUnrelatedContent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "# Managed by hand\n" +
		"nodeLinker: node-modules\n" +
		"\n" +
		"npmRegistries:\n" +
		"    \"//other.example.com\":\n" +
		"        npmAuthToken: keep-me\n" +
		"    \"//ar.example.com\":\n" +
		"        npmAuthIdent: user:pass # legacy\n" +
		"        npmAlwaysAuth: false\n" +
		"        npmAuthToken: old\n" +
		"\n" +
		"npmScopes:\n" +
		"    acme:\n" +
		"        npmRegistryServer: \"https://ar.example.com\"\n"
	require.NoError(t, os.WriteFile(yarnrcPath(home), []byte(fixture), 0o600))

	_, err := loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, "# Managed by hand\n"+
		"nodeLinker: node-modules\n"+
		"npmRegistries:\n"+
		"  \"//other.example.com\":\n"+
		"    npmAuthToken: keep-me\n"+
		"  \"//ar.example.com\":\n"+
		"    npmAuthIdent: user:pass # legacy\n"+
		"    npmAlwaysAuth: true\n"+
		"    npmAuthToken: tok1\n"+
		"npmScopes:\n"+
		"  acme:\n"+
		"    npmRegistryServer: \"https://ar.example.com\"\n", string(got))
}

func TestLoginYarn_DifferentRegistryAddsSeparateEntry_NoPrefixCrossMatch(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	_, err := loginYarn(mustURL(t, "https://ar.example.com.evil.com/"), "evil")
	require.NoError(t, err)
	_, err = loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, "npmRegistries:\n"+
		"  \"//ar.example.com.evil.com\":\n"+
		"    npmAlwaysAuth: true\n"+
		"    npmAuthToken: evil\n"+
		"  \"//ar.example.com\":\n"+
		"    npmAlwaysAuth: true\n"+
		"    npmAuthToken: tok1\n", string(got))
}

// TestLoginYarn_TreatsAnEmptyFileAsAbsent covers the files that hold no
// settings.
func TestLoginYarn_TreatsAnEmptyFileAsAbsent(t *testing.T) {
	for name, fixture := range map[string]string{
		"empty":            "",
		"blank lines only": "\n\n",
		"document start":   "---\n",
		"null document":    "~\n",
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			require.NoError(t, os.WriteFile(yarnrcPath(home), []byte(fixture), 0o600))

			_, err := loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
			require.NoError(t, err)

			got, err := os.ReadFile(yarnrcPath(home))
			require.NoError(t, err)
			assert.Equal(t, yarnrcWithEntry("//ar.example.com", "tok1"), string(got))
		})
	}
}

// TestLoginYarn_KeepsACommentOnlyFile pins that a file holding nothing but
// comments keeps them. go-yaml decodes it to no document, so there is no node
// to carry them through the round trip.
func TestLoginYarn_KeepsACommentOnlyFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "# npmRegistryServer: \"https://ar.example.com\"\n# yarnPath: .yarn/releases/yarn.cjs"
	require.NoError(t, os.WriteFile(yarnrcPath(home), []byte(fixture), 0o600))

	_, err := loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, fixture+"\n"+yarnrcWithEntry("//ar.example.com", "tok1"), string(got))
}

// TestLoginYarn_FillsAnEmptyNpmRegistries pins that an "npmRegistries:" key
// with no value, which Yarn reads as no entries, gets the entry rather than a
// refusal or a second npmRegistries key.
func TestLoginYarn_FillsAnEmptyNpmRegistries(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	require.NoError(t, os.WriteFile(yarnrcPath(home), []byte("npmRegistries:\n"), 0o600))

	_, err := loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, yarnrcWithEntry("//ar.example.com", "tok1"), string(got))
}

// TestLoginYarn_FillsAnEmptyFlowNpmRegistries pins that an entry added to
// "npmRegistries: {}" stays in flow style on one line, which Yarn still reads.
func TestLoginYarn_FillsAnEmptyFlowNpmRegistries(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	require.NoError(t, os.WriteFile(yarnrcPath(home), []byte("npmRegistries: {}\n"), 0o600))

	_, err := loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, "npmRegistries: {\"//ar.example.com\": {npmAlwaysAuth: true, npmAuthToken: tok1}}\n", string(got))
}

// TestYarnScopesWithOwnAuth pins which npmScopes entries make Yarn send their
// own credential instead of the npmRegistries token for --registry.
func TestYarnScopesWithOwnAuth(t *testing.T) {
	for name, tc := range map[string]struct {
		fixture string
		want    []string
	}{
		"no npmScopes": {"nodeLinker: pnp\n", nil},
		"scope token": {
			"npmScopes:\n  acme:\n    npmRegistryServer: \"https://ar.example.com\"\n    npmAuthToken: old\n",
			[]string{"acme"},
		},
		"scope ident": {
			"npmScopes:\n  acme:\n    npmRegistryServer: \"https://ar.example.com\"\n    npmAuthIdent: user:pass\n",
			[]string{"acme"},
		},
		"server with another scheme and a trailing slash": {
			"npmScopes:\n  acme:\n    npmRegistryServer: \"http://ar.example.com/\"\n    npmAuthToken: old\n",
			[]string{"acme"},
		},
		"server from the top level": {
			"npmRegistryServer: \"https://ar.example.com\"\nnpmScopes:\n  acme:\n    npmAuthToken: old\n",
			[]string{"acme"},
		},
		"tilde token is a string for Yarn": {
			"npmScopes:\n  acme:\n    npmRegistryServer: \"https://ar.example.com\"\n    npmAuthToken: ~\n",
			[]string{"acme"},
		},
		"empty token": {
			"npmScopes:\n  acme:\n    npmRegistryServer: \"https://ar.example.com\"\n    npmAuthToken:\n",
			nil,
		},
		"scope without a credential": {
			"npmScopes:\n  acme:\n    npmRegistryServer: \"https://ar.example.com\"\n",
			nil,
		},
		"scope on another registry": {
			"npmScopes:\n  acme:\n    npmRegistryServer: \"https://ar.example.com.evil.com\"\n    npmAuthToken: old\n",
			nil,
		},
		"scope on the default registry": {
			"npmScopes:\n  acme:\n    npmAuthToken: old\n",
			nil,
		},
	} {
		t.Run(name, func(t *testing.T) {
			var doc yaml.Node
			require.NoError(t, yaml.Unmarshal([]byte(tc.fixture), &doc))

			assert.Equal(t, tc.want, yarnScopesWithOwnAuth(doc.Content[0], mustURL(t, "https://ar.example.com/")))
		})
	}
}

// TestLoginYarn_RefusesAFileItCannotEditSafely pins that each shape this
// command will not edit is refused with the file left byte-for-byte as it
// was, rather than written in a way that changes entries it was not asked to
// touch or that Yarn would not read back.
func TestLoginYarn_RefusesAFileItCannotEditSafely(t *testing.T) {
	const (
		notAMapping = "holding something other than a mapping"
		anchor      = "anchor or alias"
	)
	for name, tc := range map[string]struct {
		fixture string
		wantErr string
	}{
		"invalid YAML":                {"npmRegistries: [\n", "parsing YAML"},
		"two documents":               {"nodeLinker: pnp\n---\nnpmRegistries: {}\n", "more than one YAML document"},
		"top level is a sequence":     {"- a\n- b\n", notAMapping},
		"top level is a string":       {"hello\n", notAMapping},
		"npmRegistries is a string":   {"npmRegistries: nope\n", notAMapping},
		"npmRegistries is a sequence": {"npmRegistries:\n  - \"//ar.example.com\"\n", notAMapping},
		"entry is a string":           {"npmRegistries:\n  \"//ar.example.com\": nope\n", notAMapping},
		"entry is a sequence":         {"npmRegistries:\n  \"//ar.example.com/\":\n    - npmAuthToken\n", notAMapping},
		"duplicate npmRegistries":     {"npmRegistries:\n  \"//ar.example.com\": {}\nnpmRegistries:\n  \"//other.example.com\": {}\n", "found 2 npmRegistries keys"},
		"anchored top level":          {"&root\nnpmRegistries: {}\n", anchor},
		"anchored npmRegistries":      {"npmRegistries: &regs\n  \"//other.example.com\": {}\nbackup: *regs\n", anchor},
		"aliased npmRegistries":       {"base: &regs\n  \"//other.example.com\": {}\nnpmRegistries: *regs\n", anchor},
		"alias key in npmRegistries":  {"k: &k \"//ar.example.com\"\nnpmRegistries:\n  *k : {}\n", anchor},
		"anchored entry":              {"npmRegistries:\n  \"//ar.example.com\": &e\n    npmAuthToken: old\n  \"//other.example.com\": *e\n", anchor},
		"anchored token":              {"npmRegistries:\n  \"//ar.example.com\":\n    npmAuthToken: &t old\n  \"//other.example.com\":\n    npmAuthToken: *t\n", anchor},
	} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			setHome(t, home)
			require.NoError(t, os.WriteFile(yarnrcPath(home), []byte(tc.fixture), 0o600))

			_, err := loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantErr)
			assert.Contains(t, err.Error(), yarnrcPath(home), "the error must name the file it refused")

			got, readErr := os.ReadFile(yarnrcPath(home))
			require.NoError(t, readErr)
			assert.Equal(t, tc.fixture, string(got), "a refused file must be left untouched")
		})
	}
}

// TestLoginYarn_IgnoresYarnRcFilename pins that the token lands in
// ~/.yarnrc.yml even when YARN_RC_FILENAME is set. Yarn only uses that name
// for the project file and its parent directories. The home file is always
// ~/.yarnrc.yml.
func TestLoginYarn_IgnoresYarnRcFilename(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)
	t.Setenv("YARN_RC_FILENAME", ".custom-yarnrc.yml")

	_, err := loginYarn(mustURL(t, "https://ar.example.com/"), "tok1")
	require.NoError(t, err)

	got, err := os.ReadFile(yarnrcPath(home))
	require.NoError(t, err)
	assert.Equal(t, yarnrcWithEntry("//ar.example.com", "tok1"), string(got))

	_, err = os.Stat(filepath.Join(home, ".custom-yarnrc.yml"))
	assert.ErrorIs(t, err, os.ErrNotExist)
}
