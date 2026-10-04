package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func (n *ImportNode) UnmarshalJSON(raw []byte) error {
	type plain ImportNode
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	var members map[string]jsontext.Value
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	_, value.parentKeyPresent = members["parentKey"]
	value.nullParentRef = bytes.Equal(bytes.TrimSpace(members["parentRef"]), []byte("null"))
	*n = ImportNode(value)
	return nil
}

func (e *ImportEdge) UnmarshalJSON(raw []byte) error {
	type plain ImportEdge
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	var members map[string]jsontext.Value
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	_, from := members["fromKey"]
	_, to := members["toKey"]
	value.legacyKeysPresent = from || to
	value.nullEndpointRef = bytes.Equal(bytes.TrimSpace(members["fromRef"]), []byte("null")) || bytes.Equal(bytes.TrimSpace(members["toRef"]), []byte("null"))
	*e = ImportEdge(value)
	return nil
}

func validateComposedWireMembers(c ImportCommand) error {
	if n := c.Node; n != nil {
		if n.parentKeyPresent || n.ParentKey != nil {
			return semantic("node.parentKey", "Source6 forbids legacy parentKey even when empty or null")
		}
		if n.nullParentRef {
			return semantic("node.parentRef", "Source6 parentRef cannot be null")
		}
	}
	if e := c.Edge; e != nil {
		if e.legacyKeysPresent || e.FromKey != "" || e.ToKey != "" {
			return semantic("edge", "Source6 forbids legacy endpoint keys even when empty or null")
		}
		if e.nullEndpointRef {
			return semantic("edge", "Source6 endpoint refs cannot be null")
		}
	}
	return nil
}
