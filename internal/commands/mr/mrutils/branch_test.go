//go:build !integration

package mrutils

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"
)

func TestCreateUniqueBranch(t *testing.T) {
	t.Parallel()

	branchExists := &gitlab.ErrorResponse{StatusCode: http.StatusBadRequest, Message: "{message: Branch already exists}"}

	tests := []struct {
		name         string
		branchErrors []error
		wantBranches []string
		wantBranch   string
		wantErr      string
	}{
		{
			name:         "creates the branch",
			branchErrors: []error{nil},
			wantBranches: []string{"7-fix-bug"},
			wantBranch:   "7-fix-bug",
		},
		{
			name:         "numbers the name while the branch exists",
			branchErrors: []error{branchExists, branchExists, nil},
			wantBranches: []string{"7-fix-bug", "7-fix-bug-1", "7-fix-bug-2"},
			wantBranch:   "7-fix-bug-2",
		},
		{
			name:         "numbers the name while a branch below it exists",
			branchErrors: []error{&gitlab.ErrorResponse{StatusCode: http.StatusBadRequest, Message: "{message: Failed to create branch '7-fix-bug': Branch name conflicts with existing branch hierarchy.}"}, nil},
			wantBranches: []string{"7-fix-bug", "7-fix-bug-1"},
			wantBranch:   "7-fix-bug-1",
		},
		{
			name:         "returns an invalid name error",
			branchErrors: []error{&gitlab.ErrorResponse{StatusCode: http.StatusBadRequest, Message: "{message: Branch name is invalid}"}},
			wantBranches: []string{"7-fix-bug"},
			wantErr:      `could not create branch "7-fix-bug": 400 {message: Branch name is invalid}`,
		},
		{
			name:         "returns an existing branch error with another status",
			branchErrors: []error{&gitlab.ErrorResponse{StatusCode: http.StatusConflict, Message: "{message: Branch already exists}"}},
			wantBranches: []string{"7-fix-bug"},
			wantErr:      `could not create branch "7-fix-bug": 409 {message: Branch already exists}`,
		},
		{
			name:         "returns a permission error",
			branchErrors: []error{&gitlab.ErrorResponse{StatusCode: http.StatusForbidden, Message: "{message: 403 Forbidden}"}},
			wantBranches: []string{"7-fix-bug"},
			wantErr:      `could not create branch "7-fix-bug": 403 {message: 403 Forbidden}`,
		},
		{
			name:         "returns an error without a response",
			branchErrors: []error{errors.New("connection refused")},
			wantBranches: []string{"7-fix-bug"},
			wantErr:      `could not create branch "7-fix-bug": connection refused`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tc := gitlabtesting.NewTestClient(t)
			var branches []string
			tc.MockBranches.EXPECT().
				CreateBranch("OWNER/REPO", gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ any, opt *gitlab.CreateBranchOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Branch, *gitlab.Response, error) {
					assert.Equal(t, "main", *opt.Ref)
					branches = append(branches, *opt.Branch)
					if err := tt.branchErrors[len(branches)-1]; err != nil {
						return nil, nil, err
					}
					return &gitlab.Branch{Name: *opt.Branch}, nil, nil
				}).
				Times(len(tt.branchErrors))

			got, err := CreateUniqueBranch(t.Context(), tc.Client, "OWNER/REPO", "7-fix-bug", "main")

			assert.Equal(t, tt.wantBranches, branches)
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.wantBranch, got)
		})
	}
}

func TestCreateUniqueBranch_StopsAfterMaxBranchSuffix(t *testing.T) {
	t.Parallel()

	tc := gitlabtesting.NewTestClient(t)
	var branches []string
	tc.MockBranches.EXPECT().
		CreateBranch("OWNER/REPO", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ any, opt *gitlab.CreateBranchOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.Branch, *gitlab.Response, error) {
			branches = append(branches, *opt.Branch)
			return nil, nil, &gitlab.ErrorResponse{StatusCode: http.StatusBadRequest, Message: "{message: Branch already exists}"}
		}).
		Times(maxBranchSuffix + 1)

	got, err := CreateUniqueBranch(t.Context(), tc.Client, "OWNER/REPO", "7-fix-bug", "main")

	require.EqualError(t, err, `could not create branch "7-fix-bug-100": 400 {message: Branch already exists}`)
	assert.Empty(t, got)
	require.Len(t, branches, maxBranchSuffix+1)
	assert.Equal(t, "7-fix-bug", branches[0])
	assert.Equal(t, "7-fix-bug-1", branches[1])
	assert.Equal(t, "7-fix-bug-100", branches[maxBranchSuffix])
}
