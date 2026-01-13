//go:build !integration

package note

import (
	"errors"
	"net/http"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/survivorbat/huhtest"
	"go.uber.org/mock/gomock"

	gitlab "gitlab.com/gitlab-org/api/client-go"
	gitlabtesting "gitlab.com/gitlab-org/api/client-go/testing"

	"gitlab.com/gitlab-org/cli/internal/cmdutils"
	"gitlab.com/gitlab-org/cli/internal/config"
	"gitlab.com/gitlab-org/cli/internal/testing/cmdtest"
)

func TestMain(m *testing.M) {
	cmdtest.InitTest(m, "mr_note_create_test")
}

func Test_NewCmdNote(t *testing.T) {
	t.Parallel()

	t.Run("--message flag specified", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock CreateMergeRequestNote
		testClient.MockNotes.EXPECT().
			CreateMergeRequestNote("OWNER/REPO", int64(1), gomock.Any()).
			DoAndReturn(func(pid any, mrIID int64, opts *gitlab.CreateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
				assert.Equal(t, "Here is my note", *opts.Body)
				return &gitlab.Note{
					ID:           301,
					NoteableID:   1,
					NoteableType: "MergeRequest",
					NoteableIID:  1,
				}, nil, nil
			})

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		output, err := exec(`1 --message "Here is my note"`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Equal(t, "https://gitlab.com/OWNER/REPO/merge_requests/1#note_301\n", output.String())
	})

	t.Run("merge request not found", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest - returns 404
		notFoundResp := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusNotFound},
		}
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(122), gomock.Any()).
			Return(nil, notFoundResp, gitlab.ErrNotFound)

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		_, err := exec(`122`)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Not Found")
	})
}

func Test_NewCmdNote_error(t *testing.T) {
	t.Parallel()

	t.Run("note could not be created", func(t *testing.T) {
		t.Parallel()

		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock CreateMergeRequestNote - returns 401
		unauthorizedResp := &gitlab.Response{
			Response: &http.Response{StatusCode: http.StatusUnauthorized},
		}
		testClient.MockNotes.EXPECT().
			CreateMergeRequestNote("OWNER/REPO", int64(1), gomock.Any()).
			Return(nil, unauthorizedResp, errors.New("401 Unauthorized"))

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
		)

		_, err := exec(`1 -m "Some message"`)
		require.Error(t, err)
	})
}

func Test_mrNoteCreate_prompt(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).

	t.Run("message provided", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock CreateMergeRequestNote
		testClient.MockNotes.EXPECT().
			CreateMergeRequestNote("OWNER/REPO", int64(1), gomock.Any()).
			DoAndReturn(func(pid any, mrIID int64, opts *gitlab.CreateMergeRequestNoteOptions, options ...gitlab.RequestOptionFunc) (*gitlab.Note, *gitlab.Response, error) {
				assert.Contains(t, *opts.Body, "some note message")
				return &gitlab.Note{
					ID:           301,
					NoteableID:   1,
					NoteableType: "MergeRequest",
					NoteableIID:  1,
				}, nil, nil
			})

		responder := huhtest.NewResponder()
		responder.AddResponse("Note message:", "some note message")

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
			cmdtest.WithResponder(t, responder),
		)

		output, err := exec(`1`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_301")
	})

	t.Run("message is empty", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		responder := huhtest.NewResponder()
		responder.AddResponse("Note message:", "")

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
			cmdtest.WithResponder(t, responder),
		)

		_, err := exec(`1`)
		require.Error(t, err)
		assert.Equal(t, "aborted... Note has an empty message.", err.Error())
	})
}

func Test_mrNoteCreate_no_duplicate(t *testing.T) {
	// NOTE: This test cannot run in parallel because the huh form library
	// uses global state (charmbracelet/bubbles runeutil sanitizer).

	t.Run("message provided", func(t *testing.T) {
		testClient := gitlabtesting.NewTestClient(t)

		// Mock GetMergeRequest
		testClient.MockMergeRequests.EXPECT().
			GetMergeRequest("OWNER/REPO", int64(1), gomock.Any()).
			Return(&gitlab.MergeRequest{
				BasicMergeRequest: gitlab.BasicMergeRequest{
					ID:     1,
					IID:    1,
					WebURL: "https://gitlab.com/OWNER/REPO/merge_requests/1",
				},
			}, nil, nil)

		// Mock ListMergeRequestNotes - returns existing notes including the duplicate
		testClient.MockNotes.EXPECT().
			ListMergeRequestNotes("OWNER/REPO", int64(1), gomock.Any()).
			Return([]*gitlab.Note{
				{ID: 0, Body: "aaa"},
				{ID: 111, Body: "bbb"},
				{ID: 222, Body: "some note message"},
				{ID: 333, Body: "ccc"},
			}, nil, nil)

		responder := huhtest.NewResponder()
		responder.AddResponse("Note message:", "some note message")

		exec := cmdtest.SetupCmdForTest(t, func(f cmdutils.Factory) *cobra.Command {
			return NewCmdNote(f)
		}, true,
			cmdtest.WithGitLabClient(testClient.Client),
			cmdtest.WithBaseRepo("OWNER", "REPO", ""),
			cmdtest.WithConfig(config.NewFromString("editor: vi")),
			cmdtest.WithResponder(t, responder),
		)

		output, err := exec(`1 --unique`)
		require.NoError(t, err)
		assert.Empty(t, output.Stderr())
		assert.Contains(t, output.String(), "https://gitlab.com/OWNER/REPO/merge_requests/1#note_222")
	})
}
