package yamlx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ErrOutputTooLarge means a YAML export exceeded its byte budget.
var ErrOutputTooLarge = errors.New("yamlx: output exceeds byte limit")

// FromJSON writes one JSON value as YAML, retaining JSON scalar types and number text.
func FromJSON(raw []byte) ([]byte, error) { return FromJSONLimit(raw, 16<<20) }

// FromJSONLimit bounds both the input and encoded output without decoding numbers as floats.
func FromJSONLimit(raw []byte, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || int64(len(raw)) > maxBytes {
		return nil, ErrOutputTooLarge
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("yamlx: decode JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("yamlx: expected exactly one JSON value")
	}
	node, err := jsonNode(value, 0)
	if err != nil {
		return nil, err
	}
	out := limitedYAMLBuffer{limit: maxBytes}
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err := encoder.Encode(node); err != nil {
		if out.exceeded {
			return nil, ErrOutputTooLarge
		}
		return nil, fmt.Errorf("yamlx: encode YAML: %w", err)
	}
	if err := encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

type limitedYAMLBuffer struct {
	bytes.Buffer
	limit    int64
	exceeded bool
}

func (b *limitedYAMLBuffer) Write(p []byte) (int, error) {
	if int64(len(p)) > b.limit-int64(b.Len()) {
		b.exceeded = true
		return 0, ErrOutputTooLarge
	}
	return b.Buffer.Write(p)
}

func jsonNode(value any, depth int) (*yaml.Node, error) {
	if depth > 256 {
		return nil, errors.New("yamlx: JSON nesting exceeds 256")
	}
	n := &yaml.Node{Kind: yaml.ScalarNode}
	switch value := value.(type) {
	case nil:
		n.Tag, n.Value = "!!null", "null"
	case bool:
		n.Tag, n.Value = "!!bool", fmt.Sprint(value)
	case string:
		n.Tag, n.Value, n.Style = "!!str", value, yaml.DoubleQuotedStyle
	case jsonx.Number:
		n.Tag, n.Value = "!!int", value.String()
		if strings.ContainsAny(n.Value, ".eE") {
			n.Tag = "!!float"
		}
	case []any:
		n.Kind, n.Tag = yaml.SequenceNode, "!!seq"
		for _, item := range value {
			child, err := jsonNode(item, depth+1)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, child)
		}
	case map[string]any:
		n.Kind, n.Tag = yaml.MappingNode, "!!map"
		for _, key := range slices.Sorted(maps.Keys(value)) {
			child, err := jsonNode(value[key], depth+1)
			if err != nil {
				return nil, err
			}
			n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key, Style: yaml.DoubleQuotedStyle}, child)
		}
	default:
		return nil, fmt.Errorf("yamlx: unsupported JSON value %T", value)
	}
	return n, nil
}
