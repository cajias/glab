package cmdutils

import (
	"regexp"
	"strings"
)

// draftTitleRE matches the markers GitLab treats as a draft title: Draft:,
// [Draft], and (Draft), in any case.
var draftTitleRE = regexp.MustCompile(`(?i)^\s*(?:\[draft\]|\(draft\)|draft:)`)

// titlePrefixRE matches a run of leading draft/WIP title prefixes, including any
// surrounding whitespace.
var titlePrefixRE = regexp.MustCompile(`(?i)^(\s*(?:\[draft\]|\(draft\)|draft:|wip:)\s*)*`)

// EnsureTitlePrefix prepends prefix (followed by a space) to title unless title
// already starts with prefix, case-insensitively and ignoring leading whitespace.
// For the Draft: prefix, a title that GitLab already treats as a draft also
// counts. It returns the resulting title and whether the prefix was already
// present.
func EnsureTitlePrefix(title, prefix string) (string, bool) {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(title)), strings.ToLower(prefix)) {
		return title, true
	}
	if strings.EqualFold(prefix, "Draft:") && draftTitleRE.MatchString(title) {
		return title, true
	}
	return prefix + " " + title, false
}

// StripTitlePrefixes removes every leading draft/WIP prefix from title.
func StripTitlePrefixes(title string) string {
	return titlePrefixRE.ReplaceAllString(title, "")
}
