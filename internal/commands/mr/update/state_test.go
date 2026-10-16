//go:build !integration

package update

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestUpdateMergeRequest_State(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      string
		state     *string
		ambiguous bool
	}{
		{name: "opened", args: "feature --state opened --title changed", state: new("opened")},
		{name: "closed", args: "feature --state closed --title changed", state: new("closed")},
		{name: "merged", args: "feature --state merged --title changed", state: new("merged")},
		{name: "locked", args: "feature --state locked --title changed", state: new("locked")},
		{name: "default retains ambiguity", args: "feature --title changed", ambiguous: true},
		{name: "any retains ambiguity", args: "feature --state any --title changed", ambiguous: true},
		{name: "current branch", args: "--state opened --title changed", state: new("opened")},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)
			mrs := []*gitlab.BasicMergeRequest{{IID: 1, Author: &gitlab.BasicUser{Username: "author"}}}
			if tt.ambiguous {
				mrs = append(mrs, &gitlab.BasicMergeRequest{IID: 2, Author: &gitlab.BasicUser{Username: "author"}})
			}
			calls := []any{tc.MockMergeRequests.EXPECT().ListProjectMergeRequests("OWNER/REPO", gomock.Any()).DoAndReturn(
				func(_ any, opts *gitlab.ListProjectMergeRequestsOptions, _ ...gitlab.RequestOptionFunc) ([]*gitlab.BasicMergeRequest, *gitlab.Response, error) {
					assert.Equal(t, tt.state, opts.State)
					assert.Equal(t, "feature", *opts.SourceBranch)
					return mrs, nil, nil
				},
			).Call}
			if !tt.ambiguous {
				mr := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{IID: 1, Title: "original", State: "opened"}}
				calls = append(calls,
					tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).Return(mr, nil, nil).Call,
					tc.MockMergeRequests.EXPECT().UpdateMergeRequest("OWNER/REPO", int64(1), gomock.Any()).DoAndReturn(
						func(_ any, _ int64, opts *gitlab.UpdateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
							assert.Equal(t, "changed", *opts.Title)
							mr.Title = *opts.Title
							return mr, nil, nil
						},
					).Call,
				)
			}
			gomock.InOrder(calls...)
			exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false, cmdtest.WithGitLabClient(tc.Client), cmdtest.WithBranch("feature"))

			out, err := exec(tt.args)

			if tt.ambiguous {
				require.ErrorContains(t, err, "merge request ID number required")
				return
			}
			require.NoError(t, err)
			assert.Contains(t, out.String(), "changed")
		})
	}
}

func TestUpdateMergeRequest_StateInvalid(t *testing.T) {
	tc := gitlabtesting.NewTestClient(t)
	exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false, cmdtest.WithGitLabClient(tc.Client))

	_, err := exec("feature --state invalid --title changed")

	require.ErrorContains(t, err, "must be one of")
}

func TestUpdateMergeRequest_StateWithIDOrURL(t *testing.T) {
	for _, target := range []string{"1", "https://gitlab.com/OWNER/REPO/-/merge_requests/1"} {
		t.Run(target, func(t *testing.T) {
			tc := gitlabtesting.NewTestClient(t)
			mr := &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{IID: 1, Title: "original", State: "opened"}}
			gomock.InOrder(
				tc.MockMergeRequests.EXPECT().GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).Return(mr, nil, nil).Call,
				tc.MockMergeRequests.EXPECT().UpdateMergeRequest("OWNER/REPO", int64(1), gomock.Any()).Return(mr, nil, nil).Call,
			)
			exec := cmdtest.SetupCmdForTest(t, NewCmdUpdate, false, cmdtest.WithGitLabClient(tc.Client))

			_, err := exec(target + " --state closed --title changed")

			require.NoError(t, err)
		})
	}
}
