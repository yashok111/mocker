// Package yamlx is the ONE importer of the YAML decoder, the way
// internal/jsonx isolates encoding/json and internal/wsmock isolates the
// WebSocket library (A8, 2026-09-02; boundary_test.go fails the build on a
// second importer). It does exactly one thing: turn a YAML document into
// the JSON bytes the rest of the tree already understands, so that
// internal/openapi never sees a YAML node, a YAML type or a YAML error.
//
// The dependency, go.yaml.in/yaml/v3 (the maintained continuation of
// gopkg.in/yaml.v3), was admitted the way P6d admitted the WebSocket
// library, on a measurement rather than a preference: one module, zero
// transitive modules, no cgo, no runtime executable memory, no goroutine of
// its own. `go list -m all | grep -ci yaml` says 1 module beside the
// indirect tools alias; go.sum gained lines for exactly one module path.
//
// Why a conversion and not a second parser branch in internal/openapi:
// YAML's data model is wider than JSON's (non-string keys, anchors,
// timestamps, multiple documents), and every one of those differences is
// a place two parsers would disagree. Rendering to JSON once, here, and
// re-decoding through the same decodeJSON every JSON document takes means
// the spec pipeline has ONE root type, one number handling
// (json.Number, never float64), and one set of errors.
package yamlx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"

	"go.yaml.in/yaml/v3"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ErrNotYAML means the input did not parse as a single YAML document.
var ErrNotYAML = errors.New("yamlx: not a YAML document")

// ErrMultipleDocuments means the input is a YAML STREAM of several
// documents (`---` separators). An OpenAPI file is one document; taking
// the first silently would import half a file without saying so.
var ErrMultipleDocuments = errors.New("yamlx: input holds more than one YAML document")

// ToJSON renders one YAML document as JSON bytes. Mapping keys become
// strings whatever YAML made of them — an OpenAPI `responses:` map is
// keyed `200:`, which YAML reads as an integer and JSON cannot key by —
// and a key that is neither a scalar nor already a string is refused
// rather than stringified into something no reader meant.
func ToJSON(raw []byte) ([]byte, error) {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var root yaml.Node
	if err := dec.Decode(&root); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotYAML, err)
	}
	var next yaml.Node
	if err := dec.Decode(&next); !errors.Is(err, io.EOF) {
		// A second document that PARSES and one that does not are refused
		// the same way: anything past the first `---` is a second document,
		// and swallowing its parse error would import the first half of a
		// file the author meant as one.
		return nil, ErrMultipleDocuments
	}
	converted, err := nodeToJSON(&root, 0)
	if err != nil {
		return nil, err
	}
	out, err := jsonx.Marshal(converted)
	if err != nil {
		return nil, fmt.Errorf("yamlx: marshal: %w", err)
	}
	return out, nil
}

// maxNodeDepth bounds the walk below; yaml.v3 refuses an alias that
// contains itself, so this is a backstop, not the mechanism.
const maxNodeDepth = 10000

// nodeToJSON walks the parsed node tree rather than a decoded `any`: the
// decoder's own scalar resolution is YAML's, not JSON's — `1.0` became the
// float 1 and re-rendered as `1`, an unquoted `2024-01-01` became a
// time.Time and re-rendered with a T00:00:00Z suffix, and a `format: date`
// example served with a time in it. Working from the node, a scalar's
// original TEXT is still there to keep: a float stays the digits the
// author wrote, a timestamp stays a string, an integer in a base JSON
// has no spelling for is rendered in decimal.
func nodeToJSON(n *yaml.Node, depth int) (any, error) {
	if depth > maxNodeDepth {
		return nil, fmt.Errorf("%w: document nests deeper than %d", ErrNotYAML, maxNodeDepth)
	}
	switch n.Kind {
	case yaml.DocumentNode:
		if len(n.Content) == 0 {
			return nil, nil
		}
		return nodeToJSON(n.Content[0], depth+1)
	case yaml.AliasNode:
		if n.Alias == nil {
			return nil, fmt.Errorf("%w: unresolved alias %q", ErrNotYAML, n.Value)
		}
		return nodeToJSON(n.Alias, depth+1)
	case yaml.SequenceNode:
		out := make([]any, len(n.Content))
		for i, c := range n.Content {
			v, err := nodeToJSON(c, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = v
		}
		return out, nil
	case yaml.MappingNode:
		return mappingToJSON(n, depth)
	case yaml.ScalarNode:
		return scalarToJSON(n)
	default:
		return nil, fmt.Errorf("%w: unexpected node kind %d", ErrNotYAML, n.Kind)
	}
}

// mappingToJSON renders a mapping; keys become the strings JSON needs (see
// keyString), and a `<<` merge key folds the aliased mapping(s) in UNDER
// the mapping's own keys, which is YAML's merge rule (an explicit key
// wins regardless of order).
func mappingToJSON(n *yaml.Node, depth int) (map[string]any, error) {
	out := make(map[string]any, len(n.Content)/2)
	var merges []*yaml.Node
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind == yaml.AliasNode && k.Alias != nil {
			k = k.Alias
		}
		if k.ShortTag() == "!!merge" {
			merges = append(merges, v)
			continue
		}
		key, err := keyString(k)
		if err != nil {
			return nil, err
		}
		val, err := nodeToJSON(v, depth+1)
		if err != nil {
			return nil, err
		}
		out[key] = val
	}
	for _, m := range merges {
		sources := []*yaml.Node{m}
		if m.Kind == yaml.SequenceNode {
			sources = m.Content
		}
		for _, src := range sources {
			v, err := nodeToJSON(src, depth+1)
			if err != nil {
				return nil, err
			}
			merged, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%w: a merge key (<<) must alias a mapping", ErrNotYAML)
			}
			for key, val := range merged {
				if _, exists := out[key]; !exists {
					out[key] = val
				}
			}
		}
	}
	return out, nil
}

// jsonNumberText is the JSON number grammar: a scalar whose text already
// fits it is carried verbatim, so `1.0` stays `1.0` and an integer past
// float64's precision stays exact.
var jsonNumberText = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][-+]?[0-9]+)?$`)

// scalarToJSON renders one scalar by the tag yaml.v3 resolves for it —
// its own core-schema resolution for a plain scalar, the explicit tag for
// a tagged one, !!str for anything quoted.
func scalarToJSON(n *yaml.Node) (any, error) {
	switch n.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!bool":
		var b bool
		if err := n.Decode(&b); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotYAML, err)
		}
		return b, nil
	case "!!int":
		if jsonNumberText.MatchString(n.Value) {
			return jsonx.Number(n.Value), nil
		}
		// 0x1F, 0o17, a leading +: YAML spellings JSON has none for.
		var i int64
		if err := n.Decode(&i); err == nil {
			return jsonx.Number(strconv.FormatInt(i, 10)), nil
		}
		var u uint64
		if err := n.Decode(&u); err == nil {
			return jsonx.Number(strconv.FormatUint(u, 10)), nil
		}
		return nil, fmt.Errorf("%w: integer %q cannot become a JSON number", ErrNotYAML, n.Value)
	case "!!float":
		if jsonNumberText.MatchString(n.Value) {
			return jsonx.Number(n.Value), nil
		}
		var f float64
		if err := n.Decode(&f); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNotYAML, err)
		}
		if math.IsInf(f, 0) || math.IsNaN(f) {
			return nil, fmt.Errorf("%w: %q has no JSON representation", ErrNotYAML, n.Value)
		}
		return jsonx.Number(strconv.FormatFloat(f, 'g', -1, 64)), nil
	default:
		// !!str, !!timestamp, !!binary and any custom tag: the text as
		// written. A timestamp in particular stays the author's own
		// `2024-01-01`, never a re-rendered instant.
		return n.Value, nil
	}
}

// keyString turns a mapping key node into the string JSON needs: a scalar
// key keeps its text (`200:` is "200", `~:` is "null"); a sequence or
// mapping key has no JSON spelling and is refused.
func keyString(k *yaml.Node) (string, error) {
	if k.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("%w: a mapping key must be a scalar to become a JSON key", ErrNotYAML)
	}
	if k.ShortTag() == "!!null" {
		return "null", nil
	}
	return k.Value, nil
}
