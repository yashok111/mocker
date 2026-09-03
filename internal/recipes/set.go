package recipes

import (
	"fmt"
	"maps"
	"strconv"

	"github.com/yashok111/mocker/internal/jsonx"
)

// --- Path grammar ------------------------------------------------------
//
// A data path, as internal/gen produces it (schema.go's joinPath and its
// "%s[%d]" array-index formatting), is a sequence of dotted property names
// and bracketed array indices: "user.profile.avatar_url", "items[2].status",
// "roles[0]". The root is "". A pattern uses the same grammar plus "[*]"
// for "any index". tokenize turns either one into the same token sequence
// so matching and precedence are one piece of code, not two that have to be
// kept in sync by hand.

type tokenKind uint8

const (
	tokProp tokenKind = iota
	tokIndex
	tokWildcard
)

type token struct {
	kind tokenKind
	name string // set for tokProp
	idx  int    // set for tokIndex
}

// tokenize splits a data path or pattern into its dotted/bracketed segments.
// The empty string (the response root) tokenizes to an empty slice.
func tokenize(path string) []token {
	var toks []token
	i, n := 0, len(path)
	for i < n {
		j := i
		for j < n && path[j] != '.' && path[j] != '[' {
			j++
		}
		if j > i {
			toks = append(toks, token{kind: tokProp, name: path[i:j]})
		}
		i = j
		if i < n && path[i] == '.' {
			i++
			continue
		}
		for i < n && path[i] == '[' {
			end := i + 1
			for end < n && path[end] != ']' {
				end++
			}
			inner := path[i+1 : end]
			if inner == "*" {
				toks = append(toks, token{kind: tokWildcard})
			} else {
				// A malformed index ("[x]") parses as 0 rather than erroring
				// here: tokenize has no error return, and a bogus index just
				// fails to match anything real, which is the safe outcome
				// for a stored pattern nobody validated against this rule.
				idx, _ := strconv.Atoi(inner)
				toks = append(toks, token{kind: tokIndex, idx: idx})
			}
			i = end + 1
			if i < n && path[i] == '.' {
				i++
			}
		}
	}
	return toks
}

// MatchPattern reports whether a compiled-style pattern matches an absolute
// data path. Exported for the auth preset, which shows the operator which
// paths a proposed binding would hit.
func MatchPattern(pattern, dataPath string) bool {
	return matchTokens(tokenize(pattern), tokenize(dataPath))
}

func matchTokens(pat, dat []token) bool {
	if len(pat) != len(dat) {
		return false
	}
	for i, pt := range pat {
		dt := dat[i]
		switch pt.kind {
		case tokWildcard:
			if dt.kind != tokIndex {
				return false
			}
		case tokIndex:
			if dt.kind != tokIndex || dt.idx != pt.idx {
				return false
			}
		case tokProp:
			if dt.kind != tokProp || dt.name != pt.name {
				return false
			}
		}
	}
	return true
}

// specVector scores a pattern's own tokens for precedence: 1 where the
// token is literal (a prop name or an exact index — both forced equal to
// dataPath's token at any position where the pattern matches at all), 0 at
// a wildcard. Comparing two vectors left-to-right (moreSpecific, below) is
// exactly DESIGN §9's "an exact index beats a wildcard, more literal
// segments beat fewer": the first position where two matching patterns
// differ decides the winner, so an earlier exact beats a later one
// regardless of how many wildcards either pattern carries elsewhere.
func specVector(toks []token) []byte {
	v := make([]byte, len(toks))
	for i, t := range toks {
		if t.kind != tokWildcard {
			v[i] = 1
		}
	}
	return v
}

// --- Set -------------------------------------------------------------------

type compiledPattern struct {
	raw    string
	recipe Recipe
	tokens []token
	spec   []byte
}

// Set is the compiled bindings for ONE response variant: pattern -> recipe.
//
// Patterns are bucketed by their own FIRST token rather than kept in one
// flat slice — see Lookup's own doc comment for why that split is exact,
// not a heuristic (round-1 finding #7: an unindexed scan was O(all bound
// patterns) per leaf/array node, with nothing bounding either factor —
// measured, 200,000 bindings turned an 83,649-byte response's 1.87ms
// generation into 3.034s on a single unauthenticated GET).
type Set struct {
	bindings map[string]Recipe

	// byFirstProp holds every pattern whose leading token is a literal
	// property name (the common case: "items[*].status",
	// "user.profile.avatar_url" both start with one), keyed by that name.
	byFirstProp map[string][]compiledPattern
	// arrayRoot holds patterns whose leading token is an index or "[*]" — a
	// bare top-level array response's own elements ("[*].status").
	arrayRoot []compiledPattern
	// root holds the (at most one meaningful) empty pattern, which matches
	// only the empty data path.
	root []compiledPattern
}

// Compile validates every recipe and precompiles every pattern. A nil or
// empty map compiles to a nil *Set, and a nil *Set is a legal receiver for
// every method below — callers must not need a nil check at each call site.
func Compile(bindings map[string]Recipe) (*Set, error) {
	if len(bindings) == 0 {
		return nil, nil
	}
	s := &Set{
		bindings:    make(map[string]Recipe, len(bindings)),
		byFirstProp: make(map[string][]compiledPattern),
	}
	for pattern, r := range bindings {
		if err := r.Validate(); err != nil {
			return nil, fmt.Errorf("recipe bound to %q: %w", pattern, err)
		}
		s.bindings[pattern] = r
		toks := tokenize(pattern)
		cp := compiledPattern{
			raw:    pattern,
			recipe: r,
			tokens: toks,
			spec:   specVector(toks),
		}
		switch {
		case len(toks) == 0:
			s.root = append(s.root, cp)
		case toks[0].kind == tokProp:
			s.byFirstProp[toks[0].name] = append(s.byFirstProp[toks[0].name], cp)
		default: // tokIndex or tokWildcard
			s.arrayRoot = append(s.arrayRoot, cp)
		}
	}
	return s, nil
}

// candidates returns the ONLY patterns that could possibly match a data
// path tokenized as dt, chosen by dt's own first token — see Lookup's doc
// comment for why narrowing to this one bucket can never discard a genuine
// match. dt is a real (never a pattern's) tokenization, so dt[0].kind is
// always tokProp or tokIndex, never tokWildcard.
func (s *Set) candidates(dt []token) []compiledPattern {
	if len(dt) == 0 {
		return s.root
	}
	if dt[0].kind == tokProp {
		return s.byFirstProp[dt[0].name]
	}
	return s.arrayRoot
}

// Len reports how many recipes are bound.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.bindings)
}

// Bindings returns a copy of the compiled pattern -> recipe map. It is a
// copy, not the Set's own map, so a caller building a preview (the auth
// preset) can hold onto and even mutate its own copy without reaching back
// into a Set that other goroutines may be reading concurrently.
func (s *Set) Bindings() map[string]Recipe {
	if s == nil {
		return nil
	}
	out := make(map[string]Recipe, len(s.bindings))
	maps.Copy(out, s.bindings)
	return out
}

// Lookup returns the recipe bound to dataPath. Patterns address data paths
// as DESIGN §9 writes them: "items[*].status", "user.profile.avatar_url",
// "roles[*]". "[*]" matches any index; "[3]" matches exactly index 3. The
// MOST SPECIFIC pattern wins (an exact-index pattern over a wildcard one, a
// longer literal prefix over a shorter one); ties break on the
// lexicographically smaller pattern so the choice is deterministic rather
// than map-order.
//
// Only candidates() — the bucket dataPath's own first token selects — is
// scanned, never every bound pattern: matchTokens' own per-position rule
// means a pattern can only ever match a dataPath whose first token agrees
// with the pattern's own first token (in KIND, and for a literal property
// name, in NAME too), so a pattern living in a different bucket could not
// have matched anyway. Narrowing to one bucket is therefore exact, not a
// heuristic — it never discards a genuine match, only the scan of patterns
// that were never going to match this dataPath in the first place.
func (s *Set) Lookup(dataPath string) (Recipe, bool) {
	if s == nil {
		return Recipe{}, false
	}
	dt := tokenize(dataPath)
	best, ok := bestMatch(s.candidates(dt), dt, nil)
	if !ok {
		return Recipe{}, false
	}
	return best.recipe, true
}

// ListSizeAt returns the length bounds a listSize recipe pins at dataPath —
// dataPath addressing the ARRAY node itself, not an element of it. lo == hi
// for a fixed size. ok is false when no listSize recipe is bound there.
func (s *Set) ListSizeAt(dataPath string) (lo, hi int, ok bool) {
	if s == nil {
		return 0, 0, false
	}
	dt := tokenize(dataPath)
	best, found := bestMatch(s.candidates(dt), dt, func(p compiledPattern) bool {
		return p.recipe.Kind == KindListSize
	})
	if !found {
		return 0, 0, false
	}
	lo, hi, err := parseListSize(best.recipe.Data)
	if err != nil {
		// Compile already ran Validate, which parses this exact Value with
		// the same function — reaching here would mean the two disagree,
		// which is a bug in this package, not bad input. Decline rather
		// than propagate a panic into the mock plane.
		return 0, 0, false
	}
	return lo, hi, true
}

// bestMatch scans patterns matching dt and passing keep (nil keep accepts
// everything), returning the most specific one under the total order
// documented on Lookup. It is shared by Lookup and ListSizeAt so the
// precedence rule is written exactly once.
func bestMatch(patterns []compiledPattern, dt []token, keep func(compiledPattern) bool) (compiledPattern, bool) {
	var best compiledPattern
	found := false
	for _, p := range patterns {
		if keep != nil && !keep(p) {
			continue
		}
		if !matchTokens(p.tokens, dt) {
			continue
		}
		if !found || moreSpecific(p, best) {
			best = p
			found = true
		}
	}
	return best, found
}

// moreSpecific reports whether a should replace b as the current best
// match. Both are guaranteed (by bestMatch's caller) to match the same
// dataPath, hence to have equal-length spec vectors — so this is a plain
// lexicographic comparison, literal (1) beating wildcard (0), with the raw
// pattern string as the final, always-decisive tiebreak.
func moreSpecific(a, b compiledPattern) bool {
	for i := range a.spec {
		if a.spec[i] != b.spec[i] {
			return a.spec[i] > b.spec[i]
		}
	}
	return a.raw < b.raw
}

// parseListSize decodes a listSize recipe's Value as either a bare integer
// (a fixed size, lo==hi) or a [lo,hi] range.
func parseListSize(raw jsonx.RawMessage) (lo, hi int, err error) {
	if len(raw) == 0 {
		return 0, 0, fmt.Errorf("needs a value")
	}
	var n int
	if err := jsonx.Unmarshal(raw, &n); err == nil {
		return n, n, nil
	}
	var pair []int
	if err := jsonx.Unmarshal(raw, &pair); err != nil || len(pair) != 2 {
		return 0, 0, fmt.Errorf("value must be a number or a [lo,hi] pair")
	}
	return pair[0], pair[1], nil
}
