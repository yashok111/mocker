package backendmodel

import "encoding/json/v2"

const (
	IncrementalSourcePolicy = "incremental-source-v1"
	MaxIncrementalSubjects  = 100000
)

type SourceSubjectRef struct {
	RecordType string `json:"recordType"`
	ID         string `json:"id"`
}

type ChangeManifestFile struct {
	Kind       string `json:"kind"`
	Path       string `json:"path"`
	BeforeHash string `json:"beforeHash,omitempty"`
	AfterHash  string `json:"afterHash,omitempty"`
}

type ChangeManifest struct {
	Scope         string               `json:"scope"`
	Files         []ChangeManifestFile `json:"files"`
	AffectedRoots []SourceSubjectRef   `json:"affectedRoots"`
}

type IncrementalAffectedScope struct {
	Affected               []SourceSubjectRef `json:"affected"`
	ValidationDependencies []SourceSubjectRef `json:"validationDependencies"`
	ForeignDependencies    []SourceSubjectRef `json:"foreignDependencies"`
	UntouchedCount         int64              `json:"untouchedCount"`
	Gaps                   []string           `json:"gaps"`
	AvailabilityChanges    []string           `json:"availabilityChanges"`
	wholeClaims            map[string]bool
	contexts               map[incrementalVisit]bool
	dependentClaims        map[string]bool
	beforeFiles            map[string]ManifestFile
	afterFiles             map[string]ManifestFile
	writable               map[string]bool
	visitedCount           int
	claims                 map[string]bool
}

func (c *ChangeManifest) UnmarshalJSON(raw []byte) error {
	type plain ChangeManifest
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if err := validateChangeManifestShape(ChangeManifest(value)); err != nil {
		return err
	}
	*c = ChangeManifest(value)
	return nil
}

func (f *ChangeManifestFile) UnmarshalJSON(raw []byte) error {
	type plain ChangeManifestFile
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	// A tagged union rejects forbidden members even when their value is empty.
	var members map[string]any
	if err := json.Unmarshal(raw, &members); err != nil {
		return err
	}
	_, before := members["beforeHash"]
	_, after := members["afterHash"]
	valid := value.Kind == "added" && !before && after || value.Kind == "modified" && before && after || value.Kind == "deleted" && before && !after
	if !valid {
		return semantic("changeManifest.files", "Change kind requires exactly its before/after hash members")
	}
	*f = ChangeManifestFile(value)
	return nil
}

type IncrementalScopeInput struct {
	Base    *SourceGraphSnapshot
	Session *ImportSession
	Staged  []ImportCommand
	// Keys are recordType + NUL + externalKey, from durable selected-partition reservations.
	ReservedIDs map[string]string
}
