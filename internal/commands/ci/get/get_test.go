//go:build !integration

package get

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestCIGet(t *testing.T) {
	t.Parallel()

	createdAt, _ := time.Parse(time.RFC3339, "2023-10-10T00:00:00Z")
	startedAt, _ := time.Parse(time.RFC3339, "2023-10-10T00:00:00Z")
	updatedAt, _ := time.Parse(time.RFC3339, "2023-10-10T00:00:00Z")

	// Response indicating last page
	lastPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 0,
	}

	type testCase struct {
		name        string
		args        string
		expectedOut string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	tests := []testCase{
		{
			name: "when get is called on an existing pipeline",
			args: "-p=123 -b=main",
			expectedOut: `# Pipeline:
id:	123
status:	pending
source:	push
ref:	main
sha:	0ff3ae198f8601a285adcf5c0fff204ee6fba5fd
tag:	false
yaml Errors:	-
user:	test
created:	2023-10-10 00:00:00 +0000 UTC
started:	2023-10-10 00:00:00 +0000 UTC
updated:	2023-10-10 00:00:00 +0000 UTC

# Jobs:

`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(123)).
					Return(&gitlab.Pipeline{
						ID:         123,
						IID:        123,
						Status:     "pending",
						Source:     "push",
						Ref:        "main",
						SHA:        "0ff3ae198f8601a285adcf5c0fff204ee6fba5fd",
						User:       &gitlab.BasicUser{Username: "test"},
						YamlErrors: "-",
						CreatedAt:  &createdAt,
						StartedAt:  &startedAt,
						UpdatedAt:  &updatedAt,
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{}, lastPageResponse, nil)
			},
		},
		{
			name: "when get is called on missing pipeline",
			args: "-b=main",
			expectedOut: `# Pipeline:
id:	123
status:	pending
source:	push
ref:	main
sha:	0ff3ae198f8601a285adcf5c0fff204ee6fba5fd
tag:	false
yaml Errors:	-
user:	test
created:	2023-10-10 00:00:00 +0000 UTC
started:	2023-10-10 00:00:00 +0000 UTC
updated:	2023-10-10 00:00:00 +0000 UTC

# Jobs:

`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockCommits.EXPECT().
					GetCommit("OWNER/REPO", "main", gomock.Any()).
					Return(&gitlab.Commit{
						LastPipeline: &gitlab.PipelineInfo{
							ID: 123,
						},
					}, nil, nil)
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(123)).
					Return(&gitlab.Pipeline{
						ID:         123,
						IID:        123,
						Status:     "pending",
						Source:     "push",
						Ref:        "main",
						SHA:        "0ff3ae198f8601a285adcf5c0fff204ee6fba5fd",
						User:       &gitlab.BasicUser{Username: "test"},
						YamlErrors: "-",
						CreatedAt:  &createdAt,
						StartedAt:  &startedAt,
						UpdatedAt:  &updatedAt,
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{}, lastPageResponse, nil)
			},
		},
		{
			name: "when get is called on an existing pipeline with job text",
			args: "-p=123 -b=main",
			expectedOut: `# Pipeline:
id:	123
status:	pending
source:	push
ref:	main
sha:	0ff3ae198f8601a285adcf5c0fff204ee6fba5fd
tag:	false
yaml Errors:	-
user:	test
created:	2023-10-10 00:00:00 +0000 UTC
started:	2023-10-10 00:00:00 +0000 UTC
updated:	2023-10-10 00:00:00 +0000 UTC

# Jobs:
publish:	failed

`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(123)).
					Return(&gitlab.Pipeline{
						ID:         123,
						IID:        123,
						Status:     "pending",
						Source:     "push",
						Ref:        "main",
						SHA:        "0ff3ae198f8601a285adcf5c0fff204ee6fba5fd",
						User:       &gitlab.BasicUser{Username: "test"},
						YamlErrors: "-",
						CreatedAt:  &createdAt,
						StartedAt:  &startedAt,
						UpdatedAt:  &updatedAt,
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{
							ID:     123,
							Name:   "publish",
							Status: "failed",
						},
					}, lastPageResponse, nil)
			},
		},
		{
			name: "when get is called on an existing pipeline with job details",
			args: "-p=123 -b=main --with-job-details",
			expectedOut: `# Pipeline:
id:	123
status:	pending
source:	push
ref:	main
sha:	0ff3ae198f8601a285adcf5c0fff204ee6fba5fd
tag:	false
yaml Errors:	-
user:	test
created:	2023-10-10 00:00:00 +0000 UTC
started:	2023-10-10 00:00:00 +0000 UTC
updated:	2023-10-10 00:00:00 +0000 UTC

# Jobs:
ID	Name	Status	Duration	Failure reason
123	publish	failed	0	bad timing

`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(123)).
					Return(&gitlab.Pipeline{
						ID:         123,
						IID:        123,
						Status:     "pending",
						Source:     "push",
						Ref:        "main",
						SHA:        "0ff3ae198f8601a285adcf5c0fff204ee6fba5fd",
						User:       &gitlab.BasicUser{Username: "test"},
						YamlErrors: "-",
						CreatedAt:  &createdAt,
						StartedAt:  &startedAt,
						UpdatedAt:  &updatedAt,
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{
							ID:            123,
							Name:          "publish",
							Status:        "failed",
							FailureReason: "bad timing",
						},
					}, lastPageResponse, nil)
			},
		},
		{
			name: "when get is called on an existing pipeline with variables",
			args: "-p=123 -b=main --with-variables",
			expectedOut: `# Pipeline:
id:	123
status:	pending
source:	push
ref:	main
sha:	0ff3ae198f8601a285adcf5c0fff204ee6fba5fd
tag:	false
yaml Errors:	-
user:	test
created:	2023-10-10 00:00:00 +0000 UTC
started:	2023-10-10 00:00:00 +0000 UTC
updated:	2023-10-10 00:00:00 +0000 UTC

# Jobs:

# Variables:
RUN_NIGHTLY_BUILD:	true

`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(123)).
					Return(&gitlab.Pipeline{
						ID:         123,
						IID:        123,
						ProjectID:  5,
						Status:     "pending",
						Source:     "push",
						Ref:        "main",
						SHA:        "0ff3ae198f8601a285adcf5c0fff204ee6fba5fd",
						User:       &gitlab.BasicUser{Username: "test"},
						YamlErrors: "-",
						CreatedAt:  &createdAt,
						StartedAt:  &startedAt,
						UpdatedAt:  &updatedAt,
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{}, lastPageResponse, nil)
				tc.MockPipelines.EXPECT().
					GetPipelineVariables(int64(5), int64(123)).
					Return([]*gitlab.PipelineVariable{
						{
							Key:          "RUN_NIGHTLY_BUILD",
							VariableType: "env_var",
							Value:        "true",
						},
					}, nil, nil)
			},
		},
		{
			name: "when get is called on an existing pipeline with variables however no variables are found",
			args: "-p=123 -b=main --with-variables",
			expectedOut: `# Pipeline:
id:	123
status:	pending
source:	push
ref:	main
sha:	0ff3ae198f8601a285adcf5c0fff204ee6fba5fd
tag:	false
yaml Errors:	-
user:	test
created:	2023-10-10 00:00:00 +0000 UTC
started:	2023-10-10 00:00:00 +0000 UTC
updated:	2023-10-10 00:00:00 +0000 UTC

# Jobs:

# Variables:
No variables found in pipeline.
`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(123)).
					Return(&gitlab.Pipeline{
						ID:         123,
						IID:        123,
						ProjectID:  5,
						Status:     "pending",
						Source:     "push",
						Ref:        "main",
						SHA:        "0ff3ae198f8601a285adcf5c0fff204ee6fba5fd",
						User:       &gitlab.BasicUser{Username: "test"},
						YamlErrors: "-",
						CreatedAt:  &createdAt,
						StartedAt:  &startedAt,
						UpdatedAt:  &updatedAt,
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{}, lastPageResponse, nil)
				tc.MockPipelines.EXPECT().
					GetPipelineVariables(int64(5), int64(123)).
					Return([]*gitlab.PipelineVariable{}, nil, nil)
			},
		},
		{
			name: "when there is a merged result pipeline and no commit pipeline",
			args: "-b=main",
			expectedOut: `# Pipeline:
id:	123
status:	pending
source:	push
ref:	main
sha:	0ff3ae198f8601a285adcf5c0fff204ee6fba5fd
tag:	false
yaml Errors:	-
user:	test
created:	2023-10-10 00:00:00 +0000 UTC
started:	2023-10-10 00:00:00 +0000 UTC
updated:	2023-10-10 00:00:00 +0000 UTC

# Jobs:

`,
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockCommits.EXPECT().
					GetCommit("OWNER/REPO", "main", gomock.Any()).
					Return(&gitlab.Commit{
						LastPipeline: nil,
					}, nil, nil)
				tc.MockMergeRequests.EXPECT().
					ListProjectMergeRequests("OWNER/REPO", gomock.Any()).
					Return([]*gitlab.BasicMergeRequest{
						{
							IID: 1,
						},
					}, lastPageResponse, nil)
				tc.MockMergeRequests.EXPECT().
					GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
					Return(&gitlab.MergeRequest{
						HeadPipeline: &gitlab.Pipeline{
							ID: 123,
						},
					}, nil, nil)
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(123)).
					Return(&gitlab.Pipeline{
						ID:         123,
						IID:        123,
						ProjectID:  5,
						Status:     "pending",
						Source:     "push",
						Ref:        "main",
						SHA:        "0ff3ae198f8601a285adcf5c0fff204ee6fba5fd",
						User:       &gitlab.BasicUser{Username: "test"},
						YamlErrors: "-",
						CreatedAt:  &createdAt,
						StartedAt:  &startedAt,
						UpdatedAt:  &updatedAt,
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(123), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{}, lastPageResponse, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdGet,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			output, err := exec(tc.args)

			// THEN
			require.NoError(t, err)
			assert.Equal(t, tc.expectedOut, output.String())
			assert.Empty(t, output.Stderr())
		})
	}
}

func TestCIGetJSON(t *testing.T) {
	t.Parallel()

	createdAt, _ := time.Parse(time.RFC3339, "2022-01-20T21:47:16.276Z")
	startedAt, _ := time.Parse(time.RFC3339, "2022-01-20T21:47:17.448Z")
	updatedAt, _ := time.Parse(time.RFC3339, "2022-01-20T21:47:31.358Z")
	finishedAt, _ := time.Parse(time.RFC3339, "2022-01-20T21:47:31.35Z")

	jobCreatedAt, _ := time.Parse(time.RFC3339, "2022-01-20T21:47:16.291Z")
	jobStartedAt, _ := time.Parse(time.RFC3339, "2022-01-20T21:47:16.693Z")
	jobFinishedAt, _ := time.Parse(time.RFC3339, "2022-01-20T21:47:31.274Z")

	// Response indicating last page
	lastPageResponse := &gitlab.Response{
		Response: &http.Response{StatusCode: http.StatusOK},
		NextPage: 0,
	}

	type testCase struct {
		name      string
		args      string
		setupMock func(tc *gitlabtesting.TestClient)
	}

	tests := []testCase{
		{
			name: "when getting JSON for pipeline",
			args: "-p 452959326 -F json -b main",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPipelines.EXPECT().
					GetPipeline("OWNER/REPO", int64(452959326)).
					Return(&gitlab.Pipeline{
						ID:         452959326,
						IID:        14,
						ProjectID:  29316529,
						SHA:        "44eb489568f7cb1a5a730fce6b247cd3797172ca",
						Ref:        "1-fake-issue-3",
						Status:     "success",
						Source:     "push",
						CreatedAt:  &createdAt,
						UpdatedAt:  &updatedAt,
						StartedAt:  &startedAt,
						FinishedAt: &finishedAt,
						BeforeSHA:  "001eb421e586a3f07f90aea102c8b2d4068ab5b6",
						Tag:        false,
						User: &gitlab.BasicUser{
							ID:        8814129,
							Username:  "OWNER",
							Name:      "Some User",
							State:     "active",
							Locked:    false,
							AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/8814129/avatar.png",
							WebURL:    "https://gitlab.com/OWNER",
						},
						WebURL:         "https://gitlab.com/OWNER/REPO/-/pipelines/452959326",
						Duration:       14,
						QueuedDuration: 1,
						DetailedStatus: &gitlab.DetailedStatus{
							Icon:        "status_success",
							Text:        "Passed",
							Label:       "passed",
							Group:       "success",
							Tooltip:     "passed",
							HasDetails:  true,
							DetailsPath: "/OWNER/REPO/-/pipelines/452959326",
							Favicon:     "/assets/ci_favicons/favicon_status_success-8451333011eee8ce9f2ab25dc487fe24a8758c694827a582f17f42b0a90446a2.png",
						},
					}, nil, nil)
				tc.MockJobs.EXPECT().
					ListPipelineJobs("OWNER/REPO", int64(452959326), gomock.Any(), gomock.Any()).
					Return([]*gitlab.Job{
						{
							ID:             1999017704,
							Status:         "success",
							Stage:          "test",
							Name:           "test_vars",
							Ref:            "1-fake-issue-3",
							Tag:            false,
							AllowFailure:   false,
							CreatedAt:      &jobCreatedAt,
							StartedAt:      &jobStartedAt,
							FinishedAt:     &jobFinishedAt,
							Duration:       14.580467,
							QueuedDuration: 0.211715,
							User: &gitlab.User{
								ID:        8814129,
								Username:  "OWNER",
								Name:      "Some User",
								State:     "active",
								Locked:    false,
								AvatarURL: "https://gitlab.com/uploads/-/system/user/avatar/8814129/avatar.png",
								WebURL:    "https://gitlab.com/OWNER",
							},
							Commit: &gitlab.Commit{
								ID:             "44eb489568f7cb1a5a730fce6b247cd3797172ca",
								ShortID:        "44eb4895",
								Title:          "Add new file",
								AuthorName:     "Some User",
								AuthorEmail:    "OWNER@gitlab.com",
								CommitterName:  "Some User",
								CommitterEmail: "OWNER@gitlab.com",
								Message:        "Add new file",
								ParentIDs:      []string{"001eb421e586a3f07f90aea102c8b2d4068ab5b6"},
								WebURL:         "https://gitlab.com/OWNER/REPO/-/commit/44eb489568f7cb1a5a730fce6b247cd3797172ca",
							},
							Pipeline: gitlab.JobPipeline{
								ID:        452959326,
								ProjectID: 29316529,
								Sha:       "44eb489568f7cb1a5a730fce6b247cd3797172ca",
								Ref:       "1-fake-issue-3",
								Status:    "success",
							},
							WebURL: "https://gitlab.com/OWNER/REPO/-/jobs/1999017704",
							Runner: gitlab.JobRunner{
								ID:          12270859,
								Description: "5-green.saas-linux-small-amd64.runners-manager.gitlab.com/default",
								Active:      true,
								IsShared:    true,
								Name:        "gitlab-runner",
							},
							Artifacts: []gitlab.JobArtifact{
								{
									FileType: "trace",
									Filename: "job.log",
									Size:     2770,
								},
							},
						},
					}, lastPageResponse, nil)
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdGet,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			output, err := exec(tc.args)

			// THEN
			require.NoError(t, err)
			// Verify it's valid JSON that contains expected fields
			assert.Contains(t, output.String(), `"id":452959326`)
			assert.Contains(t, output.String(), `"status":"success"`)
			assert.Contains(t, output.String(), `"jobs":[`)
			assert.Contains(t, output.String(), `"test_vars"`)
			assert.Empty(t, output.Stderr())
		})
	}
}
