//go:build !integration

package create

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/MakeNowJust/heredoc/v2"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/survivorbat/huhtest"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/glrepo"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/test"
)

func TestNewCmdCreate_tty(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListMilestones
	testClient.MockMilestones.EXPECT().
		ListMilestones("OWNER/REPO", gomock.Any()).
		Return([]*gitlab.Milestone{
			{
				ID:          1,
				IID:         3,
				Description: "foo",
			},
		}, nil, nil)

	// Mock ListUsers
	testClient.MockUsers.EXPECT().
		ListUsers(gomock.Any()).
		Return([]*gitlab.User{
			{
				Username: "testuser",
			},
		}, nil, nil)

	// Mock CreateMergeRequest
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:           1,
				IID:          12,
				ProjectID:    3,
				Title:        "myMRtitle",
				Description:  "myMRbody",
				State:        "opened",
				TargetBranch: "master",
				SourceBranch: "feat-new-mr",
				WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
			},
		}, nil, nil)

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"-t", "myMRtitle",
		"-d", "myMRbody",
		"-l", "test,bug",
		"--milestone", "foo",
		"--assignee", "testuser",
	}

	cli := strings.Join(cliStr, " ")

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}

	outputLines := strings.SplitN(output.String(), "\n", 2)
	assert.Contains(t, outputLines[0], "!12 myMRtitle (feat-new-mr)")
	assert.Contains(t, output.Stderr(), "\nCreating merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestNewCmdCreate_RelatedIssue(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock GetIssue
	testClient.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.Issue{
			ID:          1,
			IID:         1,
			ProjectID:   1,
			Title:       "this is a issue title",
			Description: "issue description",
		}, nil, nil)

	// Mock CreateMergeRequest and verify the title and description
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Contains(t, *opts.Title, `Draft: Resolve "this is a issue title"`)
			assert.Contains(t, *opts.Description, "\n\nCloses #1")
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        `Draft: Resolve "this is a issue title"`,
					Description:  "\n\nCloses #1",
					State:        "opened",
					TargetBranch: "master",
					SourceBranch: "feat-new-mr",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
			deadbeef HEAD
			deadb00f refs/remotes/upstream/feat-new-mr
			deadbeef refs/remotes/origin/feat-new-mr
		`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"--related-issue", "1",
		"--source-branch", "feat-new-mr",
		"--yes",
	}

	cli := strings.Join(cliStr, " ")

	t.Log(cli)

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}
	outputLines := strings.SplitN(output.String(), "\n", 2)
	assert.Contains(t, outputLines[0], `!12 Draft: Resolve "this is a issue title" (feat-new-mr)`)
	assert.Contains(t, output.Stderr(), "\nCreating draft merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestNewCmdCreate_TemplateFromCommitMessages(t *testing.T) {
	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock CreateMergeRequest and verify the description contains commit messages
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Contains(t, *opts.Description, "- commit msg 1  \n\n")
			assert.Contains(t, *opts.Description, "- commit msg 2  \ncommit body")
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        "...",
					Description:  "...",
					State:        "opened",
					TargetBranch: "master",
					SourceBranch: "feat-new-mr",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()

	cs.Stub("HEAD branch: main\n") // git remote show <name>
	cs.Stub("/")                   // git rev-parse --show-toplevel

	// git -c log.ShowSignature=false log --pretty=format:%H,%s --cherry upstream/main...feat-new-mr
	cs.Stub(heredoc.Doc(`
			deadb00f,commit msg 2
			deadbeef,commit msg 1
		`))

	// git -c log.ShowSignature=false show -s --pretty=format:%b deadbeef
	cs.Stub("")
	// git -c log.ShowSignature=false show -s --pretty=format:%b deadb00f
	cs.Stub("commit body")

	// Set up responder for prompts
	responder := huhtest.NewResponder()
	responder.AddSelect("Choose a template:", 0)                                              // Select first option: "Open a merge request with commit messages."
	responder.AddResponse("Description", "- commit msg 1  \n\n- commit msg 2  \ncommit body") // Accept the pre-filled description

	cliStr := []string{
		"--source-branch", "feat-new-mr",
		"--title", "mr-title",
		"--yes",
	}

	cli := strings.Join(cliStr, " ")

	t.Log(cli)

	// Use SetupCmdForTest pattern for responder
	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
		// Set up factory with remotes stub
		tf := f.(*cmdtest.Factory)
		tf.RemotesStub = func() (glrepo.Remotes, error) {
			return glrepo.Remotes{
				{
					Remote: &git.Remote{
						Name:     "upstream",
						Resolved: "head",
						PushURL:  pu,
					},
					Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
				},
				{
					Remote: &git.Remote{
						Name:     "origin",
						Resolved: "head",
						PushURL:  pu,
					},
					Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
				},
			}, nil
		}
		tf.BranchStub = func() (string, error) {
			return "feat-new-mr", nil
		}

		return NewCmdCreate(f)
	}, true,
		cmdtest.WithGitLabClient(testClient.Client),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
		cmdtest.WithResponder(t, responder),
	)

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}
}

func TestNewCmdCreate_RelatedIssueWithTitleAndDescription(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// Mock GetProject
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock GetIssue
	testClient.MockIssues.EXPECT().
		GetIssue("OWNER/REPO", int64(1), gomock.Any()).
		Return(&gitlab.Issue{
			ID:          1,
			IID:         1,
			ProjectID:   1,
			Title:       "this is a issue title",
			Description: "issue description",
		}, nil, nil)

	// Mock CreateMergeRequest and verify the title and description
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		DoAndReturn(func(pid any, opts *gitlab.CreateMergeRequestOptions, options ...gitlab.RequestOptionFunc) (*gitlab.MergeRequest, *gitlab.Response, error) {
			assert.Equal(t, "Draft: my custom MR title", *opts.Title)
			assert.Contains(t, *opts.Description, "my custom MR description\n\nCloses #1")
			return &gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:           1,
					IID:          12,
					ProjectID:    3,
					Title:        "my custom MR title",
					Description:  "myMRbody",
					State:        "opened",
					TargetBranch: "master",
					SourceBranch: "feat-new-mr",
					WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
				},
			}, nil, nil
		})

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
			deadbeef HEAD
			deadb00f refs/remotes/upstream/feat-new-mr
			deadbeef refs/remotes/origin/feat-new-mr
		`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	cliStr := []string{
		"--title", `"my custom MR title"`,
		"--description", `"my custom MR description"`,
		"--related-issue", "1",
		"--source-branch", "feat-new-mr",
	}

	cli := strings.Join(cliStr, " ")

	t.Log(cli)

	output, err := exec(cli)
	if err != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", output.Stderr())
		}
		t.Error(err)
		return
	}

	outputLines := strings.SplitN(output.String(), "\n", 2)
	assert.Contains(t, outputLines[0], "!12 my custom MR title (feat-new-mr)")
	assert.Contains(t, output.Stderr(), "\nCreating draft merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestMRCreate_nontty_insufficient_flags(t *testing.T) {
	t.Parallel()

	testClient := gitlabtesting.NewTestClient(t)

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, false,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "test-br", nil
			}
		},
	)

	_, err := exec("")
	require.Error(t, err)
	assert.Equal(t, "--title or --fill required for non-interactive mode.", err.Error())
}

func TestMrBodyAndTitle(t *testing.T) {
	opts := &options{
		SourceBranch:         "mr-autofill-test-br",
		TargetBranch:         "master",
		TargetTrackingBranch: "origin/master",
	}
	t.Run("", func(t *testing.T) {
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()
		cs.Stub("d1sd2e,docs: add some changes to txt file")                           // git log
		cs.Stub("Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n") // git log

		if err := mrBodyAndTitle(opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "docs: add some changes to txt file", opts.Title)
		assert.Equal(t, "Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n", opts.Description)
	})
	t.Run("given-title", func(t *testing.T) {
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()

		cs.Stub("d1sd2e,docs: add some changes to txt file")
		cs.Stub("Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n") // git log

		opts := *opts
		opts.Title = "docs: make some other stuff"
		if err := mrBodyAndTitle(&opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "docs: make some other stuff", opts.Title)
		assert.Equal(t, `Here, I am adding some commit body.
Little longer

Resolves #1
`, opts.Description)
	})
	t.Run("given-description", func(t *testing.T) {
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()

		cs.Stub("d1sd2e,docs: add some changes to txt file")

		opts := *opts
		opts.Description = `Make it multiple lines
like this

resolves #1
`
		if err := mrBodyAndTitle(&opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "docs: add some changes to txt file", opts.Title)
		assert.Equal(t, `Make it multiple lines
like this

resolves #1
`, opts.Description)
	})
	t.Run("given-fill-commit-body", func(t *testing.T) {
		opts = &options{
			SourceBranch:         "mr-autofill-test-br",
			TargetBranch:         "master",
			TargetTrackingBranch: "origin/master",
		}
		cs, csTeardown := test.InitCmdStubber()
		defer csTeardown()

		cs.Stub("d1sd2e,chore: some tidying\nd2asa3,docs: more changes to more things")
		cs.Stub("Here, I am adding some commit body.\nLittle longer\n\nResolves #1\n")
		cs.Stub("another body for another commit\ncloses 1234\n")

		opts := *opts
		opts.FillCommitBody = true

		if err := mrBodyAndTitle(&opts); err != nil {
			t.Errorf("unexpected error: %v", err)
			return
		}

		assert.Equal(t, "mr autofill test br", opts.Title)
		// Note: trailing spaces (markdown line breaks) are added to certain lines
		assert.Equal(t, "- docs: more changes to more things  \nHere, I am adding some commit body.\nLittle longer  \nResolves #1\n\n- chore: some tidying  \nanother body for another commit\ncloses 1234\n\n", opts.Description)
	})
}

func TestGenerateMRCompareURL(t *testing.T) {
	opts := &options{
		Labels:        []string{"backend", "frontend"},
		Assignees:     []string{"johndoe", "janedoe"},
		Reviewers:     []string{"user", "person"},
		Milestone:     15,
		TargetProject: &gitlab.Project{ID: 100},
		SourceProject: &gitlab.Project{
			ID:     101,
			WebURL: "https://gitlab.example.com/gitlab-org/gitlab",
		},
		Title:        "Autofill tests | for this @project",
		SourceBranch: "@|calc",
		TargetBranch: "project/my-branch",
	}

	u, err := generateMRCompareURL(opts)

	expectedUrl := "https://gitlab.example.com/gitlab-org/gitlab/-/merge_requests/new?" +
		"merge_request%5Bdescription%5D=%0A%2Flabel+~backend%2C+~frontend%0A%2Fassign+johndoe%2C+janedoe%0A%2Freviewer+user%2C+person%0A%2Fmilestone+%2515&" +
		"merge_request%5Bsource_branch%5D=%40%7Ccalc&merge_request%5Bsource_project_id%5D=101&merge_request%5Btarget_branch%5D=project%2Fmy-branch&merge_request%5Btarget_project_id%5D=100&" +
		"merge_request%5Btitle%5D=Autofill+tests+%7C+for+this+%40project"

	assert.NoError(t, err)
	assert.Equal(t, expectedUrl, u)
}

func Test_MRCreate_With_Recover_Integration(t *testing.T) {
	// NOTE: we need to force disable colors, otherwise we'd need ANSI sequences in our test output assertions.
	t.Setenv("NO_COLOR", "true")

	testClient := gitlabtesting.NewTestClient(t)

	// For the first run: GitLabClientStub returns error to trigger recovery file creation
	// For the second run: Normal API mocks

	// Mock GetProject (only called on recovery run - first run fails before API call)
	testClient.MockProjects.EXPECT().
		GetProject("OWNER/REPO", gomock.Any()).
		Return(&gitlab.Project{
			ID:                   1,
			DefaultBranch:        "master",
			WebURL:               "http://gitlab.com/OWNER/REPO",
			Name:                 "OWNER",
			Path:                 "REPO",
			MergeRequestsEnabled: true,
			PathWithNamespace:    "OWNER/REPO",
		}, nil, nil)

	// Mock ListUsers (called on recovery)
	testClient.MockUsers.EXPECT().
		ListUsers(gomock.Any()).
		Return([]*gitlab.User{
			{
				Username: "testuser",
			},
		}, nil, nil)

	// Mock CreateMergeRequest (called on recovery)
	testClient.MockMergeRequests.EXPECT().
		CreateMergeRequest("OWNER/REPO", gomock.Any()).
		Return(&gitlab.MergeRequest{
			BasicMergeRequest: gitlab.BasicMergeRequest{
				ID:           1,
				IID:          12,
				ProjectID:    3,
				Title:        "myMRtitle",
				Description:  "myMRbody",
				State:        "opened",
				TargetBranch: "master",
				SourceBranch: "feat-new-mr",
				WebURL:       "https://gitlab.com/OWNER/REPO/-/merge_requests/12",
			},
		}, nil, nil)

	cs, csTeardown := test.InitCmdStubber()
	defer csTeardown()
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))
	// For recovery run
	cs.Stub("HEAD branch: master\n")
	cs.Stub(heredoc.Doc(`
		deadbeef HEAD
		deadb00f refs/remotes/upstream/feat-new-mr
		deadbeef refs/remotes/origin/feat-new-mr
	`))

	pu, _ := url.Parse("https://gitlab.com/OWNER/REPO.git")

	cliStr := []string{
		"-t", "myMRtitle",
		"-d", "myMRbody",
		"-l", "test,bug",
		"--milestone", "1",
		"--assignee", "testuser",
	}

	cli := strings.Join(cliStr, " ")

	// First run - let it fail to create recovery file
	exec := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
			// Return error to trigger recovery file creation
			f.GitLabClientStub = func() (*gitlab.Client, error) {
				return nil, errors.New("fail on purpose")
			}
		},
	)

	output, err := exec(cli)

	outErr := output.Stderr()

	require.Errorf(t, err, "fail on purpose")
	require.Contains(t, outErr, "Failed to create merge request. Created recovery file: ")

	// Run create issue with recover
	newCliStr := append(cliStr, "--recover")

	newCli := strings.Join(newCliStr, " ")

	// Second run - recover from file
	exec2 := cmdtest.SetupCmdForTest(t, NewCmdCreate, true,
		cmdtest.WithGitLabClient(testClient.Client),
		func(f *cmdtest.Factory) {
			f.RemotesStub = func() (glrepo.Remotes, error) {
				return glrepo.Remotes{
					{
						Remote: &git.Remote{
							Name:     "upstream",
							Resolved: "head",
							PushURL:  pu,
						},
						Repo: glrepo.New("OWNER", "REPO", glinstance.DefaultHostname),
					},
					{
						Remote: &git.Remote{
							Name:     "origin",
							Resolved: "base",
							PushURL:  pu,
						},
						Repo: glrepo.New("monalisa", "REPO", glinstance.DefaultHostname),
					},
				}, nil
			}
			f.BranchStub = func() (string, error) {
				return "feat-new-mr", nil
			}
		},
	)

	newOutput, newErr := exec2(newCli)
	if newErr != nil {
		if errors.Is(err, cmdutils.SilentError) {
			t.Errorf("Unexpected error: %q", newOutput.Stderr())
		}
		t.Error(newErr)
		return
	}

	outputLines := strings.SplitN(newOutput.String(), "\n", 2)
	require.NoError(t, newErr)
	assert.Contains(t, outputLines[0], "Recovered create options from file")
	assert.Contains(t, newOutput.String(), "!12 myMRtitle (feat-new-mr)")
	assert.Contains(t, newOutput.Stderr(), "\nCreating merge request for feat-new-mr into master in OWNER/REPO\n\n")
	assert.Contains(t, newOutput.String(), "https://gitlab.com/OWNER/REPO/-/merge_requests/12")
}

func TestMRCreate_RemotesError_PropagatesError(t *testing.T) {
	t.Parallel()

	// Test that errors from Remotes() are properly propagated (not swallowed)
	// This ensures the bug from issue #8112 doesn't regress

	testClient := gitlabtesting.NewTestClient(t)

	// Setup command using cmdtest.SetupCmdForTest
	exec := cmdtest.SetupCmdForTest(t,
		func(f cmdutils.Factory) *cobra.Command {
			tf := f.(*cmdtest.Factory)

			// Simulate being outside a git repository - Remotes() fails
			remotesErr := errors.New("fatal: not a git repository (or any of the parent directories): .git")
			tf.RemotesStub = func() (glrepo.Remotes, error) {
				return nil, remotesErr
			}
			tf.BranchStub = func() (string, error) {
				return "test-branch", nil
			}

			return NewCmdCreate(f)
		},
		false,
		cmdtest.WithGitLabClient(testClient.Client),
	)

	cli := "--source-branch test-branch --target-branch main --title Test --description TestDesc --no-editor --yes"
	output, err := exec(cli)

	require.Error(t, err, "expected error when Remotes() fails")
	assert.Contains(t, err.Error(), "not a git repository", "error should mention git repository")
	assert.NotContains(t, output.String(), "!12", "should not have created a merge request")
}
