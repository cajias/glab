//go:build !integration

package verify

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlab_testing "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

type MockCommandExecutor struct {
	output string
	err    bool
}

func (m *MockCommandExecutor) CombinedOutput() ([]byte, error) {
	if !m.err {
		return []byte(m.output), nil
	} else {
		return []byte("Stdout,Stderr"), errors.New("Exit code 1")
	}
}

func mocks(t *testing.T, tc *gitlab_testing.TestClient) {
	t.Helper()
	tc.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any(), gomock.Any()).
		Return(&gitlab.Project{
			PathWithNamespace: "OWNER/REPO",
		}, &gitlab.Response{}, nil).
		Times(1)

	tc.MockAttestations.EXPECT().
		ListAttestations("OWNER/REPO", "f2d4bc357309c633154f1e94c6fda3583ae429f6adc882d4d9006380ea3a79da").
		Return([]*gitlab.Attestation{
			{
				ID:            1,
				IID:           1,
				CreatedAt:     gitlab.Ptr(time.Now().Add(-24 * time.Hour)),
				UpdatedAt:     gitlab.Ptr(time.Now().Add(-24 * time.Hour)),
				ExpireAt:      gitlab.Ptr(time.Now().Add(-24 * time.Hour)),
				ProjectID:     1,
				BuildID:       1,
				Status:        "success",
				PredicateKind: "provenance",
				PredicateType: "https://slsa.dev/provenance/v1",
				SubjectDigest: "76c34666f719ef14bd2b124a7db51e9c05e4db2e12a84800296d559064eebe2c",
				DownloadURL:   "https://gitlab.example.com/api/v4/projects/1/attestations/1/download",
			},
		}, &gitlab.Response{}, nil).
		Times(1)

	attestation, err := os.ReadFile("testdata/attestationDownload.json")
	assert.Nil(t, err)

	tc.MockAttestations.EXPECT().
		DownloadAttestation("OWNER/REPO", int64(1)).
		Return(attestation, nil, nil).
		Times(1)
}

func Test_AttestationVerify(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlab_testing.NewTestClient(t)
	opts := []cmdtest.FactoryOption{cmdtest.WithGitLabClient(tc.Client)}
	exec := cmdtest.SetupCmdForTest(t, NewCmdVerify, false, opts...)

	mocks(t, tc)

	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()
	lookPath = func(path string) (string, error) {
		return "/usr/bin/cosign", nil
	}

	origShellCommandFunc := execCommand
	defer func() { execCommand = origShellCommandFunc }()

	shellCommandCalled := false
	execCommand = func(name string, args ...string) commandExecutor {
		shellCommandCalled = true
		assert.Contains(t, name, "cosign")
		assert.Equal(t, args[0], "verify-blob-attestation")

		assert.Contains(t, args, "./testdata/example_artifact.txt")
		assert.Contains(t, args, "^https://gitlab.com/OWNER/REPO/")
		assert.Contains(t, args, "https://gitlab.com")

		return &MockCommandExecutor{output: "Output not used."}
	}

	output, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

	assert.Nil(t, err)
	assert.Contains(t, output.String(), "Artifact provenance successfully verified")
	assert.True(t, shellCommandCalled, "shell command called")
}

func Test_AttestationVerify_Failure(t *testing.T) {
	t.Setenv("NO_COLOR", "true")

	tc := gitlab_testing.NewTestClient(t)
	opts := []cmdtest.FactoryOption{cmdtest.WithGitLabClient(tc.Client)}
	exec := cmdtest.SetupCmdForTest(t, NewCmdVerify, false, opts...)

	mocks(t, tc)

	origLookPath := lookPath
	defer func() { lookPath = origLookPath }()
	lookPath = func(path string) (string, error) {
		return "/usr/bin/cosign", nil
	}

	origShellCommandFunc := execCommand
	defer func() { execCommand = origShellCommandFunc }()

	shellCommandCalled := false
	execCommand = func(name string, args ...string) commandExecutor {
		shellCommandCalled = true

		return &MockCommandExecutor{err: true}
	}

	_, err := exec("OWNER/REPO ./testdata/example_artifact.txt")

	assert.EqualError(t, err, "Exit code 1: Stdout,Stderr\n")
	assert.True(t, shellCommandCalled, "shell command called")
}
