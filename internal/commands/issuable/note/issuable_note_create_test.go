//go:build !integration

package note

import (
	"fmt"
	"net/http"
	"os"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/commands/issuable"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/prompt"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/testing/httpmock"
	"gitlab.com/gitlab-org/cli/test"
)

func runCommand(t *testing.T, rt http.RoundTripper, cli string, issueType issuable.IssueType) (*test.CmdOut, error) {
	t.Helper()

	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))
	factory := cmdtest.NewTestFactory(ios,
		cmdtest.WithGitLabClient(cmdtest.NewTestApiClient(t, &http.Client{Transport: rt}, "", glinstance.DefaultHostname).Lab()),
		cmdtest.WithConfig(config.NewFromString("editor: vi")),
	)

	cmd := NewCmdNote(factory, issueType)

	return cmdtest.ExecuteCommand(cmd, cli, stdout, stderr)
}

func Test_NewCmdNote(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	commands := []struct {
		name      string
		issueType issuable.IssueType
	}{
		{"issue", issuable.TypeIssue},
		{"incident", issuable.TypeIncident},
	}

	for _, cc := range commands {
		t.Run("--message flag specified", func(t *testing.T) {
			fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/issues/1/notes",
				httpmock.NewStringResponse(http.StatusCreated, `
				{
					"id": 301,
					"created_at": "2013-10-02T08:57:14Z",
					"updated_at": "2013-10-02T08:57:14Z",
					"system": false,
					"noteable_id": 1,
					"noteable_type": "MergeRequest",
					"noteable_iid": 1
				}
			`))

			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/1",
				httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`
				{
					"id": 1,
					"iid": 1,
					"issue_type": "%s",
					"web_url": "https://gitlab.com/OWNER/REPO/issues/1"
				}
			`, cc.issueType)))

			// glab issue note 1 --message "Here is my note"
			// glab incident note 1 --message "Here is my note"
			output, err := runCommand(t, fakeHTTP, `1 --message "Here is my note"`, cc.issueType)
			if err != nil {
				t.Error(err)
				return
			}
			assert.Equal(t, output.Stderr(), "")
			assert.Equal(t, output.String(), "https://gitlab.com/OWNER/REPO/issues/1#note_301\n")
		})

		t.Run("issue not found", func(t *testing.T) {
			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/122",
				httpmock.NewStringResponse(http.StatusNotFound, `
				{
					"message": "issue not found"
				}
			`))

			// glab issue note 1 --message "Here is my note"
			// glab incident note 1 --message "Here is my note"
			_, err := runCommand(t, fakeHTTP, `122`, cc.issueType)
			assert.NotNil(t, err)
			assert.Equal(t, "404 Not Found", err.Error())
		})
	}
}

func Test_NewCmdNote_error(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	commands := []struct {
		name      string
		issueType issuable.IssueType
	}{
		{"issue", issuable.TypeIssue},
		{"incident", issuable.TypeIncident},
	}

	for _, cc := range commands {
		t.Run("note could not be created", func(t *testing.T) {
			fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/issues/1/notes",
				httpmock.NewStringResponse(http.StatusUnauthorized, `
				{
					"message": "Unauthorized"
				}
			`))

			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/1",
				httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`
				{
					"id": 1,
					"iid": 1,
					"issue_type": "%s",
					"web_url": "https://gitlab.com/OWNER/REPO/issues/1"
				}
			`, cc.issueType)))

			// glab issue note 1 --message "Here is my note"
			// glab incident note 1 --message "Here is my note"
			_, err := runCommand(t, fakeHTTP, `1 -m "Some message"`, cc.issueType)
			assert.NotNil(t, err)
			assert.Equal(t, "POST https://gitlab.com/api/v4/projects/OWNER%2FREPO/issues/1/notes: 401 {message: Unauthorized}", err.Error())
		})
	}

	t.Run("using incident note command with issue ID", func(t *testing.T) {
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/1",
			httpmock.NewStringResponse(http.StatusOK, `
				{
					"id": 1,
					"iid": 1,
					"issue_type": "issue",
					"web_url": "https://gitlab.com/OWNER/REPO/issues/1"
				}
			`))

		output, err := runCommand(t, fakeHTTP, `1 -m "Some message"`, issuable.TypeIncident)
		assert.Nil(t, err)
		assert.Equal(t, "Incident not found, but an issue with the provided ID exists. Run `glab issue comment <id>` to comment.\n", output.String())
	})
}

func Test_IssuableNoteCreate_prompt(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	commands := []struct {
		name      string
		issueType issuable.IssueType
	}{
		{"issue", issuable.TypeIssue},
		{"incident", issuable.TypeIncident},
	}

	for _, cc := range commands {
		t.Run("message provided", func(t *testing.T) {
			fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/issues/1/notes",
				httpmock.NewStringResponse(http.StatusCreated, `
				{
					"id": 301,
					"created_at": "2013-10-02T08:57:14Z",
					"updated_at": "2013-10-02T08:57:14Z",
					"system": false,
					"noteable_id": 1,
					"noteable_type": "MergeRequest",
					"noteable_iid": 1
				}
			`))

			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/1",
				httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`
				{
					"id": 1,
					"iid": 1,
					"issue_type": "%s",
					"web_url": "https://gitlab.com/OWNER/REPO/issues/1"
				}
			`, cc.issueType)))
			as, teardown := prompt.InitAskStubber()
			defer teardown()
			as.StubOne("some note message")

			// glab issue note 1
			// glab incident note 1
			output, err := runCommand(t, fakeHTTP, `1`, cc.issueType)

			// get the editor used
			notePrompt := *as.AskOnes[0]
			actualEditor := reflect.ValueOf(notePrompt).Elem().FieldByName("EditorCommand").String()

			if err != nil {
				t.Error(err)
				return
			}
			assert.Equal(t, "", output.Stderr())
			assert.Equal(t, "https://gitlab.com/OWNER/REPO/issues/1#note_301\n", output.String())

			editor := os.Getenv("EDITOR")
			if editor == "" {
				editor = "vi"
			}
			assert.Equal(t, editor, actualEditor)
		})

		tests := []struct {
			name    string
			message string
		}{
			{"message is empty", ""},
			{"message contains only spaces", "   "},
			{"message contains only line breaks", "\n\n"},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/1",
					httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`
					{
						"id": 1,
						"iid": 1,
						"issue_type": "%s",
						"web_url": "https://gitlab.com/OWNER/REPO/issues/1"
					}
				`, cc.issueType)))

				as, teardown := prompt.InitAskStubber()
				defer teardown()
				as.StubOne(tt.message)

				_, err := runCommand(t, fakeHTTP, `1`, cc.issueType)
				if err == nil {
					t.Error("expected error")
					return
				}
				assert.Equal(t, "aborted... Note is empty.", err.Error())
			})
		}
	}
}

// Test_IssueNote_ArrayResponse_Regression tests the regression fix for GitLab instances
// that return an array of notes instead of a single note when creating a note.
// This is the main regression test for the issue:
// "glab mr note/comment fails with JSON unmarshal error"
func Test_IssueNote_ArrayResponse_Regression(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	commands := []struct {
		name      string
		issueType issuable.IssueType
	}{
		{"issue", issuable.TypeIssue},
		{"incident", issuable.TypeIncident},
	}

	for _, cc := range commands {
		t.Run(fmt.Sprintf("%s API returns array with multiple notes", cc.name), func(t *testing.T) {
			// Register the issue lookup
			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/10",
				httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`
			{
				"id": 10,
				"iid": 10,
				"issue_type": "%s",
				"web_url": "https://gitlab.com/OWNER/REPO/issues/10"
			}
		`, cc.issueType)))

			// The POST request returns an array with multiple notes (buggy GitLab behavior)
			fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/issues/10/notes",
				httpmock.NewStringResponse(http.StatusCreated, `
			[
				{
					"id": 100,
					"created_at": "2024-01-01T08:00:00Z",
					"body": "Old comment"
				},
				{
					"id": 200,
					"created_at": "2024-01-02T08:00:00Z",
					"body": "Newer comment"
				},
				{
					"id": 501,
					"created_at": "2024-01-03T08:57:14Z",
					"body": "My new comment"
				}
			]
		`))

			// Fallback: listing notes to get the most recent one
			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/10/notes",
				httpmock.NewStringResponse(http.StatusOK, `
			[
				{
					"id": 501,
					"created_at": "2024-01-03T08:57:14Z",
					"body": "My new comment"
				}
			]
		`))

			output, err := runCommand(t, fakeHTTP, `10 --message "My new comment"`, cc.issueType)
			if err != nil {
				t.Errorf("Expected no error, got: %v", err)
				return
			}
			assert.Equal(t, "", output.Stderr())
			assert.Equal(t, "https://gitlab.com/OWNER/REPO/issues/10#note_501\n", output.String())
		})

		t.Run(fmt.Sprintf("%s API returns array with single note", cc.name), func(t *testing.T) {
			// Register the issue lookup
			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/11",
				httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`
			{
				"id": 11,
				"iid": 11,
				"issue_type": "%s",
				"web_url": "https://gitlab.com/OWNER/REPO/issues/11"
			}
		`, cc.issueType)))

			// The POST request returns an array with single note
			fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/issues/11/notes",
				httpmock.NewStringResponse(http.StatusCreated, `
			[
				{
					"id": 601,
					"created_at": "2024-01-03T08:57:14Z",
					"body": "Single array note"
				}
			]
		`))

			// Fallback: listing notes to get the most recent one
			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/11/notes",
				httpmock.NewStringResponse(http.StatusOK, `
			[
				{
					"id": 601,
					"created_at": "2024-01-03T08:57:14Z",
					"body": "Single array note"
				}
			]
		`))

			output, err := runCommand(t, fakeHTTP, `11 --message "Single array note"`, cc.issueType)
			if err != nil {
				t.Errorf("Expected no error, got: %v", err)
				return
			}
			assert.Equal(t, "", output.Stderr())
			assert.Equal(t, "https://gitlab.com/OWNER/REPO/issues/11#note_601\n", output.String())
		})

		t.Run(fmt.Sprintf("%s normal single object response still works", cc.name), func(t *testing.T) {
			// Register the issue lookup
			fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/issues/12",
				httpmock.NewStringResponse(http.StatusOK, fmt.Sprintf(`
			{
				"id": 12,
				"iid": 12,
				"issue_type": "%s",
				"web_url": "https://gitlab.com/OWNER/REPO/issues/12"
			}
		`, cc.issueType)))

			// Normal response: single object (not array)
			fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/issues/12/notes",
				httpmock.NewStringResponse(http.StatusCreated, `
			{
				"id": 701,
				"created_at": "2024-01-03T08:57:14Z",
				"body": "Normal single object response"
			}
		`))

			output, err := runCommand(t, fakeHTTP, `12 --message "Normal single object response"`, cc.issueType)
			if err != nil {
				t.Errorf("Expected no error, got: %v", err)
				return
			}
			assert.Equal(t, "", output.Stderr())
			assert.Equal(t, "https://gitlab.com/OWNER/REPO/issues/12#note_701\n", output.String())
		})
	}
}
