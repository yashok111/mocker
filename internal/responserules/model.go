// Package responserules implements authoring graphs, immutable execution programs
// and pure logical simulation.
package responserules

import (
	"context"
	"github.com/yashok111/mocker/internal/overrides"
)

const Extension = "x-mocker-response-rules"

const (
	MaxRules          = 20
	MaxNodes          = 100
	MaxEdges          = 200
	MaxCommands       = 200
	MaxGraphBytes     = 512 << 10
	MaxExtensionBytes = 1 << 20
	MaxBodyBytes      = 64 << 10
	MaxBodyDepth      = 64
	MaxFixtureBytes   = 128 << 10
	MaxDiagnostics    = 200
	MaxResultBytes    = 512 << 10
	MaxDelayMs        = 30000
)

type Envelope struct {
	FormatVersion int    `json:"formatVersion"`
	Rules         []Rule `json:"rules"`
}
type Binding struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}
type Rule struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Binding *Binding `json:"binding,omitempty"`
	Nodes   []Node   `json:"nodes"`
	Edges   []Edge   `json:"edges"`
}
type Field struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}
type Response struct {
	Status    int       `json:"status"`
	MediaType string    `json:"mediaType"`
	Headers   []Field   `json:"headers"`
	BodyJSON  *string   `json:"bodyJSON,omitempty"`
	BodyFrom  *ValueRef `json:"bodyFrom,omitempty"`
}
type Node struct {
	ID        string               `json:"id"`
	Type      string               `json:"type"`
	Name      string               `json:"name"`
	X         float64              `json:"x"`
	Y         float64              `json:"y"`
	Condition *overrides.Condition `json:"condition,omitempty"`
	DelayMs   *int                 `json:"delayMs,omitempty"`
	Response  *Response            `json:"response,omitempty"`
	Entity    *EntityOperation     `json:"entity,omitempty"`
}
type Edge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	Port string `json:"port"`
	To   string `json:"to"`
}
type Position struct {
	NodeID string  `json:"nodeId"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
}
type Command struct {
	Type      string     `json:"type"`
	Name      *string    `json:"name,omitempty"`
	Binding   *Binding   `json:"binding,omitempty"`
	Node      *Node      `json:"node,omitempty"`
	Edge      *Edge      `json:"edge,omitempty"`
	NodeID    string     `json:"nodeId,omitempty"`
	EdgeID    string     `json:"edgeId,omitempty"`
	Positions []Position `json:"positions,omitempty"`
}
type Request struct {
	Query    []Field         `json:"query"`
	Headers  []Field         `json:"headers"`
	BodyJSON *string         `json:"bodyJSON,omitempty"`
	Path     []Field         `json:"path,omitempty"`
	Entities []EntityFixture `json:"entities,omitempty"`
}
type Diagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Pointer  string `json:"pointer"`
	RuleID   string `json:"ruleId,omitempty"`
	NodeID   string `json:"nodeId,omitempty"`
	EdgeID   string `json:"edgeId,omitempty"`
}
type Validation struct {
	Valid                bool         `json:"valid"`
	Diagnostics          []Diagnostic `json:"diagnostics"`
	DiagnosticsTruncated bool         `json:"diagnosticsTruncated"`
}
type Step struct {
	Step        int    `json:"step"`
	NodeID      string `json:"nodeId"`
	EdgeID      string `json:"edgeId,omitempty"`
	Matched     *bool  `json:"matched,omitempty"`
	DelayMs     *int   `json:"delayMs,omitempty"`
	EntityFound *bool  `json:"entityFound,omitempty"`
	EntityCount *int   `json:"entityCount,omitempty"`
}
type Simulation struct {
	Validation
	InputHash      string            `json:"inputHash"`
	Outcome        string            `json:"outcome"`
	Trace          []Step            `json:"trace"`
	TerminalNodeID string            `json:"terminalNodeId,omitempty"`
	TotalDelayMs   *int              `json:"totalDelayMs,omitempty"`
	Response       *Response         `json:"response,omitempty"`
	Results        map[string]string `json:"results,omitempty"`
	Entities       []EntityFixture   `json:"entities,omitempty"`
}

// ValueRef preserves literal JSON as text across the browser boundary.
type ValueRef struct {
	Source    string  `json:"source"`
	ValueJSON *string `json:"valueJSON,omitempty"`
	Name      string  `json:"name,omitempty"`
	NodeID    string  `json:"nodeId,omitempty"`
	Pointer   string  `json:"pointer,omitempty"`
}
type EntityOperation struct {
	Family    string      `json:"family"`
	Operation string      `json:"operation,omitempty"`
	Scope     *[]ValueRef `json:"scope,omitempty"`
	Key       *ValueRef   `json:"key,omitempty"`
	Data      *ValueRef   `json:"data,omitempty"`
}
type EntityFixtureRow struct {
	Key      string   `json:"key"`
	Scope    []string `json:"scope"`
	DataJSON string   `json:"dataJSON"`
}
type EntityFixture struct {
	Family  string             `json:"family"`
	IDField string             `json:"idField"`
	IDType  string             `json:"idType"`
	Rows    []EntityFixtureRow `json:"rows"`
}
type EntityTarget struct {
	Family string
	Scope  []string
}
type EntityResult struct {
	Found bool
	Value any
}
type EntityExecutor interface {
	Read(context.Context, EntityTarget, *string) (EntityResult, error)
	Create(context.Context, EntityTarget, map[string]any) (EntityResult, error)
	Update(context.Context, EntityTarget, string, map[string]any) (EntityResult, error)
}
type EvaluationInput struct {
	Request overrides.Input
	Path    map[string]string
}
type EvaluationOptions struct {
	Entities EntityExecutor
	Delay    func(context.Context, int) error
}
type EntityAdmission struct {
	MediaType string
	NoBody    bool
}
