//go:build !integration

package api

import (
	"encoding/json"
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
