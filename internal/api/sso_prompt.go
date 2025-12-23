package api

import (
	"bufio"
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
)

// NewSSOPrompt creates an SSOConsentFunc that prompts the user for consent
// before redirecting to an SSO identity provider.
//
// The prompt is shown on stderr and reads from stdin. If the terminal is not
// interactive (stdin is not a TTY), an error is returned instead of prompting.
func NewSSOPrompt(io *iostreams.IOStreams) SSOConsentFunc {
	return func(domain string) (bool, error) {
		// Check if we can prompt interactively
		if !io.IsInputTTY() {
			return false, fmt.Errorf("SSO redirect to %s requires consent but stdin is not a terminal; run interactively or use --cookie-file with pre-authenticated cookies", domain)
		}

		// Show the prompt on stderr (not stdout, which may be redirected)
		fmt.Fprintf(io.StdErr, "SSO redirect to %s detected.\n", domain)
		fmt.Fprint(io.StdErr, "Allow this redirect? [y/N]: ")

		// Read response from stdin
		reader := bufio.NewReader(io.In)
		response, err := reader.ReadString('\n')
		if err != nil {
			return false, fmt.Errorf("failed to read response: %w", err)
		}

		response = strings.TrimSpace(strings.ToLower(response))
		return response == "y" || response == "yes", nil
	}
}
