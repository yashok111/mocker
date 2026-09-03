// faker.go is P2e's producer half of D6's faker seam: internal/recipes
// publishes the twelve-token vocabulary (recipes.FakerTokens) as a plain
// list, because Validate needs to reject an unknown token with no upward
// import; this package owns what each token actually PRODUCES, reusing the
// realism generators values.go and seed.go already have rather than
// inventing a second set. The two sides are joined by recipes.Faker, a
// typed func value — never an import in either direction.
package gen

// fakerProducers is the registry: exactly the twelve token strings D6
// publishes (recipes.FakerTokens), each mapped to a producer that is TOTAL
// under a seed alone — the same "seed only, no field name needed" property
// that separates every one of these twelve from fieldKindURL and
// fieldKindGeneric, which D6 excludes for the opposite reason. Every
// producer already exists elsewhere in this package; nothing here is a new
// generator, only new names for old ones.
//
//   - person.fullName reuses genPersonName with an EMPTY field name, which
//     is TOTAL under "" — an empty name yields the full name (values.go),
//     exactly what the token asks for; that is what admits it while
//     fieldKindURL (whose flavor genuinely depends on the field NAME) stays
//     excluded.
//   - datetime.timestamp/date reuse ordinaryTimestamp/ordinaryDate, the
//     SEED-derived half of the time split (values.go) — never
//     deadlineValue, which needs the request's frozen "now", not a token.
//   - string.uuid reuses idString(seed, "uuid") (seed.go) — the same
//     producer format:"uuid" fields already call.
var fakerProducers = map[string]func(seed uint64) any{
	"person.fullName":    func(seed uint64) any { return genPersonName(seed, "") },
	"internet.email":     func(seed uint64) any { return genEmail(seed) },
	"phone.number":       func(seed uint64) any { return genPhone(seed) },
	"datetime.timestamp": func(seed uint64) any { return ordinaryTimestamp(seed) },
	"datetime.date":      func(seed uint64) any { return ordinaryDate(seed) },
	"lorem.title":        func(seed uint64) any { return genWords(seed, 2, 4, true) },
	"lorem.description":  func(seed uint64) any { return genWords(seed, 6, 14, false) },
	"status.value":       func(seed uint64) any { return statusCorpus[seed%uint64(len(statusCorpus))] },
	"code.value":         func(seed uint64) any { return genCode(seed) },
	"color.hex":          func(seed uint64) any { return genColor(seed) },
	"slug.value":         func(seed uint64) any { return genSlug(seed) },
	"string.uuid":        func(seed uint64) any { return idString(seed, "uuid") },
}

// fakerValue is the recipes.Faker this package hands to Recipe.Value (D6(4))
// — wired in as recipeValue's third argument (values.go), the ONE
// production path that reaches Recipe.Value. ok=false means token is not in
// the registry: unreachable through a compiled *recipes.Set (Validate
// already rejected any token outside recipes.FakerTokens before Compile
// ever saw it), but Value's own contract requires an answer for every
// input, and this function's contract does too — a registry miss declines
// exactly like every other value source that doesn't apply, never a panic
// or an invented value.
func fakerValue(token string, seed uint64) (any, bool) {
	producer, ok := fakerProducers[token]
	if !ok {
		return nil, false
	}
	return producer(seed), true
}
