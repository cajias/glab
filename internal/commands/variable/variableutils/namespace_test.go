//go:build !integration

package variableutils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGroupChain(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		namespace string
		want      []string
	}{
		{
			name:      "a subgroup yields itself and its parent",
			namespace: "acme/platform",
			want:      []string{"acme/platform", "acme"},
		},
		{
			name:      "a nested subgroup walks closest first",
			namespace: "acme/platform/backend",
			want:      []string{"acme/platform/backend", "acme/platform", "acme"},
		},
		{
			name:      "a top-level group yields itself",
			namespace: "acme",
			want:      []string{"acme"},
		},
		{
			name:      "surrounding slashes are ignored",
			namespace: "/acme/platform/",
			want:      []string{"acme/platform", "acme"},
		},
		{
			name:      "an empty namespace yields nothing",
			namespace: "",
			want:      nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, GroupChain(tt.namespace))
		})
	}
}

func TestParentNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		fullPath string
		want     string
	}{
		{
			name:     "a project in a subgroup",
			fullPath: "acme/platform/api",
			want:     "acme/platform",
		},
		{
			name:     "a project in a top-level group",
			fullPath: "acme/api",
			want:     "acme",
		},
		{
			name:     "a top-level path has no parent",
			fullPath: "acme",
			want:     "",
		},
		{
			name:     "surrounding slashes are ignored",
			fullPath: "/acme/api/",
			want:     "acme",
		},
		{
			name:     "an empty path has no parent",
			fullPath: "",
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, ParentNamespace(tt.fullPath))
		})
	}
}
