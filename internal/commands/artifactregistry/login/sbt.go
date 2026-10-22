package login

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// sbtCredentialRealm is the realm sbt records the credentials under. It is
// what the Artifact Registry sends on the maven route that both --gradle and
// --sbt use: `Basic realm="artifact-registry"`, lower case and hyphenated.
//
// The realm has to match that header because Ivy keys its credentials store on
// (host, realm), so `sbt publish` finds no entry when they disagree. The
// ordinary resolve is looser: sbt's own lookup keys on the host alone
// (Credentials.forHost), and coursier's DirectCredentials.matches checks the
// host and user, with the realm gating only the credentials it resends when
// following a redirect.
const sbtCredentialRealm = "artifact-registry"

// sbtCredentialLine renders the line loginSbt writes for host.
func sbtCredentialLine(realm, host, token string) string {
	return fmt.Sprintf(`credentials += Credentials(%q, %q, "__token__", %q)`, realm, host, token)
}

// sbtCredentialLineRegexp builds the pattern that finds a line previously
// written by sbtCredentialLine for realm, capturing the host that appears as
// the second quoted argument.
//
// realm goes through regexp.QuoteMeta because it is a constant interpolated
// into a pattern rather than a literal. A realm carrying "(", ")" or "[" would
// still compile without it, and would still be written into the file, but would
// stop matching the line this file writes: every login would then append
// another entry holding a live token instead of refreshing the existing one.
//
// The trailing \r? tolerates a CRLF credentials.sbt. upsertLines splits on "\n"
// only, so on a CRLF file every line arrives with a trailing carriage return,
// and an anchored pattern without this would never match a line it had written.
//
// Whitespace is tolerated wherever Scala ignores it, and so is a trailing
// comment in either spelling, "//" or "/* */", which the second group captures
// so the caller can keep it. All of these are the same credential to sbt, so
// matching them strictly would append a second entry for the same host instead
// of refreshing the first. Credentials.forHost then takes the first entry it
// finds for the host, so sbt would keep using the stale token while this
// command reported a fresh login.
func sbtCredentialLineRegexp(realm string) *regexp.Regexp {
	return regexp.MustCompile(`^[ \t]*credentials[ \t]*\+=[ \t]*Credentials\([ \t]*"` + regexp.QuoteMeta(realm) + `"[ \t]*,[ \t]*"([^"]*)"[ \t]*,[ \t]*"__token__"[ \t]*,[ \t]*"[^"]*"[ \t]*\)[ \t]*((?://|/\*)[^\r]*?)?[ \t]*\r?$`)
}

// sbtCommentedLines reports, for each line, whether it starts inside a /* */
// block comment, and how deep in one the file ends.
//
// sbt compiles credentials.sbt as Scala, so a credentials line inside a
// comment is not a credential. Refreshing one in place would write a live
// token into a comment: sbt would still have nothing to authenticate with,
// and this command would report a login that no build has. Appending after a
// comment the file never closes has the same effect, which is what the
// returned depth is for.
//
// Scala nests block comments, so the depth counts up and down rather than
// toggling, and a "//" hides the rest of its own line, including a "/*" that
// would otherwise open a block.
//
// The scan is string-literal-blind, and that cuts both ways. A "/*" inside a
// string literal opens a comment here that Scala never opened, so live entries
// below it look commented and the next login appends instead of refreshing. In
// the other direction the "//" every URL carries hides the rest of its line,
// including a real "/*" after it, so an entry inside that block comment is
// refreshed in place: a live token written where sbt never reads it. Neither
// case can arise from a line this command writes; both need a hand-written one
// that puts a comment marker inside a string.
func sbtCommentedLines(lines []string) ([]bool, int) {
	commented := make([]bool, len(lines))
	depth := 0

	for i, line := range lines {
		commented[i] = depth > 0

		for j := 0; j < len(line); j++ {
			switch {
			case depth == 0 && strings.HasPrefix(line[j:], "//"):
				j = len(line)
			case strings.HasPrefix(line[j:], "/*"):
				depth++
				j++
			case depth > 0 && strings.HasPrefix(line[j:], "*/"):
				depth--
				j++
			}
		}
	}

	return commented, depth
}

var sbtCredentialLineRe = sbtCredentialLineRegexp(sbtCredentialRealm)

// sbtCredentialsFile is one credentials.sbt loginSbt writes, with the sbt
// builds that read it.
type sbtCredentialsFile struct {
	readBy string
	path   string
}

// sbtCredentialsFilesFor returns the credentials.sbt files this command writes,
// one in each sbt global base, and the name of the environment variable it
// skipped while resolving the sbt 2.x base, or "" when it skipped none.
//
// sbt keeps one global base per binary version, and neither version reads the
// other's. Which one a build reads follows the sbt.version the project declares
// in project/build.properties, not the sbt installed, so no single file covers
// every project on a machine. An unread file in the other base is inert, so
// writing both is safe.
//
// sbt 1.x reads ~/.sbt/1.0 ("Settings that should be applied to all projects
// can go in $HOME/.sbt/1.0/global.sbt, or any file in $HOME/.sbt/1.0 with a
// .sbt extension"). sbt 2.x reads <base>/2, and SysProp.defaultGlobalBaseDirectory
// in sbt 2.0.8 picks <base> from $SBT_CONFIG_HOME, then $XDG_CONFIG_HOME/sbt
// (%LOCALAPPDATA%\sbt on Windows), then ~/.config/sbt. Those are environment
// variables, so the 2.x base is resolved here the way sbt resolves it, trimmed
// as sbt trims them.
//
// The -Dsbt.global.base override is a JVM property, so it is not visible here,
// and BuildPaths.getGlobalBase uses it as is, with no version directory
// appended. The one that matters is what the sbt.bat shipped with sbt 2.x
// (2.0.8, lines 727-731) passes whenever LOCALAPPDATA is set:
// %LOCALAPPDATA%\sbt. Every build that launcher starts, on either sbt version,
// reads that base, so on Windows it gets a third file. With a 1.x launcher
// nothing reads it. The property also outranks SBT_CONFIG_HOME, so such a
// build reads neither of the other two files, and its readBy says so: without
// that, a user who set SBT_CONFIG_HOME would take the sbt 2.x file for the one
// in use.
//
// One departure: a relative value is skipped, as the XDG Base Directory spec
// requires ("If an implementation encounters a relative path in any of these
// variables it should consider the path invalid and ignore it"). sbt instead
// resolves it against the directory the build runs in, which this command
// cannot know, so no file written here would match it. A blank value is
// skipped too: sbt drops only an empty one, so it resolves a blank one to the
// build directory as well. Either way the sbt 2.x file reported is one sbt 2.x
// does not read, which is why the skipped name is returned.
func sbtCredentialsFilesFor(home, goos string) ([]sbtCredentialsFile, string) {
	configVar := "XDG_CONFIG_HOME"
	if goos == "windows" {
		configVar = "LOCALAPPDATA"
	}

	base2 := filepath.Join(home, ".config", "sbt")
	skipped := ""
	for _, name := range []string{"SBT_CONFIG_HOME", configVar} {
		if value, ok := absoluteEnv(name); ok {
			base2 = value
			if name == configVar {
				base2 = filepath.Join(value, "sbt")
			}
			break
		}
		if skipped == "" && os.Getenv(name) != "" {
			skipped = name
		}
	}

	files := []sbtCredentialsFile{
		{readBy: "sbt 1.x", path: filepath.Join(home, ".sbt", "1.0", "credentials.sbt")},
		{readBy: "sbt 2.x", path: filepath.Join(base2, "2", "credentials.sbt")},
	}
	if goos == "windows" {
		if localAppData, ok := absoluteEnv("LOCALAPPDATA"); ok {
			files = append(files, sbtCredentialsFile{
				readBy: "sbt 1.x and 2.x builds started by the sbt.bat of sbt 2.x, which read no other file, even with SBT_CONFIG_HOME set",
				path:   filepath.Join(localAppData, "sbt", "credentials.sbt"),
			})
		}
	}
	return files, skipped
}

// absoluteEnv returns the environment variable name with surrounding
// whitespace trimmed, and false when it is unset, blank, or a relative path.
func absoluteEnv(name string) (string, bool) {
	value := strings.TrimSpace(os.Getenv(name))
	return value, filepath.IsAbs(value)
}

// describeSbtCredentialsFiles renders files as "<path> for <readBy>", joined
// with "and", for the success line and the partial-write error.
func describeSbtCredentialsFiles(files []sbtCredentialsFile) string {
	parts := make([]string, len(files))
	for i, file := range files {
		parts[i] = fmt.Sprintf("%s for %s", file.path, file.readBy)
	}
	return strings.Join(parts, " and ")
}

// loginSbt upserts a `credentials += Credentials("artifact-registry", host,
// "__token__", token)` line in every file sbtCredentialsFilesFor returns, for
// registry's host, matched by the host appearing as the second quoted argument,
// so a different host's line is left untouched. It returns the files written
// and the variable sbtCredentialsFilesFor skipped.
//
// A failure after an earlier file was written still fails the login, and the
// error names the files that do hold the token: a build that reads the file
// that failed would go out unauthenticated after a login reported as done.
func loginSbt(registry *url.URL, token string) ([]sbtCredentialsFile, string, error) {
	// Hostname(), not Host: sbt's IvyAuthenticator and coursier both look
	// credentials up by the port-less requesting host
	// (Authenticator.getRequestingHost / URI.getHost). Writing "host:port"
	// here for a registry on a non-standard port would leave sbt finding no
	// match at build time and sending the request unauthenticated, with no
	// error at login time.
	//
	// Lower-cased for the same reason the match has to be reliable: host names
	// are case-insensitive, so the same registry typed two ways must land on one
	// entry rather than two that disagree about which token is current.
	host := strings.ToLower(registry.Hostname())

	line := sbtCredentialLine(sbtCredentialRealm, host, token)

	home, err := homeDir()
	if err != nil {
		return nil, "", err
	}

	files, skipped := sbtCredentialsFilesFor(home, runtime.GOOS)
	var written []sbtCredentialsFile
	for _, file := range files {
		if err := upsertSbtCredentialLine(file.path, host, line); err != nil {
			if len(written) == 0 {
				return nil, skipped, err
			}
			return written, skipped, fmt.Errorf("wrote %s, but not the file for %s: %w", describeSbtCredentialsFiles(written), file.readBy, err)
		}
		written = append(written, file)
	}

	return written, skipped, nil
}

// upsertSbtCredentialLine refreshes host's entry in the credentials.sbt at
// path with line, or appends line when the file has none.
func upsertSbtCredentialLine(path, host, line string) error {
	return upsertLines(path, func(lines []string) ([]string, error) {
		commented, depth := sbtCommentedLines(lines)
		if depth > 0 {
			return nil, errors.New("found an unterminated /* comment: the credentials line would be appended inside it, where sbt never reads it; close the comment, or add the line by hand")
		}

		found := false
		for i, l := range lines {
			if commented[i] {
				continue
			}
			m := sbtCredentialLineRe.FindStringSubmatch(l)
			if len(m) < 3 || m[1] != host {
				continue
			}
			// The entry keeps the indentation and the trailing comment it had:
			// only the token is this command's to change, and a refresh that
			// drops the note somebody left on the line is a silent edit.
			indent := l[:len(l)-len(strings.TrimLeft(l, " \t"))]
			lines[i] = indent + line
			if m[2] != "" {
				lines[i] += " " + m[2]
			}
			found = true
		}
		if !found {
			lines = append(lines, line)
		}
		return lines, nil
	})
}
