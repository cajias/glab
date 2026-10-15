//go:build !integration

package helpers

import (
	"fmt"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"
)

const repoName = "OWNER/REPO"

func Test_GetPackageByName(t *testing.T) {
	type testCase struct {
		name           string
		packageName    string
		packageType    string
		packageVersion string
		wantID         int64
		wantErr        bool
		wantErrMsg     string
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

	testCases := []testCase{
		{
			name:           "package found by name, type and version",
			packageName:    "example",
			packageType:    "generic",
			packageVersion: "2.0.0",
			wantID:         2,
			setupMock:      expectList,
		},
		{
			name:           "same name and version but different type is distinguished",
			packageName:    "example",
			packageType:    "maven",
			packageVersion: "1.0.0",
			wantID:         3,
			setupMock:      expectList,
		},
		{
			name:           "filters are sent to the server",
			packageName:    "example",
			packageType:    "maven",
			packageVersion: "1.0.0",
			wantID:         3,
			setupMock: func(tc *gitlabtesting.TestClient) {
				name, pkgType, version := "example", "maven", "1.0.0"
				tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, &gitlab.ListProjectPackagesOptions{
						ListOptions:    gitlab.ListOptions{Page: 1, PerPage: 100},
						PackageName:    &name,
						PackageType:    &pkgType,
						PackageVersion: &version,
					}, gomock.Any()).
					Return(packages[2:], &gitlab.Response{}, nil)
			},
		},
		{
			name:           "server-side name substring match is not treated as a match",
			packageName:    "exam",
			packageType:    "generic",
			packageVersion: "1.0.0",
			wantErr:        true,
			wantErrMsg:     "couldn't locate package \"exam\" with package-type \"generic\" and version \"1.0.0\"",
			setupMock:      expectList,
		},
		{
			name:           "multiple matching packages is ambiguous",
			packageName:    "example",
			packageType:    "generic",
			packageVersion: "1.0.0",
			wantErr:        true,
			wantErrMsg:     "found 2 packages named \"example\" with package-type \"generic\" and version \"1.0.0\" (IDs: 1, 4)",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(append(slices.Clone(packages), &gitlab.Package{ID: 4, Name: "example", Version: "1.0.0", PackageType: "generic"}),
						&gitlab.Response{}, nil)
			},
		},
		{
			name:           "version does not match",
			packageName:    "example",
			packageType:    "generic",
			packageVersion: "9.9.9",
			wantErr:        true,
			wantErrMsg:     "couldn't locate package \"example\" with package-type \"generic\" and version \"9.9.9\"",
			setupMock:      expectList,
		},
		{
			name:           "name does not match",
			packageName:    "missing",
			packageType:    "generic",
			packageVersion: "1.0.0",
			wantErr:        true,
			wantErrMsg:     "couldn't locate package \"missing\" with package-type \"generic\" and version \"1.0.0\"",
			setupMock:      expectList,
		},
		{
			name:           "package found on a later page",
			packageName:    "example",
			packageType:    "generic",
			packageVersion: "2.0.0",
			wantID:         2,
			setupMock: func(tc *gitlabtesting.TestClient) {
				firstPage := tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(packages[:1], &gitlab.Response{NextPage: 2}, nil)
				secondPage := tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(packages[1:], &gitlab.Response{}, nil)
				gomock.InOrder(firstPage, secondPage)
			},
		},
		{
			name:           "package not found on any page",
			packageName:    "example",
			packageType:    "generic",
			packageVersion: "9.9.9",
			wantErr:        true,
			wantErrMsg:     "couldn't locate package \"example\" with package-type \"generic\" and version \"9.9.9\"",
			setupMock: func(tc *gitlabtesting.TestClient) {
				firstPage := tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(packages[:1], &gitlab.Response{NextPage: 2}, nil)
				secondPage := tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(packages[1:], &gitlab.Response{}, nil)
				gomock.InOrder(firstPage, secondPage)
			},
		},
		{
			name:           "gitlab API error on a later page",
			packageName:    "example",
			packageType:    "generic",
			packageVersion: "9.9.9",
			wantErr:        true,
			wantErrMsg: "error fetching packages: " +
				"GET https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages: 500",
			setupMock: func(tc *gitlabtesting.TestClient) {
				firstPage := tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(packages[:1], &gitlab.Response{NextPage: 2}, nil)
				secondPage := tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(nil, &gitlab.Response{}, fmt.Errorf(
						"GET https://gitlab.com/api/v4/projects/OWNER%%2FREPO/packages: 500",
					))
				gomock.InOrder(firstPage, secondPage)
			},
		},
		{
			name:           "gitlab API error",
			packageName:    "example",
			packageType:    "generic",
			packageVersion: "1.0.0",
			wantErr:        true,
			wantErrMsg: "error fetching packages: " +
				"GET https://gitlab.com/api/v4/projects/OWNER%2FREPO/packages: 500",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockPackages.EXPECT().
					ListProjectPackages(repoName, gomock.Any(), gomock.Any()).
					Return(nil, &gitlab.Response{}, fmt.Errorf(
						"GET https://gitlab.com/api/v4/projects/OWNER%%2FREPO/packages: 500",
					))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)

			// WHEN
			pkg, err := GetPackageByName(t.Context(), testClient.Client, tc.packageName, tc.packageType, tc.packageVersion, repoName)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Equal(t, tc.wantErrMsg, err.Error())
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantID, pkg.ID)
		})
	}
}

func Test_PackageTypes(t *testing.T) {
	assert.Contains(t, PackageTypes, DefaultPackageType)
}
