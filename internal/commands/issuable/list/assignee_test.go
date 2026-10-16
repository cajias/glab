//go:build !integration

package list

import (
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
	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestIssueList_AssigneeSentinels(t *testing.T) {
	for _, scope := range []string{"project", "group", "epic"} {
		t.Run(scope, func(t *testing.T) {
			for _, tt := range []struct {
				assignee string
				id       *gitlab.AssigneeIDValue
				output   string
			}{
				{assignee: "@none", id: gitlab.AssigneeID(gitlab.UserIDNone), output: "1\n"},
				{assignee: "@any", id: gitlab.AssigneeID(gitlab.UserIDAny), output: "2\n"},
				{output: "1\n2\n"},
			} {
				t.Run(tt.assignee, func(t *testing.T) {
					tc := gitlabtesting.NewTestClient(t)
					issues := []*gitlab.Issue{
						{IID: 1, State: "opened"},
						{IID: 2, State: "opened", Assignees: []*gitlab.IssueAssignee{{ID: 10}}},
					}
					matching := issues
					if tt.assignee == "@none" {
						matching = issues[:1]
					} else if tt.assignee == "@any" {
						matching = issues[1:]
					}
					args := "--output-format ids"
					switch scope {
					case "project":
						tc.MockIssues.EXPECT().ListProjectIssues("OWNER/REPO", gomock.Any()).DoAndReturn(
							func(_ any, opts *gitlab.ListProjectIssuesOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
								assert.Equal(t, tt.id, opts.AssigneeID)
								return matching, nil, nil
							},
						)
					case "group":
						args += " --group group"
						tc.MockIssues.EXPECT().ListGroupIssues("group", gomock.Any()).DoAndReturn(
							func(_ any, opts *gitlab.ListGroupIssuesOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.Issue, *gitlab.Response, error) {
								assert.Equal(t, tt.id, opts.AssigneeID)
								return matching, nil, nil
							},
						)
					case "epic":
						args += " --group group --epic 42"
						tc.MockEpicIssues.EXPECT().ListEpicIssues("group", int64(42), gomock.Any()).Return(issues, &gitlab.Response{}, nil)
					}
					if tt.assignee != "" {
						args += " --assignee " + tt.assignee
					}
					client, err := api.NewClient(func(*http.Client) (gitlab.AuthSource, error) {
						return gitlab.AccessTokenAuthSource{Token: "test-token"}, nil
					}, api.WithGitLabClient(tc.Client))
					require.NoError(t, err)
					exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
						return NewCmdList(f, nil, issuable.TypeIssue)
					}, true, cmdtest.WithApiClient(client))

					out, err := exec(args)

					require.NoError(t, err)
					assert.Equal(t, tt.output, out.String())
				})
			}
		})
	}
}
