//go:build !integration

package get

import (
	"errors"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

// apiError builds the error shape the API client returns for a status code.
// ErrorResponse.Error reads the originating request, so one is supplied.
func apiError(status int) error {
	request, err := http.NewRequest(http.MethodGet, "https://gitlab.com/api/v4/variables/TEST_VAR", http.NoBody)
	if err != nil {
		panic(err)
	}

	return &gitlab.ErrorResponse{
		StatusCode: status,
		Response:   &http.Response{StatusCode: status, Request: request},
		Message:    http.StatusText(status),
	}
}

func inheritedOpts(t *testing.T, testClient *gitlabtesting.TestClient, owner, group string) (*options, *testCapture) {
	t.Helper()

	io, _, stdout, stderr := cmdtest.TestIOStreams()
	return &options{
		apiClient: func(repoHost string) (*api.Client, error) {
			return cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client)), nil
		},
		baseRepo: func() (glrepo.Interface, error) {
			return glrepo.New(owner, "api", "gitlab.com"), nil
		},
		io:        io,
		key:       "TEST_VAR",
		scope:     "*",
		group:     group,
		inherited: true,
	}, &testCapture{stdout: stdout.String, stderr: stderr.String}
}

type testCapture struct {
	stdout func() string
	stderr func() string
}

func Test_getRun_inherited_fallsBackToParentGroup(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/platform/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))

	// The closest group is read first and defines the key, so the top-level
	// group is never asked.
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme/platform", "TEST_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VAR", Value: "from_subgroup"}, nil, nil)

	opts, out := inheritedOpts(t, testClient, "acme/platform", "")

	require.NoError(t, opts.run())
	assert.Equal(t, "from_subgroup", out.stdout())
	assert.Contains(t, out.stderr(), "Using the value from group acme/platform")
}

func Test_getRun_inherited_walksToTopLevelGroup(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/platform/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme/platform", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VAR", Value: "from_root_group"}, nil, nil)

	opts, out := inheritedOpts(t, testClient, "acme/platform", "")

	require.NoError(t, opts.run())
	assert.Equal(t, "from_root_group", out.stdout())
	assert.Contains(t, out.stderr(), "Using the value from group acme")
}

func Test_getRun_inherited_fallsBackToInstanceVariables(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockInstanceVariables.EXPECT().
		GetVariable("TEST_VAR").
		Return(&gitlab.InstanceVariable{Key: "TEST_VAR", Value: "from_instance"}, nil, nil)

	opts, out := inheritedOpts(t, testClient, "acme", "")

	require.NoError(t, opts.run())
	assert.Equal(t, "from_instance", out.stdout())
	assert.Contains(t, out.stderr(), "Using the value from instance variables")
}

// Reading a group or instance variable needs permission the user may not have.
// A walk must treat that as "not defined here" and carry on.
func Test_getRun_inherited_skipsLevelsTheUserCannotRead(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/platform/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme/platform", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusForbidden))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VAR", Value: "from_root_group"}, nil, nil)

	opts, out := inheritedOpts(t, testClient, "acme/platform", "")

	require.NoError(t, opts.run())
	assert.Equal(t, "from_root_group", out.stdout())
}

// Instance variables are administrator-only, so a 403 there ends the walk
// quietly and the project error is reported, as it is without the flag.
func Test_getRun_inherited_reportsTheProjectErrorWhenNothingDefinesTheKey(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockInstanceVariables.EXPECT().
		GetVariable("TEST_VAR").
		Return(nil, nil, apiError(http.StatusForbidden))

	opts, _ := inheritedOpts(t, testClient, "acme", "")

	err := opts.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Not Found")
}

// A failure that is not "missing" or "forbidden" means something is wrong, and
// hiding it behind the next level would mislead the user.
func Test_getRun_inherited_surfacesUnexpectedErrors(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusInternalServerError))

	opts, _ := inheritedOpts(t, testClient, "acme", "")

	err := opts.run()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Internal Server Error")
}

// Without the flag the command still reads one level and reports its error.
func Test_getRun_withoutInherited_doesNotWalk(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))

	opts, out := inheritedOpts(t, testClient, "acme", "")
	opts.inherited = false

	err := opts.run()
	require.Error(t, err)
	assert.Empty(t, out.stdout())
}

// --group selects the starting level, and --inherited continues above it.
func Test_getRun_inherited_startsFromTheRequestedGroup(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme/platform", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VAR", Value: "from_root_group"}, nil, nil)

	opts, out := inheritedOpts(t, testClient, "acme/platform", "acme/platform")

	require.NoError(t, opts.run())
	assert.Equal(t, "from_root_group", out.stdout())
}

func Test_skippableLevel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "not found", err: apiError(http.StatusNotFound), want: true},
		{name: "forbidden", err: apiError(http.StatusForbidden), want: true},
		{name: "unauthorized", err: apiError(http.StatusUnauthorized), want: true},
		{name: "server error", err: apiError(http.StatusInternalServerError), want: false},
		// The client also returns a sentinel that carries no HTTP response.
		{name: "sentinel not found", err: gitlab.ErrNotFound, want: true},
		{name: "plain error", err: errors.New("dial tcp: connection refused"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, skippableLevel(tt.err))
		})
	}
}

func execGetCmd(t *testing.T, testClient *gitlabtesting.TestClient, cli string) (*test.CmdOut, error) {
	t.Helper()

	exec := cmdtest.SetupCmdForTest(
		t,
		func(f cmdutils.Factory) *cobra.Command {
			return NewCmdGet(f, nil)
		},
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "gitlab.com", api.WithGitLabClient(testClient.Client))),
		cmdtest.WithBaseRepo("acme/platform", "api", ""),
	)
	return exec(cli)
}

func Test_getCmd_inherited_namesTheProjectItStartedFrom(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjectVariables.EXPECT().
		GetVariable("acme/platform/api", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme/platform", "TEST_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VAR", Value: "from_subgroup"}, nil, nil)

	out, err := execGetCmd(t, testClient, "--inherited TEST_VAR")

	require.NoError(t, err)
	assert.Equal(t, "from_subgroup", out.OutBuf.String())
	assert.Equal(t, "TEST_VAR is not defined in project acme/platform/api. Using the value from group acme/platform.\n", out.ErrBuf.String())
}

func Test_getCmd_inherited_namesTheGroupItStartedFrom(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme/platform", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VAR", Value: "from_root_group"}, nil, nil)

	out, err := execGetCmd(t, testClient, "--group acme/platform --inherited TEST_VAR")

	require.NoError(t, err)
	assert.Equal(t, "from_root_group", out.OutBuf.String())
	assert.Equal(t, "TEST_VAR is not defined in group acme/platform. Using the value from group acme.\n", out.ErrBuf.String())
}

// A numeric ID has no path to derive parent groups from, so the walk must
// look the group up first.
func Test_getCmd_inherited_resolvesANumericGroupID(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockGroupVariables.EXPECT().
		GetVariable("123", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroups.EXPECT().
		GetGroup("123", gomock.Any()).
		Return(&gitlab.Group{ID: 123, FullPath: "acme/platform"}, nil, nil)
	testClient.MockGroupVariables.EXPECT().
		GetVariable("acme", "TEST_VAR", gomock.Any()).
		Return(&gitlab.GroupVariable{Key: "TEST_VAR", Value: "from_root_group"}, nil, nil)

	out, err := execGetCmd(t, testClient, "--group 123 --inherited TEST_VAR")

	require.NoError(t, err)
	assert.Equal(t, "from_root_group", out.OutBuf.String())
	assert.Equal(t, "TEST_VAR is not defined in group acme/platform. Using the value from group acme.\n", out.ErrBuf.String())
}

func Test_getCmd_inherited_reportsANumericGroupThatCannotBeResolved(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockGroupVariables.EXPECT().
		GetVariable("123", "TEST_VAR", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))
	testClient.MockGroups.EXPECT().
		GetGroup("123", gomock.Any()).
		Return(nil, nil, apiError(http.StatusNotFound))

	out, err := execGetCmd(t, testClient, "--group 123 --inherited TEST_VAR")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "finding the parent groups of group 123")
	assert.Empty(t, out.OutBuf.String())
}
