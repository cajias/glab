//go:build !integration

package note

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"gitlab.com/gitlab-org/cli/internal/git"
	"gitlab.com/gitlab-org/cli/internal/glinstance"
	"gitlab.com/gitlab-org/cli/internal/prompt"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
	"gitlab.com/gitlab-org/cli/internal/testing/httpmock"
	"gitlab.com/gitlab-org/cli/test"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "mr_note_create_test")
}

func runCommand(t *testing.T, rt http.RoundTripper, cli string) (*test.CmdOut, error) {
	t.Helper()

	ios, _, stdout, stderr := cmdtest.TestIOStreams(cmdtest.WithTestIOStreamsAsTTY(true))

	factory := cmdtest.NewTestFactory(ios,
		cmdtest.WithGitLabClient(cmdtest.NewTestApiClient(t, &http.Client{Transport: rt}, "", glinstance.DefaultHostname).Lab()),
	)
	factory.BranchStub = git.CurrentBranch

	cmd := NewCmdNote(factory)

	return cmdtest.ExecuteCommand(cmd, cli, stdout, stderr)
}

func Test_NewCmdNote(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	t.Run("--message flag specified", func(t *testing.T) {
		fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/merge_requests/1/notes",
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

		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/1",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 1,
  			"iid": 1,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/1"
		}
	`))

		// glab mr note 1 --message "Here is my note"
		output, err := runCommand(t, fakeHTTP, `1 --message "Here is my note"`)
		if err != nil {
			t.Error(err)
			return
		}
		assert.Equal(t, output.Stderr(), "")
		assert.Equal(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_301\n")
	})

	t.Run("merge request not found", func(t *testing.T) {
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/122",
			httpmock.NewStringResponse(http.StatusNotFound, `
		{
  			"message": "merge request not found"
		}
	`))

		// glab mr note 1 --message "Here is my note"
		_, err := runCommand(t, fakeHTTP, `122`)
		assert.NotNil(t, err)
		assert.Equal(t, "failed to get merge request 122: 404 Not Found", err.Error())
	})

	t.Run("API returns array instead of single note", func(t *testing.T) {
		// Some GitLab instances return an array of notes instead of a single note
		// when creating a note. The API should handle this gracefully.
		fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/merge_requests/2/notes",
			httpmock.NewStringResponse(http.StatusCreated, `
		[
			{
				"id": 401,
				"created_at": "2024-01-02T08:57:14Z",
				"updated_at": "2024-01-02T08:57:14Z",
				"system": false,
				"noteable_id": 2,
				"noteable_type": "MergeRequest",
				"noteable_iid": 2,
				"body": "Test comment"
			}
		]
	`))

		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/2",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 2,
  			"iid": 2,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/2"
		}
	`))

		// When array is returned, we fall back to listing notes
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/2/notes",
			httpmock.NewStringResponse(http.StatusOK, `
		[
			{
				"id": 401,
				"created_at": "2024-01-02T08:57:14Z",
				"updated_at": "2024-01-02T08:57:14Z",
				"system": false,
				"noteable_id": 2,
				"noteable_type": "MergeRequest",
				"noteable_iid": 2,
				"body": "Test comment"
			}
		]
	`))

		// glab mr note 2 --message "Test comment"
		output, err := runCommand(t, fakeHTTP, `2 --message "Test comment"`)
		if err != nil {
			t.Error(err)
			return
		}
		assert.Equal(t, output.Stderr(), "")
		assert.Equal(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/2#note_401\n")
	})
}

func Test_NewCmdNote_error(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	t.Run("note could not be created", func(t *testing.T) {
		fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/merge_requests/1/notes",
			httpmock.NewStringResponse(http.StatusUnauthorized, `
		{
			"message": "Unauthorized"
		}
	`))

		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/1",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 1,
  			"iid": 1,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/1"
		}
	`))

		// glab mr note 1 --message "Here is my note"
		_, err := runCommand(t, fakeHTTP, `1 -m "Some message"`)
		assert.NotNil(t, err)
		assert.Equal(t, "POST https://gitlab.com/api/v4/projects/OWNER%2FREPO/merge_requests/1/notes: 401 {message: Unauthorized}", err.Error())
	})
}

func Test_mrNoteCreate_prompt(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	t.Run("message provided", func(t *testing.T) {
		fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/merge_requests/1/notes",
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

		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/1",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 1,
  			"iid": 1,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/1"
		}
	`))
		as, teardown := prompt.InitAskStubber()
		defer teardown()
		as.StubOne("some note message")

		// glab mr note 1
		output, err := runCommand(t, fakeHTTP, `1`)
		if err != nil {
			t.Error(err)
			return
		}
		assert.Equal(t, output.Stderr(), "")
		assert.Equal(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_301\n")
	})

	t.Run("message is empty", func(t *testing.T) {
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/1",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 1,
  			"iid": 1,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/1"
		}
	`))

		as, teardown := prompt.InitAskStubber()
		defer teardown()
		as.StubOne("")

		// glab mr note 1
		_, err := runCommand(t, fakeHTTP, `1`)
		if err == nil {
			t.Error("expected error")
			return
		}
		assert.Equal(t, err.Error(), "aborted... Note has an empty message.")
	})
}

func Test_mrNoteCreate_no_duplicate(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	t.Run("message provided", func(t *testing.T) {
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/1",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 1,
  			"iid": 1,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/1"
		}
	`))

		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/1/notes",
			httpmock.NewStringResponse(http.StatusOK, `
		[
			{"id": 0, "body": "aaa"},
			{"id": 111, "body": "bbb"},
			{"id": 222, "body": "some note message"},
			{"id": 333, "body": "ccc"}
		]
	`))
		as, teardown := prompt.InitAskStubber()
		defer teardown()
		as.StubOne("some note message")

		// glab mr note 1
		output, err := runCommand(t, fakeHTTP, `1 --unique`)
		if err != nil {
			t.Error(err)
			return
		}
		println(output.String())
		assert.Equal(t, output.Stderr(), "")
		assert.Equal(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_222\n")
	})
}

// Test_mrNote_ArrayResponse_Regression tests the regression fix for GitLab instances
// that return an array of notes instead of a single note when creating a note.
// This is the main regression test for the issue:
// "glab mr note/comment fails with JSON unmarshal error"
func Test_mrNote_ArrayResponse_Regression(t *testing.T) {
	fakeHTTP := httpmock.New()
	defer fakeHTTP.Verify(t)

	t.Run("API returns array with multiple notes", func(t *testing.T) {
		// Register the merge request lookup
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/3",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 3,
  			"iid": 3,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/3"
		}
	`))

		// The POST request returns an array with multiple notes (buggy GitLab behavior)
		fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/merge_requests/3/notes",
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
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/3/notes",
			httpmock.NewStringResponse(http.StatusOK, `
		[
			{
				"id": 501,
				"created_at": "2024-01-03T08:57:14Z",
				"body": "My new comment"
			}
		]
	`))

		output, err := runCommand(t, fakeHTTP, `3 --message "My new comment"`)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
			return
		}
		assert.Equal(t, "", output.Stderr())
		assert.Equal(t, "https://gitlab.com/OWNER/REPO/merge_requests/3#note_501\n", output.String())
	})

	t.Run("API returns array with single note", func(t *testing.T) {
		// Register the merge request lookup
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/4",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 4,
  			"iid": 4,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/4"
		}
	`))

		// The POST request returns an array with single note
		fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/merge_requests/4/notes",
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
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/4/notes",
			httpmock.NewStringResponse(http.StatusOK, `
		[
			{
				"id": 601,
				"created_at": "2024-01-03T08:57:14Z",
				"body": "Single array note"
			}
		]
	`))

		output, err := runCommand(t, fakeHTTP, `4 --message "Single array note"`)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
			return
		}
		assert.Equal(t, "", output.Stderr())
		assert.Equal(t, "https://gitlab.com/OWNER/REPO/merge_requests/4#note_601\n", output.String())
	})

	t.Run("normal single object response still works", func(t *testing.T) {
		// Register the merge request lookup
		fakeHTTP.RegisterResponder(http.MethodGet, "/projects/OWNER/REPO/merge_requests/5",
			httpmock.NewStringResponse(http.StatusOK, `
		{
  			"id": 5,
  			"iid": 5,
			"web_url": "https://gitlab.com/OWNER/REPO/merge_requests/5"
		}
	`))

		// Normal response: single object (not array)
		fakeHTTP.RegisterResponder(http.MethodPost, "/projects/OWNER/REPO/merge_requests/5/notes",
			httpmock.NewStringResponse(http.StatusCreated, `
		{
			"id": 701,
			"created_at": "2024-01-03T08:57:14Z",
			"body": "Normal single object response"
		}
	`))

		output, err := runCommand(t, fakeHTTP, `5 --message "Normal single object response"`)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
			return
		}
		assert.Equal(t, "", output.Stderr())
		assert.Equal(t, "https://gitlab.com/OWNER/REPO/merge_requests/5#note_701\n", output.String())
	})
}
