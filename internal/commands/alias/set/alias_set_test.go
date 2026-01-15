//go:build !integration

package set

import (
	"bytes"
	"io"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

// newCmdSetWithFakeHierarchy creates NewCmdSet wrapped in a fake command hierarchy
// needed for validCommand testing.
func newCmdSetWithFakeHierarchy(f cmdutils.Factory) *cobra.Command {
	cmd := NewCmdSet(f)

	// fake command nesting structure needed for validCommand
	rootCmd := &cobra.Command{}
	rootCmd.AddCommand(cmd)
	mrCmd := &cobra.Command{Use: "mr"}
	mrCmd.AddCommand(&cobra.Command{Use: "checkout"})
	mrCmd.AddCommand(&cobra.Command{Use: "rebase"})
	rootCmd.AddCommand(mrCmd)
	issueCmd := &cobra.Command{Use: "issue"}
	issueCmd.AddCommand(&cobra.Command{Use: "list"})
	rootCmd.AddCommand(issueCmd)

	return rootCmd
}

func TestAliasSet_glab_command(t *testing.T) {
	defer config.StubWriteConfig(io.Discard, io.Discard)()

	cfg := config.NewFromString(``)

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	_, err := exec("set mr 'mr rebase'")

	if assert.Error(t, err) {
		assert.Equal(t, `could not create alias: "mr" is already a glab command.`, err.Error())
	}
}

func TestAliasSet_empty_aliases(t *testing.T) {
	mainBuf := bytes.Buffer{}
	defer config.StubWriteConfig(io.Discard, &mainBuf)()

	cfg := config.NewFromString(heredoc.Doc(`
		aliases:
		editor: vim
	`))

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	output, err := exec("set co 'mr checkout'")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	assert.Contains(t, output.Stderr(), "Added alias")
	assert.Empty(t, output.String())

	expected := `co: mr checkout
`
	assert.Equal(t, expected, mainBuf.String())
}

func TestAliasSet_existing_alias(t *testing.T) {
	mainBuf := bytes.Buffer{}
	defer config.StubWriteConfig(io.Discard, &mainBuf)()

	cfg := config.NewFromString(heredoc.Doc(`
		aliases:
		  co: mr checkout
	`))

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	output, err := exec("set co 'mr checkout -Rcool/repo'")
	require.NoError(t, err)

	assert.Regexp(t, "Changed alias.*co.*from.*mr checkout.*to.*mr checkout -Rcool/repo.", output.Stderr())
}

func TestAliasSet_space_args(t *testing.T) {
	mainBuf := bytes.Buffer{}
	defer config.StubWriteConfig(io.Discard, &mainBuf)()

	cfg := config.NewFromString(``)

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	output, err := exec(`set il 'issue list -l "cool story"'`)
	require.NoError(t, err)

	assert.Regexp(t, `Adding alias for.*il.*issue list -l "cool story".`, output.Stderr())

	assert.Regexp(t, `il: issue list -l "cool story"`, mainBuf.String())
}

func TestAliasSet_arg_processing(t *testing.T) {
	cases := []struct {
		Cmd                string
		ExpectedOutputLine string
		ExpectedConfigLine string
	}{
		{`il "issue list"`, "- Adding alias for.*il.*issue list", "il: issue list"},

		{`iz 'issue list'`, "- Adding alias for.*iz.*issue list", "iz: issue list"},

		{
			`ii 'issue list --author="$1" --label="$2"'`,
			`- Adding alias for.*ii.*issue list --author="\$1" --label="\$2"`,
			`ii: issue list --author="\$1" --label="\$2"`,
		},

		{
			`ix "issue list --author='\$1' --label='\$2'"`,
			`- Adding alias for.*ix.*issue list --author='\$1' --label='\$2'`,
			`ix: issue list --author='\$1' --label='\$2'`,
		},
	}

	for _, c := range cases {
		t.Run(c.Cmd, func(t *testing.T) {
			mainBuf := bytes.Buffer{}
			defer config.StubWriteConfig(io.Discard, &mainBuf)()

			cfg := config.NewFromString(``)

			exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
			output, err := exec("set " + c.Cmd)
			if err != nil {
				t.Fatalf("got unexpected error running %s: %s", c.Cmd, err)
			}

			assert.Regexp(t, c.ExpectedOutputLine, output.Stderr())
			assert.Regexp(t, c.ExpectedConfigLine, mainBuf.String())
		})
	}
}

func TestAliasSet_init_alias_cfg(t *testing.T) {
	mainBuf := bytes.Buffer{}
	defer config.StubWriteConfig(io.Discard, &mainBuf)()

	cfg := config.NewFromString(heredoc.Doc(`
		editor: vim
	`))

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	output, err := exec("set diff 'mr diff'")
	require.NoError(t, err)

	expected := `diff: mr diff
`

	assert.Regexp(t, "Adding alias for.*diff.*mr diff", output.Stderr())
	assert.Contains(t, output.Stderr(), "Added alias.")
	assert.Equal(t, expected, mainBuf.String())
}

func TestAliasSet_existing_aliases(t *testing.T) {
	mainBuf := bytes.Buffer{}
	defer config.StubWriteConfig(io.Discard, &mainBuf)()

	cfg := config.NewFromString(heredoc.Doc(`
		aliases:
		  foo: bar
	`))

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	output, err := exec("set view 'mr view'")
	require.NoError(t, err)

	expected := `foo: bar
view: mr view
`

	assert.Regexp(t, "Adding alias for.*view.*mr view", output.Stderr())
	assert.Contains(t, output.Stderr(), "Added alias.")
	assert.Equal(t, expected, mainBuf.String())
}

func TestAliasSet_invalid_command(t *testing.T) {
	defer config.StubWriteConfig(io.Discard, io.Discard)()

	cfg := config.NewFromString(``)

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	_, err := exec("set co 'pe checkout'")
	if assert.Error(t, err) {
		assert.Equal(t, "could not create alias: pe checkout does not correspond to a glab command.", err.Error())
	}
}

func TestShellAlias_flag(t *testing.T) {
	mainBuf := bytes.Buffer{}
	defer config.StubWriteConfig(io.Discard, &mainBuf)()

	cfg := config.NewFromString(``)

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	output, err := exec("set --shell igrep 'glab issue list | grep'")
	if err != nil {
		t.Fatalf("unexpected error: %s", err)
	}

	assert.Regexp(t, "Adding alias for.*igrep.", output.Stderr())

	expected := `igrep: '!glab issue list | grep'
`
	assert.Equal(t, expected, mainBuf.String())
}

func TestShellAlias_bang(t *testing.T) {
	mainBuf := bytes.Buffer{}
	defer config.StubWriteConfig(io.Discard, &mainBuf)()

	cfg := config.NewFromString(``)

	exec := cmdtest.SetupCmdForTest(t, newCmdSetWithFakeHierarchy, true, cmdtest.WithConfig(cfg))
	output, err := exec("set igrep '!glab issue list | grep'")
	require.NoError(t, err)

	assert.Regexp(t, "Adding alias for.*igrep.", output.Stderr())

	expected := `igrep: '!glab issue list | grep'
`
	assert.Equal(t, expected, mainBuf.String())
}
