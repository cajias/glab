package login

import (
	"fmt"
	"strings"

	"gitlab.com/gitlab-org/cli/internal/commands/auth/authutils"
)

// setupGitCredentialHelper makes glab the Git credential helper for hostname
// when --setup-git-credential-helper is set. CI/CD jobs and setup scripts
// cannot answer the interactive prompt, but still need Git to authenticate.
func setupGitCredentialHelper(opts *LoginOptions, hostname, gitProtocol string) error {
	if !opts.SetupGitCredentialHelper {
		return nil
	}

	credentialFlow := &authutils.GitCredentialFlow{Executable: glabExecutable()}
	if err := credentialFlow.SetupHelper(hostname, gitCredentialProtocol(gitProtocol)); err != nil {
		return fmt.Errorf("failed to configure the Git credential helper: %w", err)
	}

	c := opts.IO.Color()
	opts.IO.LogErrorf("%s Configured Git to authenticate with glab for %s.\n", c.GreenCheck(), hostname)
	return nil
}

// gitCredentialProtocol returns the protocol for the credential helper
// configuration key. Git credential helpers apply to HTTP and HTTPS remotes
// only, so an SSH preference still configures the helper for HTTPS, which is
// the protocol that needs a token.
func gitCredentialProtocol(gitProtocol string) string {
	if strings.EqualFold(gitProtocol, "http") {
		return "http"
	}
	return "https"
}
