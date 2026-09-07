// options_from.go holds the two constructors that stood, hand-written, at
// five call sites across two packages: the [Options] literal that copies six
// fields out of a [domain.Settings], and the Load -> NewResolver -> New
// triplet that turns a raw document into a generator. Both were literally
// identical everywhere; the reason to give them a name is not brevity but
// that a SEVENTH Options field (the next per-workspace knob) would otherwise
// have to be remembered five times, and the sites that forgot it would keep
// compiling and keep generating — silently, from the wrong settings.
package gen

import (
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/openapi"
)

// OptionsFrom is the one place a [domain.Settings] becomes [Options]. It
// deliberately fills only the six fields the settings carry plus MaxBytes:
// MaxDepth has no source in config or settings (see Options' own doc
// comment) and Now is left nil so the package clock applies — a caller that
// needs an injected clock sets it on the returned value, which is why this
// returns Options by value rather than taking a pointer to fill.
//
// maxBytes is an int64 because that is config.Config.MaxResponse's type;
// <=0 still means "the package default", exactly as a hand-written literal
// would have.
func OptionsFrom(s domain.Settings, maxBytes int64) Options {
	return Options{
		Seed:     s.Seed,
		ListSize: s.ListSize,
		NullRate: s.NullRate,
		MaxBytes: maxBytes,
		Identity: s.Identity,
		Auth:     s.Auth,
	}
}

// OverDocument loads raw, wraps it in a resolver at the default $ref budget
// and builds a generator over it — the triplet buildRuntime ran twice (the
// skeleton document and the normalized spec) and resources' buildGenerator a
// third time, always with the SAME budget. The budget is not a parameter on
// purpose: three call sites passing openapi.DefaultRefBudget is not a choice
// any of them was making, and a fourth that wanted a different one should say
// so by building its resolver itself rather than by threading a number nobody
// else varies.
//
// The resolver is returned alongside the generator because two of the three
// callers need it directly afterwards — patchedSchemas compiles against it,
// and resources' two-hop $ref walk reads it — and [Generator] does not expose
// the one it holds.
//
// The error is returned unwrapped: each caller already says which document it
// was ("the skeleton document", "normalized document for spec 12"), and this
// function does not know which one it was handed.
func OverDocument(raw []byte, opts Options) (*Generator, *openapi.Resolver, error) {
	doc, _, err := openapi.Load(raw)
	if err != nil {
		return nil, nil, err
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	return New(resolver, opts), resolver, nil
}
