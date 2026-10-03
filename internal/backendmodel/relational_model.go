package backendmodel

import "encoding/json/jsontext"

// Relational bounds count semantic values; native bounds count UTF-8 bytes.
const (
	MaxRelationalFacets         = 16
	MaxRelationalOrderedColumns = 64
	MaxRelationalIndexTerms     = 64
	MaxRelationalNativeBytes    = 64 << 10
	MaxRelationalReferences     = 500
)

type relationalReference struct{ Path, Kind, Key, ID, HistoricalRevisionID, RecordType string }
type relationalScalar struct {
	Status string         `json:"status"`
	Value  jsontext.Value `json:"value,omitzero"`
	Reason string         `json:"reason,omitzero"`
}
type relationalFacetCommon struct {
	SourceKind       string              `json:"sourceKind"`
	Dialect          string              `json:"dialect"`
	AnalysisStatus   string              `json:"analysisStatus"`
	Gaps             []string            `json:"gaps"`
	EvidenceKeys     []string            `json:"evidenceKeys,omitzero"`
	EvidenceIDs      []string            `json:"evidenceIds,omitzero"`
	Freshness        *AssertionFreshness `json:"freshness,omitzero"`
	SourceSnapshotID string              `json:"sourceSnapshotId,omitzero"`
}
type relationalFacet struct {
	relationalFacetCommon
	DatabaseName        string                      `json:"databaseName,omitzero"`
	QualifiedName       string                      `json:"qualifiedName,omitzero"`
	NativeDefinition    *string                     `json:"nativeDefinition,omitzero"`
	ConstraintsStatus   string                      `json:"constraintsStatus,omitzero"`
	ColumnsStatus       string                      `json:"columnsStatus,omitzero"`
	NativeType          *relationalScalar           `json:"nativeType,omitzero"`
	TypeFamily          *relationalScalar           `json:"typeFamily,omitzero"`
	Nullable            *relationalScalar           `json:"nullable,omitzero"`
	DefaultExpression   *relationalScalar           `json:"defaultExpression,omitzero"`
	GeneratedExpression *relationalScalar           `json:"generatedExpression,omitzero"`
	Identity            *relationalScalar           `json:"identity,omitzero"`
	Ordinal             *relationalScalar           `json:"ordinal,omitzero"`
	ConstraintKind      string                      `json:"constraintKind,omitzero"`
	ColumnKeys          []string                    `json:"columnKeys,omitzero"`
	ColumnIDs           []string                    `json:"columnIds,omitzero"`
	Expression          *relationalScalar           `json:"expression,omitzero"`
	Deferrable          *relationalScalar           `json:"deferrable,omitzero"`
	InitiallyDeferred   *relationalScalar           `json:"initiallyDeferred,omitzero"`
	Terms               []relationalIndexTerm       `json:"terms,omitzero"`
	Unique              *relationalScalar           `json:"unique,omitzero"`
	Predicate           *relationalScalar           `json:"predicate,omitzero"`
	Method              *relationalScalar           `json:"method,omitzero"`
	Materialized        bool                        `json:"materialized,omitzero"`
	Definition          string                      `json:"definition,omitzero"`
	DependencyKeys      []string                    `json:"dependencyKeys,omitzero"`
	DependencyIDs       []string                    `json:"dependencyIds,omitzero"`
	DependenciesStatus  string                      `json:"dependenciesStatus,omitzero"`
	Order               *relationalScalar           `json:"order,omitzero"`
	ParentKeys          []string                    `json:"parentKeys,omitzero"`
	ParentIDs           []string                    `json:"parentIds,omitzero"`
	Changes             []relationalMigrationChange `json:"changes,omitzero"`
	DerivationStatus    string                      `json:"derivationStatus,omitzero"`
	RoutineKind         string                      `json:"routineKind,omitzero"`
	BodyStatus          string                      `json:"bodyStatus,omitzero"`
	ColumnPairs         []relationalColumnPair      `json:"columnPairs,omitzero"`
	UpdateAction        *relationalScalar           `json:"updateAction,omitzero"`
	DeleteAction        *relationalScalar           `json:"deleteAction,omitzero"`
	MatchType           *relationalScalar           `json:"matchType,omitzero"`
	TargetReason        string                      `json:"targetReason,omitzero"`
}
type relationalIndexTerm struct {
	ColumnKey  string  `json:"columnKey,omitzero"`
	ColumnID   string  `json:"columnId,omitzero"`
	Expression *string `json:"expression,omitzero"`
	Direction  string  `json:"direction"`
	Nulls      string  `json:"nulls"`
}
type relationalColumnPair struct {
	FromColumnKey string `json:"fromColumnKey,omitzero"`
	ToColumnKey   string `json:"toColumnKey,omitzero"`
	FromColumnID  string `json:"fromColumnId,omitzero"`
	ToColumnID    string `json:"toColumnId,omitzero"`
}
type relationalMigrationChange struct {
	Target      relationalMigrationTarget `json:"target"`
	Operation   string                    `json:"operation"`
	Description string                    `json:"description"`
}
type relationalMigrationTarget struct {
	Kind          string `json:"kind"`
	ObjectKey     string `json:"objectKey,omitzero"`
	ObjectID      string `json:"objectId,omitzero"`
	RevisionID    string `json:"revisionId,omitzero"`
	ExternalKey   string `json:"externalKey,omitzero"`
	ExpectedKind  string `json:"expectedKind,omitzero"`
	QualifiedName string `json:"qualifiedName,omitzero"`
	Reason        string `json:"reason,omitzero"`
}
