//go:build !integration

package cmdutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEnsureTitlePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		title          string
		prefix         string
		wantTitle      string
		wantAlreadySet bool
	}{
		{
			name:      "prepends prefix when missing",
			title:     "my custom MR title",
			prefix:    "Draft:",
			wantTitle: "Draft: my custom MR title",
		},
		{
			name:           "leaves title untouched when prefix already present",
			title:          "Draft: my custom MR title",
			prefix:         "Draft:",
			wantTitle:      "Draft: my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:           "matches prefix case-insensitively",
			title:          "draft: my custom MR title",
			prefix:         "Draft:",
			wantTitle:      "draft: my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:           "ignores leading whitespace before the prefix",
			title:          " Draft: my custom MR title",
			prefix:         "Draft:",
			wantTitle:      " Draft: my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:           "counts a [Draft] title as a draft",
			title:          "[Draft] my custom MR title",
			prefix:         "Draft:",
			wantTitle:      "[Draft] my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:           "counts a (draft) title as a draft",
			title:          "(draft) my custom MR title",
			prefix:         "Draft:",
			wantTitle:      "(draft) my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:      "does not count a [Draft] title as WIP",
			title:     "[Draft] my custom MR title",
			prefix:    "WIP:",
			wantTitle: "WIP: [Draft] my custom MR title",
		},
		{
			name:           "ignores leading whitespace before WIP",
			title:          " WIP: my custom MR title",
			prefix:         "WIP:",
			wantTitle:      " WIP: my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:           "matches WIP case-insensitively",
			title:          "wip: my custom MR title",
			prefix:         "WIP:",
			wantTitle:      "wip: my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:           "ignores leading whitespace before [Draft]",
			title:          " [Draft] my custom MR title",
			prefix:         "Draft:",
			wantTitle:      " [Draft] my custom MR title",
			wantAlreadySet: true,
		},
		{
			name:      "prepends when the prefix appears later in the title",
			title:     "Fix Draft: handling",
			prefix:    "Draft:",
			wantTitle: "Draft: Fix Draft: handling",
		},
		{
			name:      "supports the WIP prefix",
			title:     "my custom MR title",
			prefix:    "WIP:",
			wantTitle: "WIP: my custom MR title",
		},
		{
			name:      "does not count a Draft prefix as WIP",
			title:     "Draft: my custom MR title",
			prefix:    "WIP:",
			wantTitle: "WIP: Draft: my custom MR title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			gotTitle, gotAlreadySet := EnsureTitlePrefix(tt.title, tt.prefix)
			assert.Equal(t, tt.wantTitle, gotTitle)
			assert.Equal(t, tt.wantAlreadySet, gotAlreadySet)
		})
	}
}

func TestStripTitlePrefixes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		title string
		want  string
	}{
		{
			name:  "leaves a title without a prefix alone",
			title: "my custom MR title",
			want:  "my custom MR title",
		},
		{
			name:  "removes a Draft prefix",
			title: "Draft: my custom MR title",
			want:  "my custom MR title",
		},
		{
			name:  "removes stacked prefixes regardless of case and whitespace",
			title: " draft: WIP:  Draft: my custom MR title",
			want:  "my custom MR title",
		},
		{
			name:  "removes the bracketed draft markers GitLab accepts",
			title: "[Draft] (draft) Draft: my custom MR title",
			want:  "my custom MR title",
		},
		{
			name:  "keeps a prefix that appears later in the title",
			title: "Fix Draft: handling",
			want:  "Fix Draft: handling",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, StripTitlePrefixes(tt.title))
		})
	}
}
