//go:build !integration

package clone

import (
	"testing"

	"github.com/google/shlex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "repo_clone_test")
}

func TestNewCmdClone(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		args        string
		wantOpts    options
		wantCtxOpts ContextOpts
		wantErr     string
	}{
		{
			name:    "no arguments",
			args:    "",
			wantErr: "Specify repository argument, or use the --group flag to specify a group to clone all repos from the group.",
		},
		{
			name: "repo argument",
			args: "NAMESPACE/REPO",
			wantOpts: options{
				gitFlags: []string{},
			},
			wantCtxOpts: ContextOpts{
				Repo: "NAMESPACE/REPO",
			},
		},
		{
			name: "directory argument",
			args: "NAMESPACE/REPO mydir",
			wantOpts: options{
				gitFlags: []string{},
				dir:      "mydir",
			},
			wantCtxOpts: ContextOpts{
				Repo: "NAMESPACE/REPO",
			},
		},
		{
			name: "git clone arguments",
			args: "NAMESPACE/REPO -- --depth 1 --recurse-submodules",
			wantOpts: options{
				gitFlags: []string{"--depth", "1", "--recurse-submodules"},
			},
			wantCtxOpts: ContextOpts{
				Repo: "NAMESPACE/REPO",
			},
		},
		{
			name: "group clone arguments",
			args: "-g NAMESPACE/REPO -- --depth 1 --recurse-submodules",
			wantOpts: options{
				gitFlags:  []string{"--depth", "1", "--recurse-submodules"},
				groupName: "NAMESPACE/REPO",
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name:    "unknown argument",
			args:    "NAMESPACE/REPO --depth 1",
			wantErr: "unknown flag: --depth\nSeparate Git clone flags with '--'.",
		},
		{
			name: "group clone with active=true",
			args: "-g mygroup --active=true",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "mygroup",
				active:    true,
				activeSet: true,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "group clone with active=false",
			args: "-g mygroup --active=false",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "mygroup",
				active:    false,
				activeSet: true,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
		{
			name: "group clone without active flag",
			args: "-g mygroup",
			wantOpts: options{
				gitFlags:  []string{},
				groupName: "mygroup",
				active:    false,
				activeSet: false,
			},
			wantCtxOpts: ContextOpts{
				Repo: "",
			},
		},
	}
	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			io, stdin, stdout, stderr := cmdtest.TestIOStreams()
			fac := cmdtest.NewTestFactory(io)

			var opts *options
			var ctxOpts *ContextOpts
			cmd := NewCmdClone(fac, func(co *options, cx *ContextOpts) error {
				opts = co
				ctxOpts = cx
				return nil
			})

			argv, err := shlex.Split(tt.args)
			require.NoError(t, err)
			cmd.SetArgs(argv)

			cmd.SetIn(stdin)
			cmd.SetOut(stdout)
			cmd.SetErr(stderr)

			_, err = cmd.ExecuteC()
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Equal(t, tt.wantErr, err.Error())
				return
			}

			require.NoError(t, err)
			assert.Equal(t, "", stdout.String())
			assert.Equal(t, "", stderr.String())

			assert.Equal(t, tt.wantCtxOpts.Repo, ctxOpts.Repo)
			assert.Equal(t, tt.wantOpts.gitFlags, opts.gitFlags)
			assert.Equal(t, tt.wantOpts.groupName, opts.groupName)
			assert.Equal(t, tt.wantOpts.active, opts.active)
			assert.Equal(t, tt.wantOpts.activeSet, opts.activeSet)
		})
	}
}
