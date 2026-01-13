//go:build !integration

package ciutils

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/survivorbat/huhtest"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestGetJobId(t *testing.T) {
	// Response indicating last page
	lastPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 0,
	}

	// Response indicating there's a next page
	nextPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 2,
	}

	type testCase struct {
		name          string
		jobName       string
		pipelineId    int
		responder     *huhtest.Responder
		setupMock     func(tc *gitlabtesting.TestClient)
		expectedOut   int64
		expectedError string
	}

	tests := []testCase{
		{
			name:        "when getJobId with integer is requested",
			jobName:     "1122",
			expectedOut: 1122,
			setupMock:   func(tc *gitlabtesting.TestClient) {},
		},
		{
			name:        "when getJobId with name and pipelineId is requested",
			jobName:     "lint",
			pipelineId:  123,
			expectedOut: 1122,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
		{
			name:        "when getJobId with name and pipelineId is requested and job is found on page 2",
			jobName:     "deploy",
			pipelineId:  123,
			expectedOut: 1144,
			setupMock: func(tc *gitlabtesting.TestClient) {
				// First page
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, nextPageResponse, nil)
				// Second page
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1133, Name: "test", Status: "failed"},
						{ID: 1144, Name: "deploy", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
		{
			name:          "when getJobId with name and pipelineId is requested and listJobs throws error",
			jobName:       "lint",
			pipelineId:    123,
			expectedError: "list pipeline jobs:",
			expectedOut:   0,
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:        "when getJobId with name and last pipeline is requested",
			jobName:     "lint",
			pipelineId:  0,
			expectedOut: 1122,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Pipeline{ID: 123}, nil, nil)

				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
		{
			name:          "when getJobId with name and last pipeline is requested and getCommits throws error",
			jobName:       "lint",
			pipelineId:    0,
			expectedError: "get pipeline: get last pipeline:",
			expectedOut:   0,
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:          "when getJobId with name and last pipeline is requested and getJobs throws error",
			jobName:       "lint",
			pipelineId:    0,
			expectedError: "list pipeline jobs:",
			expectedOut:   0,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetLatestPipeline("OWNER/REPO", gomock.Any()).
					Return(&gitlab.Pipeline{ID: 123}, nil, nil)

				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:        "when getJobId with pipelineId is requested, ask for job and answer",
			jobName:     "",
			pipelineId:  123,
			expectedOut: 1122,
			responder: huhtest.NewResponder().
				AddSelect("Select pipeline job to trace:", 0),
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{ID: 1122, Name: "lint", Status: "failed"},
						{ID: 1124, Name: "publish", Status: "failed"},
					}, lastPageResponse, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			factoryOpts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("main"),
			}

			if tc.responder != nil {
				factoryOpts = append(factoryOpts, cmdtest.WithResponder(t, tc.responder))
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios, factoryOpts...)

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()

			output, err := GetJobId(t.Context(), &JobInputs{
				JobName:    tc.jobName,
				PipelineId: tc.pipelineId,
				Branch:     "main",
			}, &JobOptions{
				IO:     f.IO(),
				Repo:   repo,
				Client: client,
			})

			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			}
			assert.Equal(t, tc.expectedOut, output)
		})
	}
}

func TestParseCSVToIntSlice(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expectedOut []int
	}{
		{
			name:        "when input is empty",
			input:       "",
			expectedOut: nil,
		},
		{
			name:        "when input is a comma-separated string",
			input:       "111,222,333",
			expectedOut: []int{111, 222, 333},
		},
		{
			name:        "when input is a space-separated string",
			input:       "111 222 333 4444",
			expectedOut: []int{111, 222, 333, 4444},
		},
		{
			name:        "when input is a space-separated and comma-separated string",
			input:       "111, 222, 333, 4444",
			expectedOut: []int{111, 222, 333, 4444},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Simulate splitting raw input
			args := strings.Fields(tc.input)

			output, err := IDsFromArgs(args)
			if err != nil {
				require.Nil(t, err)
			}

			assert.Equal(t, tc.expectedOut, output)
		})
	}
}

func TestTraceJob(t *testing.T) {
	type testCase struct {
		name          string
		jobName       string
		pipelineId    int
		setupMock     func(tc *gitlabtesting.TestClient)
		expectedError string
	}

	tests := []testCase{
		{
			name:    "when traceJob is requested",
			jobName: "1122",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(&gitlab.Job{
						ID:     1122,
						Name:   "lint",
						Status: "success",
					}, nil, nil)

				tc.MockJobs.EXPECT().
					GetTraceFile("OWNER/REPO", int64(1122), gomock.Any()).
					Return(bytes.NewReader([]byte("Lorem ipsum")), nil, nil)
			},
		},
		{
			name:          "when traceJob is requested and getJob throws error",
			jobName:       "1122",
			expectedError: "failed to find job:",
			setupMock: func(tc *gitlabtesting.TestClient) {
				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
		{
			name:          "when traceJob is requested and trace job throws error",
			jobName:       "1122",
			expectedError: "failed to find job:",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockJobs.EXPECT().
					GetJob("OWNER/REPO", int64(1122), gomock.Any()).
					Return(&gitlab.Job{
						ID:     1122,
						Name:   "lint",
						Status: "success",
					}, nil, nil)

				forbiddenResponse := &gitlab.Response{Response: &http.Response{StatusCode: http.StatusForbidden}}
				tc.MockJobs.EXPECT().
					GetTraceFile("OWNER/REPO", int64(1122), gomock.Any()).
					Return(nil, forbiddenResponse, assert.AnError)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			ios, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClient(testClient.Client),
				cmdtest.WithBranch("main"),
			)

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()

			err := TraceJob(t.Context(), &JobInputs{
				JobName:    tc.jobName,
				PipelineId: tc.pipelineId,
				Branch:     "main",
			}, &JobOptions{
				IO:     f.IO(),
				Repo:   repo,
				Client: client,
			})

			if tc.expectedError == "" {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			}
		})
	}
}

// TestGetDefaultBranch_HappyPath tests successful scenarios for GetDefaultBranch
func TestGetDefaultBranch_HappyPath(t *testing.T) {
	tests := []struct {
		name           string
		defaultBranch  string
		expectedResult string
	}{
		{
			name:           "when API returns default branch",
			defaultBranch:  "develop",
			expectedResult: "develop",
		},
		{
			name:           "when API returns main as default branch",
			defaultBranch:  "main",
			expectedResult: "main",
		},
		{
			name:           "when API returns empty default branch",
			defaultBranch:  "",
			expectedResult: "main",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			project := &gitlab.Project{
				DefaultBranch: tc.defaultBranch,
			}
			testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(project, nil, nil)

			ios, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()
			result := GetDefaultBranch(repo, client)
			require.Equal(t, tc.expectedResult, result)
		})
	}
}

// TestGetDefaultBranch_ErrorCases tests error scenarios for GetDefaultBranch
func TestGetDefaultBranch_ErrorCases(t *testing.T) {
	tests := []struct {
		name           string
		factoryOptions []cmdtest.FactoryOption
		expectMain     bool
	}{
		{
			name:           "when BaseRepo fails",
			factoryOptions: []cmdtest.FactoryOption{cmdtest.WithBaseRepoError(assert.AnError)},
			expectMain:     true,
		},
		{
			name:           "when GitLabClient fails",
			factoryOptions: []cmdtest.FactoryOption{cmdtest.WithGitLabClientError(assert.AnError)},
			expectMain:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ios, _, _, _ := cmdtest.TestIOStreams()
			factory := cmdtest.NewTestFactory(ios, tc.factoryOptions...)

			client, _ := factory.GitLabClient()
			repo, _ := factory.BaseRepo()

			// Run the test with the configured factory
			result := GetDefaultBranch(repo, client)
			require.Equal(t, "main", result)
		})
	}
}

// TestGetDefaultBranch_APIErrorCases tests API failure scenarios for GetDefaultBranch
func TestGetDefaultBranch_APIErrorCases(t *testing.T) {
	t.Run("when API call fails", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(nil, nil, assert.AnError)

		ios, _, _, _ := cmdtest.TestIOStreams()
		f := cmdtest.NewTestFactory(ios,
			cmdtest.WithGitLabClient(testClient.Client),
		)

		client, _ := f.GitLabClient()
		repo, _ := f.BaseRepo()
		result := GetDefaultBranch(repo, client)
		require.Equal(t, "main", result)
	})
}

// TestGetBranch_HappyPath tests successful scenarios for GetBranch
func TestGetBranch_HappyPath(t *testing.T) {
	tests := []struct {
		name             string
		specifiedBranch  string
		gitBranch        string
		apiDefaultBranch string
		expectedResult   string
	}{
		{
			name:            "when branch is specified",
			specifiedBranch: "feature-branch",
			expectedResult:  "feature-branch",
		},
		{
			name:           "when no branch specified and git works",
			gitBranch:      "current-git-branch",
			expectedResult: "current-git-branch",
		},
		{
			name:             "when no branch specified and git fails, uses API default",
			apiDefaultBranch: "develop",
			expectedResult:   "develop",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)

			if tc.apiDefaultBranch != "" {
				project := &gitlab.Project{
					DefaultBranch: tc.apiDefaultBranch,
				}
				testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(project, nil, nil)
			}

			ios, _, _, _ := cmdtest.TestIOStreams()
			f := cmdtest.NewTestFactory(ios,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			if tc.gitBranch != "" {
				f.BranchStub = func() (string, error) {
					return tc.gitBranch, nil
				}
			} else if tc.apiDefaultBranch != "" {
				f.BranchStub = func() (string, error) {
					return "", assert.AnError
				}
			}

			client, _ := f.GitLabClient()
			repo, _ := f.BaseRepo()
			result := GetBranch(tc.specifiedBranch, f.BranchStub, repo, client)
			require.Equal(t, tc.expectedResult, result)
		})
	}
}

// TestGetBranch_ErrorFallback tests error scenarios that fallback to main
func TestGetBranch_ErrorFallback(t *testing.T) {
	t.Run("when no branch specified and git fails, falls back to main", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock API failure
		testClient.MockProjects.EXPECT().GetProject("OWNER/REPO", gomock.Any()).Return(nil, nil, assert.AnError)

		ios, _, _, _ := cmdtest.TestIOStreams()
		f := cmdtest.NewTestFactory(ios,
			cmdtest.WithGitLabClient(testClient.Client),
		)

		f.BranchStub = func() (string, error) {
			return "", assert.AnError
		}

		client, _ := f.GitLabClient()
		repo, _ := f.BaseRepo()
		result := GetBranch("", f.BranchStub, repo, client)
		require.Equal(t, "main", result)
	})
}
