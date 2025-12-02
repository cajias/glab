//go:build !integration

package api

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsJSONArrayUnmarshalError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name: "array unmarshal error",
			err: &json.UnmarshalTypeError{
				Value: "array",
			},
			expected: true,
		},
		{
			name: "object unmarshal error",
			err: &json.UnmarshalTypeError{
				Value: "object",
			},
			expected: false,
		},
		{
			name: "string unmarshal error",
			err: &json.UnmarshalTypeError{
				Value: "string",
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isJSONArrayUnmarshalError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestUnmarshalArrayIntoStruct verifies that unmarshaling an array into a struct
// produces the expected error type that we detect
func TestUnmarshalArrayIntoStruct(t *testing.T) {
	type TestStruct struct {
		ID int `json:"id"`
	}

	// This is the JSON array response that some GitLab instances return
	arrayJSON := `[{"id": 1}, {"id": 2}]`

	var result TestStruct
	err := json.Unmarshal([]byte(arrayJSON), &result)

	assert.NotNil(t, err)
	assert.True(t, isJSONArrayUnmarshalError(err), "should detect array unmarshal error")
}

// TestUnmarshalArrayIntoStruct_WrappedError verifies that wrapped errors are properly detected
func TestUnmarshalArrayIntoStruct_WrappedError(t *testing.T) {
	type TestStruct struct {
		ID int `json:"id"`
	}

	// This is the JSON array response that some GitLab instances return
	arrayJSON := `[{"id": 1}, {"id": 2}]`

	var result TestStruct
	err := json.Unmarshal([]byte(arrayJSON), &result)

	// Wrap the error as would happen in real code
	wrappedErr := fmt.Errorf("failed to create note: %w", err)

	assert.NotNil(t, wrappedErr)
	assert.True(t, isJSONArrayUnmarshalError(wrappedErr), "should detect wrapped array unmarshal error")
}

// TestUnmarshalEmptyArrayIntoStruct verifies that empty arrays also produce the expected error
func TestUnmarshalEmptyArrayIntoStruct(t *testing.T) {
	type TestStruct struct {
		ID int `json:"id"`
	}

	// Empty array response
	emptyArrayJSON := `[]`

	var result TestStruct
	err := json.Unmarshal([]byte(emptyArrayJSON), &result)

	assert.NotNil(t, err)
	assert.True(t, isJSONArrayUnmarshalError(err), "should detect empty array unmarshal error")
}

// TestUnmarshalSingleObjectIntoStruct verifies that single objects work correctly
func TestUnmarshalSingleObjectIntoStruct(t *testing.T) {
	type TestStruct struct {
		ID int `json:"id"`
	}

	// Single object response (expected response)
	singleObjectJSON := `{"id": 1}`

	var result TestStruct
	err := json.Unmarshal([]byte(singleObjectJSON), &result)

	assert.Nil(t, err)
	assert.Equal(t, 1, result.ID)
}

// TestIsJSONArrayUnmarshalError_GenericError verifies that generic errors are not detected as array errors
func TestIsJSONArrayUnmarshalError_GenericError(t *testing.T) {
	genericErr := fmt.Errorf("some generic error")
	assert.False(t, isJSONArrayUnmarshalError(genericErr), "generic errors should not be detected as array unmarshal errors")
}

// TestIsJSONArrayUnmarshalError_SyntaxError verifies that JSON syntax errors are not detected as array errors
func TestIsJSONArrayUnmarshalError_SyntaxError(t *testing.T) {
	// Invalid JSON
	invalidJSON := `{invalid json}`

	type TestStruct struct {
		ID int `json:"id"`
	}

	var result TestStruct
	err := json.Unmarshal([]byte(invalidJSON), &result)

	assert.NotNil(t, err)
	assert.False(t, isJSONArrayUnmarshalError(err), "JSON syntax errors should not be detected as array unmarshal errors")
}
