//go:build !integration

package login

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func sbtCredentialsPath(home string) string {
	return filepath.Join(home, ".sbt", "1.0", "credentials.sbt")
}

// sbt2CredentialsPath is the sbt 2.x file for a home where neither
// SBT_CONFIG_HOME nor XDG_CONFIG_HOME is set, as setHome leaves it.
func sbt2CredentialsPath(home string) string {
	return filepath.Join(home, ".config", "sbt", "2", "credentials.sbt")
}

func sbtCredentialsLine(host, token string) string {
	return `credentials += Credentials("artifact-registry", "` + host + `", "__token__", "` + token + `")`
}

func requireLoginSbt(t *testing.T, registry *url.URL, token string) {
	t.Helper()

	_, _, err := loginSbt("", registry, token)
	require.NoError(t, err)
}

// TestLoginSbt_WritesBothGlobalBases pins that one login covers a project on
// either sbt version. Which base a build reads follows the sbt.version the
// project declares, so a login that wrote only one would leave every project on
// the other version sending no credential.
func TestLoginSbt_WritesBothGlobalBases(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	written, skipped, err := loginSbt("", mustURL(t, "https://ar.example.com"), "tok1")
	require.NoError(t, err)

	assert.Equal(t, []sbtCredentialsFile{
		{readBy: "sbt 1.x", path: sbtCredentialsPath(home)},
		{readBy: "sbt 2.x", path: sbt2CredentialsPath(home)},
	}, written)
	assert.Empty(t, skipped)

	for _, path := range []string{sbtCredentialsPath(home), sbt2CredentialsPath(home)} {
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1")+"\n", string(got), path)

		info, err := os.Stat(path)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), path)
	}
}

// TestSbtCredentialsFilesFor_SbtTwoBaseOrder pins the order sbt 2.x resolves
// its global base in, as SysProp.defaultGlobalBaseDirectory does in sbt 2.0.8.
// Getting it wrong writes a file sbt 2.x never opens on a machine that sets
// one of these variables.
func TestSbtCredentialsFilesFor_SbtTwoBaseOrder(t *testing.T) {
	home := t.TempDir()
	sbtConfigHome := filepath.Join(home, "sbt-config")
	configHome := filepath.Join(home, "config-home")

	launcherFile := sbtCredentialsFile{
		readBy: "sbt 1.x and 2.x builds started by the sbt.bat of sbt 2.x, which read no other file, even with SBT_CONFIG_HOME set",
		path:   filepath.Join(configHome, "sbt", "credentials.sbt"),
	}

	tests := []struct {
		name        string
		goos        string
		env         map[string]string
		want        string
		wantExtra   []sbtCredentialsFile
		wantSkipped string
	}{
		{
			name: "SBT_CONFIG_HOME wins over XDG_CONFIG_HOME",
			goos: "linux",
			env:  map[string]string{"SBT_CONFIG_HOME": sbtConfigHome, "XDG_CONFIG_HOME": configHome},
			want: filepath.Join(sbtConfigHome, "2", "credentials.sbt"),
		},
		{
			name: "XDG_CONFIG_HOME when SBT_CONFIG_HOME is unset",
			goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": configHome},
			want: filepath.Join(configHome, "sbt", "2", "credentials.sbt"),
		},
		{
			name: "~/.config when neither is set",
			goos: "linux",
			want: sbt2CredentialsPath(home),
		},
		{
			name: "surrounding whitespace is trimmed, as sbt trims it",
			goos: "linux",
			env:  map[string]string{"XDG_CONFIG_HOME": " " + configHome + "\t"},
			want: filepath.Join(configHome, "sbt", "2", "credentials.sbt"),
		},
		{
			name:        "a relative XDG_CONFIG_HOME is ignored, as the XDG spec requires",
			goos:        "linux",
			env:         map[string]string{"XDG_CONFIG_HOME": filepath.Join("relative", "config")},
			want:        sbt2CredentialsPath(home),
			wantSkipped: "XDG_CONFIG_HOME",
		},
		{
			name:        "a relative SBT_CONFIG_HOME is ignored the same way",
			goos:        "linux",
			env:         map[string]string{"SBT_CONFIG_HOME": "relative", "XDG_CONFIG_HOME": configHome},
			want:        filepath.Join(configHome, "sbt", "2", "credentials.sbt"),
			wantSkipped: "SBT_CONFIG_HOME",
		},
		{
			name:        "a blank SBT_CONFIG_HOME is skipped and reported, since sbt resolves it to the build directory",
			goos:        "linux",
			env:         map[string]string{"SBT_CONFIG_HOME": "  "},
			want:        sbt2CredentialsPath(home),
			wantSkipped: "SBT_CONFIG_HOME",
		},
		{
			name: "an empty SBT_CONFIG_HOME is unset to sbt too, so it is not reported",
			goos: "linux",
			env:  map[string]string{"SBT_CONFIG_HOME": "", "XDG_CONFIG_HOME": configHome},
			want: filepath.Join(configHome, "sbt", "2", "credentials.sbt"),
		},
		{
			name:      "LOCALAPPDATA instead of XDG_CONFIG_HOME on Windows",
			goos:      "windows",
			env:       map[string]string{"LOCALAPPDATA": configHome, "XDG_CONFIG_HOME": filepath.Join(home, "xdg")},
			want:      filepath.Join(configHome, "sbt", "2", "credentials.sbt"),
			wantExtra: []sbtCredentialsFile{launcherFile},
		},
		{
			name: "XDG_CONFIG_HOME is not read on Windows",
			goos: "windows",
			env:  map[string]string{"XDG_CONFIG_HOME": configHome},
			want: sbt2CredentialsPath(home),
		},
		{
			name:      "SBT_CONFIG_HOME wins over LOCALAPPDATA on Windows, and the sbt.bat base is still written",
			goos:      "windows",
			env:       map[string]string{"SBT_CONFIG_HOME": sbtConfigHome, "LOCALAPPDATA": configHome},
			want:      filepath.Join(sbtConfigHome, "2", "credentials.sbt"),
			wantExtra: []sbtCredentialsFile{launcherFile},
		},
		{
			name: "LOCALAPPDATA is not read off Windows",
			goos: "linux",
			env:  map[string]string{"LOCALAPPDATA": configHome},
			want: sbt2CredentialsPath(home),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			setHome(t, home)
			for name, value := range tc.env {
				t.Setenv(name, value)
			}

			files, skipped := sbtCredentialsFilesFor(home, tc.goos)
			want := append([]sbtCredentialsFile{
				{readBy: "sbt 1.x", path: sbtCredentialsPath(home)},
				{readBy: "sbt 2.x", path: tc.want},
			}, tc.wantExtra...)
			assert.Equal(t, want, files, "the sbt 1.x file follows none of these variables")
			assert.Equal(t, tc.wantSkipped, skipped)
		})
	}
}

// TestLoginSbt_FailsNamingTheFileItWroteWhenTheSecondWriteFails pins that a
// login which reached only the sbt 1.x base is reported as a failure, and that
// the error names the file that does hold the token. Reporting success would
// leave every sbt 2.x build unauthenticated after a green check.
func TestLoginSbt_FailsNamingTheFileItWroteWhenTheSecondWriteFails(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	// A regular file where ~/.config has to be a directory, so creating the
	// sbt 2.x base fails after the sbt 1.x file is written.
	require.NoError(t, os.WriteFile(filepath.Join(home, ".config"), nil, 0o600))

	written, _, err := loginSbt("", mustURL(t, "https://ar.example.com"), "tok1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "wrote "+sbtCredentialsPath(home)+" for sbt 1.x, but not the file for sbt 2.x")
	assert.Equal(t, []sbtCredentialsFile{{readBy: "sbt 1.x", path: sbtCredentialsPath(home)}}, written)

	got, readErr := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, readErr)
	assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1")+"\n", string(got))
}

// TestLoginSbt_FirstWriteFailureWritesNothing pins the other order: a login
// that cannot write the sbt 1.x file stops there, rather than leaving a token
// in the sbt 2.x base behind an error.
func TestLoginSbt_FirstWriteFailureWritesNothing(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "/* work in progress\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	written, _, err := loginSbt("", mustURL(t, "https://ar.example.com"), "tok1")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "wrote ")
	assert.Empty(t, written)

	_, statErr := os.Stat(sbt2CredentialsPath(home))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

func TestLoginSbt_CreatesFileWhenAbsent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")

	want := sbtCredentialsLine("ar.example.com", "tok1") + "\n"

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	assert.Equal(t, want, string(got))

	fileInfo, err := os.Stat(sbtCredentialsPath(home))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), fileInfo.Mode().Perm())

	dirInfo, err := os.Stat(filepath.Join(home, ".sbt", "1.0"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o700), dirInfo.Mode().Perm())
}

func TestLoginSbt_PreservesUnrelatedContent(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := sbtCredentialsLine("other.example.com", "other-secret") + "\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, sbtCredentialsLine("other.example.com", "other-secret"))
	assert.Contains(t, content, sbtCredentialsLine("ar.example.com", "tok1"))
}

func TestLoginSbt_UpdatesExistingHostInPlace(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")
	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok2")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, `"ar.example.com"`), "must not duplicate the Credentials(...) line")
	assert.Contains(t, content, sbtCredentialsLine("ar.example.com", "tok2"))
	assert.NotContains(t, content, sbtCredentialsLine("ar.example.com", "tok1"))
}

// TestLoginSbt_StripsThePort pins the host written for a registry on a
// non-standard port. sbt's IvyAuthenticator and coursier look credentials up
// by the port-less requesting host, so a "host:port" entry never matches and
// the build request goes out unauthenticated — a 401 with nothing wrong at
// login time.
func TestLoginSbt_StripsThePort(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	requireLoginSbt(t, mustURL(t, "https://ar.example.com:8443"), "tok1")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1")+"\n", string(got))
}

// TestLoginSbt_RefreshMatchesPortedAndPortlessRegistry covers the upsert side
// of stripping the port: the same registry given with and without its port
// must land on one line, not two.
func TestLoginSbt_RefreshMatchesPortedAndPortlessRegistry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	requireLoginSbt(t, mustURL(t, "https://ar.example.com:8443"), "tok1")
	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok2")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, `"ar.example.com"`))
	assert.Contains(t, content, sbtCredentialsLine("ar.example.com", "tok2"))
}

// TestLoginSbt_RefreshMatchesCRLFFile pins that a credentials.sbt with Windows
// line endings is refreshed rather than appended to. upsertLines splits on "\n"
// only, so every line arrives with a trailing carriage return; an anchored
// pattern that does not allow for it never matches the line it wrote, and every
// login adds another entry holding a live token.
func TestLoginSbt_RefreshMatchesCRLFFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := sbtCredentialsLine("ar.example.com", "tok1") + "\r\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok2")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "credentials +="), "must refresh the line, not append a second one")
	assert.Contains(t, content, sbtCredentialsLine("ar.example.com", "tok2"))
	assert.NotContains(t, content, "tok1")
}

// TestLoginSbt_LowerCasesTheHost pins that the recorded host is lower-cased, so
// the same registry typed two ways lands on one entry rather than two that
// disagree about which token is current.
func TestLoginSbt_LowerCasesTheHost(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	requireLoginSbt(t, mustURL(t, "https://AR.Example.COM:8443"), "tok1")
	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok2")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Equal(t, 1, strings.Count(content, "credentials +="))
	assert.Contains(t, content, sbtCredentialsLine("ar.example.com", "tok2"))
}

// TestSbtCredentialLineRegexp_QuotesRealmMetacharacters pins that the realm is
// regex-quoted before it is interpolated into the match pattern.
//
// The realm is a constant this package writes into a file it later has to
// find again (see sbtCredentialRealm). A realm carrying "(" or "[" would still compile
// without QuoteMeta, and would still be written to the file, but would stop
// matching the line this package writes, so every login would append another
// entry holding a live token instead of refreshing the existing one.
func TestSbtCredentialLineRegexp_QuotesRealmMetacharacters(t *testing.T) {
	for _, realm := range []string{
		"Artifact Registry",
		"Artifact Registry (v2)",
		"GitLab Artifact Registry [beta]",
		"ar.example.com",
	} {
		t.Run(realm, func(t *testing.T) {
			re := sbtCredentialLineRegexp(realm)

			m := re.FindStringSubmatch(sbtCredentialLine(realm, "ar.example.com", "tok1"))

			require.NotNil(t, m, "the pattern must match the line the same realm produces")
			assert.Equal(t, "ar.example.com", m[1])
		})
	}
}

// TestSbtCredentialLineRegexp_DoesNotMatchAnotherRealm guards the flip side of
// quoting: a metacharacter must be matched literally, not as a wildcard.
func TestSbtCredentialLineRegexp_DoesNotMatchAnotherRealm(t *testing.T) {
	re := sbtCredentialLineRegexp("ar.example.com")

	assert.Nil(t, re.FindStringSubmatch(sbtCredentialLine("arXexampleXcom", "h", "tok1")))
}

func TestLoginSbt_DifferentHostAddsSeparateEntry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	requireLoginSbt(t, mustURL(t, "https://ar1.example.com"), "tok1")
	requireLoginSbt(t, mustURL(t, "https://ar2.example.com"), "tok2")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, sbtCredentialsLine("ar1.example.com", "tok1"))
	assert.Contains(t, content, sbtCredentialsLine("ar2.example.com", "tok2"))
}

// TestLoginSbt_LeavesACommentedEntryAlone pins that an entry inside a /* */
// comment is not refreshed in place. sbt compiles credentials.sbt as Scala, so
// updating the commented line would put a live token where sbt never reads it:
// the build would still go out unauthenticated, after a login that reported
// success.
func TestLoginSbt_LeavesACommentedEntryAlone(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "/*\n" + sbtCredentialsLine("ar.example.com", "commented-out") + "\n*/\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, sbtCredentialsLine("ar.example.com", "commented-out"), "the commented entry stays as it was")
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1"), lines[len(lines)-1], "the live entry is appended after the comment")
}

// TestLoginSbt_LeavesALineCommentedEntryAlone covers the line-comment form of
// the same case.
func TestLoginSbt_LeavesALineCommentedEntryAlone(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "// " + sbtCredentialsLine("ar.example.com", "commented-out") + "\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)
	content := string(got)

	assert.Contains(t, content, "// "+sbtCredentialsLine("ar.example.com", "commented-out"))
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1"), lines[len(lines)-1])
}

// TestLoginSbt_RefusesAnUnterminatedComment pins that a file ending inside a
// comment it never closes is left untouched, with an error naming that comment.
// The credentials line would otherwise be appended inside it, which is a login
// that quietly does nothing.
func TestLoginSbt_RefusesAnUnterminatedComment(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := "/* work in progress\n" + sbtCredentialsLine("other.example.com", "old") + "\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	_, _, err := loginSbt("", mustURL(t, "https://ar.example.com"), "tok1")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unterminated /* comment")

	got, readErr := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, readErr)
	assert.Equal(t, fixture, string(got), "the file must be left untouched")
}

// TestLoginSbt_RefreshMatchesAnIndentedEntry pins that an entry the user
// indented, or spaced differently, is refreshed rather than duplicated. Scala
// ignores that whitespace, so both spellings are the same credential, and sbt
// would otherwise hold two entries for one host that disagree on the token.
func TestLoginSbt_RefreshMatchesAnIndentedEntry(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := `  credentials  +=  Credentials( "artifact-registry" , "ar.example.com" , "__token__" , "old" )` + "\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)

	assert.Equal(t, "  "+sbtCredentialsLine("ar.example.com", "tok1")+"\n", string(got), "the entry is refreshed in place, keeping its indentation")
}

// TestLoginSbt_RefreshKeepsATrailingComment pins the same for an entry with a
// note on the end of it: the token is refreshed, the note stays.
func TestLoginSbt_RefreshKeepsATrailingComment(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := sbtCredentialsLine("ar.example.com", "old") + " // refreshed by glab\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)

	assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1")+" // refreshed by glab\n", string(got))
}

// TestLoginSbt_RefreshKeepsATrailingBlockComment covers the "/* */" spelling of
// a trailing comment. Missing it appends a second entry for the host, and
// Credentials.forHost takes the first one it finds, so sbt would keep sending
// the stale token while this command reported a fresh login.
func TestLoginSbt_RefreshKeepsATrailingBlockComment(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	fixture := sbtCredentialsLine("ar.example.com", "old") + " /* written by glab */\n"
	require.NoError(t, os.MkdirAll(filepath.Dir(sbtCredentialsPath(home)), 0o700))
	require.NoError(t, os.WriteFile(sbtCredentialsPath(home), []byte(fixture), 0o600))

	requireLoginSbt(t, mustURL(t, "https://ar.example.com"), "tok1")

	got, err := os.ReadFile(sbtCredentialsPath(home))
	require.NoError(t, err)

	assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1")+" /* written by glab */\n", string(got))
	assert.Equal(t, 1, strings.Count(string(got), "credentials +="), "must refresh the entry, not add a second one")
}

// TestSbtCommentedLines pins the nesting and the line-comment rule the scan
// relies on.
func TestSbtCommentedLines(t *testing.T) {
	lines := []string{
		"credentials += one",
		"/* /* still open",
		"inside",
		"*/ still inside",
		"*/",
		"credentials += two",
		"// /* not a comment",
		"credentials += three",
	}

	commented, depth := sbtCommentedLines(lines)

	assert.Equal(t, []bool{false, false, true, true, true, false, false, false}, commented)
	assert.Equal(t, 0, depth)
}

func TestLoginSbt_WritesConfigFile(t *testing.T) {
	home := t.TempDir()
	setHome(t, home)

	path := filepath.Join(t.TempDir(), "sbt", "2", "credentials.sbt")
	written, skipped, err := loginSbt(path, mustURL(t, "https://ar.example.com"), "tok1")
	require.NoError(t, err)
	assert.Equal(t, []sbtCredentialsFile{{readBy: "--config-file", path: path}}, written)
	assert.Empty(t, skipped)

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, sbtCredentialsLine("ar.example.com", "tok1")+"\n", string(got))

	_, err = os.Stat(sbtCredentialsPath(home))
	assert.ErrorIs(t, err, os.ErrNotExist, "nothing may be written under ~/.sbt when a path is given")
}
