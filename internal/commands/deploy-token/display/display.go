// Package display formats deploy token fields for terminal output.
// Both the project and the group endpoints return gitlab.DeployToken, so the
// same formatting serves every deploy-token subcommand.
package display

import (
	"strings"
	"time"
)

// ExpiresAt renders a deploy token expiry as a full timestamp, since the UI
// stores its time of day in the browser's time zone.
func ExpiresAt(expiresAt *time.Time) string {
	if expiresAt == nil {
		return "-"
	}
	return expiresAt.Format(time.RFC3339)
}

// Scopes renders a deploy token's scopes as a comma-delimited list.
func Scopes(scopes []string) string {
	if len(scopes) == 0 {
		return "-"
	}
	return strings.Join(scopes, ", ")
}
