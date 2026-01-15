//go:build !integration

package remove

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_SecurefileRemove(t *testing.T) {
	type testCase struct {
		name        string
		cli         string
		expectedMsg []string
		wantErr     bool
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	testCases := []testCase{
		{
			name:        "Remove a secure file",
			cli:         "1 -y",
			expectedMsg: []string{"• Deleting secure file repo=OWNER/REPO fileID=1", "✓ Secure file 1 deleted."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile("OWNER/REPO", int64(1)).
					Return(nil, nil)
			},
		},
		{
			name: "Remove a secure file but API errors",
			cli:  "1 -y",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockSecureFiles.EXPECT().
					RemoveSecureFile("OWNER/REPO", int64(1)).
					Return(nil, fmt.Errorf("DELETE https://gitlab.com/api/v4/projects/OWNER%%2FREPO/secure_files/1: 400"))
			},
			wantErr:    true,
			wantStderr: "Error removing secure file: DELETE https://gitlab.com/api/v4/projects/OWNER%2FREPO/secure_files/1: 400",
		},
		{
			name:       "Remove a secure file with invalid file ID",
			cli:        "abc -y",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantStderr: "Secure file ID must be an integer: abc",
		},
		{
			name:       "Remove a secure file without force delete when not running interactively",
			cli:        "1",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
			wantErr:    true,
			wantStderr: "--yes or -y flag is required when not running interactively.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdRemove,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantStderr, err.Error())
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.String(), msg)
			}
		})
	}
}
