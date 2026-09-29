// Package responserules implements authoring graphs, immutable execution programs
// and pure logical simulation.
package responserules

import "github.com/yashok111/mocker/internal/overrides"

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
	Status    int     `json:"status"`
	MediaType string  `json:"mediaType"`
	Headers   []Field `json:"headers"`
	BodyJSON  *string `json:"bodyJSON,omitempty"`
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
	Query    []Field `json:"query"`
	Headers  []Field `json:"headers"`
	BodyJSON *string `json:"bodyJSON,omitempty"`
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
	Step    int    `json:"step"`
	NodeID  string `json:"nodeId"`
	EdgeID  string `json:"edgeId,omitempty"`
	Matched *bool  `json:"matched,omitempty"`
	DelayMs *int   `json:"delayMs,omitempty"`
}
type Simulation struct {
	Validation
	InputHash      string    `json:"inputHash"`
	Outcome        string    `json:"outcome"`
	Trace          []Step    `json:"trace"`
	TerminalNodeID string    `json:"terminalNodeId,omitempty"`
	TotalDelayMs   *int      `json:"totalDelayMs,omitempty"`
	Response       *Response `json:"response,omitempty"`
}
