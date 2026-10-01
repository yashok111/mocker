package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"strings"
	"time"
)

const SavedViewDocumentVersion = "saved-view-v1"
const MaxSavedViewBodyBytes = 128 << 10
const MaxSavedViews = 1000
const MaxSavedViewVersions = 1000
const MaxSavedViewBytes = 64 << 20

type SavedViewSelection struct {
	RecordType string `json:"recordType"`
	ID         string `json:"id"`
}
type SavedViewPosition struct {
	NodeID string  `json:"nodeId"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}
type SavedFlowViewScope struct {
	EntrypointID string `json:"entrypointId,omitempty"`
	FlowID       string `json:"flowId,omitempty"`
	DataNodeID   string `json:"dataNodeId,omitempty"`
}
type SavedDatabaseViewScope struct {
	DatastoreID string `json:"datastoreId"`
	FacetKey    string `json:"facetKey"`
}
type SavedFlowViewFilters struct {
	Search            string `json:"search"`
	AccessKind        string `json:"accessKind"`
	ReverseAccessKind string `json:"reverseAccessKind"`
}
type SavedDatabaseViewFilters struct {
	Search              string `json:"search"`
	RelationshipTableID string `json:"relationshipTableId,omitempty"`
}
type SavedFlowViewState struct {
	Kind              string               `json:"kind"`
	Scope             SavedFlowViewScope   `json:"scope"`
	Filters           SavedFlowViewFilters `json:"filters"`
	Selection         *SavedViewSelection  `json:"selection"`
	Positions         []SavedViewPosition  `json:"positions"`
	CollapsedGroupIDs []string             `json:"collapsedGroupIds"`
}
type SavedDatabaseViewState struct {
	Kind              string                   `json:"kind"`
	Scope             SavedDatabaseViewScope   `json:"scope"`
	Filters           SavedDatabaseViewFilters `json:"filters"`
	Selection         *SavedViewSelection      `json:"selection"`
	Positions         []SavedViewPosition      `json:"positions"`
	CollapsedGroupIDs []string                 `json:"collapsedGroupIds"`
}

// Exactly one variant is present. The wire representation has no wrapper.
type SavedViewState struct {
	Flow     *SavedFlowViewState     `json:"-"`
	Database *SavedDatabaseViewState `json:"-"`
}

func (s SavedViewState) MarshalJSON() ([]byte, error) {
	if (s.Flow == nil) == (s.Database == nil) {
		return nil, invalid("state", "Select exactly one saved view kind")
	}
	if s.Flow != nil {
		return json.Marshal(s.Flow)
	}
	return json.Marshal(s.Database)
}
func (s *SavedViewState) UnmarshalJSON(b []byte) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("state", "Expected a strict state object")
	}
	var kind string
	if err := json.Unmarshal(m["kind"], &kind); err != nil {
		return invalid("state.kind", "Expected flow or database")
	}
	var out SavedViewState
	switch kind {
	case "flow":
		out.Flow = new(SavedFlowViewState)
		err = savedViewDecode(b, out.Flow, "state")
	case "database":
		out.Database = new(SavedDatabaseViewState)
		err = savedViewDecode(b, out.Database, "state")
	default:
		return invalid("state.kind", "Expected flow or database")
	}
	if err != nil {
		return err
	}
	if err := validateSavedViewState(out); err != nil {
		return err
	}
	*s = out
	return nil
}
func (s SavedViewState) kind() string {
	if s.Flow != nil {
		return "flow"
	}
	if s.Database != nil {
		return "database"
	}
	return ""
}

type CreateSavedViewInput struct {
	Name           string            `json:"name"`
	Target         BackendReadTarget `json:"target"`
	State          SavedViewState    `json:"state"`
	IdempotencyKey string            `json:"idempotencyKey"`
}
type SaveSavedViewInput struct {
	Name            string         `json:"name"`
	State           SavedViewState `json:"state"`
	ExpectedVersion int64          `json:"expectedVersion"`
	IdempotencyKey  string         `json:"idempotencyKey"`
}
type SavedViewListInput struct {
	Kind   string
	Limit  int
	Cursor string
}
type GetSavedViewInput struct{ Version int64 }
type SavedViewPins struct {
	RevisionID   string            `json:"revisionId"`
	SemanticHash string            `json:"semanticHash"`
	Proposal     *ProposalReadPins `json:"proposal"`
}
type SavedView struct {
	ID              string            `json:"id"`
	ProjectID       string            `json:"projectId"`
	Version         int64             `json:"version"`
	DocumentVersion string            `json:"documentVersion"`
	Name            string            `json:"name"`
	Target          BackendReadTarget `json:"target"`
	Pins            SavedViewPins     `json:"pins"`
	State           SavedViewState    `json:"state"`
	CreatedAt       time.Time         `json:"createdAt"`
	UpdatedAt       time.Time         `json:"updatedAt"`
	receiptJSON     string
}

func (s SavedView) MarshalJSON() ([]byte, error) {
	if s.receiptJSON != "" {
		return []byte(s.receiptJSON), nil
	}
	type document SavedView
	return json.Marshal(document(s))
}

type SavedViewSummary struct {
	ID        string            `json:"id"`
	ProjectID string            `json:"projectId"`
	Version   int64             `json:"version"`
	Name      string            `json:"name"`
	Kind      string            `json:"kind"`
	Target    BackendReadTarget `json:"target"`
	CreatedAt time.Time         `json:"createdAt"`
	UpdatedAt time.Time         `json:"updatedAt"`
}
type SavedViewPage struct {
	Items      []SavedViewSummary `json:"items"`
	NextCursor string             `json:"nextCursor"`
}

func (in *CreateSavedViewInput) UnmarshalJSON(b []byte) error {
	type input CreateSavedViewInput
	var out input
	if err := savedViewDecode(b, &out, "body"); err != nil {
		return err
	}
	if err := validateSavedViewTarget(out.Target); err != nil {
		return err
	}
	if _, err := normalizeName(out.Name); err != nil {
		return err
	}
	if err := validateKey(out.IdempotencyKey); err != nil {
		return err
	}
	*in = CreateSavedViewInput(out)
	return nil
}
func (in *SaveSavedViewInput) UnmarshalJSON(b []byte) error {
	type input SaveSavedViewInput
	var out input
	if err := savedViewDecode(b, &out, "body"); err != nil {
		return err
	}
	if out.ExpectedVersion <= 0 {
		return invalid("expectedVersion", "Use a positive signed int64")
	}
	if _, err := normalizeName(out.Name); err != nil {
		return err
	}
	if err := validateKey(out.IdempotencyKey); err != nil {
		return err
	}
	*in = SaveSavedViewInput(out)
	return nil
}

// Presence is validated before decoding can erase absent/null/empty values.
func savedViewDecode(b []byte, out any, path string) error {
	if err := savedViewWire(b, reflect.TypeOf(out).Elem(), path); err != nil {
		return err
	}
	return json.Unmarshal(b, out, json.RejectUnknownMembers(true))
}
func savedViewWire(b []byte, typ reflect.Type, path string) error {
	if bytes.Equal(bytes.TrimSpace(b), []byte("null")) {
		if path == "state.selection" {
			return nil
		}
		return invalid(path, "Null is not allowed")
	}
	if typ == reflect.TypeFor[SavedViewState]() {
		return nil
	} // Variant decoder validates its own members.
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	switch typ.Kind() {
	case reflect.Struct:
		m, err := relationalObject(b)
		if err != nil {
			return invalid(path, "Expected a strict object")
		}
		if typ == reflect.TypeFor[BackendReadTarget]() {
			_, a := m["revisionId"]
			_, c := m["proposal"]
			if a == c {
				return invalid(path, "Select exactly one source or proposal")
			}
		}
		allowed := map[string]bool{}
		for i := range typ.NumField() {
			f := typ.Field(i)
			tag := f.Tag.Get("json")
			if tag == "" || tag == "-" {
				continue
			}
			name, options, _ := strings.Cut(tag, ",")
			allowed[name] = true
			raw, exists := m[name]
			optional := strings.Contains(options, "omit")
			if !exists {
				if !optional {
					return invalid(path+"."+name, "Required member is absent")
				}
				continue
			}
			if optional && f.Type.Kind() == reflect.String && bytes.Equal(raw, []byte(`""`)) {
				return invalid(path+"."+name, "Optional members must be omitted when empty")
			}
			if err := savedViewWire(raw, f.Type, path+"."+name); err != nil {
				return err
			}
		}
		for key := range m {
			if !allowed[key] {
				return invalid(path+"."+key, "Unknown member")
			}
		}
	case reflect.Slice:
		var values []jsontext.Value
		if err := json.Unmarshal(b, &values); err != nil {
			return invalid(path, "Expected an array")
		}
		for _, v := range values {
			if err := savedViewWire(v, typ.Elem(), path); err != nil {
				return err
			}
		}
	}
	return nil
}
