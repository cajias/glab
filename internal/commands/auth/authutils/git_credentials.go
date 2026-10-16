package authutils

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/huh/v2"
	"github.com/MakeNowJust/heredoc/v2"
	"github.com/google/shlex"

	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/run"
)

type GitCredentialFlow struct {
	Executable string

	shouldSetup bool
	helper      string
}

func (gc *GitCredentialFlow) Prompt(ctx context.Context, io *iostreams.IOStreams, hostname, protocol string) error {
	gc.helper, _ = gitCredentialHelper(hostname, protocol)
	if isOurCredentialHelper(gc.helper) {
		return nil
	}

	gc.shouldSetup = true // default value
	confirm := huh.NewConfirm().
		Title("Authenticate Git with your GitLab credentials?").
		Value(&gc.shouldSetup)
	err := io.Run(ctx, confirm)
	if err != nil {
		return fmt.Errorf("could not prompt: %w", err)
	}

	return nil
}

func (gc *GitCredentialFlow) ShouldSetup() bool {
	return gc.shouldSetup
}

func (gc *GitCredentialFlow) Setup(hostname, protocol, username, authToken string) error {
	return gc.gitCredentialSetup(hostname, protocol, username, authToken)
}

// SetupHelper configures glab as the Git credential helper for hostname
// without prompting, for callers that already know the user's intent: a
// non-interactive login cannot answer Prompt, but still needs Git to
// authenticate. It is safe to run again: the host key is rewritten rather than
// appended to, whatever the executable is called.
func (gc *GitCredentialFlow) SetupHelper(hostname, protocol string) error {
	helper, _ := gitCredentialHelper(hostname, protocol)
	if isOurCredentialHelper(helper) {
		return nil
	}

	// An empty helper selects the branch in gitCredentialSetup that makes glab
	// the helper for this host. Any other helper would only receive the
	// credentials instead, which is not what the caller asked for.
	gc.helper = ""
	return gc.gitCredentialSetup(hostname, protocol, "", "")
}

func (gc *GitCredentialFlow) gitCredentialSetup(hostname, protocol, username, password string) error {
	if gc.helper == "" {
		// first use a blank value to indicate to git we want to sever the chain of credential helpers.
		// --replace-all also collapses a key that holds several values, for example
		// glab installed under another executable name by an earlier run, which a
		// plain `git config` refuses to overwrite.
		preConfigureCmd := git.GitCommand("config", "--global", "--replace-all", gitCredentialHelperKey(hostname, protocol), "")
		if err := run.PrepareCmd(preConfigureCmd).Run(); err != nil {
			return err
		}

		// use glab as a credential helper (for this host only)
		configureCmd := git.GitCommand(
			"config", "--global", "--add",
			gitCredentialHelperKey(hostname, protocol),
			fmt.Sprintf("!%s auth git-credential", shellQuote(gc.Executable)),
		)
		return run.PrepareCmd(configureCmd).Run()
	}

	// clear previous cached credentials
	rejectCmd := git.GitCommand("credential", "reject")

	rejectCmd.Stdin = bytes.NewBufferString(heredoc.Docf(`
		protocol=%s
		host=%s
	`, protocol, hostname))

	err := run.PrepareCmd(rejectCmd).Run()
	if err != nil {
		return err
	}

	approveCmd := git.GitCommand("credential", "approve")

	approveCmd.Stdin = bytes.NewBufferString(heredoc.Docf(`
		protocol=https
		host=%s
		username=%s
		password=%s
	`, hostname, username, password))

	err = run.PrepareCmd(approveCmd).Run()
	if err != nil {
		return err
	}

	return nil
}

func gitCredentialHelperKey(hostname, protocol string) string {
	return fmt.Sprintf("credential.%s://%s.helper", protocol, hostname)
}

func gitCredentialHelper(hostname, protocol string) (string, error) {
	helper, err := git.Config(gitCredentialHelperKey(hostname, protocol))
	if helper != "" {
		return helper, err
	}
	return git.Config("credential.helper")
}

func isOurCredentialHelper(cmd string) bool {
	if !strings.HasPrefix(cmd, "!") {
		return false
	}

	args, err := shlex.Split(cmd[1:])
	if err != nil || len(args) == 0 {
		return false
	}

	return strings.TrimSuffix(filepath.Base(args[0]), ".exe") == "glab"
}

func shellQuote(s string) string {
	if strings.ContainsAny(s, " $") {
		return "'" + s + "'"
	}
	return s
}
