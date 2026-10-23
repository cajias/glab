//go:build !integration

package gomodproxy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRequestAccepts(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		path string
		want request
	}{
		{
			name: "download",
			path: "/github.com/pkg/errors/@v/v0.9.1.zip",
			want: request{module: "github.com/pkg/errors", version: "v0.9.1", kind: kindZip},
		},
		{
			name: "bang-encoded module",
			path: "/github.com/spf13/!cobra/@v/v1.0.0.zip",
			want: request{module: "github.com/spf13/Cobra", version: "v1.0.0", kind: kindZip},
		},
		{
			name: "major version suffix preserved",
			path: "/example.com/m/v2/@v/v2.1.0.zip",
			want: request{module: "example.com/m/v2", version: "v2.1.0", kind: kindZip},
		},
		{
			name: "info",
			path: "/github.com/pkg/errors/@v/v0.9.1.info",
			want: request{module: "github.com/pkg/errors", version: "v0.9.1", kind: kindInfo},
		},
		{
			name: "mod",
			path: "/github.com/pkg/errors/@v/v0.9.1.mod",
			want: request{module: "github.com/pkg/errors", version: "v0.9.1", kind: kindMod},
		},
		{
			name: "list",
			path: "/github.com/pkg/errors/@v/list",
			want: request{module: "github.com/pkg/errors", kind: kindList},
		},
		{
			name: "latest",
			path: "/github.com/pkg/errors/@latest",
			want: request{module: "github.com/pkg/errors", kind: kindLatest},
		},
		{
			name: "pseudo-version",
			path: "/github.com/pkg/errors/@v/v0.0.0-20191128015809-6d18c012aee9.info",
			want: request{module: "github.com/pkg/errors", version: "v0.0.0-20191128015809-6d18c012aee9", kind: kindInfo},
		},
		{
			name: "branch query",
			path: "/example.com/m/@v/main.info",
			want: request{module: "example.com/m", version: "main", kind: kindInfo},
		},
		{
			name: "bang-encoded version",
			path: "/example.com/m/@v/v1.0.0-!r!c1.info",
			want: request{module: "example.com/m", version: "v1.0.0-RC1", kind: kindInfo},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := parseRequest(tc.path)
			require.True(t, ok)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestParseRequestSumDBIsNotParsed(t *testing.T) {
	t.Parallel()
	_, ok := parseRequest("/sumdb/sum.golang.org/supported")
	assert.False(t, ok)
	assert.True(t, isSumDB("/sumdb/sum.golang.org/supported"))
	assert.False(t, isSumDB("/github.com/pkg/errors/@v/v0.9.1.zip"))
}

func TestParseRequestRejects(t *testing.T) {
	t.Parallel()
	rejected := []string{
		"",
		"/",
		"/github.com/pkg/errors",
		"/@v/v1.0.0.zip",
		"/example.com/M/@v/v1.0.0.zip",
		"/example.com/m/@v/v1.0.0.ziphash",
		"/example.com/m/@v/.zip",
		"/../../etc/passwd",
	}
	for _, p := range rejected {
		_, ok := parseRequest(p)
		assert.False(t, ok, "expected %q to be rejected", p)
	}
}
