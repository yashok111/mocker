// golden_p1b_test.go is HARD RULE 6's guard, not an ordinary regression test:
// "no recipe rows must mean no change" is a claim about every byte P1b's
// generator ever produced, across every workspace in the installation, and
// the ONLY thing that can actually prove that is a snapshot captured from
// the tree BEFORE this phase touched internal/gen at all. TestAcceptance_
// Determinism (acceptance_test.go) is a DIFFERENT, weaker check: it proves
// the generator is internally consistent WITHIN one build (two Generators
// constructed in the SAME test binary agree), which cannot see a bug that
// shifted every body the same way — a seed input reordered, an iteration
// order changed, a priority-chain seam wired one step too early. This file
// is what catches that class of regression instead: it hashes every
// response variant's body under a fixed Options/Request shape and compares
// against testdata/p1b_body_hashes.json, a file this test itself wrote,
// once, from the pre-P1c tree, under MOCKER_REGENERATE_GOLDEN=1 — and never
// again for the rest of this run. A hash that stops matching means the code
// changed what P1b promised never to change; the fix is in the code, never
// in re-running this file with the env var set again. (Regenerated exactly
// once more, 2026-09-03, when the acceptance document itself was replaced
// by its sanitised public twin — a different corpus, not a different
// generator: the P1b determinism claim was re-established over the new
// document from the tree that shipped P7b.)
//
// package gen_test, NOT gen — enumerating fx.ops/fx.variants needs a real
// *specs.Repo, and internal/specs imports internal/gen, so a package-gen
// test importing specs would be an import cycle (acceptance_test.go's own
// header explains this the same way; its helpers are reused here rather
// than redeclared, since a second copy could silently drift from the first
// and defeat the whole point of "one shared enumeration").
package gen_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/gen"
)

// goldenOptions is the JSON-serializable slice of gen.Options this golden
// actually pins — gen.Options itself carries a `Now func() time.Time` field,
// which encoding/json cannot marshal at all, so the frozen instant is
// recorded as its own RFC3339 string instead of the func that produces it.
type goldenOptions struct {
	Seed     int64   `json:"seed"`
	ListSize int     `json:"listSize"`
	NullRate float64 `json:"nullRate"`
	MaxBytes int64   `json:"maxBytes"`
	Now      string  `json:"now"`
}

// goldenDocument is testdata/p1b_body_hashes.json's own shape: the Options
// the corpus was generated under, plus the corpus itself — selector-keyed
// (see goldenCorpus) hex sha256 of each variant's body, or "-" for a
// variant whose Body legitimately returns nothing.
type goldenDocument struct {
	Options goldenOptions     `json:"options"`
	Bodies  map[string]string `json:"bodies"`
}

// goldenHashesPath is testdata/p1b_body_hashes.json, relative to this
// package's own directory (`go test` sets the working directory to the
// package under test).
const goldenHashesPath = "testdata/p1b_body_hashes.json"

// goldenOpts is the ONE fixed Options this whole golden is generated and
// compared under — every entry in the corpus must be produced under
// identical settings, or a hash mismatch would just as easily mean "the
// options changed" as "the code changed", defeating the file's purpose.
func goldenOpts() gen.Options {
	return gen.Options{
		Seed: 12345, ListSize: 10, NullRate: 0, MaxBytes: 4 << 20, Now: fixedClock,
	}
}

// goldenCorpus builds the selector-keyed body-hash map every later agent
// that touches internal/gen compares against. Declared ONCE, here, and
// reused by both the write path (MOCKER_REGENERATE_GOLDEN=1) and the
// compare path — two independently hand-written enumerations of "every
// operation, every variant" would inevitably diverge from each other over
// time, and a divergence here is indistinguishable from the very regression
// this file exists to catch.
//
// The request shape mirrors acceptance_test.go's own full-corpus pass
// (TestAcceptance_SchemaConformance, lines 389-392) exactly: SeedList
// hashes Method, CanonicalPath, the sorted PathParams and Status, so any
// drift from that shape would silently pin a DIFFERENT seed than the one
// real requests actually land on. ListFamily and IDParam are deliberately
// left zero — a golden has to pin ONE shape, and list generation's own
// identity contract already has its own dedicated acceptance test
// (TestAcceptance_ListContract).
//
// THE MAP KEY IS THE SELECTOR, NOT THE STATUS: HTTPStatus is not unique per
// variant (specs' classifySelector maps both "default" and "2XX" to 200),
// so a status-keyed golden would silently let a "default" error body
// overwrite a "200" success body's own entry for every operation that
// declares both — including POST /api/v1/auth/token, this phase's own
// criterion. v.Selector, in contrast, is a JSON object key in the response
// map, unique within one operation by construction, and op.Path (the
// templated path, never CanonicalPath) plus op.Method makes it unique
// across the whole corpus.
func goldenCorpus(t *testing.T, fx *acceptanceFixture) map[string]string {
	t.Helper()
	g := gen.New(fx.resolver, goldenOpts())

	corpus := make(map[string]string)
	for _, op := range fx.ops {
		for _, v := range fx.variants[op.ID] {
			key := fmt.Sprintf("%s %s #%s", strings.ToUpper(op.Method), op.Path, v.Selector)
			req := gen.Request{
				Method: strings.ToUpper(op.Method), CanonicalPath: op.CanonicalPath,
				PathParams: samplePathParams(op.Path), Query: url.Values{}, Status: v.HTTPStatus,
			}
			b, err := g.Body(v, req)
			if err != nil {
				t.Fatalf("golden corpus: %s: Body: %v", key, err)
			}
			if b == nil {
				// A variant that legitimately produces no body (most 204s,
				// and any variant with SchemaPtr=="") is recorded as "-" so
				// a LATER regression that starts emitting bytes for it — or
				// stops emitting bytes for one that used to — shows up as a
				// visible diff, never as a silently absent map key.
				corpus[key] = "-"
				continue
			}
			sum := sha256.Sum256(b)
			corpus[key] = hex.EncodeToString(sum[:])
		}
	}
	return corpus
}

// TestGoldenP1bBodyHashes is HARD RULE 6's actual guard. Two modes:
//
//   - MOCKER_REGENERATE_GOLDEN=1: writes testdata/p1b_body_hashes.json from
//     the CURRENT tree. This is legal exactly ONCE for this run — the Engine
//     agent's STEP ZERO, before internal/gen is touched at all — and the
//     phase digest is explicit that no later agent may set this env var
//     again: doing so would silently rebase the guard onto whatever the
//     code THEN produces, which keeps the assertion green while making it
//     prove nothing.
//   - unset (the normal `go test ./...` path): reads the committed golden
//     and compares every entry. A mismatch means P1c changed a byte P1b
//     promised not to change; the fix belongs in the code that changed it,
//     never in re-running this file with the env var set.
func TestGoldenP1bBodyHashes(t *testing.T) {
	// C8 (as it stood until 2026-09-03): the golden guard must actually RUN,
	// not silently t.Skipf away on a machine without the acceptance document
	// — a stat on the document's path was the first statement here for that
	// reason. The corpus is embedded now (internal/testspec), so there is no
	// machine without it and nothing left to stat.
	fx := loadAcceptance(t)
	corpus := goldenCorpus(t, fx)

	if os.Getenv("MOCKER_REGENERATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenHashesPath), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", filepath.Dir(goldenHashesPath), err)
		}
		doc := goldenDocument{
			Options: goldenOptions{
				Seed: 12345, ListSize: 10, NullRate: 0, MaxBytes: 4 << 20,
				Now: fixedClock().Format(time.RFC3339),
			},
			Bodies: corpus,
		}
		raw, err := json.MarshalIndent(doc, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		if err := os.WriteFile(goldenHashesPath, raw, 0o644); err != nil {
			t.Fatalf("write golden %s: %v", goldenHashesPath, err)
		}
		t.Logf("MOCKER_REGENERATE_GOLDEN=1: wrote %d variant hashes to %s", len(corpus), goldenHashesPath)
		return
	}

	raw, err := os.ReadFile(goldenHashesPath)
	if err != nil {
		if os.IsNotExist(err) {
			t.Fatalf("golden %s does not exist — it must be produced once, by the Engine agent's STEP ZERO, with MOCKER_REGENERATE_GOLDEN=1 set; see this file's own header", goldenHashesPath)
		}
		t.Fatalf("read golden %s: %v", goldenHashesPath, err)
	}
	var want goldenDocument
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("decode golden %s: %v", goldenHashesPath, err)
	}

	var mismatches, added, missing int
	for key, gotHash := range corpus {
		wantHash, ok := want.Bodies[key]
		if !ok {
			added++
			t.Errorf("golden %s: variant %q is new — not present in the golden captured before this phase", goldenHashesPath, key)
			continue
		}
		if wantHash != gotHash {
			mismatches++
			t.Errorf("HARD RULE 6 violation: golden %s: variant %q body hash changed (want %s, got %s) — a no-recipes request must produce byte-identical output to P1b",
				goldenHashesPath, key, wantHash, gotHash)
		}
	}
	for key := range want.Bodies {
		if _, ok := corpus[key]; !ok {
			missing++
			t.Errorf("golden %s: variant %q from the golden is missing from this run's corpus", goldenHashesPath, key)
		}
	}

	t.Logf("MEASUREMENTS golden: %d variants compared, %d mismatches, %d new, %d missing (golden captured under %+v)",
		len(corpus), mismatches, added, missing, want.Options)
	if len(corpus) == 0 {
		t.Fatal("golden corpus is empty — the assertion did not actually run")
	}
}
