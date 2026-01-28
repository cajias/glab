package iostreams

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPrintJSON_TopLevelNilSliceBecomesEmptyArray(t *testing.T) {
	// Test that top-level nil slices (like from gitlab.ScanAndCollect) are
	// normalized to [] instead of null
	type Token struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	buf := &bytes.Buffer{}
	io := &IOStreams{StdOut: buf}

	// Simulate what gitlab.ScanAndCollect returns for empty results
	var tokens []Token // nil slice

	err := io.PrintJSON(tokens)
	require.NoError(t, err)

	// Verify the actual JSON output is []
	assert.Equal(t, "[]\n", buf.String())
}

func TestPrintJSON_TopLevelSliceWithData(t *testing.T) {
	// Test that top-level slices with data are preserved correctly
	type Token struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	buf := &bytes.Buffer{}
	io := &IOStreams{StdOut: buf}

	tokens := []Token{
		{ID: 1, Name: "token1"},
		{ID: 2, Name: "token2"},
	}

	err := io.PrintJSON(tokens)
	require.NoError(t, err)

	// Parse the output
	var result []Token
	jsonBytes := bytes.TrimSpace(buf.Bytes())
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	assert.Len(t, result, 2)
	assert.Equal(t, tokens[0].ID, result[0].ID)
	assert.Equal(t, tokens[0].Name, result[0].Name)
	assert.Equal(t, tokens[1].ID, result[1].ID)
	assert.Equal(t, tokens[1].Name, result[1].Name)
}

func TestPrintJSON_NestedNilSlicesPreserved(t *testing.T) {
	// Test that nested nil slices (from API responses) are preserved as null
	// to maintain the semantic difference between absent and empty
	type Token struct {
		ID     int      `json:"id"`
		Scopes []string `json:"scopes"` // nil should stay null in JSON
	}

	buf := &bytes.Buffer{}
	io := &IOStreams{StdOut: buf}

	tokens := []Token{
		{ID: 1, Scopes: nil}, // This nil should remain null in the JSON output
	}

	err := io.PrintJSON(tokens)
	require.NoError(t, err)

	// Verify the nested nil slice is preserved as null
	jsonBytes := bytes.TrimSpace(buf.Bytes())
	var result []map[string]any
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)

	require.Len(t, result, 1)

	// The scopes field should be null (present but nil)
	scopes, exists := result[0]["scopes"]
	assert.True(t, exists, "expected scopes field to exist")
	assert.Nil(t, scopes, "expected scopes to be null")
}
