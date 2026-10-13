//go:build !integration

package proxy

import (
	"bytes"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestScanPackumentReadsPublishFields(t *testing.T) {
	t.Parallel()
	body := ` {"_id":"left-pad","name":"left-pad","description":"pad \"left\" \\ {not a brace}",` +
		`"dist-tags":{"latest":"1.3.0"},"versions":{"1.3.0":{"name":"left-pad","keywords":["a",["b"]]}},` +
		`"access":null,"private":false,"n":-1.5e3,"_attachments":{"left-pad-1.3.0.tgz":{"data":"H4sI\"AA"}}} `
	got, err := scanPackument(strings.NewReader(body), 1024)
	require.NoError(t, err)
	assert.Equal(t, packument{name: "left-pad", versions: []string{"1.3.0"}, attachments: true}, got)
}

func TestScanPackumentLaterKeyWins(t *testing.T) {
	t.Parallel()
	// The registry's JSON parser keeps the last occurrence of a key, so the
	// scanner must too, including a key spelled with a unicode escape.
	body := `{"name":"benign","versions":{"1.0.0":{}},"_attachments":{"x.tgz":{"data":"AAAA"}},` +
		`"na\u006de":"evil","versions":{"6.6.6":{}}}`
	got, err := scanPackument(strings.NewReader(body), 1024)
	require.NoError(t, err)
	assert.Equal(t, "evil", got.name)
	assert.Equal(t, []string{"6.6.6"}, got.versions)
}

func TestScanPackumentKeysAreCaseSensitive(t *testing.T) {
	t.Parallel()
	// encoding/json matches struct fields case-insensitively; the registry
	// does not, so "NAME" must not replace "name".
	body := `{"name":"benign","versions":{"1.0.0":{}},"_attachments":{},"NAME":"evil","Versions":{"6.6.6":{}}}`
	got, err := scanPackument(strings.NewReader(body), 1024)
	require.NoError(t, err)
	assert.Equal(t, "benign", got.name)
	assert.Equal(t, []string{"1.0.0"}, got.versions)
}

func TestScanPackumentNullAttachmentsIsNotAPublish(t *testing.T) {
	t.Parallel()
	got, err := scanPackument(strings.NewReader(`{"name":"left-pad","_attachments":null}`), 1024)
	require.NoError(t, err)
	assert.False(t, got.attachments)
}

func TestScanPackumentRejectsMalformed(t *testing.T) {
	t.Parallel()
	cases := map[string]string{
		"not an object":      `["left-pad"]`,
		"unquoted key":       `{name:"left-pad"}`,
		"missing colon":      `{"name" "left-pad"}`,
		"truncated value":    `{"name":"left-pad","_attachments":{"x.tgz":{"data":"AAA`,
		"truncated object":   `{"name":"left-pad"`,
		"non-string name":    `{"name":42}`,
		"name over the cap":  `{"name":"` + strings.Repeat("a", 64) + `"}`,
		"garbage after pair": `{"name":"left-pad" x}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, err := scanPackument(strings.NewReader(body), 32)
			assert.Error(t, err)
		})
	}
}

// decodePackumentReference decodes body the way the registry does: keys are
// case-sensitive and a repeated key replaces the earlier one.
func decodePackumentReference(body []byte) (packument, bool) {
	var top map[string]json.RawMessage
	if json.Unmarshal(body, &top) != nil || top == nil {
		return packument{}, false
	}
	var p packument
	if raw, ok := top["name"]; ok && json.Unmarshal(raw, &p.name) != nil {
		return packument{}, false
	}
	if raw, ok := top["versions"]; ok {
		var versions map[string]json.RawMessage
		if json.Unmarshal(raw, &versions) != nil {
			return packument{}, false
		}
		p.versions = slices.Sorted(maps.Keys(versions))
	}
	if raw, ok := top["_attachments"]; ok {
		p.attachments = raw[0] == '{'
	}
	return p, true
}

func FuzzScanPackumentMatchesReference(f *testing.F) {
	for _, s := range []string{
		`{"name":"a","versions":{"1.0.0":{}},"_attachments":{"a.tgz":{"data":"x\"y\\z"}}}`,
		`{"na\u006de":"b","name":"a","versions":{"1":{},"2":[1,{"x":"}"}]}}`,
		`{"_attachments":null,"name":"\ud83d\ude00","versions":{"\u0031":true}}`,
		`{"x":[[[{"y":"]"}]]],"name":"c","n":-0.5e-3,"t":true,"f":false,"z":null}`,
		`{"name":"d","NAME":"e"}`,
		` { "name" : "d" , "versions" : { } } `,
		`{}`,
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, body []byte) {
		want, ok := decodePackumentReference(body)
		if !ok {
			return
		}
		got, err := scanPackument(bytes.NewReader(body), 1<<20)
		require.NoError(t, err, "scanner rejected valid JSON %q", body)
		assert.Equal(t, want, got, "body %q", body)
	})
}
