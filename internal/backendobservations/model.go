// Package backendobservations owns immutable uploaded observations, never collection.
package backendobservations

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"strings"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

const DocumentVersion = "backend-observations-v1"
const CorrelationPolicy = "backend-correlation-v1"
const BatchBytes = 1 << 20
const AdapterBytes = 4 << 20

type Producer struct {
	ID             string `json:"id"`
	SchemaVersion  string `json:"schemaVersion"`
	AdapterVersion string `json:"adapterVersion"`
}
type Source struct {
	Status          string `json:"status"`
	ServiceID       string `json:"serviceId"`
	BuildID         string `json:"buildId,omitempty"`
	SourceFilesHash string `json:"sourceFilesHash,omitempty"`
	RepositoryID    string `json:"repositoryId,omitempty"`
	Reason          string `json:"reason,omitempty"`
}
type Environment struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}
type Window struct {
	Start string `json:"start"`
	End   string `json:"end"`
}
type InputContext struct {
	Description string  `json:"description"`
	Size        *int64  `json:"size"`
	Unit        *string `json:"unit"`
}
type Sampling struct {
	Kind        string  `json:"kind"`
	Probability *string `json:"probability"`
	Reason      string  `json:"reason"`
}
type Instrumentation struct {
	SQL           string   `json:"sql"`
	ExternalCalls string   `json:"externalCalls"`
	Retries       string   `json:"retries"`
	Bytes         string   `json:"bytes"`
	Latency       string   `json:"latency"`
	Limitations   []string `json:"limitations"`
}
type Scenario struct {
	Kind        string `json:"kind"`
	ID          string `json:"id,omitempty"`
	RevisionID  string `json:"revisionId,omitempty"`
	ContentHash string `json:"contentHash,omitempty"`
	PackageID   string `json:"packageId,omitempty"`
	Version     int64  `json:"version,omitzero"`
	Hash        string `json:"hash,omitempty"`
}
type ObservationContext struct {
	Producer          Producer        `json:"producer"`
	Source            Source          `json:"source"`
	Environment       Environment     `json:"environment"`
	Window            Window          `json:"window"`
	ConfigurationHash string          `json:"configurationHash"`
	Input             InputContext    `json:"input"`
	Sampling          Sampling        `json:"sampling"`
	Instrumentation   Instrumentation `json:"instrumentation"`
	Scenario          *Scenario       `json:"scenario"`
}
type LinkProof struct {
	Profile          string `json:"profile"`
	MessageIDHash    string `json:"messageIdHash"`
	PredecessorEvent string `json:"predecessorEvent"`
	SuccessorEvent   string `json:"successorEvent"`
}
type SpanLink struct {
	TraceID  string     `json:"traceId"`
	SpanID   string     `json:"spanId"`
	Relation string     `json:"relation"`
	Proof    *LinkProof `json:"proof,omitzero"`
}

// Attributes are deliberately structural. No raw SQL, names, URLs or arbitrary keys.
type Attributes struct {
	Operation     string `json:"operation,omitempty"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	SourcePath    string `json:"sourcePath,omitempty"`
	SourceLine    int64  `json:"sourceLine,omitzero"`
	MessageRole   string `json:"messageRole,omitempty"`
	MessageIDHash string `json:"messageIdHash,omitempty"`
	RequestBytes  *int64 `json:"requestBytes,omitzero"`
	ResponseBytes *int64 `json:"responseBytes,omitzero"`
}
type Assertion struct {
	ID      string `json:"id"`
	Outcome string `json:"outcome"`
	Scope   string `json:"scope"`
}
type RunPins struct {
	RunID          string    `json:"runId"`
	ReportHash     string    `json:"reportHash"`
	Package        *Scenario `json:"package,omitzero"`
	TriggerVerdict string    `json:"triggerVerdict"`
	SourceHash     string    `json:"sourceHash"`
}
type IdentityRef struct {
	RepositoryID      string `json:"repositoryId"`
	ProviderNamespace string `json:"providerNamespace"`
	ExternalKey       string `json:"externalKey"`
	RecordType        string `json:"recordType"`
}
type DiagramWitness struct {
	Pin         bm.DiagramPin           `json:"pin"`
	Selector    bm.DiagramScopeSelector `json:"selector"`
	Kind        string                  `json:"kind"`
	AssertionID string                  `json:"assertionId,omitempty"`
}
type Record struct {
	IdentityRef       *IdentityRef    `json:"identityRef,omitzero"`
	DiagramWitness    *DiagramWitness `json:"diagramWitness,omitzero"`
	Type              string          `json:"type"`
	ID                string          `json:"id"`
	ExecutionID       string          `json:"executionId"`
	TraceID           string          `json:"traceId,omitempty"`
	SpanID            string          `json:"spanId,omitempty"`
	ParentSpanID      string          `json:"parentSpanId,omitempty"`
	StartTimeUnixNano string          `json:"startTimeUnixNano,omitempty"`
	EndTimeUnixNano   string          `json:"endTimeUnixNano,omitempty"`
	Kind              string          `json:"kind,omitempty"`
	Category          string          `json:"category,omitempty"`
	Status            string          `json:"status,omitempty"`
	Attrs             *Attributes     `json:"attrs,omitzero"`
	Links             *[]SpanLink     `json:"links,omitzero"`
	BackendRef        *bm.DiagramRef  `json:"backendRef,omitzero"`
	SuiteID           string          `json:"suiteId,omitempty"`
	CaseID            string          `json:"caseId,omitempty"`
	Outcome           string          `json:"outcome,omitempty"`
	Timestamp         string          `json:"timestamp,omitempty"`
	DurationNs        string          `json:"durationNs,omitempty"`
	Assertions        *[]Assertion    `json:"assertions,omitzero"`
	RunPins           *RunPins        `json:"runPins,omitzero"`
	Metric            string          `json:"metric,omitempty"`
	Value             string          `json:"value,omitempty"`
	Unit              string          `json:"unit,omitempty"`
	Basis             string          `json:"basis,omitempty"`
	Scope             string          `json:"scope,omitempty"`
}
type ImportInput struct {
	Mode            string              `json:"mode"`
	Context         *ObservationContext `json:"context,omitzero"`
	Name            string              `json:"name,omitempty"`
	SetID           string              `json:"setId,omitempty"`
	ExpectedVersion int64               `json:"expectedVersion,omitzero"`
	BatchID         string              `json:"batchId"`
	IdempotencyKey  string              `json:"idempotencyKey"`
	Records         []Record            `json:"records"`
}
type VersionReceipt struct {
	SetID       string `json:"setId"`
	Version     int64  `json:"version"`
	ContentHash string `json:"contentHash"`
	RecordCount int    `json:"recordCount"`
}
type Version struct {
	VersionReceipt
	Context      ObservationContext `json:"context"`
	Name         string             `json:"name"`
	LogicalBytes int64              `json:"logicalBytes"`
}
type SetPage struct {
	Items      []Version `json:"items"`
	NextCursor string    `json:"nextCursor"`
}
type RecordPage struct {
	Pin        VersionReceipt `json:"pin"`
	Items      []Record       `json:"items"`
	NextCursor string         `json:"nextCursor"`
}

func fault(status int, code string) error {
	return &bm.FaultError{Status: status, Code: "backend_observation_" + code, Message: "Observation request: " + code}
}
func invalid() error                      { return fault(422, "invalid") }
func canonical(v any) ([]byte, error)     { return json.Marshal(v, json.Deterministic(true)) }
func RecordHash(v Record) (string, error) { return p.Hash(DocumentVersion+"/record", v) }

// Required-field validation includes explicit nullable context members. Unknown and
// duplicate keys are rejected by json/v2 before any semantic admission.
func decode(raw []byte, out any) error {
	if len(raw) > AdapterBytes {
		return fault(413, "limit")
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		return invalid()
	}
	return required(raw, reflect.TypeOf(out).Elem(), false)
}
func required(raw []byte, t reflect.Type, nullable bool) error {
	if string(raw) == "null" {
		if nullable {
			return nil
		}
		return invalid()
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Struct:
		return requiredFields(raw, t)
	case reflect.Slice:
		var values []jsontext.Value
		if json.Unmarshal(raw, &values) != nil {
			return invalid()
		}
		for _, v := range values {
			if err := required(v, t.Elem(), false); err != nil {
				return err
			}
		}
	}
	return nil
}

// requiredFields requires every field of struct t without an option in its
// tag to be present, recursively; an embedded struct's fields are its own.
func requiredFields(raw []byte, t reflect.Type) error {
	var fields map[string]jsontext.Value
	if json.Unmarshal(raw, &fields) != nil {
		return invalid()
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "-" {
			continue
		}
		if f.Anonymous && tag == "" {
			if err := required(raw, f.Type, false); err != nil {
				return err
			}
			continue
		}
		name, opts, _ := strings.Cut(tag, ",")
		v, ok := fields[name]
		if !ok {
			if opts != "" {
				continue
			}
			return invalid()
		}
		if err := required(v, f.Type, nullableField(t, name)); err != nil {
			return err
		}
	}
	return nil
}

// nullableField names the only fields whose JSON null means "absent".
func nullableField(t reflect.Type, name string) bool {
	return t == reflect.TypeFor[InputContext]() && (name == "size" || name == "unit") || t == reflect.TypeFor[Sampling]() && name == "probability" || t == reflect.TypeFor[ObservationContext]() && name == "scenario"
}
func (v *ImportInput) UnmarshalJSON(b []byte) error {
	type wire ImportInput
	if err := decode(b, (*wire)(v)); err != nil {
		return err
	}
	if v.Mode == "create" {
		return closedFields(b, "mode context name batchId idempotencyKey records", "")
	}
	if v.Mode == "append" {
		return closedFields(b, "mode setId expectedVersion batchId idempotencyKey records", "")
	}
	return invalid()
}
func (v *ObservationContext) UnmarshalJSON(b []byte) error {
	type wire ObservationContext // preserve nullable context rules under the alias
	if err := json.Unmarshal(b, (*wire)(v), json.RejectUnknownMembers(true)); err != nil {
		return invalid()
	}
	return required(b, reflect.TypeFor[ObservationContext](), false)
}
func (v *Record) UnmarshalJSON(b []byte) error {
	type wire Record
	if err := decode(b, (*wire)(v)); err != nil {
		return err
	}
	var fields map[string]jsontext.Value
	if json.Unmarshal(b, &fields) != nil {
		return invalid()
	}
	allowed := "type id executionId backendRef identityRef diagramWitness "
	var req string
	switch v.Type {
	case "span":
		req = "traceId spanId startTimeUnixNano endTimeUnixNano kind category status attrs"
		allowed += req + " parentSpanId links"
	case "test":
		req = "suiteId caseId outcome timestamp durationNs assertions runPins"
		allowed += req
	case "measurement":
		req = "timestamp metric value unit basis scope"
		allowed += req
	default:
		return invalid()
	}
	for _, k := range strings.Fields(req) {
		if _, ok := fields[k]; !ok {
			return invalid()
		}
	}
	for k := range fields {
		if !strings.Contains(" "+allowed+" ", " "+k+" ") {
			return invalid()
		}
	}
	return nil
}

func closedFields(raw []byte, requiredNames, optionalNames string) error {
	var fields map[string]jsontext.Value
	if json.Unmarshal(raw, &fields) != nil {
		return invalid()
	}
	for _, name := range strings.Fields(requiredNames) {
		if _, ok := fields[name]; !ok {
			return invalid()
		}
	}
	allowed := " " + requiredNames + " " + optionalNames + " "
	for name := range fields {
		if !strings.Contains(allowed, " "+name+" ") {
			return invalid()
		}
	}
	return nil
}
func (v *Source) UnmarshalJSON(raw []byte) error {
	type wire Source
	if e := decode(raw, (*wire)(v)); e != nil {
		return e
	}
	switch v.Status {
	case "known":
		return closedFields(raw, "status serviceId buildId sourceFilesHash repositoryId", "")
	case "unknown":
		return closedFields(raw, "status serviceId reason", "")
	default:
		return invalid()
	}
}
func (v *Scenario) UnmarshalJSON(raw []byte) error {
	type wire Scenario
	if e := decode(raw, (*wire)(v)); e != nil {
		return e
	}
	switch v.Kind {
	case "design_scenario":
		return closedFields(raw, "kind id revisionId contentHash", "")
	case "backend_replay":
		return closedFields(raw, "kind packageId version hash", "")
	default:
		return invalid()
	}
}
