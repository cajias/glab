//go:build !integration

package delete

import (
	"fmt"
	"testing"

	"git.sr.ht/~timofurrer/ugh"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/iostreams"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_PackagesDelete(t *testing.T) {
	t.Parallel()

	const repoName = "OWNER/REPO"

	type testCase struct {
		name           string
		cli            string
		expectedMsg    []string
		wantErr        bool
		wantErrMsg     string
		wantErrMsgPart string
		setupMock      func(tc *gitlabtesting.TestClient)
	}

	packages := []*gitlab.Package{
		{ID: 1, Name: "example", Version: "1.0.0", PackageType: "generic"},
		{ID: 2, Name: "example", Version: "2.0.0", PackageType: "generic"},
		{ID: 3, Name: "example", Version: "1.0.0", PackageType: "maven"},
	}
	expectList := func(tc *gitlabtesting.TestClient) {
		tc.MockPackages.EXPECT().
			ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
			Return(packages, &gitlab.Response{}, nil)
	}
	expectDelete := func(tc *gitlabtesting.TestClient, id int64) {
		tc.MockPackages.EXPECT().
			DeleteProjectPackage(repoName, id, gomock.Any()).
			Return(nil, nil)
	}
	noMock := func(tc *gitlabtesting.TestClient) {}

	testCases := []testCase{
		{
			name:        "Delete package via id arg",
			cli:         "1 -y",
			expectedMsg: []string{"Deleting package repo=OWNER/REPO packageID=1", "Package 1 deleted."},
			setupMock:   func(tc *gitlabtesting.TestClient) { expectDelete(tc, 1) },
		},
		{
			name:        "Delete package via id flag",
			cli:         "--id 1 -y",
			expectedMsg: []string{"Deleting package repo=OWNER/REPO packageID=1", "Package 1 deleted."},
			setupMock:   func(tc *gitlabtesting.TestClient) { expectDelete(tc, 1) },
		},
		{
			name:        "Delete package via name and version defaults to generic type",
			cli:         "--name example --version 2.0.0 -y",
			expectedMsg: []string{"Deleting package repo=OWNER/REPO packageID=2", "Package 2 deleted."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				expectList(tc)
				expectDelete(tc, 2)
			},
		},
		{
			name:        "Delete package via name, version and explicit type",
			cli:         "--name example --version 1.0.0 --package-type maven -y",
			expectedMsg: []string{"Deleting package repo=OWNER/REPO packageID=3", "Package 3 deleted."},
			setupMock: func(tc *gitlabtesting.TestClient) {
				expectList(tc)
				expectDelete(tc, 3)
			},
		},
		{
			name:       "Delete package via name but no matching version",
			cli:        "--name example --version 9.9.9 -y",
			setupMock:  expectList,
			wantErr:    true,
			wantErrMsg: "couldn't locate package \"example\" with package-type \"generic\" and version \"9.9.9\"",
		},
		{
			name:       "Delete package via name has no match for an explicit package type",
			cli:        "--name example --version 1.0.0 --package-type npm -y",
			setupMock:  expectList,
			wantErr:    true,
			wantErrMsg: "couldn't locate package \"example\" with package-type \"npm\" and version \"1.0.0\"",
		},
		{
			name: "Delete package but API errors",
			cli:  "1 -y",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPackages.EXPECT().
					DeleteProjectPackage(repoName, int64(1), gomock.Any()).
					Return(nil, fmt.Errorf("DELETE https://gitlab.com/api/v4/projects/OWNER%%2FREPO/packages/1: 403"))
			},
			wantErr:    true,
			wantErrMsg: "failed to delete package: DELETE https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages/1: 403",
		},
		{
			name:       "Delete package with non-integer ID",
			cli:        "abc -y",
			setupMock:  noMock,
			wantErr:    true,
			wantErrMsg: "package ID must be an integer: abc",
		},
		{
			name:       "No identifier given",
			cli:        "-y",
			setupMock:  noMock,
			wantErr:    true,
			wantErrMsg: "provide a package ID argument, or the --id or --name flag",
		},
		{
			name:       "Conflicting id argument and --id flag is rejected",
			cli:        "1 --id 2 -y",
			setupMock:  noMock,
			wantErr:    true,
			wantErrMsg: `the package ID argument "1" cannot be combined with --id or --name`,
		},
		{
			name:       "Conflicting id argument and --name flag is rejected",
			cli:        "1 --name example --version 1.0.0 -y",
			setupMock:  noMock,
			wantErr:    true,
			wantErrMsg: `the package ID argument "1" cannot be combined with --id or --name`,
		},
		{
			name:           "--id and --name are mutually exclusive",
			cli:            "--id 1 --name example --version 1.0.0 -y",
			setupMock:      noMock,
			wantErr:        true,
			wantErrMsgPart: "if any flags in the group [id name] are set none of the others can be; [id name] were all set",
		},
		{
			name:           "--name requires --version",
			cli:            "--name example -y",
			setupMock:      noMock,
			wantErr:        true,
			wantErrMsgPart: "if any flags in the group [name version] are set they must all be set; missing [version]",
		},
		{
			name:           "--version requires --name",
			cli:            "--version 1.0.0 -y",
			setupMock:      noMock,
			wantErr:        true,
			wantErrMsgPart: "if any flags in the group [name version] are set they must all be set; missing [name]",
		},
		{
			name:           "--version cannot be combined with an id argument",
			cli:            "1 --version 1.0.0 -y",
			setupMock:      noMock,
			wantErr:        true,
			wantErrMsgPart: "if any flags in the group [name version] are set they must all be set; missing [name]",
		},
		{
			name:       "--package-type cannot be combined with an id",
			cli:        "1 --package-type maven -y",
			setupMock:  noMock,
			wantErr:    true,
			wantErrMsg: "--package-type can only be used with --name",
		},
		{
			name:           "Invalid --package-type is rejected",
			cli:            "--name example --version 1.0.0 --package-type bogus -y",
			setupMock:      noMock,
			wantErr:        true,
			wantErrMsgPart: `invalid argument "bogus" for "--package-type" flag: must be one of`,
		},
		{
			name:       "Delete package non-interactively without -y",
			cli:        "1",
			setupMock:  noMock,
			wantErr:    true,
			wantErrMsg: "--yes or -y flag is required when not running interactively",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmd,
				false,
				cmdtest.WithGitLabClient(testClient.Client),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				if tc.wantErrMsgPart != "" {
					assert.Contains(t, err.Error(), tc.wantErrMsgPart)
				} else {
					assert.Equal(t, tc.wantErrMsg, err.Error())
				}
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.expectedMsg {
				assert.Contains(t, out.String(), msg)
			}
		})
	}
}

func Test_PackagesDelete_CancelPrompt(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)
	// The package must never be deleted when the user declines the prompt.
	testClient.MockPackages.EXPECT().
		DeleteProjectPackage(gomock.Any(), gomock.Any(), gomock.Any()).
		Times(0)

	c := ugh.New(t)
	c.Expect(ugh.Confirm("Are you ABSOLUTELY SURE you wish to delete this package 1?")).
		Do(ugh.Reject)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmd,
		true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithConsole(t, c),
	)

	_, err := exec("1")
	require.ErrorIs(t, err, iostreams.ErrUserCancelled)
}
