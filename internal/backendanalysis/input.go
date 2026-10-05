package backendanalysis

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

const maxInputBytes = 2 << 20
const maxResultBytes = 32 << 20
const terminalHeadroom = 128 << 10
const maxManifestBytes = 64 << 10
const maxProjectBytes = 256 << 20

func fault(status int, code, message string) error {
	return &backendmodel.FaultError{Status: status, Code: "backend_analysis_" + code, Message: message}
}
func malformed(message string) error { return fault(400, "invalid", message) }
func closed(raw []byte, required, optional []string) (map[string]jsontext.Value, error) {
	var m map[string]jsontext.Value
	if len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' || json.Unmarshal(raw, &m) != nil {
		return nil, malformed("Expected object")
	}
	for k, v := range m {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return nil, malformed("Null member: " + k)
		}
		if !slices.Contains(required, k) && !slices.Contains(optional, k) {
			return nil, malformed("Unknown member: " + k)
		}
	}
	for _, k := range required {
		if _, ok := m[k]; !ok {
			return nil, malformed("Required member: " + k)
		}
	}
	return m, nil
}
func decode(raw []byte, out any) error {
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		return malformed(err.Error())
	}
	return nil
}
func digest(raw []byte) string { s := sha256.Sum256(raw); return hex.EncodeToString(s[:]) }
func canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	return canonicalRaw(raw)
}

// Sort object keys recursively without converting number tokens to float64 or
// RFC8785 numbers: imported native numeric lexemes are immutable evidence.
func canonicalRaw(raw []byte) ([]byte, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, malformed("Empty JSON")
	}
	switch raw[0] {
	case '{':
		var object map[string]jsontext.Value
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, err
		}
		for key, value := range object {
			next, err := canonicalRaw(value)
			if err != nil {
				return nil, err
			}
			object[key] = next
		}
		return json.Marshal(object, json.Deterministic(true))
	case '[':
		var array []jsontext.Value
		if err := json.Unmarshal(raw, &array); err != nil {
			return nil, err
		}
		for i, value := range array {
			next, err := canonicalRaw(value)
			if err != nil {
				return nil, err
			}
			array[i] = next
		}
		return json.Marshal(array)
	default:
		return bytes.Clone(raw), nil
	}
}
func requestHash(v any) (string, error) {
	raw, err := canonical(v)
	if err != nil {
		return "", err
	}
	return digest(raw), nil
}

func (in *StartInput) UnmarshalJSON(raw []byte) error {
	if len(raw) > maxInputBytes {
		return fault(413, "input_limit", "Input exceeds 2 MiB")
	}
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(raw, &discriminator); err != nil {
		return malformed(err.Error())
	}
	if b43Kind(discriminator.Kind) {
		return in.unmarshalB43(raw, discriminator.Kind)
	}
	if _, err := closed(raw, []string{"kind", "fromRevisionId", "target", "scope", "limits", "observationMode", "idempotencyKey"}, []string{"observationPins"}); err != nil {
		return err
	}
	type plain StartInput
	var next plain
	if err := decode(raw, &next); err != nil {
		return err
	}
	if next.Kind != "diff" && next.Kind != "impact" {
		return fault(422, "unsupported", "Unsupported analysis kind")
	}
	if next.ObservationMode != "none" || len(next.ObservationPins) > 0 {
		return fault(422, "unsupported", "Observations are not supported")
	}
	if !backendmodel.ValidID(next.FromRevisionID) || !validKey(next.IdempotencyKey) {
		return malformed("Invalid source revision or key")
	}
	next.ObservationPins = []jsontext.Value{}
	*in = StartInput(next)
	return nil
}
func (t *AnalysisTarget) UnmarshalJSON(raw []byte) error {
	m, err := closed(raw, nil, []string{"revisionId", "proposal", "changeProposal", "commandPreview"})
	if err != nil {
		return err
	}
	if len(m) != 1 {
		return malformed("Select exactly one target")
	}
	type plain AnalysisTarget
	var next plain
	if err = decode(raw, &next); err != nil {
		return err
	}
	if _, ok := m["revisionId"]; ok && !backendmodel.ValidID(next.RevisionID) {
		return malformed("Invalid exact revision")
	}
	*t = AnalysisTarget(next)
	return nil
}
func (p *CommandPreviewTarget) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, []string{"changeProposal", "expectedVersion", "commands", "candidateHash"}, nil); err != nil {
		return err
	}
	type plain CommandPreviewTarget
	var next plain
	if err := decode(raw, &next); err != nil {
		return err
	}
	if next.ExpectedVersion <= 0 || len(next.Commands) == 0 || len(next.Commands) > 100 || len(next.CandidateHash) != 64 {
		return malformed("Invalid preview")
	}
	*p = CommandPreviewTarget(next)
	return nil
}
func (s *Scope) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, nil, []string{"changedIds", "service", "kind", "certainty", "direction", "depth"}); err != nil {
		return err
	}
	type plain Scope
	var next plain
	if err := decode(raw, &next); err != nil {
		return err
	}
	if next.Depth < 0 || next.Depth > 32 {
		return malformed("Invalid scope depth")
	}
	if next.Direction != "" && !slices.Contains([]string{"upstream", "downstream", "both"}, next.Direction) {
		return malformed("Invalid direction")
	}
	if next.Certainty != "" && !slices.Contains([]string{"confirmed", "possible", "unknown"}, next.Certainty) {
		return malformed("Invalid certainty")
	}
	for _, a := range next.ChangedIDs {
		if !slices.Contains([]string{"node", "edge", "evidence", "source", "identity", "artifact", "desired", "artifact_object"}, a.RecordType) || a.ID == "" || len(a.ID) > 4096 {
			return malformed("Invalid changed address")
		}
	}
	slices.SortFunc(next.ChangedIDs, func(a, b ObjectAddress) int {
		return bytes.Compare([]byte(a.RecordType+":"+a.ID), []byte(b.RecordType+":"+b.ID))
	})
	next.ChangedIDs = slices.Compact(next.ChangedIDs)
	*s = normalizedScope(Scope(next))
	return nil
}
func normalizedScope(s Scope) Scope {
	if s.Depth == 0 {
		s.Depth = 32
	}
	if s.Direction == "" {
		s.Direction = "downstream"
	}
	return s
}
func (s Scope) MarshalJSON() ([]byte, error) {
	type plain Scope
	return json.Marshal(plain(normalizedScope(s)))
}
func defaultLimits() Limits {
	return Limits{States: 10000, DependencyVisits: 50000, Depth: 32, Findings: 10000, Records: 20000, WitnessesPerObject: 8, ResultBytes: maxResultBytes}
}
func (l *Limits) UnmarshalJSON(raw []byte) error {
	m, err := closed(raw, nil, []string{"states", "dependencyVisits", "depth", "findings", "records", "witnessesPerObject", "resultBytes"})
	if err != nil {
		return err
	}
	type plain Limits
	next := plain(defaultLimits())
	if err = decode(raw, &next); err != nil {
		return err
	}
	d := defaultLimits()
	checks := []struct {
		k      string
		n, max int64
	}{{"states", int64(next.States), int64(d.States)}, {"dependencyVisits", int64(next.DependencyVisits), int64(d.DependencyVisits)}, {"depth", int64(next.Depth), 32}, {"findings", int64(next.Findings), 10000}, {"records", int64(next.Records), 20000}, {"witnessesPerObject", int64(next.WitnessesPerObject), 8}, {"resultBytes", next.ResultBytes, maxResultBytes}}
	for _, c := range checks {
		if _, ok := m[c.k]; ok && (c.n < 1 || c.n > c.max) {
			return malformed("Invalid limit: " + c.k)
		}
	}
	*l = Limits(next)
	return nil
}
func (a *ObjectAddress) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, []string{"recordType", "id"}, nil); err != nil {
		return err
	}
	type plain ObjectAddress
	return decode(raw, (*plain)(a))
}
func keyBody(raw []byte) (string, error) {
	if _, err := closed(raw, []string{"idempotencyKey"}, nil); err != nil {
		return "", err
	}
	var v struct {
		Key string `json:"idempotencyKey"`
	}
	if err := decode(raw, &v); err != nil {
		return "", err
	}
	if !validKey(v.Key) {
		return "", malformed("Invalid key")
	}
	return v.Key, nil
}
func (c *CancelInput) UnmarshalJSON(raw []byte) error {
	key, err := keyBody(raw)
	c.IdempotencyKey = key
	return err
}
func (c *RetryInput) UnmarshalJSON(raw []byte) error {
	key, err := keyBody(raw)
	c.IdempotencyKey = key
	return err
}

// Persist pins through the historical codec; the public context decoder only
// accepts tagged v2 and must not be broadened to accommodate saved analysis.
type plainPins backendmodel.EffectiveGraphPins
type persistedPins struct {
	plainPins
	ArtifactContext jsontext.Value `json:"artifactContext"`
}

func encodePins(p backendmodel.EffectiveGraphPins) (persistedPins, error) {
	out := persistedPins{plainPins: plainPins(p), ArtifactContext: jsontext.Value("null")}
	if p.ArtifactContext != nil {
		raw, err := backendmodel.EncodeArtifactContext(*p.ArtifactContext, p.ArtifactPins)
		if err != nil {
			return out, err
		}
		out.ArtifactContext = raw
	}
	return out, nil
}
func decodePins(p persistedPins) (backendmodel.EffectiveGraphPins, error) {
	out := backendmodel.EffectiveGraphPins(p.plainPins)
	if len(p.ArtifactContext) > 0 && !bytes.Equal(p.ArtifactContext, []byte("null")) {
		c, err := backendmodel.DecodeArtifactContext(p.ArtifactContext, p.ArtifactPins)
		if err != nil {
			return out, err
		}
		out.ArtifactContext = c
	}
	return out, nil
}
func (in ImmutableInput) MarshalJSON() ([]byte, error) {
	if in.V2 != nil {
		return json.Marshal(in.V2, json.Deterministic(true))
	}
	type plain ImmutableInput
	before, err := encodePins(in.BeforePins)
	if err != nil {
		return nil, err
	}
	after, err := encodePins(in.AfterPins)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		*plain
		BeforePins persistedPins `json:"beforePins"`
		AfterPins  persistedPins `json:"afterPins"`
	}{(*plain)(&in), before, after}, json.Deterministic(true))
}
func (in *ImmutableInput) UnmarshalJSON(raw []byte) error {
	var version struct {
		DocumentVersion string `json:"documentVersion"`
	}
	if err := json.Unmarshal(raw, &version); err != nil {
		return err
	}
	if version.DocumentVersion == "backend-analysis-input/v2" {
		var v ImmutableInputV2
		if err := json.Unmarshal(raw, &v); err != nil {
			return err
		}
		*in = ImmutableInput{V2: &v, DocumentVersion: v.DocumentVersion, Kind: v.Kind, ProjectID: v.ProjectID, Limits: v.Limits, RuleSetVersion: v.RuleSetVersion, TraversalVersion: v.TraversalVersion, ObservationMode: v.ObservationMode, Scope: normalizedScope(Scope{})}
		return nil
	}
	type plain ImmutableInput
	var next ImmutableInput
	wire := struct {
		*plain
		BeforePins persistedPins `json:"beforePins"`
		AfterPins  persistedPins `json:"afterPins"`
	}{plain: (*plain)(&next)}
	if err := json.Unmarshal(raw, &wire, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	var err error
	next.BeforePins, err = decodePins(wire.BeforePins)
	if err != nil {
		return err
	}
	next.AfterPins, err = decodePins(wire.AfterPins)
	if err != nil {
		return err
	}
	*in = next
	return nil
}
func (p ResultPage) MarshalJSON() ([]byte, error) {
	items := jsontext.Value(p.ItemsJSON)
	if len(items) == 0 {
		items = jsontext.Value("[]")
	}
	if len(bytes.TrimSpace(items)) == 0 || bytes.TrimSpace(items)[0] != '[' {
		return nil, fmt.Errorf("result items must be an array")
	}
	type plain ResultPage
	return json.Marshal(struct {
		plain
		Items jsontext.Value `json:"items"`
	}{plain(p), items})
}

func (l Limits) MarshalJSON() ([]byte, error) {
	d := defaultLimits()
	if l.States != 0 {
		d.States = l.States
	}
	if l.DependencyVisits != 0 {
		d.DependencyVisits = l.DependencyVisits
	}
	if l.Depth != 0 {
		d.Depth = l.Depth
	}
	if l.Findings != 0 {
		d.Findings = l.Findings
	}
	if l.Records != 0 {
		d.Records = l.Records
	}
	if l.WitnessesPerObject != 0 {
		d.WitnessesPerObject = l.WitnessesPerObject
	}
	if l.ResultBytes != 0 {
		d.ResultBytes = l.ResultBytes
	}
	type plain Limits
	return json.Marshal(plain(d))
}

func validKey(key string) bool {
	return len(key) > 0 && len(key) <= backendmodel.MaxKeyLength && !strings.ContainsFunc(key, func(r rune) bool { return r < 33 || r > 126 })
}
