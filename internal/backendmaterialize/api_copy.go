package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/json/v2"
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
	var root map[string]any
	var value any
	if err := json.Unmarshal([]byte(c.APIDocument), &root); err != nil {
		return err
	}
	if root == nil {
		return invalid("Destination API must be an object")
	}
	if err := json.Unmarshal([]byte(object.Document), &value); err != nil {
		return err
	}
	current := root
	for i, segment := range segments[1:] {
		name := strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		if i == len(segments)-2 {
			current[name] = value
			break
		}
		child, exists := current[name]
		if !exists {
			child = map[string]any{}
			current[name] = child
		}
		nested, ok := child.(map[string]any)
		if !ok {
			return invalid("Copy destination traverses a non-object")
		}
		current = nested
	}
	raw, err := json.Marshal(root)
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
