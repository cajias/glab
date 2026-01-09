//go:build !integration

package init

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestInit_CommandConstruction(t *testing.T) {
	// GIVEN
	ctrl := gomock.NewController(t)
	tc := gitlabtesting.NewTestClientWithCtrl(ctrl, gitlab.WithBaseURL("https://gitlab.example.com"))
	execMock := cmdtest.NewMockExecutor(ctrl)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "testtoken", "gitlab.example.com", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithExecutor(execMock),
	)

	execMock.EXPECT().Exec(gomock.Any(), "my-tofu", []string{
		"-chdir=infra",
		"init",
		"-backend-config=address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production",
		"-backend-config=lock_address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production/lock",
		"-backend-config=unlock_address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production/lock",
		"-backend-config=lock_method=POST",
		"-backend-config=unlock_method=DELETE",
		"-backend-config=retry_wait_min=5",
		"-backend-config=headers={\"Authorization\" = \"Bearer testtoken\"}",
	}, nil)

	// WHEN
	_, err := exec("production -d infra -b my-tofu")
	require.NoError(t, err)
}

func TestInit_CommandConstruction_InitArgs(t *testing.T) {
	// GIVEN
	ctrl := gomock.NewController(t)
	tc := gitlabtesting.NewTestClientWithCtrl(ctrl, gitlab.WithBaseURL("https://gitlab.example.com"))
	execMock := cmdtest.NewMockExecutor(ctrl)
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		false,
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "testtoken", "gitlab.example.com", api.WithGitLabClient(tc.Client))),
		cmdtest.WithBaseRepo("OWNER", "REPO", "gitlab.example.com"),
		cmdtest.WithExecutor(execMock),
	)

	execMock.EXPECT().Exec(gomock.Any(), "my-tofu", []string{
		"-chdir=infra",
		"init",
		"-backend-config=address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production",
		"-backend-config=lock_address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production/lock",
		"-backend-config=unlock_address=https://gitlab.example.com/api/v4/projects/OWNER%2FREPO/terraform/state/production/lock",
		"-backend-config=lock_method=POST",
		"-backend-config=unlock_method=DELETE",
		"-backend-config=retry_wait_min=5",
		"-backend-config=headers={\"Authorization\" = \"Bearer testtoken\"}",
		"-reconfigure",
	}, nil)

	// WHEN
	_, err := exec("production -d infra -b my-tofu -- -reconfigure")
	require.NoError(t, err)
}
