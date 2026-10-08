package backendmodel

import "slices"

type ImportCardinalities struct {
	Nodes    int64 `json:"nodes"`
	Edges    int64 `json:"edges"`
	Evidence int64 `json:"evidence"`
}
type ImportPreflightInput struct {
	Profile       string              `json:"profile"`
	Counts        ImportCardinalities `json:"counts"`
	SemanticBytes *int64              `json:"semanticBytes,omitzero"`
	Surfaces      []string            `json:"surfaces"`
}
type ImportStorageAvailability struct {
	Status  string           `json:"status"`
	Reasons []string         `json:"reasons"`
	Limits  map[string]int64 `json:"limits"`
}
type ImportConsumerAvailability struct {
	Surface     string           `json:"surface"`
	Status      string           `json:"status"`
	Reasons     []string         `json:"reasons"`
	Remedy      string           `json:"remedy"`
	Admission   map[string]int64 `json:"admission"`
	Traversal   map[string]int64 `json:"traversal"`
	Response    map[string]int64 `json:"response"`
	Concurrency map[string]int64 `json:"concurrency"`
}
type ImportPreflight struct {
	Version       string                       `json:"version"`
	Basis         string                       `json:"basis"`
	Profile       string                       `json:"profile"`
	Counts        ImportCardinalities          `json:"counts"`
	SemanticBytes *int64                       `json:"semanticBytes,omitzero"`
	Storage       ImportStorageAvailability    `json:"storage"`
	Consumers     []ImportConsumerAvailability `json:"consumers"`
}

func (in *ImportPreflightInput) UnmarshalJSON(b []byte) error {
	type plain ImportPreflightInput
	*in = ImportPreflightInput{}
	return strictAPIObject(b, []string{"profile", "counts", "surfaces"}, []string{"semanticBytes"}, (*plain)(in))
}
func (in *ImportCardinalities) UnmarshalJSON(b []byte) error {
	type plain ImportCardinalities
	*in = ImportCardinalities{}
	return strictAPIObject(b, []string{"nodes", "edges", "evidence"}, nil, (*plain)(in))
}

// Preflight only predicts admission from the supplied final graph cardinalities.
// Structural validation, actual traversals and source fidelity remain separate.
// In particular, small pages do not change Events' whole-graph admission rule.
func PlanImport(in ImportPreflightInput) (*ImportPreflight, error) {
	if !slices.Contains([]string{GraphProfile, RelationalProfile, RuntimeProfile, LineageProfile, EventsProfile, ComposedProfile}, in.Profile) {
		return nil, invalid("profile", "Select an advertised source profile")
	}
	if in.Counts.Nodes < 0 || in.Counts.Edges < 0 || in.Counts.Evidence < 0 || in.SemanticBytes != nil && *in.SemanticBytes < 0 {
		return nil, invalid("counts", "Counts and byte estimates must be nonnegative")
	}
	if len(in.Surfaces) == 0 || len(in.Surfaces) > 5 {
		return nil, invalid("surfaces", "Select one to five consumer surfaces")
	}
	out := &ImportPreflight{Version: "import-preflight-v1", Basis: "declared", Profile: in.Profile, Counts: in.Counts, SemanticBytes: in.SemanticBytes, Consumers: []ImportConsumerAvailability{}, Storage: ImportStorageAvailability{Status: "within_limits", Reasons: []string{}, Limits: map[string]int64{"maxNodes": MaxRevisionNodes, "maxEdges": MaxRevisionEdges, "maxEvidence": MaxRevisionEvidence, "maxSemanticBytes": MaxRevisionBytes, "maxBatchCommands": MaxImportCommands, "maxBatchBytes": MaxImportBatchBytes}}}
	storage := &out.Storage
	if in.SemanticBytes == nil {
		storage.Status = "byte_estimate_required"
		storage.Reasons = append(storage.Reasons, "semantic_bytes_unknown")
	}
	for _, check := range []struct {
		value, limit int64
		reason       string
	}{{in.Counts.Nodes, MaxRevisionNodes, "node_limit"}, {in.Counts.Edges, MaxRevisionEdges, "edge_limit"}, {in.Counts.Evidence, MaxRevisionEvidence, "evidence_limit"}} {
		if check.value > check.limit {
			storage.Reasons = append(storage.Reasons, check.reason)
			storage.Status = "blocked"
		}
	}
	if in.SemanticBytes != nil && *in.SemanticBytes > MaxRevisionBytes {
		storage.Status = "blocked"
		storage.Reasons = append(storage.Reasons, "semantic_byte_limit")
	}
	seen := map[string]bool{}
	for _, surface := range in.Surfaces {
		if seen[surface] {
			return nil, invalid("surfaces", "Duplicate consumer surface")
		}
		seen[surface] = true
		consumer, err := importConsumerPreflight(surface, in)
		if err != nil {
			return nil, err
		}
		out.Consumers = append(out.Consumers, consumer)
	}
	return out, nil
}

func importConsumerPreflight(surface string, in ImportPreflightInput) (ImportConsumerAvailability, error) {
	c := ImportConsumerAvailability{Surface: surface, Status: "scope_check_required", Reasons: []string{}, Remedy: "Validate the candidate, then inspect the exact selected scope and all result pages; source storage does not prove complete consumer coverage.", Admission: map[string]int64{}, Traversal: map[string]int64{}, Response: map[string]int64{"maxPageItems": MaxGraphPageSize}, Concurrency: map[string]int64{}}
	switch surface {
	case "graph":
		c.Status = "cardinality_admitted"
		c.Admission = map[string]int64{"maxNodes": MaxRevisionNodes, "maxEdges": MaxRevisionEdges, "maxEvidence": MaxRevisionEvidence}
	case "events":
		c.Response["maxPageItems"] = MaxPageSize
		c.Admission["maxTotalEdges"] = EventsMaxExaminedEdges
		c.Traversal = map[string]int64{"maxItems": EventsMaxItems, "maxAuxiliaryRecords": EventsMaxAuxiliaryRecords, "maxWitnessRecords": EventsMaxWitnessRecords}
		if in.Profile != EventsProfile && in.Profile != ComposedProfile {
			c.Status = "unsupported_profile"
			c.Reasons = append(c.Reasons, "events_profile_required")
		} else if in.Counts.Edges > EventsMaxExaminedEdges {
			c.Status = "blocked"
			c.Reasons = append(c.Reasons, "edge_limit")
			c.Remedy = "Keep the complete source graph. Generic graph/evidence reads remain available; smaller pages and service filters cannot repair Events admission. No lossless Events continuation is currently supported."
		}
	case "data_access":
		c.Response["maxPageItems"] = MaxPageSize
		c.Traversal = map[string]int64{"maxCallHops": runtimeMaxCallHops, "maxVisitedStates": runtimeMaxStates, "maxExaminedEdges": runtimeMaxExaminedEdges, "maxAccessPairs": runtimeMaxAccessPairs, "maxWitnessEdges": runtimeMaxWitnessEdges}
		if !hasRuntimeProfile(in.Profile) {
			c.Status = "unsupported_profile"
			c.Reasons = append(c.Reasons, "runtime_profile_required")
		}
	case "lineage":
		c.Response["maxPageItems"] = MaxPageSize
		c.Traversal = map[string]int64{"maxVisitedValues": lineageMaxVisitedValues, "maxExaminedMappings": lineageMaxExaminedMappings, "maxReferenceIncidences": lineageMaxReferenceIncidences}
		if !hasLineageProfile(in.Profile) {
			c.Status = "unsupported_profile"
			c.Reasons = append(c.Reasons, "lineage_profile_required")
		}
	case "architecture":
		c.Concurrency["maxBuilders"] = MaxArchitectureReadConcurrency
		c.Traversal["maxExaminedEdges"] = 250000
		c.Admission = map[string]int64{"maxElementMembers": MaxArchitectureElementMembers, "maxDocumentMembers": MaxArchitectureDocumentMembers, "maxDocumentBytes": 1 << 20}
		c.Remedy = "Preview an explicit exact membership document before saving; use compact-v1 pages and retry the same pins after backend_projection_busy. Membership and proof quality cannot be inferred from graph counts."
	default:
		return c, invalid("surfaces", "Unknown consumer surface")
	}
	if in.Counts.Nodes > MaxRevisionNodes || in.Counts.Edges > MaxRevisionEdges || in.Counts.Evidence > MaxRevisionEvidence {
		c.Status = "blocked"
		c.Reasons = append(c.Reasons, "storage_cardinality_limit")
	}
	return c, nil
}
