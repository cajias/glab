// Package api provides wrapper functions for GitLab API calls.
// This file contains wrappers for notes-related API calls.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	gitlab "gitlab.com/gitlab-org/api/client-go"
)

// CreateMergeRequestNote creates a note on a merge request.
// This function handles both single note and array responses from the GitLab API,
// as some self-hosted GitLab instances return an array instead of a single object.
//
// Note: When the fallback to listing notes is used, there is a potential race condition
// in high-concurrency environments where multiple notes are being created simultaneously.
// The function retrieves the most recently created note, which may not be the one just created
// if another note was created in between. This is an acceptable trade-off for handling
// non-standard API responses from certain GitLab instances.
//
// Attention: this is a global variable and may be overridden in tests.
var CreateMergeRequestNote = func(client *gitlab.Client, projectID string, mrID int64, opts *gitlab.CreateMergeRequestNoteOptions) (*gitlab.Note, error) {
	note, resp, err := client.Notes.CreateMergeRequestNote(projectID, mrID, opts)
	if err != nil {
		// Check if error is due to array response instead of single object
		if isJSONArrayUnmarshalError(err) {
			// Try to get the first note from the listing
			return getLatestMergeRequestNote(client, projectID, mrID)
		}
		return nil, err
	}

	// If the response was successful but note is nil, try to get the latest note
	if note == nil && resp != nil && resp.StatusCode == http.StatusCreated {
		return getLatestMergeRequestNote(client, projectID, mrID)
	}

	return note, nil
}

// CreateIssueNote creates a note on an issue.
// This function handles both single note and array responses from the GitLab API,
// as some self-hosted GitLab instances return an array instead of a single object.
//
// Note: When the fallback to listing notes is used, there is a potential race condition
// in high-concurrency environments where multiple notes are being created simultaneously.
// The function retrieves the most recently created note, which may not be the one just created
// if another note was created in between. This is an acceptable trade-off for handling
// non-standard API responses from certain GitLab instances.
//
// Attention: this is a global variable and may be overridden in tests.
var CreateIssueNote = func(client *gitlab.Client, projectID string, issueID int64, opts *gitlab.CreateIssueNoteOptions) (*gitlab.Note, error) {
	note, resp, err := client.Notes.CreateIssueNote(projectID, issueID, opts)
	if err != nil {
		// Check if error is due to array response instead of single object
		if isJSONArrayUnmarshalError(err) {
			// Try to get the first note from the listing
			return getLatestIssueNote(client, projectID, issueID)
		}
		return nil, err
	}

	// If the response was successful but note is nil, try to get the latest note
	if note == nil && resp != nil && resp.StatusCode == http.StatusCreated {
		return getLatestIssueNote(client, projectID, issueID)
	}

	return note, nil
}

// isJSONArrayUnmarshalError checks if the error is due to trying to unmarshal
// a JSON array into a single object.
func isJSONArrayUnmarshalError(err error) bool {
	if err == nil {
		return false
	}
	// Check for the specific unmarshal type error using errors.As
	// to properly handle wrapped errors
	var unmarshalErr *json.UnmarshalTypeError
	if errors.As(err, &unmarshalErr) {
		return unmarshalErr.Value == "array"
	}
	return false
}

// getLatestMergeRequestNote retrieves the most recently created note on a merge request.
// This is used as a fallback when the API returns an array instead of a single note.
func getLatestMergeRequestNote(client *gitlab.Client, projectID string, mrID int64) (*gitlab.Note, error) {
	orderBy := "created_at"
	sortDesc := "desc"
	opts := &gitlab.ListMergeRequestNotesOptions{
		ListOptions: gitlab.ListOptions{PerPage: 1},
		OrderBy:     &orderBy,
		Sort:        &sortDesc,
	}
	notes, _, err := client.Notes.ListMergeRequestNotes(projectID, mrID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve notes after creation: %w", err)
	}
	if len(notes) == 0 {
		return nil, fmt.Errorf("note was created but could not be retrieved")
	}
	return notes[0], nil
}

// getLatestIssueNote retrieves the most recently created note on an issue.
// This is used as a fallback when the API returns an array instead of a single note.
func getLatestIssueNote(client *gitlab.Client, projectID string, issueID int64) (*gitlab.Note, error) {
	orderBy := "created_at"
	sortDesc := "desc"
	opts := &gitlab.ListIssueNotesOptions{
		ListOptions: gitlab.ListOptions{PerPage: 1},
		OrderBy:     &orderBy,
		Sort:        &sortDesc,
	}
	notes, _, err := client.Notes.ListIssueNotes(projectID, issueID, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve notes after creation: %w", err)
	}
	if len(notes) == 0 {
		return nil, fmt.Errorf("note was created but could not be retrieved")
	}
	return notes[0], nil
}
