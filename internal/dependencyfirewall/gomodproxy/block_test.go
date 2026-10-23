//go:build !integration

package gomodproxy

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteBlockedResponseShape(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeBlocked(rec, "github.com/pkg/errors", "v0.9.1", "known vulnerability CVE-2024-0001")

	res := rec.Result()
	defer res.Body.Close()

	assert.Equal(t, http.StatusForbidden, res.StatusCode)
	assert.Equal(t, "text/plain; charset=utf-8", res.Header.Get("Content-Type"))
	assert.Equal(t, blockedValue, res.Header.Get("X-Gitlab-Dependency-Firewall"))
	assert.Equal(t, "no-store", res.Header.Get("Cache-Control"))

	body := rec.Body.String()
	assert.Contains(t, body, "github.com/pkg/errors")
	assert.Contains(t, body, "v0.9.1")
	assert.Contains(t, body, "known vulnerability CVE-2024-0001")
}

func TestBlockBodyHonorsGoErrorDetailLimits(t *testing.T) {
	t.Parallel()
	hostile := "\x1b[31mred\x1b[0m\n\nblank line above\x00control\u200bzero\u202eright\ufeffbom\u00adsoft\n" + strings.Repeat("long ", 500)
	body := blockBody("example.com/m", "v1.0.0", hostile)

	assert.True(t, utf8.ValidString(body), "body must be valid UTF-8")
	assert.LessOrEqual(t, len(body), maxDetailBytes)
	assert.LessOrEqual(t, strings.Count(body, "\n")+1, maxDetailLines)

	for line := range strings.SplitSeq(body, "\n") {
		assert.NotEmpty(t, strings.TrimSpace(line), "a blank line truncates the detail")
	}
	for _, r := range body {
		if r == '\n' {
			continue
		}
		assert.True(t, unicode.IsGraphic(r) || unicode.IsSpace(r), "go voids the whole detail on rune %U", r)
	}
	assert.NotContains(t, body, "\x1b")
}

// goErrorDetail renders body the way cmd/go shows a proxy error body to the
// user, copied from the loop in cmd/go/internal/web/api.go (go1.26): lines are
// joined with "\n\t", and a line that would push the detail past
// 8*81 bytes is dropped whole, not cut.
func goErrorDetail(body string) string {
	const maxLines, maxBytes = 8, 8 * 81
	var detail strings.Builder
	for i, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			break
		}
		if i > 0 {
			detail.WriteString("\n\t")
		}
		if i >= maxLines {
			detail.WriteString("[Truncated: too many lines.]")
			break
		}
		if detail.Len()+len(line) > maxBytes {
			detail.WriteString("[Truncated: too long.]")
			break
		}
		detail.WriteString(line)
	}
	return detail.String()
}

func TestLongReasonSurvivesGoRendering(t *testing.T) {
	t.Parallel()
	for _, n := range []int{560, 565, 566, 567, 600, 11_309} {
		reason := "policy check failed: " + strings.Repeat("x", n)
		shown := goErrorDetail(blockBody("github.com/BurntSushi/toml", "v1.6.0", reason))
		assert.NotContains(t, shown, "[Truncated", "reason of %d bytes", n)
		assert.Contains(t, shown, "reason: policy check failed: xxx", "reason of %d bytes", n)
	}

	// A cut through a multi-byte rune would void the whole detail.
	shown := goErrorDetail(blockBody("example.com/m", "v1.0.0", strings.Repeat("é", 400)))
	assert.True(t, utf8.ValidString(shown))
	assert.NotContains(t, shown, "[Truncated")
	assert.True(t, strings.HasSuffix(shown, "é…"), "a cut reason ends with an ellipsis")
}

func TestShortReasonIsShownVerbatim(t *testing.T) {
	t.Parallel()
	shown := goErrorDetail(blockBody("example.com/m", "v1.0.0", "Package 'm' violates 'Deny high volume' policy"))
	assert.Equal(t, "blocked by the GitLab Dependency Firewall: example.com/m@v1.0.0\n\treason: Package 'm' violates 'Deny high volume' policy", shown)
}

func TestBlockBodyOmitsEmptyReason(t *testing.T) {
	t.Parallel()
	body := blockBody("example.com/m", "v1.0.0", "")
	assert.Equal(t, "blocked by the GitLab Dependency Firewall: example.com/m@v1.0.0", body)
	assert.NotContains(t, body, "reason:")
}

func TestSanitizeReasonCollapsesWhitespace(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a b c", sanitizeReason("a\n\nb\tc"))
	assert.Empty(t, sanitizeReason("   \n\t "))
}

func TestWriteBlockedContentLengthMatchesBody(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	writeBlocked(rec, "example.com/m", "v1.0.0", "nope")
	res := rec.Result()
	defer res.Body.Close()
	require.Equal(t, rec.Body.Len(), int(res.ContentLength))
}
