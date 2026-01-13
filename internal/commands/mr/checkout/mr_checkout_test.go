//go:build !integration

package checkout

import (
	"net/url"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func setupTest(t *testing.T, testClient *gitlabtesting.TestClient, opts ...cmdtest.FactoryOption) func(string) (*test.CmdOut, error) {
	t.Helper()

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	// Default options
	defaultOpts := []cmdtest.FactoryOption{
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithBranch("main"),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
		},
	}

	return cmdtest.SetupCmdForTest(t, NewCmdCheckout, false, append(defaultOpts, opts...)...)
}

func TestMrCheckout(t *testing.T) {
	t.Run("when a valid MR is checked out using MR id", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:                 123,
					IID:                123,
					ProjectID:          3,
					SourceProjectID:    3,
					SourceBranch:       "feat-new-mr",
					Title:              "test mr title",
					Description:        "test mr description",
					AllowCollaboration: false,
					State:              "opened",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()
		cs.Stub("HEAD branch: master\n")
		cs.Stub("\n")
		cs.Stub("\n")
		cs.Stub(heredoc.Doc(`
			deadbeef HEAD
			deadb00f refs/remotes/upstream/feat-new-mr
			deadbeef refs/remotes/origin/feat-new-mr
		`))

		exec := setupTest(t, testClient)
		output, err := exec("123")

		if assert.NoErrorf(t, err, "error running command `mr checkout 123`: %v", err) {
			assert.Empty(t, output.String())
			assert.Empty(t, output.Stderr())
		}

		expectedShellouts := []string{
			"git fetch git@gitlab.com:OWNER/REPO.git refs/heads/feat-new-mr:feat-new-mr",
			"git config branch.feat-new-mr.remote git@gitlab.com:OWNER/REPO.git",
			"git config branch.feat-new-mr.merge refs/heads/feat-new-mr",
			"git checkout feat-new-mr",
		}

		assert.Equal(t, len(expectedShellouts), cs.Count)
		for idx, expectedShellout := range expectedShellouts {
			assert.Equal(t, expectedShellout, strings.Join(cs.Calls[idx].Args, " "))
		}
	})

	t.Run("when a valid MR comes from a forked private project", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:                 123,
					IID:                123,
					ProjectID:          3,
					SourceProjectID:    3,
					TargetProjectID:    4,
					SourceBranch:       "feat-new-mr",
					Title:              "test mr title",
					Description:        "test mr description",
					AllowCollaboration: false,
					State:              "opened",
				},
			}, nil, nil)

		// First call for source project (ID 3) - returns not found (private fork)
		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(nil, nil, &gitlab.ErrorResponse{Message: "404 Project Not Found"})

		// Second call for target project (ID 4) - to get remote URL for MR ref
		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           4,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()
		cs.Stub("HEAD branch: master\n")
		cs.Stub("\n")
		cs.Stub("\n")
		cs.Stub(heredoc.Doc(`
			deadbeef HEAD
			deadb00f refs/remotes/upstream/feat-new-mr
			deadbeef refs/remotes/origin/feat-new-mr
		`))

		exec := setupTest(t, testClient)
		output, err := exec("123")

		if assert.NoErrorf(t, err, "error running command `mr checkout 123`: %v", err) {
			assert.Empty(t, output.String())
			assert.Empty(t, output.Stderr())
		}

		expectedShellouts := []string{
			"git fetch git@gitlab.com:OWNER/REPO.git refs/merge-requests/123/head:feat-new-mr",
			"git config branch.feat-new-mr.remote git@gitlab.com:OWNER/REPO.git",
			"git config branch.feat-new-mr.merge refs/merge-requests/123/head",
			"git checkout feat-new-mr",
		}

		assert.Equal(t, len(expectedShellouts), cs.Count)
		for idx, expectedShellout := range expectedShellouts {
			assert.Equal(t, expectedShellout, strings.Join(cs.Calls[idx].Args, " "))
		}
	})

	t.Run("when a valid MR is checked out using MR id and specifying branch", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:                 123,
					IID:                123,
					ProjectID:          3,
					SourceProjectID:    4,
					SourceBranch:       "feat-new-mr",
					Title:              "test mr title",
					Description:        "test mr description",
					AllowCollaboration: true,
					State:              "opened",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:FORK_OWNER/REPO.git",
			}, nil, nil)

		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()
		cs.Stub("HEAD branch: master\n")
		cs.Stub("\n")
		cs.Stub("\n")
		cs.Stub("\n")
		cs.Stub(heredoc.Doc(`
			deadbeef HEAD
			deadb00f refs/remotes/upstream/feat-new-mr
			deadbeef refs/remotes/origin/feat-new-mr
		`))

		exec := setupTest(t, testClient)
		output, err := exec("123 --branch foo")

		if assert.NoErrorf(t, err, "error running command `mr checkout 123 --branch foo`: %v", err) {
			assert.Empty(t, output.String())
			assert.Empty(t, output.Stderr())
		}

		expectedShellouts := []string{
			"git fetch git@gitlab.com:FORK_OWNER/REPO.git refs/heads/feat-new-mr:foo",
			"git config branch.foo.remote git@gitlab.com:FORK_OWNER/REPO.git",
			"git config branch.foo.pushRemote git@gitlab.com:FORK_OWNER/REPO.git",
			"git config branch.foo.merge refs/heads/feat-new-mr",
			"git checkout foo",
		}

		assert.Equal(t, len(expectedShellouts), cs.Count)
		for idx, expectedShellout := range expectedShellouts {
			assert.Equal(t, expectedShellout, strings.Join(cs.Calls[idx].Args, " "))
		}
	})
}

func TestMrCheckout_HTTPSProtocolConfiguration(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:                 123,
				IID:                123,
				ProjectID:          3,
				SourceProjectID:    3,
				SourceBranch:       "feat-new-mr",
				Title:              "test mr title",
				Description:        "test mr description",
				AllowCollaboration: false,
				State:              "opened",
			},
		}, nil, nil)

	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:            3,
			HTTPURLToRepo: "https://gitlab.com/OWNER/REPO.git",
			SSHURLToRepo:  "git@gitlab.com:OWNER/REPO.git",
		}, nil, nil)

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()

	cs.Stub("HEAD branch: master\n")
	cs.Stub("\n")
	cs.Stub("\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	// Create config with HTTPS protocol
	cfg := config.NewBlankConfig()
	err := cfg.Set("gitlab.com", "git_protocol", "https")
	assert.NoError(t, err)

	exec := setupTest(t, testClient, cmdtest.WithConfig(cfg))
	output, err := exec("123")

	assert.NoError(t, err)
	assert.Empty(t, output.String())
	assert.Empty(t, output.Stderr())

	expectedShellouts := []string{
		"git fetch https://gitlab.com/OWNER/REPO.git refs/heads/feat-new-mr:feat-new-mr",
		"git config branch.feat-new-mr.remote https://gitlab.com/OWNER/REPO.git",
		"git config branch.feat-new-mr.merge refs/heads/feat-new-mr",
		"git checkout feat-new-mr",
	}

	assert.Equal(t, len(expectedShellouts), cs.Count)
	for idx, expectedShellout := range expectedShellouts {
		assert.Equal(t, expectedShellout, strings.Join(cs.Calls[idx].Args, " "))
	}
}
