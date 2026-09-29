package apidesign

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// AnalyzeImpactDocuments compares exact JSON documents without normalizing or
// mutating either document. Unsupported dependencies remain explicit warnings.
func AnalyzeImpactDocuments(ctx context.Context, before, after string) (ImpactAnalysis, error) {
	c := newImpactCollector(ctx)
	if err := ctx.Err(); err != nil {
		return c.result, err
	}
	a, err := impactDocument(before)
	if err != nil {
		return c.result, err
	}
	b, err := impactDocument(after)
	if err != nil {
		return c.result, err
	}
	if !c.guardDocument(a, 0) || !c.guardDocument(b, 0) {
		return c.result, ctx.Err()
	}
	c.diff("", a, b, 0)
	old, err := c.snapshot(a, "before")
	if err != nil {
		return c.result, err
	}
	next, err := c.snapshot(b, "after")
	if err != nil {
		return c.result, err
	}
	c.matchOperations(old, next)
	for i := range c.result.Changes {
		if !c.visit() {
			break
		}
		change := &c.result.Changes[i]
		c.classifyImpact(change, old, next)
		if change.ChangeClass == "metadata" && change.Compatibility == "compatible" {
			continue
		}
		c.propagate(*change, old)
		c.propagate(*change, next)
	}
	if err := ctx.Err(); err != nil {
		return c.result, err
	}
	c.hydrateLocators(old)
	c.hydrateLocators(next)
	c.checkStateBindings(next)
	if err := ctx.Err(); err != nil {
		return c.result, err
	}
	c.result.Coverage.ChangesReturned = len(c.result.Changes)
	c.result.Coverage.EntitiesReturned = len(c.result.Affected)
	c.result.Coverage.EvidenceReturned = len(c.result.Evidence)
	slices.Sort(c.result.Coverage.TruncatedReasons)
	return c.result, nil
}
func impactDocument(raw string) (map[string]any, error) {
	dec := jsonx.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var root map[string]any
	if err := dec.Decode(&root); err != nil {
		return nil, invalidField("document", "Ожидается JSON-объект: "+err.Error())
	}
	if root == nil {
		return nil, invalidField("document", "Ожидается JSON-объект")
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, invalidField("document", "Ожидается один JSON-объект")
	}
	return root, nil
}
func impactID(kind, value string) string {
	hash := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%s-%x", kind, hash[:12])
}
func (c *impactCollector) diff(pointer string, a, b any, depth int) {
	if !c.visit() {
		return
	}
	if depth > impactMaxDepth {
		c.truncate("traversal")
		return
	}
	am, aok := a.(map[string]any)
	bm, bok := b.(map[string]any)
	if aok && bok {
		keys := maps.Clone(am)
		maps.Copy(keys, bm)
		for _, key := range slices.Sorted(maps.Keys(keys)) {
			if !c.visit() {
				return
			}
			av, ae := am[key]
			bv, be := bm[key]
			at := pointer + "/" + escape(key)
			switch {
			case !ae:
				c.change(at, "added", nil, bv)
			case !be:
				c.change(at, "removed", av, nil)
			default:
				c.diff(at, av, bv, depth+1)
			}
		}
		return
	}

	aa, aok := a.([]any)
	ba, bok := b.([]any)
	parts := impactTokens(pointer)
	atomicArray := len(parts) > 0 && slices.Contains([]string{"required", "enum", "parameters", "security"}, parts[len(parts)-1])
	if aok && bok && !atomicArray {
		for i := 0; i < max(len(aa), len(ba)); i++ {
			if !c.visit() {
				return
			}
			at := pointer + "/" + strconv.Itoa(i)
			switch {
			case i >= len(aa):
				c.change(at, "added", nil, ba[i])
			case i >= len(ba):
				c.change(at, "removed", aa[i], nil)
			default:
				c.diff(at, aa[i], ba[i], depth+1)
			}
		}
		return
	}
	// Required and parameter arrays are atomic so semantic rules compare whole
	// sets and identities, never index replacements.

	if !reflect.DeepEqual(a, b) {
		c.change(pointer, "changed", a, b)
	}
}
func (c *impactCollector) change(pointer, kind string, a, b any) {
	if len(c.result.Changes) >= impactMaxChanges {
		c.truncate("changes")
		return
	}
	change := ImpactChange{ID: impactID("change", pointer), Pointer: pointer, Kind: kind, ChangeClass: "contract", Compatibility: "review", ReasonCode: "contract_changed", Explanation: "Изменился контракт; требуется проверка потребителей"}
	if kind != "added" {
		change.BeforeJSON, change.BeforeTruncated = impactExcerpt(a)
	}
	if kind != "removed" {
		change.AfterJSON, change.AfterTruncated = impactExcerpt(b)
	}
	c.result.Changes = append(c.result.Changes, change)
}
func impactExcerpt(value any) (*string, bool) {
	raw, err := jsonx.Marshal(value)
	if err != nil || len(raw) > impactMaxExcerpt {
		return nil, true
	}
	return new(string(raw)), false
}
func impactObject(v any) map[string]any { m, _ := v.(map[string]any); return m }
func impactText(v any) string           { s, _ := v.(string); return s }
func impactTokens(pointer string) []string {
	if pointer == "" {
		return []string{}
	}
	out := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for i, s := range out {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(s, "~1", "/"), "~0", "~")
	}
	return out
}
func impactParent(pointer string) string {
	parent, _, _ := strings.CutLast(pointer, "/")
	return parent
}
func impactAncestor(parent, child string) bool {
	return parent == "" || parent == child || strings.HasPrefix(child, parent+"/")
}
func impactOverlap(a, b string) bool { return impactAncestor(a, b) || impactAncestor(b, a) }

// Bound opaque data too: examples and extensions are excluded from dependency
// discovery, but still participate in exact structural comparison.
func (c *impactCollector) guardDocument(value any, depth int) bool {
	if !c.visit() {
		return false
	}
	if depth > impactMaxDepth {
		c.truncate("traversal")
		return false
	}
	switch node := value.(type) {
	case map[string]any:
		for _, key := range slices.Sorted(maps.Keys(node)) {
			if !c.guardDocument(node[key], depth+1) {
				return false
			}
		}
	case []any:
		for _, child := range node {
			if !c.guardDocument(child, depth+1) {
				return false
			}
		}
	}
	return true
}
