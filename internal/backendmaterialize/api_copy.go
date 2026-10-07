package backendmaterialize

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func (s *Service) copyAPIObject(ctx context.Context, tx *sql.Tx, installation string, g *backendmodel.EffectiveGraphSnapshot, c *Command) error {
	pin, err := c.CopyFrom.LocalPin(installation)
	if err != nil {
		return err
	}
	if pin.Kind != "api_design" || !containsPin(g, pin, *c.CopyFrom) {
		return invalid("Copy source must be an exact API pin in the selected proposal")
	}
	rev, err := s.apis.VerifiedRevisionTx(ctx, tx, ownerID(pin.ID), ownerID(pin.RevisionID))
	if err != nil {
		return err
	}
	if rev.Hash != pin.ContentHash {
		return conflict("Copy source digest differs")
	}
	selector := *c.Selector
	object, err := apidesign.ResolveArtifactObject(ctx, &apidesign.ArtifactSnapshot{DesignID: rev.DesignID, RevisionID: rev.ID, ContentHash: rev.Hash, Document: rev.Document}, apidesign.ArtifactSelector{ObjectKey: selector.ObjectKey, JSONPointer: selector.JSONPointer})
	if err != nil {
		return err
	}
	// Copy only authored operations or component schemas. Broader owner edits use
	// an explicit typed replacement and the owner's normal validators.
	segments := strings.Split(c.Destination, "/")
	operation := selector.ObjectKey != "" && len(segments) == 4 && segments[1] == "paths" && slices.Contains([]string{"get", "post", "put", "patch", "delete", "options", "head", "trace"}, segments[3])
	schema := selector.JSONPointer != "" && len(segments) == 4 && segments[1] == "components" && segments[2] == "schemas" && segments[3] != ""
	if !operation && !schema {
		return invalid("Copy destination must be an operation or component schema")
	}
	// Splice the one member with jsontext so every untouched subtree and the
	// copied object keep their exact bytes. Decoding into map[string]any made
	// every number a float64: an int64 maximum 9223372036854775807 was saved as
	// 9223372036854775808 and a 1.0 default as 1, and the mangled document was
	// hashed into the candidate with no diagnostic (review 2026-10-06, F125).
	names := make([]string, 0, len(segments)-1)
	for _, segment := range segments[1:] {
		names = append(names, strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~"))
	}
	if !jsontext.Value(object.Document).IsValid() {
		return invalid("Copy source object is not valid JSON")
	}
	raw, err := spliceJSONMember(jsontext.Value(c.APIDocument), names, jsontext.Value(object.Document), true)
	if err != nil {
		return err
	}
	c.APIDocument = string(raw)
	c.CopyFrom = nil
	c.Selector = nil
	c.Destination = ""
	c.Type = "replace_api_document"
	return nil
}

type jsonMember struct {
	name  string
	value jsontext.Value
}

var errNotObject = errors.New("not a JSON object")

// spliceJSONMember sets path inside the object doc to value, creating missing
// intermediate objects. Member order and every other value's bytes are kept;
// duplicate names are refused by the decoder, as json.Unmarshal refused them.
func spliceJSONMember(doc jsontext.Value, path []string, value jsontext.Value, root bool) (jsontext.Value, error) {
	members, err := jsonObjectMembers(doc)
	if errors.Is(err, errNotObject) {
		if root {
			return nil, invalid("Destination API must be an object")
		}
		return nil, invalid("Copy destination traverses a non-object")
	}
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(members, func(m jsonMember) bool { return m.name == path[0] })
	if i < 0 {
		members = append(members, jsonMember{name: path[0], value: jsontext.Value("{}")})
		i = len(members) - 1
	}
	if len(path) == 1 {
		members[i].value = value
	} else if members[i].value, err = spliceJSONMember(members[i].value, path[1:], value, false); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := jsontext.NewEncoder(&buf)
	if err := enc.WriteToken(jsontext.BeginObject); err != nil {
		return nil, err
	}
	for _, m := range members {
		if err := enc.WriteToken(jsontext.String(m.name)); err != nil {
			return nil, err
		}
		if err := enc.WriteValue(m.value); err != nil {
			return nil, err
		}
	}
	if err := enc.WriteToken(jsontext.EndObject); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

func jsonObjectMembers(doc jsontext.Value) ([]jsonMember, error) {
	dec := jsontext.NewDecoder(bytes.NewReader(doc))
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, err
	}
	if tok.Kind() != '{' {
		return nil, errNotObject
	}
	members := []jsonMember{}
	for dec.PeekKind() != '}' {
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, err
		}
		// A Token is voided by the next decoder call: copy the name first.
		name := tok.String()
		value, err := dec.ReadValue()
		if err != nil {
			return nil, err
		}
		members = append(members, jsonMember{name: name, value: value.Clone()})
	}
	if _, err := dec.ReadToken(); err != nil {
		return nil, err
	}
	if _, err := dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, errors.Join(errors.New("trailing data after destination JSON"), err)
	}
	return members, nil
}
