//go:build !integration

package checkout

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	git_testing "gitlab.com/gitlab-org/cli/internal/git/testing"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func setupTest(t *testing.T, testClient *gitlabtesting.TestClient, opts ...cmdtest.FactoryOption) func(string) (*test.CmdOut, error) {
	t.Helper()

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

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
	t.Parallel()
	t.Run("when a valid MR is checked out using MR id", func(t *testing.T) {
		t.Parallel()
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
					SHA:                "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "Counting objects")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when the API returns a repository URL that is not a valid remote", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:            3,
				SSHURLToRepo:  "--upload-pack=touch x",
				HTTPURLToRepo: "--upload-pack=touch x",
			}, nil, nil)

		// No git expectations: any fetch or config call fails the test.
		mockGit := git_testing.NewMockGitRunner(gomock.NewController(t))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "--upload-pack=touch x")
	})

	t.Run("when the API returns a source branch that looks like a git option", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "--orphan=x",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		mockGit := git_testing.NewMockGitRunner(gomock.NewController(t))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.EqualError(t, err, `invalid branch name "--orphan=x"`)
	})

	t.Run("when a remote matches the MR project, its name is used for the fetch and the branch remote", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "monalisa/REPO",
				SSHURLToRepo:      "git@gitlab.com:monalisa/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "origin", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "origin").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.NoError(t, err)
	})

	t.Run("when no remote matches the MR project, the project URL is used for the fetch and the branch remote", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "contributor/REPO",
				SSHURLToRepo:      "git@gitlab.com:contributor/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:contributor/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:contributor/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.NoError(t, err)
	})

	t.Run("when the remotes cannot be read, the project URL is used for the fetch and the branch remote", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit), func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return nil, errors.New("not a git repository")
			}
		})
		_, err := exec("123")

		require.NoError(t, err)
	})

	t.Run("when a valid MR comes from a forked private project", func(t *testing.T) {
		t.Parallel()
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
					SHA:                "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(nil, nil, &gitlab.ErrorResponse{Message: "404 Project Not Found"})

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                4,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/merge-requests/123/head:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/merge-requests/123/head:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/merge-requests/123/head").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/merge-requests/123/head:feat-new-mr")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when a valid MR is checked out using MR id and specifying branch", func(t *testing.T) {
		t.Parallel()
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
					SHA:                "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "FORK_OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:FORK_OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/foo").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:FORK_OWNER/REPO.git", "refs/heads/feat-new-mr:foo").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:foo"))
		mockGit.EXPECT().Git("config", "branch.foo.remote", "git@gitlab.com:FORK_OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.foo.pushRemote", "git@gitlab.com:FORK_OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.foo.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "foo").
			DoAndReturn(git.CheckoutStub("foo"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123 --branch foo")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:foo")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'foo'")
	})

	t.Run("when initial fetch fails but retry succeeds", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "couldn't find remote ref"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found")).Times(2)
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "fetch attempt refs/heads/feat-new-mr:feat-new-mr failed")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when fetch fails completely", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "fetch failed"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr", "fetch failed"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "fetch failed")
		assert.Contains(t, output.Stderr(), "fetch attempt refs/heads/feat-new-mr:feat-new-mr failed")
		assert.Contains(t, output.Stderr(), "fetch attempt refs/heads/feat-new-mr failed")
		assert.Empty(t, output.String())
	})

	t.Run("when checkout fails", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.FailingCheckoutStub("feat-new-mr", "pathspec 'feat-new-mr' did not match"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not checkout branch")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.Contains(t, output.Stderr(), "error: pathspec 'feat-new-mr' did not match")
		assert.Empty(t, output.String())
	})

	t.Run("when git config fails", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").
			Return("", errors.New("could not set config"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "could not set config")
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.Empty(t, output.String())
	})

	t.Run("when the checked-out branch is behind the merge request, fast-forwards", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		// git refuses to fetch into the branch that is checked out, whatever the history.
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "refusing to fetch into branch 'refs/heads/feat-new-mr' checked out at '/repo'"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "merge", "--ff-only", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.NoError(t, err)
	})

	t.Run("when another branch is checked out and the local branch is behind, moves it", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("main\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "branch", "-f", "feat-new-mr", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.NoError(t, err)
	})

	t.Run("when the checked-out branch is behind but the working tree blocks the fast-forward, fails", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "refusing to fetch into branch 'refs/heads/feat-new-mr' checked out at '/repo'"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		// git refuses the merge because a local modification would be overwritten;
		// the mock is strict, so no config or checkout call may follow.
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "merge", "--ff-only", "FETCH_HEAD").
			DoAndReturn(func(_, stderr io.Writer, _ ...string) error {
				fmt.Fprintln(stderr, "error: Your local changes to the following files would be overwritten by merge:\n\tfile.go")
				return errors.New("exit status 1")
			})

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), `could not fast-forward local branch "feat-new-mr"`)
		assert.ErrorContains(t, err, "exit status 1")
	})

	t.Run("when another branch is checked out and moving the local branch fails", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("main\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "branch", "-f", "feat-new-mr", "FETCH_HEAD").
			Return(errors.New("exit status 128"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), `could not fast-forward local branch "feat-new-mr"`)
		assert.ErrorContains(t, err, "exit status 128")
	})

	t.Run("when diverged without --force and non-interactive", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", errors.New("exit status 1"))
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("main\n", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "--force")
		var fe cmdutils.FlagError
		require.ErrorAs(t, err, &fe)
	})

	t.Run("when --force, on target, clean", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", errors.New("exit status 1"))
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("", nil)
		mockGit.EXPECT().Git("ls-files", "--others", "--exclude-standard").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "reset", "--hard", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.NoError(t, err)
	})

	t.Run("when --force, on target, tracked modifications refuse", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", errors.New("exit status 1"))
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("file.go\n", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "changes that would be lost")
	})

	t.Run("when --force, on target, untracked file conflicts with incoming", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", errors.New("exit status 1"))
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("", nil)
		mockGit.EXPECT().Git("ls-files", "--others", "--exclude-standard").Return("local.txt\n", nil)
		mockGit.EXPECT().Git("ls-tree", "-r", "--name-only", "new").Return("main.txt\nlocal.txt\n", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "changes that would be lost")
	})

	t.Run("when --force, on target, unrelated untracked file allowed", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", errors.New("exit status 1"))
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("feat-new-mr\n", nil)
		mockGit.EXPECT().Git("diff", "--name-only", "HEAD").Return("", nil)
		mockGit.EXPECT().Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\n", nil)
		mockGit.EXPECT().Git("ls-tree", "-r", "--name-only", "new").Return("main.txt\nfeature.go\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "reset", "--hard", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.NoError(t, err)
	})

	t.Run("when --force, not on target", func(t *testing.T) {
		t.Parallel()
		testClient := newDivergenceTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", errors.New("exit status 1"))
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("main\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "branch", "-f", "feat-new-mr", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --force")

		require.NoError(t, err)
	})

	t.Run("when local branch already matches MR head, fetch is skipped", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("abc123\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", gomock.Any(), gomock.Any()).Times(0)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.String(), "already at merge request head")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when local branch exists but at a different SHA, fetch still happens", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("stale000\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.NotContains(t, output.String(), "skipping fetch")
	})

	t.Run("when commit is already local but branch is missing, branch is created and fetch is skipped", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("abc123\n", nil)
		mockGit.EXPECT().Git("branch", "feat-new-mr", "abc123").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", gomock.Any(), gomock.Any()).Times(0)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.String(), "Created branch")
		assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
	})

	t.Run("when branch and commit are both missing, fetch happens", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:                3,
				PathWithNamespace: "OWNER/REPO",
				SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
			DoAndReturn(git.CheckoutStub("feat-new-mr"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
		assert.NotContains(t, output.String(), "skipping fetch")
	})

	t.Run("when merge request has no head SHA", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           123,
					IID:          123,
					SourceBranch: "feat-new-mr",
					State:        "opened",
				},
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "has no head commit")
	})
}

func newDivergenceTestClient(t *testing.T) *gitlabtesting.TestClient {
	t.Helper()
	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				State:           "opened",
				SHA:             "abc123",
			},
		}, nil, nil)
	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                3,
			PathWithNamespace: "OWNER/REPO",
			SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
		}, nil, nil)
	return testClient
}

func TestUnsafeToReset(t *testing.T) {
	t.Parallel()
	boom := errors.New("boom")
	cases := []struct {
		name        string
		mocks       func(m *git_testing.MockGitRunnerMockRecorder)
		wantBlocked bool
		wantErr     error
	}{
		{
			name: "clean tree",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("", nil)
			},
		},
		{
			name: "tracked diff single file",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("file.go\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "tracked diff multiple files",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("a.go\nb.go\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "unrelated untracked only",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "new").Return("main.txt\nfeature.go\n", nil)
			},
		},
		{
			name: "conflicting untracked",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("conflict.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "new").Return("main.txt\nconflict.txt\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "multiple untracked one conflicts",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\nconflict.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "new").Return("main.txt\nconflict.txt\n", nil)
			},
			wantBlocked: true,
		},
		{
			name: "trailing blank lines parsed",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("notes.txt\n\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "new").Return("main.txt\n\n", nil)
			},
		},
		{
			name: "diff errors propagate",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", boom)
			},
			wantErr: boom,
		},
		{
			name: "ls-files errors propagate",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("", boom)
			},
			wantErr: boom,
		},
		{
			name: "ls-tree errors propagate",
			mocks: func(m *git_testing.MockGitRunnerMockRecorder) {
				m.Git("diff", "--name-only", "HEAD").Return("", nil)
				m.Git("ls-files", "--others", "--exclude-standard").Return("foo.txt\n", nil)
				m.Git("ls-tree", "-r", "--name-only", "new").Return("", boom)
			},
			wantErr: boom,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			mockGit := git_testing.NewMockGitRunner(ctrl)
			tc.mocks(mockGit.EXPECT())

			o := &options{gr: mockGit}
			got, err := o.unsafeToReset("", "new")

			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantBlocked, got)
		})
	}
}

func TestMrCheckout_SetUpstreamTo(t *testing.T) {
	t.Parallel()
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				State:           "opened",
				SHA:             "abc123",
			},
		}, nil, nil)

	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                3,
			PathWithNamespace: "OWNER/REPO",
			SSHURLToRepo:      "git@gitlab.com:OWNER/REPO.git",
		}, nil, nil)

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
	mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "upstream", "refs/heads/feat-new-mr:feat-new-mr").
		DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "upstream").Return("", nil)
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
		DoAndReturn(git.CheckoutStub("feat-new-mr"))
	mockGit.EXPECT().Git("branch", "--set-upstream-to", "upstream/main", "feat-new-mr").Return("", nil)

	exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
	output, err := exec("123 --set-upstream-to upstream/main")

	require.NoError(t, err)
	assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
	assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
}

func TestMrCheckout_Worktree(t *testing.T) {
	t.Parallel()

	newTestClient := func(t *testing.T) *gitlabtesting.TestClient {
		t.Helper()
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		testClient.MockProjects.EXPECT().
			GetProject(gomock.Any(), gomock.Any()).
			Return(&gitlab.Project{
				ID:           3,
				SSHURLToRepo: "git@gitlab.com:OWNER/REPO.git",
			}, nil, nil)

		return testClient
	}

	// validate() consults this before any side effects, so every --worktree
	// subtest has to answer it.
	worktreeList := func(entries string) string {
		return "worktree /repo" + "\n" + "HEAD aaa000" + "\n" + "branch refs/heads/main" + "\n" + entries
	}

	expectNoOtherWorktree := func(mockGit *git_testing.MockGitRunner) {
		mockGit.EXPECT().Git("worktree", "list", "--porcelain").Return(worktreeList(""), nil)
	}

	expectFetchAndConfig := func(mockGit *git_testing.MockGitRunner) {
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	}

	t.Run("adds a worktree instead of checking out the branch", func(t *testing.T) {
		t.Parallel()
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectNoOtherWorktree(mockGit)
		expectFetchAndConfig(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "worktree", "add", "--", "../review-mr-123", "feat-new-mr").Return(nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123 --worktree ../review-mr-123")

		require.NoError(t, err)
		assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
	})

	t.Run("passes a worktree path after -- so a leading dash is not an option", func(t *testing.T) {
		t.Parallel()
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectNoOtherWorktree(mockGit)
		expectFetchAndConfig(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "worktree", "add", "--", "-review-mr-123", "feat-new-mr").Return(nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree=-review-mr-123")

		require.NoError(t, err)
	})

	t.Run("sets the upstream of the worktree branch by name", func(t *testing.T) {
		t.Parallel()
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectNoOtherWorktree(mockGit)
		expectFetchAndConfig(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "worktree", "add", "--", "../review-mr-123", "feat-new-mr").Return(nil)
		mockGit.EXPECT().Git("branch", "--set-upstream-to", "upstream/main", "feat-new-mr").Return("", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree ../review-mr-123 --set-upstream-to upstream/main")

		require.NoError(t, err)
	})

	t.Run("reports a failure to add the worktree", func(t *testing.T) {
		t.Parallel()
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectNoOtherWorktree(mockGit)
		expectFetchAndConfig(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "worktree", "add", "--", "../review-mr-123", "feat-new-mr").
			Return(errors.New("fatal: '../review-mr-123' already exists"))

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree ../review-mr-123")

		require.ErrorContains(t, err, `could not add worktree "../review-mr-123" for branch "feat-new-mr"`)
	})

	t.Run("skips the fetch when the branch is already at the merge request head", func(t *testing.T) {
		t.Parallel()
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectNoOtherWorktree(mockGit)
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("abc123"+"\n", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "worktree", "add", "--", "../review-mr-123", "feat-new-mr").Return(nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123 --worktree ../review-mr-123")

		require.NoError(t, err)
		assert.Contains(t, output.String(), "skipping fetch")
	})

	// gomock is strict, so the absence of fetch and config expectations here is
	// the assertion that the guard runs before any side effects.
	t.Run("fails early when the branch is checked out in another worktree", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("worktree", "list", "--porcelain").
			Return(worktreeList("worktree /elsewhere/review"+"\n"+"HEAD abc123"+"\n"+"branch refs/heads/feat-new-mr"+"\n"), nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree ../review-mr-123")

		require.ErrorContains(t, err, `branch "feat-new-mr" is already checked out at "/elsewhere/review"`)
		require.ErrorContains(t, err, "--branch")
	})

	// A repeat review of the same merge request lands here. Returning early would
	// leave the reviewer on stale code while reporting success, so the branch is
	// brought up to date inside the worktree that holds it.
	expectBranchInRequestedWorktree := func(mockGit *git_testing.MockGitRunner, requested string) {
		abs, err := filepath.Abs(requested)
		require.NoError(t, err)
		mockGit.EXPECT().Git("worktree", "list", "--porcelain").
			Return(worktreeList("worktree "+abs+"\n"+"HEAD old"+"\n"+"branch refs/heads/feat-new-mr"+"\n"), nil)
	}

	// No `refs/heads/x:refs/heads/x` fetch here: git always refuses that for a branch
	// a worktree holds, so an in-place update goes straight to the FETCH_HEAD fetch.
	expectFetchIntoExistingWorktree := func(mockGit *git_testing.MockGitRunner) {
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old"+"\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new"+"\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	}

	t.Run("updates the branch in place when it is already in the requested worktree", func(t *testing.T) {
		t.Parallel()
		requested := "../review-mr-123"
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectBranchInRequestedWorktree(mockGit, requested)
		expectFetchIntoExistingWorktree(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "-C", requested, "merge", "--ff-only", "new").Return(nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		output, err := exec("123 --worktree " + requested)

		require.NoError(t, err)
		assert.Contains(t, output.String(), "already checked out at")
	})

	t.Run("still applies --set-upstream-to when the worktree already exists", func(t *testing.T) {
		t.Parallel()
		requested := "../review-mr-123"
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectBranchInRequestedWorktree(mockGit, requested)
		expectFetchIntoExistingWorktree(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "-C", requested, "merge", "--ff-only", "new").Return(nil)
		mockGit.EXPECT().Git("branch", "--set-upstream-to", "upstream/main", "feat-new-mr").Return("", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree " + requested + " --set-upstream-to upstream/main")

		require.NoError(t, err)
	})

	// The reset lands in the review worktree, so --force must not discard work
	// there without looking, which is what the flag's own help promises.
	expectDivergedInPlace := func(mockGit *git_testing.MockGitRunner) {
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old"+"\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new"+"\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").
			Return("", errors.New("not an ancestor"))
	}

	t.Run("refuses --force when the existing worktree has uncommitted work", func(t *testing.T) {
		t.Parallel()
		requested := "../review-mr-123"
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectBranchInRequestedWorktree(mockGit, requested)
		expectDivergedInPlace(mockGit)
		mockGit.EXPECT().Git("-C", requested, "diff", "--name-only", "HEAD").Return("dirty.go\n", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree " + requested + " --force")

		require.ErrorContains(t, err, requested)
		require.ErrorContains(t, err, "would be lost by reset")
	})

	t.Run("resets inside the existing worktree when it is clean", func(t *testing.T) {
		t.Parallel()
		requested := "../review-mr-123"
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectBranchInRequestedWorktree(mockGit, requested)
		expectDivergedInPlace(mockGit)
		mockGit.EXPECT().Git("-C", requested, "diff", "--name-only", "HEAD").Return("", nil)
		mockGit.EXPECT().Git("-C", requested, "ls-files", "--others", "--exclude-standard").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "-C", requested, "reset", "--hard", "new").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree " + requested + " --force")

		require.NoError(t, err)
	})

	// A worktree whose directory was deleted is still listed, marked prunable, and
	// git keeps counting it as holding the branch, so `worktree add` would refuse.
	// The guard fails here, before the fetch, with a `git worktree prune` hint
	// rather than letting the add fail later with git's own wording. The absence of
	// fetch and add expectations under strict gomock asserts nothing runs first.
	t.Run("fails early with a prune hint when the branch's worktree is prunable", func(t *testing.T) {
		t.Parallel()
		testClient := gitlabtesting.NewTestClient(t)
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:              123,
					IID:             123,
					ProjectID:       3,
					SourceProjectID: 3,
					SourceBranch:    "feat-new-mr",
					State:           "opened",
					SHA:             "abc123",
				},
			}, nil, nil)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		mockGit.EXPECT().Git("worktree", "list", "--porcelain").
			Return(worktreeList("worktree /gone"+"\n"+"HEAD old"+"\n"+"branch refs/heads/feat-new-mr"+"\n"+"prunable gitdir file points to non-existent location"+"\n"), nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree ../review-mr-123")

		require.ErrorContains(t, err, `branch "feat-new-mr" is held by a worktree at "/gone"`)
		require.ErrorContains(t, err, "git worktree prune")
	})

	// Worktree mode touches no working tree, so the diverged branch ref is moved
	// with `git branch -f`. A `reset --hard` here would hit the current directory,
	// which is the thing --worktree exists to avoid.
	t.Run("moves the ref instead of resetting when the branch has diverged", func(t *testing.T) {
		t.Parallel()
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectNoOtherWorktree(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old"+"\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new"+"\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").
			Return("", errors.New("not an ancestor"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "branch", "-f", "feat-new-mr", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "worktree", "add", "--", "../review-mr-123", "feat-new-mr").Return(nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree ../review-mr-123 --force")

		require.NoError(t, err)
	})

	// A branch that is merely behind reaches fastForward() rather than the reset
	// path. The branch is checked out nowhere, so the ref moves with `branch -f`;
	// `merge --ff-only` would act on the current directory.
	t.Run("fast-forwards a behind branch with branch -f rather than merging", func(t *testing.T) {
		t.Parallel()
		testClient := newTestClient(t)

		ctrl := gomock.NewController(t)
		mockGit := git_testing.NewMockGitRunner(ctrl)
		expectNoOtherWorktree(mockGit)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
			DoAndReturn(git.FailingFetchStub("refs/heads/feat-new-mr:feat-new-mr", "non-fast-forward"))
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "git@gitlab.com:OWNER/REPO.git", "refs/heads/feat-new-mr").
			DoAndReturn(git.FetchStub("refs/heads/feat-new-mr"))
		mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("old"+"\n", nil).Times(2)
		mockGit.EXPECT().Git("rev-parse", "FETCH_HEAD^{commit}").Return("new"+"\n", nil)
		mockGit.EXPECT().Git("merge-base", "--is-ancestor", "refs/heads/feat-new-mr", "FETCH_HEAD").Return("", nil)
		mockGit.EXPECT().Git("symbolic-ref", "--quiet", "--short", "HEAD").Return("main"+"\n", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "branch", "-f", "feat-new-mr", "FETCH_HEAD").Return(nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "git@gitlab.com:OWNER/REPO.git").Return("", nil)
		mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
		mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "worktree", "add", "--", "../review-mr-123", "feat-new-mr").Return(nil)

		exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit))
		_, err := exec("123 --worktree ../review-mr-123")

		require.NoError(t, err)
	})
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func clientForSHA(t *testing.T, sha, remoteURL string) *gitlabtesting.TestClient {
	t.Helper()

	testClient := gitlabtesting.NewTestClient(t)
	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				State:           "opened",
				SHA:             sha,
			},
		}, nil, nil).AnyTimes()
	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:            3,
			SSHURLToRepo:  remoteURL,
			HTTPURLToRepo: remoteURL,
		}, nil, nil).AnyTimes()

	return testClient
}

// The mocked subtests above stub the very commands that behave differently against
// real git, so the repeat review is driven end to end instead. FETCH_HEAD is
// per-worktree, and a merge that reads the review worktree's own copy either fails
// or silently leaves it on the old commit.
//
// No t.Parallel(): InitGitRepoWithCommit calls t.Chdir.
func TestMrCheckout_WorktreeRepeatReviewRealGit(t *testing.T) {
	repo := git.InitGitRepoWithCommit(t)
	remote := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, repo, "init", "--bare", remote)
	gitIn(t, repo, "push", remote, "HEAD:refs/heads/feat-new-mr")
	c1 := gitIn(t, repo, "rev-parse", "HEAD")

	// RemoteURL now rejects a bare filesystem path, so the project advertises a valid
	// URL and insteadOf rewrites it back to the local bare repo used as the transport.
	const remoteURL = "https://gitlab.example.com/OWNER/REPO.git"
	gitIn(t, repo, "config", "url."+filepath.ToSlash(remote)+".insteadOf", remoteURL)

	// Forward slashes keep the argument intact when the command string is split.
	worktree := filepath.ToSlash(filepath.Join(t.TempDir(), "review"))

	_, err := setupTest(t, clientForSHA(t, c1, remoteURL))("123 --worktree " + worktree)
	require.NoError(t, err)
	assert.Equal(t, c1, gitIn(t, worktree, "rev-parse", "HEAD"))

	other := filepath.Join(t.TempDir(), "other")
	gitIn(t, repo, "clone", "-q", "-b", "feat-new-mr", remote, other)
	gitIn(t, other, "-c", "user.name=x", "-c", "user.email=x@x", "-c", "commit.gpgsign=false", "commit", "-q", "--allow-empty", "-m", "c2")
	gitIn(t, other, "push", "-q", "origin", "feat-new-mr")
	c2 := gitIn(t, other, "rev-parse", "HEAD")

	_, err = setupTest(t, clientForSHA(t, c2, remoteURL))("123 --worktree " + worktree)
	require.NoError(t, err)
	assert.Equal(t, c2, gitIn(t, worktree, "rev-parse", "HEAD"))
}

func TestSameWorktreePath(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("cannot create a symlink on this platform: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)

	// git reports a resolved path, so a request made through a symlink, such as
	// /tmp on macOS or a symlinked home directory, has to resolve to the same place.
	assert.True(t, sameWorktreePath(resolved, link))
	assert.True(t, sameWorktreePath(link, resolved))
	assert.True(t, sameWorktreePath(resolved, dir))
	assert.False(t, sameWorktreePath(resolved, filepath.Join(dir, "elsewhere")))

	// A path that does not exist yet is the ordinary first-run case: EvalSymlinks
	// fails there, so the fallback has to clean the two spellings to the same place.
	missing := filepath.Join(dir, "not-created-yet")
	assert.True(t, sameWorktreePath(missing, filepath.Join(dir, "sub", "..", "not-created-yet")))
	assert.False(t, sameWorktreePath(missing, filepath.Join(dir, "sub", "not-created-yet")))
}

func TestMrCheckout_HTTPSProtocolConfiguration(t *testing.T) {
	t.Parallel()
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
				SHA:                "abc123",
			},
		}, nil, nil)

	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                3,
			PathWithNamespace: "contributor/REPO",
			HTTPURLToRepo:     "https://gitlab.com/contributor/REPO.git",
			SSHURLToRepo:      "git@gitlab.com:contributor/REPO.git",
		}, nil, nil)

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
	mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "https://gitlab.com/contributor/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
		DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "https://gitlab.com/contributor/REPO.git").Return("", nil)
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
		DoAndReturn(git.CheckoutStub("feat-new-mr"))

	cfg := config.NewBlankConfig()
	err := cfg.Set("gitlab.com", "git_protocol", "https")
	require.NoError(t, err)

	exec := setupTest(t, testClient, cmdtest.WithGitRunner(mockGit), cmdtest.WithConfig(cfg))
	output, err := exec("123")

	require.NoError(t, err)
	assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
	assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
}

// TestMrCheckout_CrossHostURLUsesURLHostProtocol verifies that when an MR URL pointing at a
// different host is given, the git_protocol lookup uses the URL's host (not the local repo's
// host). Before the refactor, baseRepo came from f.BaseRepo(); now it comes from MRFromArgs,
// which overrides baseRepo with the URL repo for cross-host URLs.
func TestMrCheckout_CrossHostURLUsesURLHostProtocol(t *testing.T) {
	t.Parallel()
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockMergeRequests.EXPECT().
		GetMergeRequest("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:              123,
				IID:             123,
				ProjectID:       3,
				SourceProjectID: 3,
				SourceBranch:    "feat-new-mr",
				Title:           "test mr title",
				Description:     "test mr description",
				State:           "opened",
				SHA:             "abc123",
			},
		}, nil, nil)

	testClient.MockProjects.EXPECT().
		GetProject(gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			ID:                3,
			HTTPURLToRepo:     "https://custom.host.com/OWNER/REPO.git",
			PathWithNamespace: "OWNER/REPO",
			SSHURLToRepo:      "git@custom.host.com:OWNER/REPO.git",
		}, nil, nil)

	ctrl := gomock.NewController(t)
	mockGit := git_testing.NewMockGitRunner(ctrl)
	mockGit.EXPECT().Git("rev-parse", "--verify", "refs/heads/feat-new-mr").Return("", errors.New("not found"))
	mockGit.EXPECT().Git("rev-parse", "--verify", "abc123^{commit}").Return("", errors.New("not found"))
	// The fetch URL must use HTTPS (from custom.host.com's config), not SSH (from gitlab.com's config).
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "fetch", "--", "https://custom.host.com/OWNER/REPO.git", "refs/heads/feat-new-mr:feat-new-mr").
		DoAndReturn(git.FetchStub("refs/heads/feat-new-mr:feat-new-mr"))
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.remote", "https://custom.host.com/OWNER/REPO.git").Return("", nil)
	mockGit.EXPECT().Git("config", "branch.feat-new-mr.merge", "refs/heads/feat-new-mr").Return("", nil)
	mockGit.EXPECT().GitWithIO(gomock.Any(), gomock.Any(), "checkout", "feat-new-mr").
		DoAndReturn(git.CheckoutStub("feat-new-mr"))

	cfg := config.NewBlankConfig()
	err := cfg.Set("custom.host.com", "git_protocol", "https")
	require.NoError(t, err)
	// gitlab.com intentionally left with default (ssh) to distinguish the two hosts.

	exec := setupTest(t, testClient,
		cmdtest.WithGitRunner(mockGit),
		cmdtest.WithConfig(cfg),
		cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
	)
	output, err := exec("https://custom.host.com/OWNER/REPO/-/merge_requests/123")

	require.NoError(t, err)
	assert.Contains(t, output.Stderr(), "[new branch] refs/heads/feat-new-mr:feat-new-mr")
	assert.Contains(t, output.Stderr(), "Switched to a new branch 'feat-new-mr'")
}
