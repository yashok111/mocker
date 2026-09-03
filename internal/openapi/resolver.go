package openapi

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// DefaultRefBudget bounds a $ref chain's length. DESIGN §7 names 32: the
// acceptance document's longest real chain reachable from an operation is 12
// hops, and the spec's original budget of 6 would have failed 28 of its 130
// operations outright.
const DefaultRefBudget = 32

// Errors a Resolver returns. None are fatal to an import: a caller that gets
// ErrBudgetExhausted or ErrCycle records a Report warning and moves on
// (DESIGN §7's import invariant — a bad $ref degrades one operation, it
// never aborts the whole spec).
var (
	ErrBudgetExhausted = errors.New("openapi: $ref chain exceeds the depth budget")
	ErrCycle           = errors.New("openapi: $ref cycle")
	ErrPointerNotFound = errors.New("openapi: json pointer not found")
)

// Resolver navigates $ref pointers within one Document (DESIGN §7:
// "разрешение $ref — ленивое"). It never builds a fully dereferenced tree —
// DESIGN §2 explicitly rejects that ("взрывается на рекурсии"), and the
// acceptance document alone carries a schema (FeedbackSection) that
// references itself. Resolve/ResolveNode chase a $ref chain iteratively —
// never recursively, so no chain length can overflow the stack — under a
// depth budget and a visited-pointer set scoped to that one call, so a
// pointer reached from two different branches is never mistaken for a
// cycle. A Resolver is safe for concurrent use.
type Resolver struct {
	doc    *Document
	budget int

	memo sync.Map // pointer -> the raw node found there, NOT further
	// chased — see lookup for why that distinction is load-bearing.
}

// NewResolver builds a Resolver over d with the given hop budget. A
// non-positive budget falls back to DefaultRefBudget.
func NewResolver(d *Document, budget int) *Resolver {
	if budget < 1 {
		budget = DefaultRefBudget
	}
	return &Resolver{doc: d, budget: budget}
}

// Resolve looks up pointer (e.g. "#/components/responses/X") and, if what it
// finds is itself a $ref, keeps following the chain to the first concrete
// (non-$ref) node.
func (r *Resolver) Resolve(pointer string) (any, error) {
	return r.chase(map[string]any{"$ref": pointer})
}

// ResolveNode follows node's $ref chain to the first non-$ref node. If node
// is not a $ref at all, it is returned unchanged — the common case for an
// inline (non-$ref) schema. A $ref node with sibling keys (OAS 3.0 allows
// them; the spec says a $ref's siblings are ignored) is still a $ref: it is
// followed and the siblings are discarded.
func (r *Resolver) ResolveNode(node any) (any, error) {
	_, resolved, err := r.chaseTracked(node)
	return resolved, err
}

// ResolveNodePointer is [ResolveNode], plus the pointer of the LAST $ref hop
// actually taken to reach resolved — that hop's own location in the
// document, as opposed to node's (the referring site's) location. pointer is
// "" when node was not a $ref at all (zero hops taken): the caller already
// knows node's own location in that case, there is nothing this method could
// add. A caller anchoring a pointer at a $ref'd node (e.g. [specs]'s
// resolveResponseContent building schema_ptr) needs THIS, not node's own
// site: for a chained $ref (A -> B -> {content:...}), the resolved value
// lives at B, and a pointer built from A would name a location that has no
// "content" key to navigate into at all.
func (r *Resolver) ResolveNodePointer(node any) (pointer string, resolved any, err error) {
	return r.chaseTracked(node)
}

// chase follows a $ref chain with a for loop — never recursion — so no
// chain length can overflow the stack.
func (r *Resolver) chase(node any) (any, error) {
	_, resolved, err := r.chaseTracked(node)
	return resolved, err
}

// chaseTracked is chase's implementation, additionally reporting the
// pointer of the last hop taken (see [Resolver.ResolveNodePointer]).
// budget and visited are both local to this one call: a pointer reached
// twice within the SAME call is a cycle (visited catches it), but a pointer
// reached from two SEPARATE top-level Resolve/ResolveNode calls is not,
// because each call starts its own visited set. That is the "per-branch,
// not global" requirement — a schema legitimately referenced from two
// different operations must resolve both times, not fail the second time as
// a false cycle.
func (r *Resolver) chaseTracked(node any) (lastPointer string, resolved any, err error) {
	current := node
	visited := make(map[string]bool)
	hops := 0
	for {
		ref, ok := refPointer(current)
		if !ok {
			return lastPointer, current, nil
		}
		if visited[ref] {
			return "", nil, fmt.Errorf("%w: %s", ErrCycle, ref)
		}
		visited[ref] = true
		hops++
		if hops > r.budget {
			return "", nil, fmt.Errorf("%w: %s", ErrBudgetExhausted, ref)
		}
		next, lerr := r.lookup(ref)
		if lerr != nil {
			return "", nil, lerr
		}
		lastPointer = ref
		current = next
	}
}

// lookup is pure JSON-pointer navigation of the document — it does NOT chase
// further $refs, and is memoized precisely because it doesn't: caching a
// fully-chased result would let a later call, arriving at a different point
// in its own budget, skip paying for hops it never actually took. That is
// the exact bug the per-branch depth budget exists to prevent, so the memo
// only ever holds one navigation step. Caching that step is always safe: the
// document is immutable after Load, so "what's at this pointer" never
// depends on how the caller got there.
func (r *Resolver) lookup(pointer string) (any, error) {
	// sync.Map, not a map under a mutex: one Resolver serves every concurrent
	// request of a (workspace, revision), and every $ref hop of every
	// schema node went through the mutex twice — thousands of acquisitions
	// per 200-row list, all on one lock. The memo is append-only over an
	// immutable document, the exact case sync.Map is built for.
	if v, ok := r.memo.Load(pointer); ok {
		return v, nil
	}
	node, err := navigatePointer(r.doc.Root(), pointer)
	if err != nil {
		return nil, err
	}
	r.memo.Store(pointer, node)
	return node, nil
}

// refPointer reports whether node is a $ref object and, if so, its pointer.
// A $ref with sibling keys still counts (OAS 3.0 §4.8.23.1: siblings next to
// $ref are ignored, not an error).
func refPointer(node any) (string, bool) {
	m, ok := node.(map[string]any)
	if !ok {
		return "", false
	}
	ref, ok := m["$ref"].(string)
	if !ok || ref == "" {
		return "", false
	}
	return ref, true
}

// navigatePointer walks root by the RFC 6901 JSON pointer in pointer, which
// may carry a leading "#" — every $ref in an OpenAPI document has one.
func navigatePointer(root any, pointer string) (any, error) {
	p := strings.TrimPrefix(pointer, "#")
	if p == "" {
		return root, nil
	}
	if !strings.HasPrefix(p, "/") {
		return nil, fmt.Errorf("%w: %q", ErrPointerNotFound, pointer)
	}
	current := root
	for seg := range strings.SplitSeq(p[1:], "/") {
		seg = unescapePointerToken(seg)
		switch node := current.(type) {
		case map[string]any:
			v, ok := node[seg]
			if !ok {
				return nil, fmt.Errorf("%w: %q", ErrPointerNotFound, pointer)
			}
			current = v
		case []any:
			idx, err := strconv.Atoi(seg)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, fmt.Errorf("%w: %q", ErrPointerNotFound, pointer)
			}
			current = node[idx]
		default:
			return nil, fmt.Errorf("%w: %q", ErrPointerNotFound, pointer)
		}
	}
	return current, nil
}

// unescapePointerToken decodes RFC 6901 escapes: "~1" first (to "/"), then
// "~0" (to "~") — reversing that order would corrupt a key containing both.
func unescapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~1", "/")
	tok = strings.ReplaceAll(tok, "~0", "~")
	return tok
}

// EscapePointerToken RFC-6901-escapes one segment for embedding in a JSON
// pointer: "~" first (to "~0"), then "/" (to "~1"). That order is the
// reverse of [unescapePointerToken] on purpose — escaping "/" first would
// let the "~" just inserted as part of "~1" get mangled by the very next
// replacement.
//
// Exported (P3a) as the canonical home for this escape, for the item
// schema pointer resource derivation (internal/specs) walks and stores as
// entities' entity_schema. internal/gen and internal/specs already carry
// their own unexported copies of this exact rule (gen.go's escapePointerToken,
// index.go's own); this slice deliberately leaves both of those standing —
// converting their seven call sites is a mechanical diff with no acceptance
// clause behind it, deferred rather than folded in here.
func EscapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}
