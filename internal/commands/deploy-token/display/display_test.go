//go:build !integration

package display

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestExpiresAt(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "-", ExpiresAt(nil))

	// A date sent by the CLI round-trips as midnight UTC.
	fromCLI := time.Date(2027, 3, 5, 0, 0, 0, 0, time.UTC)
	assert.Equal(t, "2027-03-05T00:00:00Z", ExpiresAt(&fromCLI))

	// A date picked in the UI is stored at midnight in the browser's time zone,
	// so the day alone would misreport it.
	fromUI := time.Date(2026, 11, 14, 23, 0, 0, 0, time.UTC)
	assert.Equal(t, "2026-11-14T23:00:00Z", ExpiresAt(&fromUI))
}

func TestScopes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "-", Scopes(nil))
	assert.Equal(t, "-", Scopes([]string{}))
	assert.Equal(t, "read_repository", Scopes([]string{"read_repository"}))
	assert.Equal(t, "read_repository, read_registry", Scopes([]string{"read_repository", "read_registry"}))
}
