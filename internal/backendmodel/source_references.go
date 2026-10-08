package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type SourceReferenceSite struct {
	Path, RecordType, ExpectedKind, Relation string
	Ref                                      ImportRecordRef
	Property                                 TypedSourcePropertySelector
	ValueContext                             *LineageValueRef
}

type sourceReferenceResolver func(SourceReferenceSite) (BaseAssertionRef, error)

// sourceReferenceMember names one declared wire member and its normalized site.
// Site.Path is the containing object; convert appends the output member/index.
type sourceReferenceMember struct {
	input, output string
	site          SourceReferenceSite
	array         bool
}

type sourcePayloadNormalizer struct {
	payload  SourceAssertionPayload
	session  *ImportSession
	resolve  sourceReferenceResolver
	evidence func(string) (string, error)
	bindings []SourceDependencyBinding
}

// normalizeSourcePayload visits declared reference members only. Arbitrary keys,
// native text and historical target descriptors are never treated as addresses.
func normalizeSourcePayload(
	c ImportCommand,
	s *ImportSession,
	resolve sourceReferenceResolver,
	evidence func(string) (string, error),
) (SourceAssertionPayload, []SourceDependencyBinding, error) {
	p, err := sourceCommandPayload(c)
	if err != nil {
		return p, nil, err
	}
	n := sourcePayloadNormalizer{
		payload: p, session: s, resolve: resolve, evidence: evidence,
		bindings: []SourceDependencyBinding{},
	}
	if err := n.normalizeEndpoints(c); err != nil {
		return n.payload, nil, err
	}
	if err := n.normalizeAttributes(); err != nil {
		return n.payload, nil, err
	}
	if err := n.normalizeFacets(); err != nil {
		return n.payload, nil, err
	}
	p = n.payload
	if err := validateRepresentationAttributes(p.Kind, p.Attributes, p.RecordType == "edge", true); err != nil {
		return p, nil, err
	}
	slices.SortFunc(n.bindings, func(a, b SourceDependencyBinding) int { return strings.Compare(a.Site, b.Site) })
	return p, n.bindings, nil
}

func sourceCommandPayload(c ImportCommand) (SourceAssertionPayload, error) {
	p := SourceAssertionPayload{RecordType: "node", Attributes: map[string]jsontext.Value{}}
	switch {
	case c.Node != nil:
		p.Kind, p.Name, p.Attributes = c.Node.Kind, c.Node.Name, c.Node.Attributes
	case c.Edge != nil:
		p.RecordType, p.Kind, p.Attributes = "edge", c.Edge.Kind, c.Edge.Attributes
	default:
		return p, semantic("command", "Node or edge required")
	}
	raw, err := json.Marshal(p.Attributes)
	if err != nil {
		return p, err
	}
	var attrs map[string]jsontext.Value
	if err := json.Unmarshal(raw, &attrs); err != nil {
		return p, err
	}
	p.Attributes = attrs
	return p, nil
}

// decodeSourceMember decodes one caller-supplied source6 member and answers a
// decoder failure as a 422 at the member's path. Review 2026-10-06, F49: these
// members are jsontext.Value on the wire, so no admin shape check sees them,
// and the raw decoder error ("unexpected end of JSON input" for a facet
// without its required evidenceKeys) reached backendError as a logged 500
// backend_internal with no path. A FaultError a custom UnmarshalJSON already
// returned (ImportRecordRef) is kept as it is.
func decodeSourceMember(path string, raw jsontext.Value, out any) error {
	err := json.Unmarshal(raw, out)
	if err == nil {
		return nil
	}
	if fault, ok := errors.AsType[*FaultError](err); ok {
		return fault
	}
	return semantic(path, "Source member is missing or does not match the source6 schema")
}

func (n *sourcePayloadNormalizer) reference(raw jsontext.Value, site SourceReferenceSite) (jsontext.Value, error) {
	if err := decodeSourceMember(site.Path, raw, &site.Ref); err != nil {
		return nil, err
	}
	target, err := n.resolve(site)
	if err != nil {
		return nil, err
	}
	binding := SourceDependencyBinding{Basis: "candidate", Target: target, Site: site.Path, Property: site.Property}
	if site.Ref.Base != nil {
		binding.Basis, binding.BaseRevisionID = "base", n.session.BaseRevisionID
	}
	n.bindings = append(n.bindings, binding)
	return json.Marshal(target.ExpectedID)
}

func (n *sourcePayloadNormalizer) normalizeEndpoints(c ImportCommand) error {
	if c.Node != nil {
		if c.Node.ParentKey != nil {
			return semantic("parentKey", "Source6 requires parentRef")
		}
		if c.Node.ParentRef == nil {
			return nil
		}
		raw, _ := json.Marshal(c.Node.ParentRef)
		id, err := n.reference(raw, SourceReferenceSite{
			Path: "/parentId", RecordType: "node", Relation: "same_repository",
			Property: TypedSourcePropertySelector{Kind: "parent"},
		})
		if err != nil {
			return err
		}
		n.payload.ParentID = new(runtimeString(id))
		return nil
	}
	legacyKeys := c.Edge.FromKey != "" || c.Edge.ToKey != ""
	missingRefs := c.Edge.FromRef == nil || c.Edge.ToRef == nil
	if legacyKeys || missingRefs {
		return semantic("edge", "Source6 requires fromRef and toRef exclusively")
	}
	relation := "explicit"
	if slices.Contains([]string{
		"contains", "references", "next", "branch", "error", "returns",
		"begins", "commits", "rolls_back", "reads", "writes", "deletes",
	}, c.Edge.Kind) {
		relation = "same_repository"
	}
	for _, endpoint := range []struct {
		name  string
		value *ImportRecordRef
		dest  *string
	}{
		{name: "from", value: c.Edge.FromRef, dest: &n.payload.From},
		{name: "to", value: c.Edge.ToRef, dest: &n.payload.To},
	} {
		raw, _ := json.Marshal(endpoint.value)
		id, err := n.reference(raw, SourceReferenceSite{
			Path: "/" + endpoint.name, RecordType: "node", Relation: relation,
			Property: TypedSourcePropertySelector{Kind: "edge_endpoints"},
		})
		if err != nil {
			return err
		}
		*endpoint.dest = runtimeString(id)
	}
	return nil
}

func (n *sourcePayloadNormalizer) convert(m map[string]jsontext.Value, member sourceReferenceMember) error {
	site := member.site
	site.Path += "/" + member.output
	if _, persisted := m[member.output]; persisted {
		return semantic(site.Path, "Fresh source6 references require a scoped Ref member")
	}
	value, ok := m[member.input]
	if !ok {
		return nil
	}
	if member.array {
		var values []jsontext.Value
		if err := decodeSourceMember(site.Path, value, &values); err != nil {
			return err
		}
		for i := range values {
			indexed := site
			indexed.Path = fmt.Sprintf("%s/%d", site.Path, i)
			var err error
			values[i], err = n.reference(values[i], indexed)
			if err != nil {
				return err
			}
		}
		value, _ = json.Marshal(values)
	} else {
		var err error
		value, err = n.reference(value, site)
		if err != nil {
			return err
		}
	}
	delete(m, member.input)
	m[member.output] = value
	return nil
}

func (n *sourcePayloadNormalizer) attribute(input, output, kind string, array bool) error {
	relation := "same_repository"
	if eventsSubject(n.payload.Kind, n.payload.RecordType == "edge") && n.payload.RecordType == "edge" {
		relation = "explicit"
	}
	return n.convert(n.payload.Attributes, sourceReferenceMember{
		input: input, output: output, array: array,
		site: SourceReferenceSite{
			Path: "/attributes", RecordType: "node", ExpectedKind: kind, Relation: relation,
			Property: TypedSourcePropertySelector{Kind: "attributes", Group: output},
		},
	})
}

func (n *sourcePayloadNormalizer) normalizeAttributes() error {
	switch n.payload.Kind {
	case "flow":
		if err := n.attribute("entryStepRef", "entryStepId", "flow_step", false); err != nil {
			return err
		}
		return n.attribute("exitStepRefs", "exitStepIds", "flow_step", true)
	case "flow_step":
		return n.normalizeTransactionContext()
	case "transaction", "reads", "writes", "deletes":
		return n.attribute("datastoreRef", "datastoreId", "datastore", false)
	case "emits":
		return n.attribute("channelRef", "channelId", "channel", false)
	case "delivered_to", "retries", "dead_letters":
		return n.attribute("messageRef", "messageId", "message", false)
	case "field_mapping":
		return n.normalizeMapping()
	}
	return nil
}

func (n *sourcePayloadNormalizer) normalizeTransactionContext() error {
	raw, ok := n.payload.Attributes["transactionContext"]
	if !ok {
		return nil
	}
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if runtimeString(m["status"]) == "known" {
		err = n.convert(m, sourceReferenceMember{
			input: "transactionRef", output: "transactionId",
			site: SourceReferenceSite{
				Path: "/attributes/transactionContext", RecordType: "node", ExpectedKind: "transaction", Relation: "same_repository",
				Property: TypedSourcePropertySelector{Kind: "attributes", Group: "transactionContext"},
			},
		})
		if err != nil {
			return err
		}
	}
	n.payload.Attributes["transactionContext"], _ = json.Marshal(m)
	return nil
}

func (n *sourcePayloadNormalizer) mappingValue(
	raw jsontext.Value,
	path string,
	property TypedSourcePropertySelector,
) (jsontext.Value, error) {
	m, err := relationalObject(raw)
	if err != nil {
		return nil, err
	}
	expected := runtimeString(m["kind"])
	if expected == "port" {
		expected = "port_owner"
	}
	start := len(n.bindings)
	for _, member := range []struct{ input, output, typ, kind string }{
		{input: "nodeRef", output: "nodeId", typ: "node", kind: expected},
		{input: "endpointRef", output: "endpointId", typ: "node", kind: "event_endpoint"},
		{input: "routeRef", output: "routeId", typ: "edge", kind: "event_route"},
	} {
		err := n.convert(m, sourceReferenceMember{
			input: member.input, output: member.output,
			site: SourceReferenceSite{
				Path: path, RecordType: member.typ, ExpectedKind: member.kind, Relation: "explicit", Property: property,
			},
		})
		if err != nil {
			return nil, err
		}
	}
	normalized, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	var valueContext LineageValueRef
	if err := json.Unmarshal(normalized, &valueContext); err != nil {
		return nil, err
	}
	for i := start; i < len(n.bindings); i++ {
		n.bindings[i].ValueContext = &valueContext
	}
	return normalized, nil
}

func (n *sourcePayloadNormalizer) normalizeMapping() error {
	attrs := n.payload.Attributes
	var sources []jsontext.Value
	if err := decodeSourceMember("/attributes/sources", attrs["sources"], &sources); err != nil {
		return err
	}
	for i := range sources {
		var err error
		sources[i], err = n.mappingValue(
			sources[i], fmt.Sprintf("/attributes/sources/%d", i), TypedSourcePropertySelector{Kind: "mapping_sources"},
		)
		if err != nil {
			return err
		}
	}
	attrs["sources"], _ = json.Marshal(sources)
	var err error
	attrs["destination"], err = n.mappingValue(
		attrs["destination"], "/attributes/destination", TypedSourcePropertySelector{Kind: "mapping_destination"},
	)
	if err != nil {
		return err
	}
	return n.normalizeTransport()
}

func (n *sourcePayloadNormalizer) normalizeTransport() error {
	raw, ok := n.payload.Attributes["transport"]
	if !ok {
		return nil
	}
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	for _, member := range []struct{ input, output, kind string }{
		{input: "emitsEdgeRef", output: "emitsEdgeId", kind: "emits"},
		{input: "deliveryEdgeRef", output: "deliveryEdgeId", kind: "delivered_to"},
	} {
		err := n.convert(m, sourceReferenceMember{
			input: member.input, output: member.output,
			site: SourceReferenceSite{
				Path: "/attributes/transport", RecordType: "edge", ExpectedKind: member.kind, Relation: "explicit",
				Property: TypedSourcePropertySelector{Kind: "attributes", Group: "transport"},
			},
		})
		if err != nil {
			return err
		}
	}
	n.payload.Attributes["transport"], _ = json.Marshal(m)
	return nil
}

func (n *sourcePayloadNormalizer) normalizeFacets() error {
	p := n.payload
	if !relationalSubject(p.Kind, p.Attributes, p.RecordType == "edge") {
		return nil
	}
	facets, root, err := relationalFacetObject(p.Kind, p.Attributes)
	if err != nil {
		return err
	}
	for _, key := range slices.Sorted(maps.Keys(facets)) {
		raw := facets[key]
		m, err := relationalObject(raw)
		if err != nil {
			return err
		}
		path := root + "/" + escapeRelationalPointer(key)
		for _, owned := range []string{"sourceSnapshotId", "freshness", "evidenceIds"} {
			if _, ok := m[owned]; ok {
				return semantic(path, "Fresh facets cannot supply server-owned proof metadata")
			}
		}
		if err := n.normalizeFacetReferences(m, path, key); err != nil {
			return err
		}
		if err := n.normalizeFacetProof(m, path); err != nil {
			return err
		}
		facets[key], _ = json.Marshal(m)
	}
	n.payload.Attributes = replaceRelationalFacets(p.Kind, p.Attributes, facets)
	return nil
}

func (n *sourcePayloadNormalizer) normalizeFacetReferences(m map[string]jsontext.Value, path, key string) error {
	for _, member := range []struct{ input, output, kind string }{
		{input: "columnRefs", output: "columnIds", kind: "column"},
		{input: "dependencyRefs", output: "dependencyIds", kind: "dependency"},
		{input: "parentRefs", output: "parentIds", kind: "migration"},
	} {
		err := n.convert(m, sourceReferenceMember{
			input: member.input, output: member.output, array: true,
			site: SourceReferenceSite{
				Path: path, RecordType: "node", ExpectedKind: member.kind, Relation: "same_repository",
				Property: TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: key, Group: member.output},
			},
		})
		if err != nil {
			return err
		}
	}
	for _, collection := range []string{"terms", "columnPairs", "changes"} {
		raw, ok := m[collection]
		if !ok {
			continue
		}
		var values []jsontext.Value
		if err := decodeSourceMember(path+"/"+collection, raw, &values); err != nil {
			return err
		}
		property := TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: key, Group: collection}
		for i, raw := range values {
			entry, err := relationalObject(raw)
			if err != nil {
				return err
			}
			site := SourceReferenceSite{
				Path: fmt.Sprintf("%s/%s/%d", path, collection, i), RecordType: "node",
				ExpectedKind: "column", Relation: "same_repository", Property: property,
			}
			if err := n.normalizeFacetEntry(entry, site); err != nil {
				return err
			}
			values[i], _ = json.Marshal(entry)
		}
		m[collection], _ = json.Marshal(values)
	}
	return nil
}

func (n *sourcePayloadNormalizer) normalizeFacetEntry(entry map[string]jsontext.Value, site SourceReferenceSite) error {
	switch site.Property.Group {
	case "terms":
		return n.convert(entry, sourceReferenceMember{input: "columnRef", output: "columnId", site: site})
	case "columnPairs":
		if err := n.convert(entry, sourceReferenceMember{input: "fromColumnRef", output: "fromColumnId", site: site}); err != nil {
			return err
		}
		return n.convert(entry, sourceReferenceMember{input: "toColumnRef", output: "toColumnId", site: site})
	case "changes":
		target, err := relationalObject(entry["target"])
		if err != nil {
			return err
		}
		if runtimeString(target["kind"]) == "candidate" {
			site.Path += "/target"
			site.ExpectedKind = "relational"
			if err := n.convert(target, sourceReferenceMember{input: "objectRef", output: "objectId", site: site}); err != nil {
				return err
			}
		}
		entry["target"], _ = json.Marshal(target)
	}
	return nil
}

func (n *sourcePayloadNormalizer) normalizeFacetProof(m map[string]jsontext.Value, path string) error {
	var keys []string
	// A missing evidenceKeys decodes as empty input and fails here, which is
	// how a facet without its required proof is refused.
	if err := decodeSourceMember(path+"/evidenceKeys", m["evidenceKeys"], &keys); err != nil {
		return err
	}
	ids := []string{}
	for _, key := range keys {
		id, err := n.evidence(key)
		if err != nil {
			return err
		}
		ids = append(ids, id)
	}
	delete(m, "evidenceKeys")
	m["evidenceIds"], _ = json.Marshal(ids)
	m["sourceSnapshotId"], _ = json.Marshal(n.session.SnapshotID)
	m["freshness"], _ = json.Marshal(AssertionFreshness{
		Status: "current", ConfirmedSnapshotID: n.session.SnapshotID, Reasons: []string{},
	})
	return nil
}

func sourceExpectedKind(expected string, target ProviderAssertion) bool {
	kind := target.Payload.Kind
	switch expected {
	case "":
		return true
	case "dependency":
		return slices.Contains([]string{"table", "view", "column", "symbol"}, kind)
	case "relational":
		return relationalSubject(kind, target.Payload.Attributes, target.RecordType == "edge")
	case "port_owner":
		return kind == "flow_step" || kind == "query"
	case "event_endpoint":
		return kind == "flow_step" || kind == "consumer"
	case "event_route":
		return kind == "emits" || kind == "delivered_to"
	}
	return kind == expected
}

func sourceAssertionRef(a ProviderAssertion) BaseAssertionRef {
	return BaseAssertionRef{
		RepositoryID: a.Owner.RepositoryID, ProviderNamespace: a.Owner.ProviderNamespace,
		RecordType: a.RecordType, ExternalKey: a.ExternalKey,
		ExpectedID: a.RecordID, AssertionHash: a.AssertionHash,
	}
}
func sourceClaimKey(typ, id, repository, provider string) string {
	return strings.Join([]string{typ, id, repository, provider}, "\x00")
}
func sourceAssertionKey(a ProviderAssertion) string {
	return sourceClaimKey(a.RecordType, a.RecordID, a.Owner.RepositoryID, a.Owner.ProviderNamespace)
}
func exactSourceAssertion(assertions []ProviderAssertion, ref BaseAssertionRef) (ProviderAssertion, error) {
	for _, a := range assertions {
		if sourceAssertionRef(a) == ref {
			return a, nil
		}
	}
	return ProviderAssertion{}, semantic("base", "Exact assertion is absent from the pinned base")
}
