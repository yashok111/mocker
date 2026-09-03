// Package openapi loads an OpenAPI document, classifies its dialect,
// computes a base-path hint, normalizes a handful of dialect quirks that
// would otherwise leak into every downstream consumer, and offers a lazy,
// budget-bounded $ref resolver.
//
// DESIGN §2 rejects ready-made OpenAPI tooling: a strict meta-schema library
// chokes on the malformed documents real users upload, and full
// dereferencing blows up on the recursive schemas real APIs contain. So this
// package never imports an OpenAPI library and never builds a fully
// dereferenced tree — it walks the decoded document as plain
// map[string]any / []any and resolves $ref pointers on demand, one hop at a
// time, under a hard depth budget (see resolver.go).
//
// This package is a leaf: it imports nothing else from this module. The
// operation indexer (a later phase) is what turns a *Document plus a
// *Resolver into rows in the specs / operations / operation_responses
// tables — that walk, and the parse_error/warning bookkeeping it does per
// operation, are out of scope here.
package openapi

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/yamlx"
)

// Format is the OpenAPI/Swagger dialect a document declares itself to be,
// detected from its "openapi" or "swagger" field (DESIGN §7 step 1).
type Format string

const (
	// FormatOAS31 is an "openapi": "3.1.x" document.
	FormatOAS31 Format = "oas31"
	// FormatOAS30 is an "openapi": "3.0.x" document.
	FormatOAS30 Format = "oas30"
	// FormatSwagger2 is a "swagger": "2.x" document. Detect recognizes it,
	// but Load refuses it: swagger2openapi conversion lands in a later phase
	// (DESIGN §7 step 2).
	FormatSwagger2 Format = "swagger2"
)

// Sentinel errors returned by Detect and Load. Both are wrapped with a
// human-readable reason via fmt.Errorf("%w: ...", ...), so errors.Is still
// matches after the reason is attached.
var (
	// ErrNotADocument means the input is not JSON at all, or is JSON but
	// neither an OpenAPI nor a Swagger document — no "openapi"/"swagger"
	// field identifies a known dialect.
	ErrNotADocument = errors.New("openapi: input is not an OpenAPI document")
	// ErrUnsupportedFormat means the input IS an OpenAPI/Swagger document but
	// in a form this phase still defers: Swagger 2.0 (DESIGN §7 step 2).
	// YAML stopped being one of them with A8 (2026-09-02): internal/yamlx
	// renders it to JSON before decodeJSON ever sees it, so the pipeline
	// below has one root type whatever the file's syntax was.
	ErrUnsupportedFormat = errors.New("openapi: unsupported format")
)

// yamlHeaderRe matches a YAML document's top-level "openapi:" / "swagger:"
// key. It is the gate on the YAML path: JSON is tried first, and only an
// input whose first content line looks like a YAML document goes through
// internal/yamlx — so "not a document at all" stays the answer for
// garbage, never a YAML parse error about it.
var yamlHeaderRe = regexp.MustCompile(`^(openapi|swagger)\s*:`)

// Detect classifies raw by its "openapi" / "swagger" field (DESIGN §7 step
// 1). It never partially trusts malformed input: anything that is not
// exactly one JSON object with a recognized version field is an error, never
// a zero-value Format paired with a nil error.
func Detect(raw []byte) (Format, error) {
	root, err := decodeDocument(raw)
	if err != nil {
		return "", err
	}
	return detectFormat(root)
}

// decodeDocument is the one entry for raw bytes: JSON first (the common
// case, and JSON is valid YAML, so trying YAML first would route every
// document through the converter); a YAML-looking input is rendered to
// JSON by internal/yamlx and decoded by the SAME decodeJSON. A YAML parse
// failure is reported as ErrNotADocument with the decoder's own words
// beside it, never as ErrUnsupportedFormat: that error is for a document
// we recognise and decline, not for one we could not read.
func decodeDocument(raw []byte) (any, error) {
	root, err := decodeJSON(raw)
	if err == nil {
		return root, nil
	}
	if !looksLikeYAMLDocument(raw) {
		return nil, ErrNotADocument
	}
	converted, yerr := yamlx.ToJSON(raw)
	if yerr != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotADocument, yerr)
	}
	root, err = decodeJSON(converted)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotADocument, err)
	}
	return root, nil
}

// decodeJSON decodes raw as exactly one JSON value; trailing bytes after a
// complete value are rejected rather than silently ignored.
func decodeJSON(raw []byte) (any, error) {
	dec := jsonx.NewDecoder(bytes.NewReader(raw))
	// UseNumber preserves number literals exactly. A minimum/maximum bound
	// that round-tripped through float64 could silently change value for a
	// large enough number — the kind of corruption golang-safety warns about.
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, fmt.Errorf("openapi: trailing data after JSON value")
	}
	return root, nil
}

// looksLikeYAMLDocument reports whether raw's first CONTENT line looks
// like a YAML document that declares an OpenAPI/Swagger version. Blank
// lines, `#` comments and a leading `---` document marker are skipped:
// a generated YAML spec routinely opens with a comment banner, and a
// gate that stopped at it would send the file down the "not a document"
// path with no mention of YAML at all.
func looksLikeYAMLDocument(raw []byte) bool {
	for line := range strings.SplitSeq(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || trimmed == "---" {
			continue
		}
		return yamlHeaderRe.MatchString(trimmed)
	}
	return false
}

// detectFormat classifies an already-decoded JSON value.
func detectFormat(root any) (Format, error) {
	obj, ok := root.(map[string]any)
	if !ok {
		return "", ErrNotADocument
	}
	if v, ok := obj["openapi"].(string); ok {
		switch {
		case strings.HasPrefix(v, "3.1"):
			return FormatOAS31, nil
		case strings.HasPrefix(v, "3.0"):
			return FormatOAS30, nil
		}
	}
	if v, ok := obj["swagger"].(string); ok && strings.HasPrefix(v, "2") {
		return FormatSwagger2, nil
	}
	return "", ErrNotADocument
}

// BasePathOrigin explains where a document's base path came from, so the
// admin "Specs" screen can show why an ambiguous spec landed on an empty
// prefix instead of guessing wrong silently.
type BasePathOrigin string

const (
	// BaseFromRootServers: the document root has a non-empty servers[]; the
	// FIRST server's path component wins (DESIGN §7 step 3: "первый сервер").
	BaseFromRootServers BasePathOrigin = "root-servers"
	// BaseFromPathServers: no root servers[], but every path item that
	// declares its own servers[] agrees on the same path component.
	BaseFromPathServers BasePathOrigin = "path-servers"
	// BaseAmbiguous: at least two path items disagree on their servers[]
	// path component. The base path is left empty and a Report warning
	// names both candidates.
	BaseAmbiguous BasePathOrigin = "ambiguous"
	// BaseAbsent: no servers anywhere — not at the root, not on any path item.
	BaseAbsent BasePathOrigin = "absent"
)

// Warning is one non-fatal defect found while importing a spec (DESIGN §7:
// "импорт никогда не падает" — a malformed corner is recorded, never fatal).
type Warning struct {
	Pointer string // JSON pointer into the document, e.g. "#/paths"
	Code    string // machine-readable, e.g. "ambiguous-base-path"
	Message string // human-readable, shown on the admin "Specs" screen
}

// Report accumulates what happened while importing a spec. Load fills in
// Format, BasePath, BasePathOrigin, and a base-path warning if the path
// items disagreed. The operation indexer — a later phase, not this package —
// keeps writing to the SAME Report as it walks each operation, incrementing
// Operations and Degraded and adding a warning per parse_error or
// unresolved $ref it hits.
type Report struct {
	Format         Format
	BasePath       string
	BasePathOrigin BasePathOrigin
	Warnings       []Warning
	Operations     int
	Degraded       int
}

// Add appends one warning. It has no error return: recording a defect must
// never be the thing that fails an import.
func (r *Report) Add(pointer, code, msg string) {
	r.Warnings = append(r.Warnings, Warning{Pointer: pointer, Code: code, Message: msg})
}

// Document is a loaded document: the raw bytes as handed in, the
// dialect-normalized JSON derived from them (DESIGN §7 step 4), and the
// base-path hint computed from servers[] (DESIGN §7 step 3).
type Document struct {
	format         Format
	raw            []byte
	normalized     []byte
	root           map[string]any
	basePath       string
	basePathOrigin BasePathOrigin
}

// Load decodes, classifies, dialect-normalizes, and computes the base path
// for raw. It returns an error ONLY for input Detect refuses outright:
// ErrNotADocument, or ErrUnsupportedFormat for Swagger 2.0. Any other
// defect belongs to the operation indexer that runs after Load, which
// records it as a Report warning or an operations.parse_error and keeps
// going — DESIGN §7's import invariant is enforced there, not by making Load
// itself unable to fail on a merely-imperfect document.
func Load(raw []byte) (*Document, *Report, error) {
	root, err := decodeDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	format, err := detectFormat(root)
	if err != nil {
		return nil, nil, err
	}
	if format == FormatSwagger2 {
		return nil, nil, fmt.Errorf("%w: Swagger 2.0 is converted in a later phase, not loaded directly", ErrUnsupportedFormat)
	}
	obj, ok := root.(map[string]any)
	if !ok {
		// detectFormat only returns a nil error after this same assertion
		// already succeeded once; guarded rather than trusted blindly.
		return nil, nil, fmt.Errorf("%w: expected a JSON object at the root", ErrNotADocument)
	}

	normalizeDialect(obj)

	report := &Report{Format: format}
	basePath, origin := computeBasePath(obj, report)
	report.BasePath = basePath
	report.BasePathOrigin = origin

	normalized, err := jsonx.Marshal(obj)
	if err != nil {
		return nil, nil, fmt.Errorf("openapi: marshal normalized document: %w", err)
	}

	doc := &Document{
		format:         format,
		raw:            bytes.Clone(raw),
		normalized:     normalized,
		root:           obj,
		basePath:       basePath,
		basePathOrigin: origin,
	}
	return doc, report, nil
}

// Format is the dialect this document was loaded as.
func (d *Document) Format() Format { return d.format }

// Raw returns exactly the bytes handed to Load (DESIGN §7: raw is stored and
// hashed verbatim). The returned slice is the Document's own copy, not a
// fresh copy per call — callers must treat it as read-only.
func (d *Document) Raw() []byte { return d.raw }

// Normalized returns the canonical JSON produced by dialect normalization
// (DESIGN §7 step 4). Same read-only contract as Raw.
func (d *Document) Normalized() []byte { return d.normalized }

// Root returns the parsed, normalized document root. It is the SAME map the
// Document's Resolver navigates, not a defensive copy: a 350KB acceptance
// document deep-copied on every call would cost real time for no benefit,
// since every caller in this codebase is trusted, co-located code. Callers
// must not mutate it.
func (d *Document) Root() map[string]any { return d.root }

// Title returns info.title, or "" if it is absent or not a string.
func (d *Document) Title() string { return stringField(d.root, "info", "title") }

// Version returns info.version, or "" if it is absent or not a string.
func (d *Document) Version() string { return stringField(d.root, "info", "version") }

// BasePath returns the base-path hint computed at Load time and where it
// came from. It is a hint only — the one place it is glued onto a stored
// operation path is the runtime route builder, never here (DESIGN §7 step 3).
func (d *Document) BasePath() (string, BasePathOrigin) { return d.basePath, d.basePathOrigin }

// stringField walks nested map keys and returns the string found at the end
// of the chain, or "" if any hop is missing or not the expected type.
func stringField(root map[string]any, keys ...string) string {
	var cur any = root
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return ""
		}
		cur = m[k]
	}
	s, _ := cur.(string)
	return s
}

// computeBasePath implements DESIGN §7 step 3's base-path table: root
// servers[] wins outright, taking the FIRST server. Absent that,
// path items must AGREE on their own servers[] path component, or the base
// path is left empty and a warning names both candidates.
func computeBasePath(root map[string]any, report *Report) (string, BasePathOrigin) {
	if servers, ok := root["servers"].([]any); ok && len(servers) > 0 {
		if s, ok := servers[0].(map[string]any); ok {
			if p, ok := serverPathComponent(s); ok {
				return p, BaseFromRootServers
			}
		}
	}

	paths, _ := root["paths"].(map[string]any)
	var candidate, disagreement string
	have := false
	agree := true
	// Sorted, as Index walks paths: ranging the map made the "%q vs %q"
	// warning text below depend on iteration order, so two imports of the
	// same bytes could record swapped values.
	for _, key := range slices.Sorted(maps.Keys(paths)) {
		item := paths[key]
		pathItem, ok := item.(map[string]any)
		if !ok {
			continue
		}
		servers, ok := pathItem["servers"].([]any)
		if !ok || len(servers) == 0 {
			continue
		}
		s, ok := servers[0].(map[string]any)
		if !ok {
			continue
		}
		p, ok := serverPathComponent(s)
		if !ok {
			continue
		}
		switch {
		case !have:
			candidate, have = p, true
		case p != candidate:
			agree, disagreement = false, p
		}
	}
	if !have {
		return "", BaseAbsent
	}
	if !agree {
		report.Add("#/paths", "ambiguous-base-path",
			fmt.Sprintf("path items disagree on server base path: %q vs %q", candidate, disagreement))
		return "", BaseAmbiguous
	}
	return candidate, BaseFromPathServers
}

// serverVarRe matches a {variable} placeholder in a Server Object's url template.
var serverVarRe = regexp.MustCompile(`\{([^{}]+)\}`)

// serverPathComponent extracts the path component of a Server Object's url,
// expanding {variable} placeholders with their declared default values.
func serverPathComponent(server map[string]any) (string, bool) {
	raw, ok := server["url"].(string)
	if !ok || raw == "" {
		return "", false
	}
	vars, _ := server["variables"].(map[string]any)
	expanded := serverVarRe.ReplaceAllStringFunc(raw, func(m string) string {
		name := m[1 : len(m)-1]
		v, ok := vars[name].(map[string]any)
		if !ok {
			return m
		}
		def, _ := v["default"].(string)
		return def
	})
	u, err := url.Parse(expanded)
	if err != nil {
		return "", false
	}
	return u.Path, true
}
