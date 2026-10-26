//go:build !integration

package _for

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestNewCmdFor_BranchCreationErrorIsReturned(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(7), gomock.Any()).
		Return(&gitlab.Issue{IID: 7, Title: "Fix bug"}, nil, nil)
	tc.MockBranches.EXPECT().
		CreateBranch("OWNER/REPO", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.CreateBranchOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Branch, *gitlab.Response, error) {
			assert.Equal(t, "7-fix-bug", *opt.Branch)
			return nil, nil, &gitlab.ErrorResponse{StatusCode: http.StatusForbidden, Message: "{message: 403 Forbidden}"}
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdFor, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("7 --target-branch main")

	require.EqualError(t, err, `could not create branch "7-fix-bug": 403 {message: 403 Forbidden}`)
}

func TestNewCmdFor_ExistingBranchGetsSanitizedNumberedName(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	tc.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(7), gomock.Any()).
		Return(&gitlab.Issue{IID: 7, Title: "Fix: crash in ~/.config?"}, nil, nil)
	gomock.InOrder(
		tc.MockBranches.EXPECT().
			CreateBranch("OWNER/REPO", &gitlab.CreateBranchOptions{Branch: new("7-fix-crash-in-config-"), Ref: new("main")}, gomock.Any()).
			Return(nil, nil, &gitlab.ErrorResponse{StatusCode: http.StatusBadRequest, Message: "{message: Branch already exists}"}),
		tc.MockBranches.EXPECT().
			CreateBranch("OWNER/REPO", &gitlab.CreateBranchOptions{Branch: new("7-fix-crash-in-config--1"), Ref: new("main")}, gomock.Any()).
			Return(&gitlab.Branch{Name: "7-fix-crash-in-config--1"}, nil, nil),
	)
	tc.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.CreateMergeRequestOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "7-fix-crash-in-config--1", *opt.SourceBranch)
			return &gitlab.MergeRequest{BasicMergeRequest: gitlab.BasicMergeRequest{IID: 12, SourceBranch: *opt.SourceBranch}}, nil, nil
		})

	exec := cmdtest.SetupCmdForTest(t, NewCmdFor, false, cmdtest.WithGitLabClient(tc.Client))
	_, err := exec("7 --target-branch main")

	require.NoError(t, err)
}
