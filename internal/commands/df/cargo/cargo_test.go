//go:build !integration

package cargo

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdUse(t *testing.T) {
	t.Parallel()
	cmd := NewCmd(cmdtest.NewTestFactory(nil))
	assert.Equal(t, "cargo <cargo args>", cmd.Use)
}
