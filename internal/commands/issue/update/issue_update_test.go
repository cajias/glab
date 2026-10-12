//go:build !integration

package update

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestIssueUpdate_DescriptionFile(t *testing.T) {
	const body = "# Heading\n\nMulti-line body with \"quotes\", $VARS and `backticks`.\n"

	tests := []struct {
		name     string
		useStdin bool
	}{
		{name: "reads the description from a file"},
		{name: "reads the description from standard input", useStdin: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)
			tc.MockIssues.EXPECT().
				GetIssue("OWNER/REPO", int64(42)).
				Return(&gitlab.Issue{IID: 42, Title: "issueTitle"}, nil, nil)

			var gotDescription *string
			tc.MockIssues.EXPECT().
				UpdateIssue("OWNER/REPO", int64(42), gomock.Any()).
				DoAndReturn(func(_ any, _ int64, opts *gitlab.UpdateIssueOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Issue, *gitlab.Response, error) {
					gotDescription = opts.Description
					return &gitlab.Issue{IID: 42, Title: "issueTitle", WebURL: "https://gitlab.com/OWNER/REPO/-/issues/42"}, nil, nil
				})

			factoryOpts := []cmdtest.FactoryOption{
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			}

			path := "-"
			if tt.useStdin {
				factoryOpts = append(factoryOpts, cmdtest.WithStdin(body))
			} else {
				path = filepath.Join(t.TempDir(), "description.md")
				require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			}

			exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false, factoryOpts...)

			_, err := exec(fmt.Sprintf("42 --description-file %q", path))
			require.NoError(t, err)

			require.NotNil(t, gotDescription)
			assert.Equal(t, body, *gotDescription)
		})
	}
}

func TestIssueUpdate_DescriptionFileConflictsWithDescription(t *testing.T) {
	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdUpdate,
		false,
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec(`42 --description inline --description-file some.md`)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "[description description-file]")
}

func TestIssueUpdate_TimeTracking(t *testing.T) {
	updatedIssue := &gitlab.Issue{IID: 42, Title: "issueTitle", WebURL: "https://gitlab.com/OWNER/REPO/-/issues/42"}

	tests := []struct {
		name        string
		cli         string
		expectMocks func(m *gitlabtesting.MockIssuesServiceInterface)
		wantOutput  []string
	}{
		{
			name: "sets the time estimate",
			cli:  "42 --estimate 2d",
			expectMocks: func(m *gitlabtesting.MockIssuesServiceInterface) {
				m.EXPECT().
					SetTimeEstimate("OWNER/REPO", int64(42), &gitlab.SetTimeEstimateOptions{Duration: new("2d")}, gomock.Any()).
					Return(&gitlab.TimeStats{}, nil, nil)
			},
			wantOutput: []string{"set time estimate to 2d"},
		},
		{
			name: "removes the time estimate",
			cli:  "42 --remove-estimate",
			expectMocks: func(m *gitlabtesting.MockIssuesServiceInterface) {
				m.EXPECT().
					ResetTimeEstimate("OWNER/REPO", int64(42), gomock.Any()).
					Return(&gitlab.TimeStats{}, nil, nil)
			},
			wantOutput: []string{"removed time estimate"},
		},
		{
			name: "adds time spent",
			cli:  "42 --spend 1h30m",
			expectMocks: func(m *gitlabtesting.MockIssuesServiceInterface) {
				m.EXPECT().
					AddSpentTime("OWNER/REPO", int64(42), &gitlab.AddSpentTimeOptions{Duration: new("1h30m")}, gomock.Any()).
					Return(&gitlab.TimeStats{}, nil, nil)
			},
			wantOutput: []string{"added 1h30m of time spent"},
		},
		{
			name: "removes time spent",
			cli:  "42 --remove-time-spent",
			expectMocks: func(m *gitlabtesting.MockIssuesServiceInterface) {
				m.EXPECT().
					ResetSpentTime("OWNER/REPO", int64(42), gomock.Any()).
					Return(&gitlab.TimeStats{}, nil, nil)
			},
			wantOutput: []string{"removed time spent"},
		},
		{
			name: "updates attributes and time tracking together",
			cli:  `42 --title "new title" --estimate 2d --spend 1h`,
			expectMocks: func(m *gitlabtesting.MockIssuesServiceInterface) {
				gomock.InOrder(
					m.EXPECT().
						UpdateIssue("OWNER/REPO", int64(42), &gitlab.UpdateIssueOptions{Title: new("new title")}).
						Return(updatedIssue, nil, nil),
					m.EXPECT().
						SetTimeEstimate("OWNER/REPO", int64(42), &gitlab.SetTimeEstimateOptions{Duration: new("2d")}, gomock.Any()).
						Return(&gitlab.TimeStats{}, nil, nil),
					m.EXPECT().
						AddSpentTime("OWNER/REPO", int64(42), &gitlab.AddSpentTimeOptions{Duration: new("1h")}, gomock.Any()).
						Return(&gitlab.TimeStats{}, nil, nil),
				)
			},
			wantOutput: []string{`updated title to "new title"`, "set time estimate to 2d", "added 1h of time spent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)
			tc.MockIssues.EXPECT().
				GetIssue("OWNER/REPO", int64(42)).
				Return(updatedIssue, nil, nil)
			tt.expectMocks(tc.MockIssues)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdUpdate,
				false,
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			out, err := exec(tt.cli)
			require.NoError(t, err)

			for _, want := range tt.wantOutput {
				assert.Contains(t, out.String(), want)
			}
		})
	}
}

func TestIssueUpdate_TimeTrackingAPIError(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	tc.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(42)).
		Return(&gitlab.Issue{IID: 42}, nil, nil)
	tc.MockIssues.EXPECT().
		AddSpentTime("OWNER/REPO", int64(42), gomock.Any(), gomock.Any()).
		Return(nil, nil, errors.New("400 Bad Request"))

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdUpdate,
		false,
		cmdtest.WithGitLabClient(tc.Client),
		cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
	)

	_, err := exec("42 --spend nonsense")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "400 Bad Request")
}

func TestIssueUpdate_TimeTrackingConflicts(t *testing.T) {
	tests := []struct {
		name    string
		cli     string
		wantErr string
	}{
		{
			name:    "estimate with remove-estimate",
			cli:     "42 --estimate 2d --remove-estimate",
			wantErr: "[estimate remove-estimate]",
		},
		{
			name:    "spend with remove-time-spent",
			cli:     "42 --spend 1h --remove-time-spent",
			wantErr: "[spend remove-time-spent]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdUpdate,
				false,
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			_, err := exec(tt.cli)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestIssueUpdate_TimeTrackingEmptyDuration(t *testing.T) {
	tests := []struct {
		name    string
		cli     string
		wantErr string
	}{
		{
			name:    "empty estimate",
			cli:     `42 --estimate ""`,
			wantErr: "--estimate can't be empty",
		},
		{
			name:    "empty spend",
			cli:     `42 --spend ""`,
			wantErr: "--spend can't be empty",
		},
		{
			name:    "whitespace-only spend",
			cli:     `42 --spend "  "`,
			wantErr: "--spend can't be empty",
		},
		{
			name:    "empty spend with an attribute update",
			cli:     `42 --title "new title" --spend ""`,
			wantErr: "--spend can't be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// No mock expectations: any API call, including UpdateIssue, fails the test.
			tc := gitlabtesting.NewTestClient(t)

			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdUpdate,
				false,
				cmdtest.WithGitLabClient(tc.Client),
				cmdtest.WithBaseRepo("OWNER", "REPO", glinstance.DefaultHostname),
			)

			_, err := exec(tt.cli)

			var flagErr *cmdutils.FlagError
			require.ErrorAs(t, err, &flagErr)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
