//go:build !integration

package create

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

func TestReleaseCreate_SemverError(t *testing.T) {
	t.Parallel()
	for _, existing := range []bool{false, true} {
		operation := "create"
		if existing {
			operation = "update"
		}
		for _, tt := range []struct {
			name    string
			status  int
			message string
			hint    bool
		}{
			{name: "catalog version rejected", status: http.StatusUnprocessableEntity, message: "Semver must follow semantic version", hint: true},
			{name: "catalog version rejected with mixed case", status: http.StatusUnprocessableEntity, message: "SemVer must follow semantic version", hint: true},
			{name: "catalog version rejected with lower case", status: http.StatusUnprocessableEntity, message: "semver must follow semantic version", hint: true},
			{name: "other validation rejected", status: http.StatusUnprocessableEntity, message: "Name is too long"},
			{name: "server failed", status: http.StatusInternalServerError, message: "Semver must follow semantic version"},
		} {
			t.Run(operation+"/"+tt.name, func(t *testing.T) {
				t.Parallel()
				tc := gitlabtesting.NewTestClient(t)
				apiErr := &gitlab.ErrorResponse{StatusCode: tt.status, Message: tt.message}
				tc.MockTags.EXPECT().GetTag("OWNER/REPO", "1.04", gomock.Any()).Return(&gitlab.Tag{Name: "1.04"}, nil, nil)
				if existing {
					tc.MockReleases.EXPECT().GetRelease("OWNER/REPO", "1.04", gomock.Any()).Return(&gitlab.Release{TagName: "1.04"}, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil)
					tc.MockReleases.EXPECT().UpdateRelease("OWNER/REPO", "1.04", gomock.Any()).Return(nil, nil, apiErr)
				} else {
					tc.MockReleases.EXPECT().GetRelease("OWNER/REPO", "1.04", gomock.Any()).Return(nil, &gitlab.Response{Response: &http.Response{StatusCode: http.StatusNotFound}}, gitlab.ErrNotFound)
					tc.MockReleases.EXPECT().CreateRelease("OWNER/REPO", gomock.Any()).Return(nil, nil, apiErr)
				}
				exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false, cmdtest.WithGitLabClient(tc.Client))

				_, err := exec("1.04 --notes release")

				require.Error(t, err)
				require.ErrorIs(t, err, apiErr)
				assert.Contains(t, err.Error(), tt.message)
				if tt.hint {
					assert.Contains(t, err.Error(), "MAJOR.MINOR.PATCH")
					assert.Contains(t, err.Error(), "Only a lowercase v prefix is allowed")
					assert.Contains(t, err.Error(), "https://docs.gitlab.com/ci/components/#semantic-versioning")
				} else {
					assert.NotContains(t, err.Error(), "semantic-versioning")
				}
			})
		}
	}
}
