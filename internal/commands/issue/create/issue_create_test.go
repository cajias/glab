//go:build !integration

package create

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestGenerateIssueWebURL(t *testing.T) {
	opts := &options{
		Labels:         []string{"backend", "frontend"},
		Assignees:      []string{"johndoe", "janedoe"},
		Milestone:      15,
		Weight:         3,
		IsConfidential: true,
		baseProject: &gitlab.Project{
			ID:     101,
			WebURL: "https://gitlab.example.com/gitlab-org/gitlab",
		},
		Title: "Autofill tests | for this @project",
	}

	u, err := generateIssueWebURL(opts)

	expectedUrl := "https://gitlab.example.com/gitlab-org/gitlab/-/issues/new?" +
		"issue%5Bdescription%5D=%0A%2Flabel+~%22backend%22+~%22frontend%22%0A%2Fassign+johndoe%2C+janedoe%0A%2Fmilestone+%2515%0A%2Fweight+3%0A%2Fconfidential&" +
		"issue%5Btitle%5D=Autofill+tests+%7C+for+this+%40project"

	assert.NoError(t, err)
	assert.Equal(t, expectedUrl, u)
}

func TestIssueCreateWhenIssuesDisabled(t *testing.T) {
	// GIVEN
	testClient := gitlabtesting.NewTestClient(t)

	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                37777023,
			Description:       "this is a test description",
			Name:              "REPO",
			NameWithNamespace: "Test User / REPO",
			Path:              "REPO",
			PathWithNamespace: "OWNER/REPO",
			DefaultBranch:     "main",
			HTTPURLToRepo:     "https://gitlab.com/OWNER/REPO.git",
			WebURL:            "https://gitlab.com/OWNER/REPO",
			ReadmeURL:         "https://gitlab.com/OWNER/REPO/-/blob/main/README.md",
			IssuesEnabled:     false,
		}, nil, nil)

	exec := cmdtest.SetupCmdForTest(
		t,
		NewCmdCreate,
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	// WHEN
	cli := `--title "test title" --description "test description"`
	output, err := exec(cli)

	// THEN
	assert.NotNil(t, err)
	assert.Empty(t, output.String())
	assert.Equal(t, "Issues are disabled for project \"OWNER/REPO\" or require project membership. "+
		"Make sure issues are enabled for the \"OWNER/REPO\" project, and if required, you are a member of the project.\n",
		output.Stderr())
}
