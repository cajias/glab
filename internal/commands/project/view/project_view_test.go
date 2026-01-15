//go:build !integration

package view

import (
	"net/http"
	"os/exec"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/run"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestProjectView(t *testing.T) {
	tests := []struct {
		name     string
		cli      string
		isTTY    bool
		stub     bool
		repoHost string

		// For mocking
		setupMocks func(t *testing.T, testClient *gitlabtesting.TestClient)

		expectedOutput string
	}{
		{
			name: "view the project details for the current project",
			cli:  "",
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "main",
						WebURL:            "https://gitlab.com/OWNER/REPO",
						ReadmeURL:         "https://gitlab.com/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
				testClient.MockRepositoryFiles.EXPECT().
					GetFile("OWNER/REPO", "README.md", gomock.Any()).
					Return(&gitlab.File{
						FileName: "README.md",
						FilePath: "README.md",
						Encoding: "base64",
						Ref:      "main",
						Content:  "dGVzdCByZWFkbWUK",
					}, nil, nil)
			},
			expectedOutput: heredoc.Doc(`name:	Test User / REPO
description:	this is a test description
---
test readme

`),
		},
		{
			name: "view the details of a project owned by the current user",
			cli:  "foo",
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockUsers.EXPECT().
					CurrentUser(gomock.Any()).
					Return(&gitlab.User{Username: "test_user"}, nil, nil)
				testClient.MockProjects.EXPECT().
					GetProject("test_user/foo", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "foo",
						NameWithNamespace: "test_user / foo",
						Path:              "foo",
						PathWithNamespace: "test_user/foo",
						DefaultBranch:     "main",
						WebURL:            "https://gitlab.com/test_user/foo",
						ReadmeURL:         "https://gitlab.com/test_user/foo/-/blob/main/README.md",
					}, nil, nil)
				testClient.MockRepositoryFiles.EXPECT().
					GetFile("test_user/foo", "README.md", gomock.Any()).
					Return(&gitlab.File{
						FileName: "README.md",
						FilePath: "README.md",
						Encoding: "base64",
						Ref:      "main",
						Content:  "dGVzdCByZWFkbWUK",
					}, nil, nil)
			},
			expectedOutput: heredoc.Doc(`name:	test_user / foo
description:	this is a test description
---
test readme

`),
		},
		{
			name: "view a specific project's details",
			cli:  "foo/bar",
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("foo/bar", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "bar",
						NameWithNamespace: "foo / bar",
						Path:              "bar",
						PathWithNamespace: "foo/bar",
						DefaultBranch:     "main",
						WebURL:            "https://gitlab.com/foo/bar",
						ReadmeURL:         "https://gitlab.com/foo/bar/-/blob/main/README.md",
					}, nil, nil)
				testClient.MockRepositoryFiles.EXPECT().
					GetFile("foo/bar", "README.md", gomock.Any()).
					Return(&gitlab.File{
						FileName: "README.md",
						FilePath: "README.md",
						Encoding: "base64",
						Ref:      "main",
						Content:  "dGVzdCByZWFkbWUK",
					}, nil, nil)
			},
			expectedOutput: heredoc.Doc(`name:	foo / bar
description:	this is a test description
---
test readme

`),
		},
		{
			name: "view a group's specific project details",
			cli:  "group/foo/bar",
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("group/foo/bar", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "bar",
						NameWithNamespace: "group / foo / bar",
						Path:              "bar",
						PathWithNamespace: "group/foo/bar",
						DefaultBranch:     "main",
						WebURL:            "https://gitlab.com/group/foo/bar",
						ReadmeURL:         "https://gitlab.com/group/foo/bar/-/blob/main/README.md",
					}, nil, nil)
				testClient.MockRepositoryFiles.EXPECT().
					GetFile("group/foo/bar", "README.md", gomock.Any()).
					Return(&gitlab.File{
						FileName: "README.md",
						FilePath: "README.md",
						Encoding: "base64",
						Ref:      "main",
						Content:  "dGVzdCByZWFkbWUK",
					}, nil, nil)
			},
			expectedOutput: heredoc.Doc(`name:	group / foo / bar
description:	this is a test description
---
test readme

`),
		},
		{
			name:     "view a project details from a project not hosted on the default host",
			cli:      "",
			repoHost: "gitlab.company.org",
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "bar",
						NameWithNamespace: "OWNER / REPO",
						Path:              "bar",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "main",
						WebURL:            "https://gitlab.company.org/OWNER/REPO",
						ReadmeURL:         "https://gitlab.company.org/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
				testClient.MockRepositoryFiles.EXPECT().
					GetFile("OWNER/REPO", "README.md", gomock.Any()).
					Return(&gitlab.File{
						FileName: "README.md",
						FilePath: "README.md",
						Encoding: "base64",
						Ref:      "main",
						Content:  "dGVzdCByZWFkbWUK",
					}, nil, nil)
			},
			expectedOutput: heredoc.Doc(`name:	OWNER / REPO
description:	this is a test description
---
test readme

`),
		},
		{
			name: "view project details from a git URL",
			cli:  "https://gitlab.company.org/OWNER/REPO.git",
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "bar",
						NameWithNamespace: "OWNER / REPO",
						Path:              "bar",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "main",
						WebURL:            "https://gitlab.company.org/OWNER/REPO",
						ReadmeURL:         "https://gitlab.company.org/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
				testClient.MockRepositoryFiles.EXPECT().
					GetFile("OWNER/REPO", "README.md", gomock.Any()).
					Return(&gitlab.File{
						FileName: "README.md",
						FilePath: "README.md",
						Encoding: "base64",
						Ref:      "main",
						Content:  "dGVzdCByZWFkbWUK",
					}, nil, nil)
			},
			expectedOutput: heredoc.Doc(`name:	OWNER / REPO
description:	this is a test description
---
test readme

`),
		},
		{
			name:  "view project on web where current branch is different to default branch",
			cli:   "--web",
			isTTY: true,
			stub:  true,
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "main",
						WebURL:            "https://gitlab.com/OWNER/REPO",
						ReadmeURL:         "https://gitlab.com/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.com/OWNER/REPO/-/tree/%23current-branch in your browser.\n",
		},
		{
			name:  "view project default branch on web",
			cli:   "--web",
			isTTY: true,
			stub:  true,
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "#current-branch",
						WebURL:            "https://gitlab.com/OWNER/REPO",
						ReadmeURL:         "https://gitlab.com/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.com/OWNER/REPO in your browser.\n",
		},
		{
			name:  "view project when passing a https git URL on web",
			cli:   "https://gitlab.company.org/OWNER/REPO.git --web",
			isTTY: true,
			stub:  true,
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "#current-branch",
						WebURL:            "https://gitlab.company.org/OWNER/REPO",
						ReadmeURL:         "https://gitlab.company.org/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.company.org/OWNER/REPO in your browser.\n",
		},
		{
			name:  "view project when passing a https git URL on web with branch",
			cli:   "https://gitlab.company.org/OWNER/REPO.git --web --branch foobranch",
			isTTY: true,
			stub:  true,
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "#current-branch",
						WebURL:            "https://gitlab.company.org/OWNER/REPO",
						ReadmeURL:         "https://gitlab.company.org/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.company.org/OWNER/REPO/-/tree/foobranch in your browser.\n",
		},
		{
			name:  "view project when passing a https URL on web",
			cli:   "https://gitlab.company.org/OWNER/REPO --web",
			isTTY: true,
			stub:  true,
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "#current-branch",
						WebURL:            "https://gitlab.company.org/OWNER/REPO",
						ReadmeURL:         "https://gitlab.company.org/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.company.org/OWNER/REPO in your browser.\n",
		},
		{
			name:  "view project when passing a git URL on web",
			cli:   "git@gitlab.company.org:OWNER/REPO.git --web",
			isTTY: true,
			stub:  true,
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "#current-branch",
						WebURL:            "https://gitlab.company.org/OWNER/REPO",
						ReadmeURL:         "https://gitlab.company.org/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.company.org/OWNER/REPO in your browser.\n",
		},
		{
			name:     "view a project that isn't on the default host on web",
			cli:      "--web",
			isTTY:    true,
			stub:     true,
			repoHost: "gitlab.company.org",
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "#current-branch",
						WebURL:            "https://gitlab.company.org/OWNER/REPO",
						ReadmeURL:         "https://gitlab.company.org/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.company.org/OWNER/REPO in your browser.\n",
		},
		{
			name:  "view a specific project branch on the web",
			cli:   "--branch foo --web",
			isTTY: true,
			stub:  true,
			setupMocks: func(t *testing.T, testClient *gitlabtesting.TestClient) {
				t.Helper()
				testClient.MockProjects.EXPECT().
					GetProject("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Project{
						ID:                37777023,
						Description:       "this is a test description",
						Name:              "REPO",
						NameWithNamespace: "Test User / REPO",
						Path:              "REPO",
						PathWithNamespace: "OWNER/REPO",
						DefaultBranch:     "#current-branch",
						WebURL:            "https://gitlab.com/OWNER/REPO",
						ReadmeURL:         "https://gitlab.com/OWNER/REPO/-/blob/main/README.md",
					}, nil, nil)
			},
			expectedOutput: "Opening gitlab.com/OWNER/REPO/-/tree/foo in your browser.\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			// Setup mocks
			tc.setupMocks(t, testClient)

			ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(tc.isTTY))

			// Create api.Client that wraps the mock gitlab.Client
			apiClient, err := api.NewClient(
				func(*http.Client) (gitlab.AuthSource, error) {
					return gitlab.AccessTokenAuthSource{Token: ""}, nil
				},
				api.WithGitLabClient(testClient.Client),
			)
			if err != nil {
				t.Fatalf("failed to create api client: %v", err)
			}

			factory := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("#current-branch"),
				func(f *cmdtest.Factory) {
					f.BaseRepoStub = func() (glrepo.Interface, error) {
						if tc.repoHost == "" {
							return glrepo.New("OWNER", "REPO", glinstance.DefaultHostname), nil
						}
						return glrepo.NewWithHost("OWNER", "REPO", tc.repoHost), nil
					}
					// Set ApiClientStub to return our api.Client with the mock gitlab.Client
					f.ApiClientStub = func(repoHost string) (*api.Client, error) {
						return apiClient, nil
					}
				},
			)

			cmd := NewCmdView(factory)

			var restoreCmd func()
			if tc.stub {
				restoreCmd = run.SetPrepareCmd(func(cmd *exec.Cmd) run.Runnable {
					return &test.OutputStub{}
				})
				defer restoreCmd()
			}

			output, err := cmdtest.ExecuteCommand(cmd, tc.cli, stdout, stderr)

			if assert.NoErrorf(t, err, "error running command `project view %s`: %v", tc.cli, err) {
				assert.Equal(t, tc.expectedOutput, output.String())
				assert.Empty(t, output.Stderr())
			}
		})
	}
}
