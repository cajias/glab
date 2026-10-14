package login

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

// errYarnAnchorOrAlias refuses edits through anchors or aliases, because a
// shared value would change entries this command was not asked to touch.
var errYarnAnchorOrAlias = errors.New("found a YAML anchor or alias on the path to the npmRegistries entry; this command cannot edit it safely, so add the entry by hand")

// loginYarn upserts an npmRegistries entry with npmAuthToken and
// npmAlwaysAuth in ~/.yarnrc.yml, which Yarn 2 and later read. It returns
// the npmScopes that keep sending their own credential instead.
//
// YARN_RC_FILENAME is ignored on purpose. Yarn reads the home file through
// findFolderRcFile(getHomeFolder()), which always uses ".yarnrc.yml".
func loginYarn(registry *url.URL, token string) ([]string, error) {
	home, err := homeDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(home, ".yarnrc.yml")

	content, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	updated, shadowed, err := upsertYarnRegistry(content, registry, token)
	if err != nil {
		return nil, fmt.Errorf("updating %s: %w", path, err)
	}

	if err := writeCredentialFile(path, updated); err != nil {
		return nil, err
	}
	return shadowed, nil
}

// upsertYarnRegistry refreshes every npmRegistries key Yarn resolves to
// registry, and adds one only when there is none. Re-encoding can change the
// formatting in other places too: indentation becomes two spaces, blank lines
// may be dropped, CRLF line endings become LF, and an entry added to a flow
// mapping such as {} stays on that one line.
func upsertYarnRegistry(content []byte, registry *url.URL, token string) ([]byte, []string, error) {
	var out bytes.Buffer

	decoder := yaml.NewDecoder(bytes.NewReader(content))
	var doc yaml.Node
	err := decoder.Decode(&doc)
	switch {
	case errors.Is(err, io.EOF):
		// A comment-only file decodes to no document, so its comments are kept
		// by writing the new document after them.
		if len(bytes.TrimSpace(content)) > 0 {
			out.Write(content)
			if !bytes.HasSuffix(content, []byte("\n")) {
				out.WriteByte('\n')
			}
		}
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	case err != nil:
		return nil, nil, fmt.Errorf("parsing YAML: %w", err)
	default:
		// Encoding would drop every document after the first. Yarn rejects
		// such a file anyway (js-yaml single-document load).
		var next yaml.Node
		if err := decoder.Decode(&next); !errors.Is(err, io.EOF) {
			return nil, nil, errors.New("found more than one YAML document, which Yarn does not load; keep a single document, or add the entry by hand")
		}
	}

	root, err := yarnMapping(doc.Content[0], "the top level")
	if err != nil {
		return nil, nil, err
	}

	registries, err := yarnUpsertMapping(root, "npmRegistries")
	if err != nil {
		return nil, nil, err
	}

	// Yarn strips one trailing "/" from keys, and getRegistryConfiguration
	// tries the scheme-qualified key before the "//" one. Refreshing only the
	// "//" key would leave a stale "https://" token winning.
	key := yarnRegistryKey(registry)
	entries, err := yarnPairs(registries, func(k string) bool {
		k = strings.TrimSuffix(k, "/")
		return k == key || k == registry.Scheme+":"+key
	})
	if err != nil {
		return nil, nil, err
	}
	if len(entries) == 0 {
		// Quoted the way Yarn's own documentation spells registry keys.
		pair := yarnPair{
			key:   &yaml.Node{Kind: yaml.ScalarNode, Style: yaml.DoubleQuotedStyle, Value: key},
			value: &yaml.Node{Kind: yaml.MappingNode},
		}
		registries.Content = append(registries.Content, pair.key, pair.value)
		entries = append(entries, pair)
	}

	for _, pair := range entries {
		entry, err := yarnMapping(pair.value, "the npmRegistries entry for "+pair.key.Value)
		if err != nil {
			return nil, nil, err
		}
		// Yarn sends npmAuthToken on unscoped fetches only when npmAlwaysAuth
		// is set (shouldAuthenticate in plugin-npm).
		if err := yarnSetScalar(entry, "npmAlwaysAuth", "!!bool", "true"); err != nil {
			return nil, nil, err
		}
		if err := yarnSetScalar(entry, "npmAuthToken", "!!str", token); err != nil {
			return nil, nil, err
		}
	}

	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(&doc); err != nil {
		return nil, nil, fmt.Errorf("encoding YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, nil, fmt.Errorf("encoding YAML: %w", err)
	}

	return out.Bytes(), yarnScopesWithOwnAuth(root, registry), nil
}

// yarnRegistryKey returns "//" plus the host and escaped path as typed, minus
// one trailing "/". Unlike loginNpm, it does not normalize the host, because
// Yarn matches keys against npmRegistryServer literally.
func yarnRegistryKey(registry *url.URL) string {
	return "//" + registry.Host + strings.TrimSuffix(registry.EscapedPath(), "/")
}

// yarnMapping turns a null value into an empty mapping. For Yarn only an
// empty value means no entries: it parses with the js-yaml FAILSAFE_SCHEMA, so
// "~" and "null" are strings there.
func yarnMapping(n *yaml.Node, what string) (*yaml.Node, error) {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return nil, errYarnAnchorOrAlias
	}
	if n.Kind == yaml.ScalarNode && n.Tag == "!!null" {
		n.Kind = yaml.MappingNode
		n.Tag = ""
		n.Value = ""
		n.Style = 0
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("found %s holding something other than a mapping, which this command cannot add an entry to; fix it, or add the entry by hand", what)
	}
	return n, nil
}

type yarnPair struct {
	key, value *yaml.Node
}

// yarnPairs refuses an alias key, since what it stands for is not visible here
// and it could be a duplicate of the key being refreshed.
func yarnPairs(m *yaml.Node, match func(key string) bool) ([]yarnPair, error) {
	var pairs []yarnPair
	for i := 0; i+1 < len(m.Content); i += 2 {
		key := m.Content[i]
		if key.Kind == yaml.AliasNode {
			return nil, errYarnAnchorOrAlias
		}
		if key.Kind == yaml.ScalarNode && match(key.Value) {
			pairs = append(pairs, yarnPair{key: key, value: m.Content[i+1]})
		}
	}
	return pairs, nil
}

// yarnUpsertMapping refuses a key that appears more than once. Yarn reads the
// last occurrence, so editing any one of them could leave the token where Yarn
// never looks.
func yarnUpsertMapping(m *yaml.Node, key string) (*yaml.Node, error) {
	pairs, err := yarnPairs(m, func(k string) bool { return k == key })
	if err != nil {
		return nil, err
	}
	switch len(pairs) {
	case 0:
		value := &yaml.Node{Kind: yaml.MappingNode}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, value)
		return value, nil
	case 1:
		return yarnMapping(pairs[0].value, key)
	default:
		return nil, fmt.Errorf("found %d %s keys, and Yarn reads only the last one; merge them into one, or add the entry by hand", len(pairs), key)
	}
}

// yarnSetScalar rewrites existing values in place so their comments survive.
func yarnSetScalar(m *yaml.Node, key, tag, value string) error {
	pairs, err := yarnPairs(m, func(k string) bool { return k == key })
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value})
		return nil
	}
	for _, pair := range pairs {
		v := pair.value
		if v.Kind == yaml.AliasNode || v.Anchor != "" {
			return errYarnAnchorOrAlias
		}
		v.Kind = yaml.ScalarNode
		v.Tag = tag
		v.Value = value
		v.Style = 0
		v.Content = nil
	}
	return nil
}

var yarnSchemeRe = regexp.MustCompile(`^[a-z]+:`)

// yarnScopesWithOwnAuth returns the npmScopes in root that Yarn fetches from
// registry with their own npmAuthToken or npmAuthIdent. getAuthConfiguration
// checks the scope before npmRegistries, so the npmRegistries token is never
// sent for them. A scope without npmRegistryServer uses the top-level one
// (getScopeRegistry).
func yarnScopesWithOwnAuth(root *yaml.Node, registry *url.URL) []string {
	scopes := yarnLastValue(root, "npmScopes")
	if scopes == nil || scopes.Kind != yaml.MappingNode {
		return nil
	}

	key := yarnRegistryKey(registry)
	var names []string
	for i := 0; i+1 < len(scopes.Content); i += 2 {
		scope := yarnDeref(scopes.Content[i+1])
		if scope.Kind != yaml.MappingNode {
			continue
		}
		if yarnScalarValue(scope, "npmAuthToken") == "" && yarnScalarValue(scope, "npmAuthIdent") == "" {
			continue
		}
		server := yarnScalarValue(scope, "npmRegistryServer")
		if server == "" {
			server = yarnScalarValue(root, "npmRegistryServer")
		}
		if yarnSchemeRe.ReplaceAllString(strings.TrimSuffix(server, "/"), "") == key {
			names = append(names, scopes.Content[i].Value)
		}
	}
	return names
}

// yarnLastValue returns the last value for key, the one Yarn reads.
func yarnLastValue(m *yaml.Node, key string) *yaml.Node {
	var value *yaml.Node
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			value = yarnDeref(m.Content[i+1])
		}
	}
	return value
}

// yarnScalarValue keeps "~" and "null" as set values, because Yarn's
// FAILSAFE_SCHEMA parse reads them as strings, and only "" as unset.
func yarnScalarValue(m *yaml.Node, key string) string {
	value := yarnLastValue(m, key)
	if value == nil || value.Kind != yaml.ScalarNode {
		return ""
	}
	return value.Value
}

func yarnDeref(n *yaml.Node) *yaml.Node {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return n.Alias
	}
	return n
}
