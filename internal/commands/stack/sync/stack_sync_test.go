//go:build !integration

package sync

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/git"
	git_testing "gitlab.com/gitlab-org/cli/internal/git/testing"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type SyncScenario struct {
	refs       map[string]TestRef
	title      string
	baseBranch string
	pushNeeded bool
}

type TestRef struct {
	ref   git.StackRef
	state string
}

func setupTestFactory(t *testing.T, testClient *gitlabtesting.TestClient) (cmdutils.Factory, *options) {
	t.Helper()

	ios, _, _, _ := cmdtest.TestIOStreams()

	// Create api.Client that wraps the mock gitlab.Client
	apiClient, err := api.NewClient(
		func(*http.Client) (gitlab.AuthSource, error) {
			return gitlab.AccessTokenAuthSource{Token: ""}, nil
		},
		api.WithGitLabClient(testClient.Client),
	)
	require.NoError(t, err)

	f := cmdtest.NewTestFactory(ios,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.BaseRepoStub = func() (glrepo.Interface, error) {
				return glrepo.TestProject("stack_guy", "stackproject"), nil
			}
			f.ApiClientStub = func(repoHost string) (*api.Client, error) {
				return apiClient, nil
			}
		},
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				r := glrepo.Remotes{
					&glrepo.Remote{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "head: gitlab.com/stack_guy/stackproject",
						},
						Repo: glrepo.TestProject("stack_guy", "stackproject"),
					},
				}
				return r, nil
			}
		},
	)

	client, _ := f.GitLabClient()

	return f, &options{
		io:        ios,
		remotes:   f.Remotes,
		labClient: client,
		baseRepo:  f.BaseRepo,
	}
}

func Test_stackSync(t *testing.T) {
	type args struct {
		stack SyncScenario
	}

	tests := []struct {
		name       string
		args       args
		setupMocks func(t *testing.T, testClient *gitlabtesting.TestClient)
		wantErr    bool
	}{
		{
			name: "two branches, 1st branch has MR, 2nd branch behind, stacks are named",
			args: args{
				stack: SyncScenario{
					title: "my cool stack",
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR:          "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
								Description: "single line desc",
							},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: "", Description: "multi line desc\n\ndescription, bark!"},
							state: BranchIsBehind,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockListStackMRsByBranch("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								Title:        "test mr title",
								TargetBranch: "main",
								SourceBranch: "Branch1",
								State:        "opened",
								Description:  "test mr description25",
								Author:       &gitlab.BasicUser{ID: 1, Username: "admin"},
							},
						}, nil, nil
					})

				// MockGetStackMR("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:           25,
							IID:          25,
							ProjectID:    3,
							Title:        "test mr title",
							TargetBranch: "main",
							SourceBranch: "Branch1",
							State:        "opened",
							Description:  "test mr description25",
							Author:       &gitlab.BasicUser{ID: 1, Username: "admin"},
						},
					}, nil, nil)

				// MockPostStackMR(Source: "Branch2", Target: "Branch1", Title: "multi line desc", Description: "description, bark!")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						assert.Equal(t, "multi line desc", *opts.Title)
						assert.Equal(t, "description, bark!", *opts.Description)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          42,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
								Title:        "multi line desc",
								Description:  "description, bark!",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "two branches, no MRs, nothing to commit",
			args: args{
				stack: SyncScenario{
					title: "my cool stack",
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "2", Branch: "Branch1", MR: "", Description: "some description"},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: ""},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockPostStackMR(Source: "Branch1", Target: "main", Title: "some description")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "main", *opts.TargetBranch)
						assert.Equal(t, "some description", *opts.Title)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          43,
								SourceBranch: "Branch1",
								TargetBranch: "main",
								Title:        "some description",
							},
						}, nil, nil
					})

				// MockPostStackMR(Source: "Branch2", Target: "Branch1")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          44,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
							},
						}, nil, nil
					})
			},
		},

		{
			name: "a complicated scenario",
			args: args{
				stack: SyncScenario{
					title:      "my cool stack",
					pushNeeded: true,
					refs: map[string]TestRef{
						"1": {
							ref: git.StackRef{
								SHA: "1", Prev: "", Next: "2", Branch: "Branch1",
								MR: "http://gitlab.com/stack_guy/stackproject/-/merge_requests/1",
							},
							state: NothingToCommit,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "3", Branch: "Branch2", MR: ""},
							state: NothingToCommit,
						},
						"3": {
							ref:   git.StackRef{SHA: "3", Prev: "2", Next: "4", Branch: "Branch3", MR: ""},
							state: NothingToCommit,
						},
						"4": {
							ref:   git.StackRef{SHA: "4", Prev: "3", Next: "5", Branch: "Branch4", MR: ""},
							state: BranchHasDiverged,
						},
						"5": {
							ref:   git.StackRef{SHA: "5", Prev: "4", Next: "6", Branch: "Branch5", MR: ""},
							state: NothingToCommit,
						},
						"6": {
							ref:   git.StackRef{SHA: "6", Prev: "5", Next: "", Branch: "Branch6", MR: ""},
							state: NothingToCommit,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockListStackMRsByBranch("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.ListProjectMergeRequestsOptions, options ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						return []*gitlab.BasicMergeRequest{
							{
								ID:           25,
								IID:          25,
								ProjectID:    3,
								SourceBranch: "Branch1",
								State:        "opened",
							},
						}, nil, nil
					})

				// MockGetStackMR("Branch1", "25")
				testClient.MockMergeRequests.EXPECT().
					GetMergeRequest("stack_guy/stackproject", int64(25), gomock.Any()).
					Return(&gitlab.MergeRequest{
						BasicMergeRequest: gitlab.BasicMergeRequest{
							ID:           25,
							IID:          25,
							ProjectID:    3,
							SourceBranch: "Branch1",
							State:        "opened",
						},
					}, nil, nil)

				// Create MRs for Branch2-6
				for i := 2; i <= 6; i++ {
					testClient.MockMergeRequests.EXPECT().
						CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
						Return(&gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID: int64(40 + i),
							},
						}, nil, nil)
				}
			},
		},
		{
			name: "non standard base branch",
			args: args{
				stack: SyncScenario{
					title:      "my cool stack",
					baseBranch: "jawn",
					refs: map[string]TestRef{
						"1": {
							ref:   git.StackRef{SHA: "1", Prev: "", Next: "2", Branch: "Branch1", MR: ""},
							state: BranchIsBehind,
						},
						"2": {
							ref:   git.StackRef{SHA: "2", Prev: "1", Next: "", Branch: "Branch2", MR: ""},
							state: BranchIsBehind,
						},
					},
				},
			},
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				// MockStackUser
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "stack_guy"}, nil, nil)

				// MockPostStackMR(Source: "Branch1", Target: "jawn")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch1", *opts.SourceBranch)
						assert.Equal(t, "jawn", *opts.TargetBranch)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          45,
								SourceBranch: "Branch1",
								TargetBranch: "jawn",
							},
						}, nil, nil
					})

				// MockPostStackMR(Source: "Branch2", Target: "Branch1")
				testClient.MockMergeRequests.EXPECT().
					CreateMergeRequest("stack_guy/stackproject", gomock.Any()).
					DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
						assert.Equal(t, "Branch2", *opts.SourceBranch)
						assert.Equal(t, "Branch1", *opts.TargetBranch)
						return &gitlab.MergeRequest{
							BasicMergeRequest: gitlab.BasicMergeRequest{
								IID:          46,
								SourceBranch: "Branch2",
								TargetBranch: "Branch1",
							},
						}, nil, nil
					})
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			git.InitGitRepoWithCommit(t)

			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMocks(t, testClient)

			ctrl := gomock.NewController(t)
			mockCmd := git_testing.NewMockGitRunner(ctrl)

			f, opts := setupTestFactory(t, testClient)

			err := git.SetConfig("glab.currentstack", tc.args.stack.title)
			require.NoError(t, err)

			createStack(t, tc.args.stack.title, tc.args.stack.refs)
			stack, err := git.GatherStackRefs(tc.args.stack.title)
			require.NoError(t, err)

			mockCmd.EXPECT().Git([]string{"fetch", "origin"})

			for ref := range stack.Iter() {
				state := tc.args.stack.refs[ref.SHA].state

				mockCmd.EXPECT().Git([]string{"checkout", ref.Branch})
				mockCmd.EXPECT().Git([]string{"status", "-uno"}).Return(state, nil)

				switch state {
				case BranchIsBehind:
					mockCmd.EXPECT().Git([]string{"pull"}).Return(state, nil)

				case BranchHasDiverged:
					mockCmd.EXPECT().Git([]string{"checkout", stack.Last().Branch})
					mockCmd.EXPECT().Git([]string{"rebase", "--fork-point", "--update-refs", ref.Branch})

				case NothingToCommit:
				}

				if ref.MR == "" {
					if ref.IsFirst() == true {
						if tc.args.stack.baseBranch != "" {
							err := git.AddStackBaseBranch(tc.args.stack.title, tc.args.stack.baseBranch)
							require.NoError(t, err)
							mockCmd.EXPECT().Git([]string{"ls-remote", "--exit-code", "--heads", "origin", tc.args.stack.baseBranch})
						} else {
							// this is to check for the default branch
							mockCmd.EXPECT().Git([]string{"remote", "show", "origin"}).Return("HEAD branch: main", nil)
							mockCmd.EXPECT().Git([]string{"ls-remote", "--exit-code", "--heads", "origin", "main"})
						}
					}

					mockCmd.EXPECT().Git([]string{"push", "--set-upstream", "origin", ref.Branch}).Return("a", nil)

				}
			}

			if tc.args.stack.pushNeeded {
				command := append([]string{"push", "origin", "--force-with-lease"}, stack.Branches()...)
				mockCmd.EXPECT().Git(command)
			}

			err = opts.run(f, mockCmd)

			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func createStack(t *testing.T, title string, scenario map[string]TestRef) {
	t.Helper()
	_ = git.CheckoutNewBranch("main")

	for _, ref := range scenario {
		err := git.AddStackRefFile(title, ref.ref)
		require.NoError(t, err)

		err = git.CheckoutNewBranch(ref.ref.Branch)
		require.NoError(t, err)
	}
}
