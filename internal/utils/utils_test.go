//go:build !integration

package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
)

func Test_PrettyTimeAgo(t *testing.T) {
	cases := map[string]string{
		"1s":         "less than a minute ago",
		"30s":        "less than a minute ago",
		"1m08s":      "about 1 minute ago",
		"15m0s":      "about 15 minutes ago",
		"59m10s":     "about 59 minutes ago",
		"1h10m02s":   "about 1 hour ago",
		"15h0m01s":   "about 15 hours ago",
		"30h10m":     "about 1 day ago",
		"50h":        "about 2 days ago",
		"720h05m":    "about 1 month ago",
		"3000h10m":   "about 4 months ago",
		"8760h59m":   "about 1 year ago",
		"17601h59m":  "about 2 years ago",
		"262800h19m": "about 30 years ago",
	}

	for duration, expected := range cases {
		d, e := time.ParseDuration(duration)
		if e != nil {
			t.Errorf("failed to create a duration: %s", e)
		}

		fuzzy := PrettyTimeAgo(d)
		require.Equal(t, expected, fuzzy, "unexpected fuzzy duration value: %s for %s")
	}
}

func Test_Pluralize(t *testing.T) {
	testCases := []struct {
		name   string
		word   string
		amount int
		want   string
	}{
		{
			name:   "singular",
			word:   "label",
			amount: 1,
			want:   "1 label",
		},
		{
			name:   "plural",
			word:   "label",
			amount: 3,
			want:   "3 labels",
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := Pluralize(tC.amount, tC.word)
			require.Equal(t, tC.want, got, "Pluralize() got = %s, want = %s")
		})
	}
}

func Test_PresentInStringSlice(t *testing.T) {
	testCases := []struct {
		name   string
		hay    []string
		needle string
		want   bool
	}{
		{"simple true", []string{"foo", "bar", "baz"}, "bar", true},
		{"simple false", []string{"foo", "bar", "baz"}, "qux", false},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := PresentInStringSlice(tC.hay, tC.needle)
			require.Equal(t, tC.want, got, "PresentInStringSlice() got = %t, want = %t")
		})
	}
}

func Test_SanitizePathName(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		want     string
	}{
		{
			name:     "A regular filename",
			filename: "cli-v1.22.0.json",
			want:     "/cli-v1.22.0.json",
		},
		{
			name:     "A regular filename in a directory",
			filename: "cli/cli-v1.22.0.json",
			want:     "/cli/cli-v1.22.0.json",
		},
		{
			name:     "A filename with directory traversal",
			filename: "cli-v1.../../22.0.zip",
			want:     "/22.0.zip",
		},
		{
			name:     "A particularly nasty filename",
			filename: "..././..././..././etc/password_file",
			want:     "/.../.../.../etc/password_file",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filePathWanted := SanitizePathName(tt.filename)
			require.Equal(t, filePathWanted, tt.want, "SanitizePathName() got = %s, want = %s")
		})
	}
}

func Test_CommonElementsInStringSlice(t *testing.T) {
	testCases := []struct {
		name   string
		array1 []string
		array2 []string
		want   []string
	}{
		{
			name:   "simple no matching elements",
			array1: []string{"foo", "bar", "baz"},
			array2: []string{"qux", "quux", "quz"},
			want:   []string{},
		},
		{
			name:   "simple matching elements",
			array1: []string{"foo", "quux", "baz"},
			array2: []string{"qux", "quux", "baz"},
			want:   []string{"quux", "baz"},
		},
	}
	for _, tC := range testCases {
		t.Run(tC.name, func(t *testing.T) {
			got := CommonElementsInStringSlice(tC.array1, tC.array2)
			require.Len(t, tC.want, len(got), "CommonElementsInStringSlice() size of got (%d) and wanted (%d) arrays differ")
			for i := range got {
				require.Equal(t, got[i], tC.want[i], "CommonElementsInStringSlice() got = %s, want = %s")
			}
		})
	}
}

func Test_Map(t *testing.T) {
	type SomeType struct {
		name string
	}

	type Tests[T1, T2 any] struct {
		name  string
		slice []T1
		fn    func(T1) T2
		want  []T2
	}

	tests := []Tests[any, any]{
		{
			"list of strings",
			[]any{"foo", "bar", "baz"},
			func(e any) any { return e },
			[]any{"foo", "bar", "baz"},
		},
		{
			"list of structs",
			[]any{SomeType{"foo"}, SomeType{"bar"}, SomeType{"baz"}},
			func(e any) any { s, _ := e.(SomeType); return s.name },
			[]any{"foo", "bar", "baz"},
		},
		{
			"no elements",
			[]any{},
			func(e any) any { return e },
			[]any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Map(tt.slice, tt.fn)
			require.Equal(t, tt.want, got, "Test_Map() want %v; but got %v")
		})
	}
}

func TestIsValidURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{
			name:     "invalid empty url",
			input:    "",
			expected: false,
		},
		{
			name:     "invalid url without correct scheme",
			input:    "https:/gitlab.com/group",
			expected: false,
		},
		{
			name:     "ok with correct url",
			input:    "https://gitlab.com/group",
			expected: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actualIsValid := IsValidURL(test.input)
			require.Equal(t, test.expected, actualIsValid, "TestIsValidURL() got = %s, want = %s")
		})
	}
}

func TestParseEditorCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		editor   string
		expected []string
	}{
		{
			name:     "empty string",
			editor:   "",
			expected: nil,
		},
		{
			name:     "simple editor",
			editor:   "vim",
			expected: []string{"vim"},
		},
		{
			name:     "editor with flags",
			editor:   "nano -w",
			expected: []string{"nano", "-w"},
		},
		{
			name:     "emacsclient with flags",
			editor:   "emacsclient -t -a emacs",
			expected: []string{"emacsclient", "-t", "-a", "emacs"},
		},
		{
			name:     "emacsclient with quoted fallback",
			editor:   `emacsclient -t -a "emacs -nw"`,
			expected: []string{"emacsclient", "-t", "-a", "emacs -nw"},
		},
		{
			name:     "vscode with flags",
			editor:   `code --wait --new-window`,
			expected: []string{"code", "--wait", "--new-window"},
		},
		{
			name:     "vim with complex flags",
			editor:   "vim -c 'set spell'",
			expected: []string{"vim", "-c", "set spell"},
		},
		{
			name:     "full path editor",
			editor:   "/usr/local/bin/nvim -u NONE",
			expected: []string{"/usr/local/bin/nvim", "-u", "NONE"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ParseEditorCommand(tt.editor)
			require.Equal(t, tt.expected, got)
		})
	}
}

func Test_HelperFuncs(t *testing.T) {
	t.Run("StringToInt()", func(t *testing.T) {
		t.Run("success", func(t *testing.T) {
			got := StringToInt("200")
			require.Equal(t, 200, got)
		})
		t.Run("failed-return-0", func(t *testing.T) {
			got := StringToInt("NotAnInt")
			require.Equal(t, 0, got)
		})
	})
	t.Run("ReplaceNonAlphaNumericChars()", func(t *testing.T) {
		got := ReplaceNonAlphaNumericChars("profclems-glab", "/")
		require.Equal(t, "profclems/glab", got)
	})
}

func TestFormatTimeStats(t *testing.T) {
	tests := []struct {
		name string
		ts   *gitlab.TimeStats
		want string
	}{
		{name: "nil", ts: nil, want: ""},
		{name: "nothing tracked", ts: &gitlab.TimeStats{}, want: ""},
		{
			name: "time spent only",
			ts:   &gitlab.TimeStats{HumanTotalTimeSpent: "1h 30m", TotalTimeSpent: 5400},
			want: "1h 30m spent",
		},
		{
			name: "estimate only",
			ts:   &gitlab.TimeStats{HumanTimeEstimate: "2d", TimeEstimate: 57600},
			want: "2d estimated",
		},
		{
			name: "time spent and estimate",
			ts: &gitlab.TimeStats{
				HumanTotalTimeSpent: "1h 30m", TotalTimeSpent: 5400,
				HumanTimeEstimate: "2d", TimeEstimate: 57600,
			},
			want: "1h 30m spent of 2d estimated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, FormatTimeStats(tt.ts))
		})
	}
}
