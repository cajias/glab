package gomodproxy

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gitlab.com/gitlab-org/cli/internal/text"
)

const firewallHeader = "X-Gitlab-Dependency-Firewall"

// cmd/go/internal/web shows a proxy's error body only under these
// constraints; violating any leaves the user a bare "403 Forbidden":
//
//   - the media type must be text/plain with charset utf-8, us-ascii, or
//     absent;
//   - the detail is truncated at 8 lines and 648 bytes, counted with the
//     lines rejoined by "\n\t", and a line that would overflow is dropped
//     whole;
//   - it stops at the first blank line;
//   - any rune that is neither graphic nor space (a control or format
//     character such as U+200B) voids the whole detail.
const (
	maxDetailLines = 8
	maxDetailBytes = 8 * 81
	blockedValue   = "blocked"
)

// writeBlocked answers 403 deliberately: cmd/go treats only 404 and 410 as
// "not on this proxy, try the next source", so a 404 would let go fetch the
// blocked module elsewhere; every other non-200 is terminal.
func writeBlocked(w http.ResponseWriter, modulePath, version, reason string) {
	body := blockBody(modulePath, version, reason)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set(firewallHeader, blockedValue)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusForbidden)
	_, _ = w.Write([]byte(body))
}

func blockBody(modulePath, version, reason string) string {
	lines := []string{
		fmt.Sprintf("blocked by the GitLab Dependency Firewall: %s@%s", modulePath, version),
	}
	if r := sanitizeReason(reason); r != "" {
		lines = append(lines, "reason: "+r)
	}
	return truncateDetail(lines)
}

// sanitizeReason flattens server-supplied text onto one line: a blank line
// would cut the detail short, and a rune go doesn't accept would void it.
func sanitizeReason(reason string) string {
	stripped := text.Strip(reason)
	cleaned := strings.Map(func(r rune) rune {
		if r == utf8.RuneError || unicode.IsControl(r) || unicode.IsSpace(r) {
			return ' '
		}
		if !unicode.IsGraphic(r) {
			return -1
		}
		return r
	}, stripped)
	return strings.Join(strings.Fields(cleaned), " ")
}

// truncateDetail budgets each line against go's "\n\t" rendering. Trimming
// the body to 648 bytes with "\n" breaks comes out over budget once go
// rejoins it, and go then drops the whole reason line; cutting the
// overflowing line here, on a rune boundary, keeps it readable.
func truncateDetail(lines []string) string {
	const ellipsis = "…"
	if len(lines) > maxDetailLines {
		lines = lines[:maxDetailLines]
	}
	used := 0
	kept := make([]string, 0, len(lines))
	for i, line := range lines {
		if i > 0 {
			used += len("\n\t")
		}
		room := maxDetailBytes - used
		if len(line) <= room {
			used += len(line)
			kept = append(kept, line)
			continue
		}
		if room > len(ellipsis) {
			// An invalid UTF-8 tail would void the whole detail.
			line = line[:room-len(ellipsis)]
			for !utf8.ValidString(line) {
				line = line[:len(line)-1]
			}
			kept = append(kept, line+ellipsis)
		}
		break
	}
	return strings.Join(kept, "\n")
}
