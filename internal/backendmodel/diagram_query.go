package backendmodel

import (
	"context"
	"encoding/base64"
	"encoding/json/v2"
	"slices"
)

type DiagramQueryInput struct {
	GapClassification string                `json:"gapClassification,omitempty"`
	GapScope          *ArchitectureGapScope `json:"gapScope,omitzero"`
	ResponseMode      string                `json:"responseMode,omitempty"`
	Pin               DiagramPin            `json:"pin"`
	Level             string                `json:"level,omitempty"`
	RootID            string                `json:"rootId,omitempty"`
	Search            string                `json:"search"`
	Origin            string                `json:"origin"`
	Section           string                `json:"section"`
	SubjectID         string                `json:"subjectId,omitempty"`
	Limit             int                   `json:"limit"`
	Cursor            string                `json:"cursor,omitempty"`
}
type ArchitectureProjectionContext struct {
	Policy string `json:"policy"`
	Level  string `json:"level"`
	RootID string `json:"rootId"`
}
type DiagramMember struct {
	Ref        DiagramRef    `json:"ref"`
	Origin     DiagramOrigin `json:"origin"`
	TargetHash string        `json:"targetHash"`
}

// Each row has exactly one typed data arm; no free-form response payloads.
type DiagramRow struct {
	BusinessElement     *BusinessElement
	BusinessLink        *BusinessLink
	State               *LifecycleState
	Transition          *LifecycleTransition
	Rule                *LifecycleRule
	Participant         *InteractionParticipant
	Step                *InteractionStep
	Branch              *InteractionBranch
	Order               *InteractionOrder
	ArchitectureElement *ArchitectureElement
	ArchitectureLink    *ArchitectureLink
	Member              *DiagramMember
	Gap                 *DiagramGap
}

func (r DiagramRow) MarshalJSON() ([]byte, error) {
	kind, data, count := marshalInteractionRow(r)
	if r.BusinessElement != nil {
		count++
		kind = "business_element"
		data = r.BusinessElement
	}
	if r.BusinessLink != nil {
		count++
		kind = "business_link"
		data = r.BusinessLink
	}
	if r.State != nil {
		count++
		kind = "state"
		data = r.State
	}
	if r.Transition != nil {
		count++
		kind = "transition"
		data = r.Transition
	}
	if r.Rule != nil {
		count++
		kind = "rule"
		data = r.Rule
	}
	if r.ArchitectureElement != nil {
		count++
		kind = "architecture_element"
		data = r.ArchitectureElement
	}
	if r.ArchitectureLink != nil {
		count++
		kind = "architecture_link"
		data = r.ArchitectureLink
	}
	if r.Member != nil {
		count++
		kind = "member"
		data = r.Member
	}
	if r.Gap != nil {
		count++
		kind = "gap"
		data = r.Gap
	}
	if count != 1 {
		return nil, invalid("row", "Exactly one diagram row arm required")
	}
	return json.Marshal(struct {
		RowType string `json:"rowType"`
		Data    any    `json:"data"`
	}{kind, data})
}

type DiagramPage struct {
	Cache      *ArchitectureCacheStatus       `json:"cache,omitzero"`
	GapSummary *DiagramGapSummary             `json:"gapSummary,omitzero"`
	Projection *ArchitectureProjectionContext `json:"projection,omitzero"`
	Pin        DiagramPin                     `json:"pin"`
	TargetHash string                         `json:"targetHash"`
	Total      int                            `json:"total"`
	NextCursor string                         `json:"nextCursor"`
	Items      []DiagramRow                   `json:"items"`
	Gaps       []DiagramGap                   `json:"gaps"`
	Truncated  bool                           `json:"truncated"`
}

// Retention is an operational property, not a gap in source knowledge.
type ArchitectureCacheStatus struct {
	Status     string `json:"status"`
	Reason     string `json:"reason,omitempty"`
	LimitBytes int    `json:"limitBytes"`
}

func (v *DiagramQueryInput) UnmarshalJSON(b []byte) error {
	type plain DiagramQueryInput
	*v = DiagramQueryInput{}
	return strictAPIObject(b, []string{"pin", "search", "origin", "section", "limit"}, []string{"level", "rootId", "subjectId", "cursor", "responseMode", "gapScope", "gapClassification"}, (*plain)(v))
}
func (in DiagramQueryInput) Validate() error {
	if err := validateArchitectureGapQuery(in); err != nil {
		return err
	}
	if in.ResponseMode != "" && in.ResponseMode != "compact-v1" {
		return invalid("responseMode", "Supported response mode is compact-v1")
	}
	if err := in.Pin.Validate(); err != nil {
		return err
	}
	if (in.Level != "" || in.RootID != "") && (!slices.Contains([]string{"context", "containers", "components"}, in.Level) || !ValidID(in.RootID)) {
		return invalid("projection", "Exact architecture level and root required")
	}
	if !slices.Contains([]string{"all", "source_assertion", "authored"}, in.Origin) || !validAPIText(in.Search, 0, 4096) {
		return invalid("filter", "Invalid architecture filter")
	}
	if !slices.Contains([]string{"elements", "links", "members", "gaps"}, in.Section) {
		return invalid("section", "Unsupported diagram section")
	}
	if in.Section == "members" && !ValidID(in.SubjectID) {
		return invalid("subjectId", "Member query requires exact projected subject")
	}
	if in.Section != "members" && in.SubjectID != "" {
		return invalid("subjectId", "Subject is only supported for member reads")
	}
	if in.Limit < 1 || in.Limit > 500 {
		return invalid("limit", "Use 1–500")
	}
	return nil
}
func (r *Repo) QueryDiagram(ctx context.Context, pid string, in DiagramQueryInput) (*DiagramPage, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	v, err := r.GetDiagram(ctx, pid, in.Pin)
	if err != nil {
		return nil, err
	}
	if v.Document.Kind == "business_map" {
		page, err := ProjectBusinessMap(ctx, v, in)
		if err != nil {
			return nil, err
		}
		if err := compactDiagramPage(ctx, page, in); err != nil {
			return nil, err
		}
		return page, nil
	}
	if v.Document.Kind == "lifecycle" {
		page, err := ProjectLifecycle(ctx, v, in)
		if err != nil {
			return nil, err
		}
		if err := compactDiagramPage(ctx, page, in); err != nil {
			return nil, err
		}
		return page, nil
	}
	if v.Document.Kind == "interactions" {
		page, err := ProjectInteractions(ctx, v, in)
		if err != nil {
			return nil, err
		}
		if err := compactDiagramPage(ctx, page, in); err != nil {
			return nil, err
		}
		return page, nil
	}
	return r.queryArchitecture(ctx, pid, v, in)
}

type diagramCursor struct {
	Scope  string `json:"scope"`
	Offset int    `json:"offset"`
}

func diagramCursorMismatch() error {
	return &FaultError{Status: 409, Code: "backend_diagram_cursor_mismatch", Message: "Cursor belongs to another exact projection or catalog"}
}
func diagramOffset(cursor, scope string, total int) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	if len(cursor) > 4096 {
		return 0, diagramCursorMismatch()
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, diagramCursorMismatch()
	}
	var c diagramCursor
	if json.Unmarshal(raw, &c, json.RejectUnknownMembers(true)) != nil || c.Scope != scope || c.Offset < 0 || c.Offset >= total {
		return 0, diagramCursorMismatch()
	}
	return c.Offset, nil
}
func diagramNext(scope string, offset, total int) string {
	if offset >= total {
		return ""
	}
	raw, _ := json.Marshal(diagramCursor{Scope: scope, Offset: offset})
	return base64.RawURLEncoding.EncodeToString(raw)
}
