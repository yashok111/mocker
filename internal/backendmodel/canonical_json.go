package backendmodel

import (
	"bytes"
	"cmp"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"slices"
)

// Keep a parsed tree only to reorder object members. The previous RawValue
// recursion decoded every subtree again, multiplying work by nesting depth.
// Numeric text is retained verbatim: these bytes feed persisted import hashes.
type canonicalNode struct {
	name     string
	text     string
	kind     jsontext.Kind
	children []canonicalNode
}

func canonicalValue(raw []byte) ([]byte, error) {
	// TrimSpace historically admits Unicode whitespace around the root only.
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty JSON value")
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	node, err := readCanonicalNode(decoder)
	if err == nil {
		// A streaming decoder permits multiple roots; canonicalValue does not.
		if _, endErr := decoder.ReadToken(); !errors.Is(endErr, io.EOF) {
			err = endErr
			if err == nil {
				err = fmt.Errorf("multiple JSON values")
			}
		}
	}
	if err != nil {
		switch raw[0] {
		case '{', '[', '"':
			return nil, err
		default:
			return nil, fmt.Errorf("invalid JSON number")
		}
	}
	return node.appendJSON(make([]byte, 0, len(raw))), nil
}

func readCanonicalNode(decoder *jsontext.Decoder) (canonicalNode, error) {
	token, err := decoder.ReadToken()
	if err != nil {
		return canonicalNode{}, err
	}
	node := canonicalNode{kind: token.Kind()}
	switch node.kind {
	case '{', '[':
		end := jsontext.Kind(']')
		if node.kind == '{' {
			end = '}'
		}
		for decoder.PeekKind() != end {
			var name string
			if node.kind == '{' {
				key, err := decoder.ReadToken()
				if err != nil {
					return canonicalNode{}, err
				}
				name = key.String()
			}
			child, err := readCanonicalNode(decoder)
			if err != nil {
				return canonicalNode{}, err
			}
			child.name = name
			node.children = append(node.children, child)
		}
		if _, err := decoder.ReadToken(); err != nil {
			return canonicalNode{}, err
		}
		if node.kind == '{' {
			slices.SortFunc(node.children, func(a, b canonicalNode) int { return cmp.Compare(a.name, b.name) })
		}
	default:
		// Token itself borrows decoder storage. String copies its raw text (or
		// unescaped string) into an owned Go string before the next decoder call.
		// In particular, do not convert numeric tokens through Float/Int/Uint.
		node.text = token.String()
	}
	return node, nil
}

func (node canonicalNode) appendJSON(out []byte) []byte {
	switch node.kind {
	case '{', '[':
		out = append(out, byte(node.kind))
		for i, child := range node.children {
			if i > 0 {
				out = append(out, ',')
			}
			if node.kind == '{' {
				key, _ := json.Marshal(child.name)
				out = append(out, key...)
				out = append(out, ':')
			}
			out = child.appendJSON(out)
		}
		if node.kind == '{' {
			return append(out, '}')
		}
		return append(out, ']')
	case '"':
		// The same marshaler preserves the established Unicode/HTML escaping.
		value, _ := json.Marshal(node.text)
		return append(out, value...)
	default:
		return append(out, node.text...)
	}
}
