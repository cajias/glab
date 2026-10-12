//go:build !integration

package add

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go/v3"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/v3/testing"

	"gitlab.com/gitlab-org/cli/internal/api"
	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func Test_AddSSHKey(t *testing.T) {
	type testCase struct {
		Name        string
		ExpectedMsg []string
		wantErr     bool
		cli         string
		stdin       string
		wantStderr  string
		setupMock   func(tc *gitlabtesting.TestClient)
	}

	validSSHKey := &gitlab.SSHKey{
		ID:        1,
		Key:       "ssh-ed25519 example",
		Title:     "mysshkey",
		CreatedAt: new(time.Time{}),
	}

	testCases := []testCase{
		// Success cases
		{
			Name:        "Add SSH key from file",
			ExpectedMsg: []string{"New SSH public key added to your account.\n"},
			cli:         "testdata/testkey.key --title mysshkey --usage-type 'auth'",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(validSSHKey, nil, nil)
			},
		},
		{
			Name:        "Add SSH key from stdin",
			ExpectedMsg: []string{"New SSH public key added to your account.\n\n"},
			cli:         "--title mysshkey",
			stdin:       "ssh-ed25519 example",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(validSSHKey, nil, nil)
			},
		},

		// // File errors
		{
			Name:       "Add SSH key with missing file",
			cli:        "nonexistent.key --title somekey",
			wantErr:    true,
			wantStderr: "nonexistent.key",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},
		{
			Name:       "Add SSH key with long invalid expiry",
			cli:        "testdata/testkey.key --title somekey --expires-at not-a-valid-timestamp-that-is-long",
			wantErr:    true,
			wantStderr: "cannot parse",
			setupMock:  func(tc *gitlabtesting.TestClient) {},
		},

		// // API errors
		{
			Name:       "Add SSH key with invalid key format",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "Key is invalid",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("Key is invalid"))
			},
		},
		{
			Name:       "Add SSH key with duplicate key error",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "Key has already been taken",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("Key has already been taken"))
			},
		},
		{
			Name:       "Add SSH key with unauthorized error",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "401",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("401 Unauthorized"))
			},
		},
		{
			Name:       "Add SSH key with server error",
			cli:        "testdata/testkey.key --title somekey",
			wantErr:    true,
			wantStderr: "",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("500 Internal Server Error"))
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.Name, func(t *testing.T) {
			// GIVEN
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdAdd,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			// WHEN
			out, err := exec(tc.cli)

			// THEN
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantStderr)
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.ExpectedMsg {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
		})
	}
}

func Test_AddSSHKey_Replace(t *testing.T) {
	const (
		otherKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIC/7QmKq64NnEioQCTCeMaf6qdoqCmba2K0/Fi7P2kPf other"
		sameKey  = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIPxBTowwahEEmAu2wxFivSEyic4mIM/dJnC622v0/txu old-comment"
	)

	existingKeys := []*gitlab.SSHKey{
		{ID: 10, Title: "other", Key: otherKey},
		{ID: 11, Title: "expired", Key: sameKey},
	}
	addedKey := &gitlab.SSHKey{ID: 12, Title: "renewed", Key: sameKey}

	type testCase struct {
		name        string
		cli         string
		setupMock   func(tc *gitlabtesting.TestClient)
		wantErr     string
		wantFlagErr string
		wantOut     []string
		notInOut    string
	}

	testCases := []testCase{
		{
			name: "deletes the key with the same fingerprint even when its comment differs",
			cli:  "testdata/ed25519.pub --title renewed --expires-at 2027-01-01 --replace",
			setupMock: func(tc *gitlabtesting.TestClient) {
				gomock.InOrder(
					tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).Return(existingKeys, &gitlab.Response{}, nil),
					tc.MockUsers.EXPECT().DeleteSSHKey(int64(11), gomock.Any()).Return(nil, nil),
					tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).DoAndReturn(
						func(opt *gitlab.AddSSHKeyOptions, _ ...gitlab.RequestOptionFunc) (*gitlab.SSHKey, *gitlab.Response, error) {
							assert.Equal(t, "renewed", *opt.Title)
							assert.Equal(t, "2027-01-01", opt.ExpiresAt.String())
							return addedKey, nil, nil
						}),
				)
			},
			wantOut: []string{
				`Deleted existing SSH key "expired" (ID 11) with the same fingerprint.`,
				"New SSH public key added to your account.",
			},
		},
		{
			name: "adds the key when no existing key matches",
			cli:  "testdata/ed25519.pub --title renewed --replace",
			setupMock: func(tc *gitlabtesting.TestClient) {
				gomock.InOrder(
					tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).Return(existingKeys[:1], &gitlab.Response{}, nil),
					tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(addedKey, nil, nil),
				)
			},
			wantOut:  []string{"New SSH public key added to your account."},
			notInOut: "Deleted existing SSH key",
		},
		{
			name: "searches every page of existing keys",
			cli:  "testdata/ed25519.pub --title renewed --replace",
			setupMock: func(tc *gitlabtesting.TestClient) {
				gomock.InOrder(
					tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).Return(existingKeys[:1], &gitlab.Response{NextPage: 2}, nil),
					tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any(), gomock.Any()).Return(existingKeys[1:], &gitlab.Response{}, nil),
					tc.MockUsers.EXPECT().DeleteSSHKey(int64(11), gomock.Any()).Return(nil, nil),
					tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(addedKey, nil, nil),
				)
			},
			wantOut: []string{"(ID 11)", "New SSH public key added to your account."},
		},
		{
			name:        "does not delete the existing key when the expiration date is invalid",
			cli:         "testdata/ed25519.pub --title renewed --expires-at 2027-13-45 --replace",
			setupMock:   func(tc *gitlabtesting.TestClient) {},
			wantFlagErr: `invalid --expires-at value "2027-13-45"`,
		},
		{
			name:        "does not delete the existing key when the usage type is invalid",
			cli:         "testdata/ed25519.pub --title renewed --usage-type everything --replace",
			setupMock:   func(tc *gitlabtesting.TestClient) {},
			wantFlagErr: "must be one of",
		},
		{
			name:      "fails on a key that cannot be parsed",
			cli:       "testdata/testkey.key --title renewed --replace",
			setupMock: func(tc *gitlabtesting.TestClient) {},
			wantErr:   "failed to parse SSH public key",
		},
		{
			name: "fails without adding when listing keys fails",
			cli:  "testdata/ed25519.pub --title renewed --replace",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("500 Internal Server Error"))
			},
			wantErr: "failed to list existing SSH keys",
		},
		{
			name: "fails without adding when deleting the existing key fails",
			cli:  "testdata/ed25519.pub --title renewed --replace",
			setupMock: func(tc *gitlabtesting.TestClient) {
				gomock.InOrder(
					tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).Return(existingKeys, &gitlab.Response{}, nil),
					tc.MockUsers.EXPECT().DeleteSSHKey(int64(11), gomock.Any()).Return(nil, errors.New("403 Forbidden")),
				)
			},
			wantErr: "failed to delete existing SSH key with ID 11",
		},
		{
			name: "reports the deleted key when adding fails",
			cli:  "testdata/ed25519.pub --title renewed --replace",
			setupMock: func(tc *gitlabtesting.TestClient) {
				gomock.InOrder(
					tc.MockUsers.EXPECT().ListSSHKeys(gomock.Any(), gomock.Any()).Return(existingKeys, &gitlab.Response{}, nil),
					tc.MockUsers.EXPECT().DeleteSSHKey(int64(11), gomock.Any()).Return(nil, nil),
					tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("400 Bad Request")),
				)
			},
			wantErr: "The existing key with ID 11 was already deleted",
		},
		{
			name: "does not look up existing keys without the flag",
			cli:  "testdata/ed25519.pub --title renewed",
			setupMock: func(tc *gitlabtesting.TestClient) {
				tc.MockUsers.EXPECT().AddSSHKey(gomock.Any(), gomock.Any()).Return(nil, nil, errors.New("fingerprint_sha256: [has already been taken]"))
			},
			wantErr: "failed to add new SSH public key.",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			testClient := gitlabtesting.NewTestClient(t)
			tc.setupMock(testClient)
			exec := cmdtest.SetupCmdForTest(
				t,
				NewCmdAdd,
				false,
				cmdtest.WithApiClient(cmdtest.NewTestApiClient(t, nil, "", "", api.WithGitLabClient(testClient.Client))),
			)

			out, err := exec(tc.cli)

			if tc.wantFlagErr != "" {
				require.ErrorContains(t, err, tc.wantFlagErr)
				return
			}
			if tc.wantErr != "" {
				var exitErr *cmdutils.ExitError
				require.ErrorAs(t, err, &exitErr)
				assert.Contains(t, exitErr.Details, tc.wantErr)
				return
			}
			require.NoError(t, err)
			for _, msg := range tc.wantOut {
				assert.Contains(t, out.OutBuf.String(), msg)
			}
			if tc.notInOut != "" {
				assert.NotContains(t, out.OutBuf.String(), tc.notInOut)
			}
		})
	}
}
