package gen

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// values.go implements leafValue — DESIGN §9 "Приоритет источника значения"
// / "Реалистичность" — the seam declared (as a stub) in stub_values.go.
// leafValue is called from walker.walkSchema on EVERY resolved schema node,
// before the structural object/array walk, so a pinned value can replace a
// whole object or array, not just a scalar.
//
// Priority (DESIGN §9, exact order, later steps only run if earlier ones
// decline):
//
//	recipe -> example/examples (schema-level; media-type-level is handled
//	above this package, in gen.go's Body/contentExample) -> const -> default
//	-> enum -> generated (format, then field name, then schema constraints).
func leafValue(w *walker, schema map[string]any, dataPath string) (any, bool) {
	if v, ok := recipeValue(w, schema, dataPath); ok {
		return v, true
	}
	// Every value below is the DOCUMENT's own — the resolver hands the
	// same maps to every request of a (workspace, revision) — and the
	// walkers that receive it WRITE into it when it is an object (the id
	// forced onto a list item, the page fields spliced into a wrapper, a
	// discriminator property set on a branch). Returned by reference, two
	// concurrent requests of the unauthenticated plane write the same map
	// and the runtime aborts the whole process ("concurrent map writes" is
	// a fatal error, not a panic the middleware recovers). cloneDocValue
	// is the one door such a value leaves the document through.
	if v, ok := firstExample(schema["examples"]); ok {
		return cloneDocValue(v), true
	}
	if v, ok := schema["const"]; ok {
		return cloneDocValue(v), true
	}
	if v, ok := schema["default"]; ok {
		// comma-ok, not a nil check: an explicit "default": null must reach
		// here and return (nil, true) exactly like any other pinned value —
		// walkSchema no longer short-circuits it before recipe/example/const
		// get their turn (round-1 finding #1). Only type EXACTLY ["null"]
		// (nullableInfo's "forced") is handled before leafValue runs at all;
		// this branch is where a nullable default's null actually gets
		// decided, same priority slot a non-null default would occupy.
		return cloneDocValue(v), true
	}
	if v, ok := enumValue(w, schema, dataPath); ok {
		return v, true
	}
	if v, ok := generatedValue(w, schema, dataPath); ok {
		return v, true
	}
	return nil, false
}

// recipeValue is DESIGN §9 "Рецепты"'s seam into the value-source priority
// chain: a recipe bound to dataPath wins even over a spec-declared example,
// which is the whole point of an override (the operator is overruling the
// document). It declines — falling through to the rest of leafValue's chain
// unchanged — for every one of these, all equally "no recipe applies here":
// no *recipes.Set at all (the common case for a workspace that has never
// touched op_overrides), no recipe bound to this exact (prefix-adjusted)
// path, a Deferred recipe (copy/omit/listSize — evaluated elsewhere: the
// whole-body post-pass for copy/omit, arrayLength for listSize, never
// through Value), or a recipe whose Value legitimately declined (its answer
// cannot be coerced to this node's declared type without lying about the
// schema). lookupRecipe/recipeEnv (recipes.go) do the actual path-prefix
// adjustment and Env construction; this function is only the wiring.
func recipeValue(w *walker, schema map[string]any, dataPath string) (any, bool) {
	r, path, ok := w.lookupRecipe(dataPath)
	if !ok {
		return nil, false
	}
	// fakerValue (faker.go) is this package's registry, wired in here
	// because this is the ONE production path that reaches Recipe.Value
	// (D6(4)) — every other kind ignores the argument that does not apply
	// to it. w.req.Ref is the per-request "ref" resolver (P3c, D5/D6):
	// nil at confirm/reseed/preview, the live closure when mockplane
	// serves a real request — refValue treats nil the same as a resolver
	// that declined, so the recipe's own policy still decides the result.
	v, ok, err := r.Value(w.recipeEnv(schema, dataPath), path, fakerValue, w.req.Ref)
	if err != nil {
		// Unreachable through a compiled *recipes.Set (Compile already ran
		// Validate on every recipe it holds) — but Value's own contract
		// reserves err for a structurally broken recipe, and an open mock
		// endpoint must never turn that into a 500. Decline, same as any
		// other reason this value source doesn't apply.
		return nil, false
	}
	return v, ok
}

// enumValue picks one element of schema's own "enum" deterministically from
// SeedScalar — same seed layer as every other per-field value, so the same
// field on the same request always lands on the same enum member, and two
// different fields (different dataPath) are free to land on different ones.
func enumValue(w *walker, schema map[string]any, dataPath string) (any, bool) {
	enum, ok := schema["enum"].([]any)
	if !ok || len(enum) == 0 {
		return nil, false
	}
	h := SeedScalar(w.seedList, dataPath+"\x00enum")
	return cloneDocValue(enum[int(h%uint64(len(enum)))]), true
}

// cloneDocValue deep-copies a value that came out of the spec document —
// an example, const, default or enum member — so the caller may mutate it.
// Scalars are returned as they are; maps and slices are rebuilt
// recursively. The document is immutable for the whole life of a
// Generator and shared by every concurrent request over it, which is why
// nothing may hand a document map to a walker that writes into it (see
// leafValue). No budget: the value is bounded by the document, and the
// walk's own byte/node ceilings still apply to what is built from it.
func cloneDocValue(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, e := range t {
			out[k] = cloneDocValue(e)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, e := range t {
			out[i] = cloneDocValue(e)
		}
		return out
	default:
		return v
	}
}

// generatedValue is DESIGN §9's "генерация по схеме" realism step: format
// FIRST, then the property's own name (names.go), applied only where it
// adds something a bare type-correct scalar (schema.go's genScalar) would
// not already produce — an untyped/unnamed field declines (ok=false) so
// genScalar's simpler fallback runs instead, keeping this function's
// surface limited to cases that actually need it.
func generatedValue(w *walker, schema map[string]any, dataPath string) (any, bool) {
	format, _ := schema["format"].(string)
	kind := classifyFieldName(fieldNameOf(dataPath))
	switch SchemaType(schema) {
	case "string":
		return generatedString(w, schema, dataPath, format, kind)
	case "integer":
		return generatedInteger(w, schema, dataPath, format, kind)
	case "number":
		return generatedNumber(w, schema, dataPath, format, kind)
	default:
		// boolean/object/array: nothing format- or name-driven to add;
		// defer to structural generation or genScalar's boolean coin flip.
		return nil, false
	}
}

// --- string realism ---------------------------------------------------

// generatedString is the bulk of DESIGN §9's realism rule: "format, затем
// имя поля: email, *_url/image/avatar/cover → URL, phone, *_at/date,
// first_name ... без разбора имён фронт падает на
// new URL(\"fugiat cupidatat\")".
func generatedString(w *walker, schema map[string]any, dataPath, format string, kind fieldKind) (any, bool) { //nolint:gocyclo // one arm per string format
	minLen, maxLen, hasMin, hasMax := declaredStringLength(schema)
	if hasMin && hasMax && minLen > maxLen {
		// Let the caller fall through to genScalar/stringLengthBounds,
		// which raises ErrUnsatisfiable for exactly this shape — leafValue
		// itself never returns an error, only ok=false.
		return nil, false
	}
	fit := func(s string) string { return fitStringLength(s, minLen, maxLen, hasMin, hasMax) }

	// A pattern the generator can satisfy CHEAPLY (rule 5): only the one
	// shape that requires no regex engine at all, an anchored literal. Any
	// other pattern is a documented, deliberate limitation — declared
	// unsatisfiable-by-this-generator rather than attempted and likely
	// wrong; length/format/name realism below still applies.
	if pat, ok := schema["pattern"].(string); ok {
		if v, ok := literalFromAnchoredPattern(pat); ok {
			return fit(v), true
		}
	}

	name := fieldNameOf(dataPath)
	lname := strings.ToLower(name)
	seed := SeedScalar(w.seedList, dataPath)

	switch {
	case isDeadlineField(lname):
		// DESIGN §9 "Время": a deadline-shaped field must track the
		// injected NOW, not the seed, or the mock starts looking
		// permanently expired the day after the workspace's seed was
		// fixed. Checked before format on purpose: format alone (e.g.
		// "date-time") cannot tell us WHICH clock to derive from — only
		// the name can.
		return deadlineValue(w, dataPath, lname, false), true
	case format == "date-time" || kind == fieldKindTimestamp:
		return ordinaryTimestamp(seed), true
	case format == "date":
		return ordinaryDate(seed), true
	case format == "uuid":
		return idString(seed, "uuid"), true
	case format == "uri":
		return fit(genURL(seed, isImageField(lname))), true
	case format == "binary" || format == "byte":
		return genBinaryPlaceholder(seed), true
	case format == "int64", format == "int32", format == "uint", format == "bigint":
		// A numeric format on a STRING-typed field is real (some APIs
		// stringify 64-bit ids to dodge JS float precision) — a decimal
		// digit string, not schema.go's alphanumeric genString filler.
		return fit(strconv.FormatInt(idInteger(seed, format), 10)), true
	case kind == fieldKindEmail:
		return fit(genEmail(seed)), true
	case kind == fieldKindURL:
		return fit(genURL(seed, isImageField(lname))), true
	case kind == fieldKindPhone:
		return genPhone(seed), true
	case kind == fieldKindPersonName:
		return fit(genPersonName(seed, name)), true
	case kind == fieldKindTitle:
		return fit(genWords(seed, 2, 4, true)), true
	case kind == fieldKindDescription:
		return fit(genWords(seed, 6, 14, false)), true
	case kind == fieldKindStatus:
		return statusCorpus[seed%uint64(len(statusCorpus))], true
	case kind == fieldKindCode:
		return fit(genCode(seed)), true
	case kind == fieldKindColor:
		return genColor(seed), true
	case kind == fieldKindSlug:
		return fit(genSlug(seed)), true
	}
	return nil, false
}

// declaredStringLength reads ONLY explicitly-declared minLength/maxLength —
// unlike schema.go's stringLengthBounds, it never invents a default (that
// default, 16, is genString's own filler-length choice, not a schema
// constraint, and must not leak into truncating a realistic generated
// value that the schema itself placed no ceiling on).
// clampStringLength (schema.go) floors a negative bound at 0 and ceilings a
// runaway one at maxGenStringLength — a hostile {"minLength": 50000000} on
// a format/field-name-realistic string (e.g. "email") reaches THIS function
// via fitStringLength's own strings.Repeat padding, not schema.go's bare
// genString fallback, so it needs the identical clamp independently (P1b
// round-1 review, finding 10: measured a 50,000,012-byte body for exactly
// this shape, generated in ~300ms from a single declared field).
func declaredStringLength(schema map[string]any) (minLen, maxLen int, hasMin, hasMax bool) {
	if v, ok := numFromSchema(schema, "minLength"); ok {
		minLen, hasMin = clampStringLength(int(v)), true
	}
	if v, ok := numFromSchema(schema, "maxLength"); ok {
		maxLen, hasMax = clampStringLength(int(v)), true
	}
	return
}

// fitStringLength enforces a schema's OWN declared bounds on a
// format/name-realistic string. Hard schema constraints outrank cosmetic
// realism when the two conflict: truncating a generated email/URL/sentence
// can break its shape, but an unenforced maxLength fails validation
// outright, which is worse.
func fitStringLength(s string, minLen, maxLen int, hasMin, hasMax bool) string {
	if hasMax && maxLen >= 0 && len(s) > maxLen {
		s = s[:maxLen]
	}
	if hasMin && len(s) < minLen {
		filler := byte('x')
		if len(s) > 0 {
			filler = s[len(s)-1]
		}
		s += strings.Repeat(string(filler), minLen-len(s))
	}
	return s
}

// literalFromAnchoredPattern recognizes the one pattern shape cheap enough
// to satisfy exactly without a regex engine (explicitly out of scope, task
// brief rule 5): "^literal$" with no regex metacharacters in between. Any
// other pattern declines — a documented limitation, not a guess.
func literalFromAnchoredPattern(pattern string) (string, bool) {
	if len(pattern) < 2 || pattern[0] != '^' || pattern[len(pattern)-1] != '$' {
		return "", false
	}
	body := pattern[1 : len(pattern)-1]
	if strings.ContainsAny(body, `\.[]{}()*+?|^$`) {
		return "", false
	}
	return body, true
}

// --- time -----------------------------------------------------------------

// ordinaryTimestamp is the SEED-derived half of DESIGN §9's time split: a
// non-deadline "*_at"/"*_date"/format:date-time field must be stable
// forever (same seed -> same value on any day), so it is placed on a fixed
// synthetic calendar entirely from the field's own seed, never from
// Options.Now().
func ordinaryTimestamp(seed uint64) string {
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	const dayRange = 3650 // ~10 years of distinct days
	days := int(seed % dayRange)
	secs := int((seed / dayRange) % 86400)
	return base.AddDate(0, 0, days).Add(time.Duration(secs) * time.Second).Format(time.RFC3339)
}

// ordinaryDate is ordinaryTimestamp's date-only counterpart, for the (in
// this document, unused but legal) format: "date".
func ordinaryDate(seed uint64) string {
	base := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	const dayRange = 3650
	days := int(seed % dayRange)
	return base.AddDate(0, 0, days).Format("2006-01-02")
}

// deadlineValue is the Options.Now()-anchored half of DESIGN §9's time
// split: exp/not_after/*_expires_at/*_valid_until/expires_in must track the
// clock actually in effect for THIS call (w.now, frozen once per
// Body/Headers call — see Generator.newWalker), or a frozen exp would make
// the installation stop working the day after the workspace's seed was
// picked. The per-field OFFSET is still seed-derived (so the same field
// keeps the same "distance from now" across identical requests); only the
// anchor point is real. "expires_in"/"valid_until" (bare, no "_at"/"_expires"
// prefix chain) is the one RELATIVE name in the set — a duration in
// seconds since issuance, not an absolute instant — so it returns the
// offset itself rather than now+offset.
func deadlineValue(w *walker, dataPath, lname string, numeric bool) any {
	seed := SeedScalar(w.seedList, dataPath)
	offset := time.Duration(1+seed%24) * time.Hour
	relative := lname == "expires_in"
	if numeric {
		if relative {
			return int64(offset.Seconds())
		}
		return w.now.Add(offset).Unix()
	}
	if relative {
		return strconv.FormatInt(int64(offset.Seconds()), 10)
	}
	return w.now.Add(offset).UTC().Format(time.RFC3339)
}

// --- numeric realism --------------------------------------------------

// generatedInteger applies DESIGN §9's format-driven realism to integers —
// "uint" (and the id-shaped field-name kinds) must never come out negative,
// which schema.go's own bare fallback (default range [-1000, 1000] when the
// schema gives no bounds) does not guarantee — plus multipleOf, which
// schema.go's genInteger does not implement at all. Explicit schema
// minimum/maximum always win when present; the format/name-driven defaults
// below only fill gaps the schema itself leaves open.
func generatedInteger(w *walker, schema map[string]any, dataPath, format string, kind fieldKind) (any, bool) {
	name := fieldNameOf(dataPath)
	lname := strings.ToLower(name)
	if isDeadlineField(lname) {
		return deadlineValue(w, dataPath, lname, true), true
	}

	mult, hasMult := numFromSchema(schema, "multipleOf")
	nonNegative := format == "uint" || kind == fieldKindCount || kind == fieldKindID
	wide := format == "int64" || format == "bigint" || format == "uint"
	if !hasMult && format == "" && !nonNegative {
		// Nothing format/name-driven applies: defer to genScalar's own
		// bare, schema-min/max-respecting fallback.
		return nil, false
	}

	lo, hi, hasLo, hasHi := integerBounds(schema)
	if !hasLo {
		lo = -1000
		if nonNegative {
			lo = 0
		}
	}
	if !hasHi {
		span := int64(1000)
		if wide {
			span = 1_000_000
		}
		hi = lo + span
	}
	if lo > hi {
		return nil, false
	}

	seed := SeedScalar(w.seedList, dataPath)
	if hasMult && mult >= 1 {
		m := int64(mult)
		loM := ((lo + m - 1) / m) * m // round lo up to the nearest multiple
		hiM := (hi / m) * m           // round hi down to the nearest multiple
		if loM > hiM {
			return nil, false
		}
		span := uint64((hiM-loM)/m) + 1
		return loM + int64(seed%span)*m, true
	}
	// integerSpan (schema.go), not the naive `uint64(hi-lo)+1`: a hostile
	// or malformed document's minimum/exclusiveMaximum can push lo/hi to
	// exactly math.MinInt64/math.MaxInt64, and subtracting them as plain
	// int64 arithmetic FIRST overflows and silently wraps the span to 0 —
	// `seed % span` on that zero span is a live divide-by-zero panic (P1b
	// round-1 review, finding 12; schema.go's genInteger shares this exact
	// span computation and had the identical bug).
	return lo + int64(seed%integerSpan(lo, hi)), true
}

// generatedNumber applies multipleOf to a float schema — schema.go's
// genNumber, like genInteger, does not implement it at all. Format/field
// name carry no float-specific realism in this document's corpus (no
// "amount"/"price"-style hints in the measured names), so this function's
// ENTIRE contribution beyond the deadline split is multipleOf; anything
// else defers to genNumber's own min/max-respecting fallback.
func generatedNumber(w *walker, schema map[string]any, dataPath, format string, kind fieldKind) (any, bool) {
	name := fieldNameOf(dataPath)
	lname := strings.ToLower(name)
	if isDeadlineField(lname) {
		v := deadlineValue(w, dataPath, lname, true)
		if n, ok := v.(int64); ok {
			return float64(n), true
		}
		return v, true
	}

	mult, hasMult := numFromSchema(schema, "multipleOf")
	if !hasMult || mult <= 0 {
		return nil, false
	}
	lo, hasLo := numFromSchema(schema, "minimum")
	hi, hasHi := numFromSchema(schema, "maximum")
	if !hasLo {
		lo = 0
	}
	if !hasHi {
		hi = lo + 1000
	}
	if lo > hi {
		return nil, false
	}

	seed := SeedScalar(w.seedList, dataPath)
	// span stays finite and below 2^62 even for a tiny multipleOf against
	// a wide range, so uint64(span)+1 can never wrap to the zero modulus
	// that P1b's finding 12 already fixed for the integer path.
	span := (hi - lo) / mult
	if math.IsNaN(span) || span < 0 {
		span = 0
	}
	if span > 1<<62 {
		span = 1 << 62
	}
	steps := uint64(span)
	val := lo + float64(seed%(steps+1))*mult
	if val > hi {
		val = hi
	}
	// Snap cleanly onto the multiple grid so float64 accumulation error
	// cannot leave a value that LOOKS like it violates multipleOf.
	return math.Round(val/mult) * mult, true
}

// --- small generators, each a pure function of a seed ------------------
//
// None of these claim to be a real faker library (no new dependency, HARD
// RULE 2) — they exist to satisfy DESIGN §9's realism bar ("URL-shaped
// field must produce a parseable URL") using only the seed and names.go's
// tiny built-in corpora.

func genEmail(seed uint64) string {
	first := strings.ToLower(firstNamesCorpus[seed%uint64(len(firstNamesCorpus))])
	num := fnv1a(seed, []byte("num")) % 10000
	domain := emailDomains[fnv1a(seed, []byte("domain"))%uint64(len(emailDomains))]
	return fmt.Sprintf("%s%d@%s", first, num, domain)
}

func genURL(seed uint64, imageish bool) string {
	host := urlHosts[seed%uint64(len(urlHosts))]
	token := fnv1a(seed, []byte("path")) & 0xFFFFFF
	if imageish {
		ext := imageExts[fnv1a(seed, []byte("ext"))%uint64(len(imageExts))]
		return fmt.Sprintf("https://%s/media/%06x.%s", host, token, ext)
	}
	return fmt.Sprintf("https://%s/r/%06x", host, token)
}

func genPhone(seed uint64) string {
	exch := 200 + (seed/10000)%800 // avoid the reserved 0xx/1xx exchange codes
	line := seed % 10000
	return fmt.Sprintf("+1-555-%03d-%04d", exch, line)
}

func genPersonName(seed uint64, fieldName string) string {
	lname := strings.ToLower(fieldName)
	first := firstNamesCorpus[seed%uint64(len(firstNamesCorpus))]
	last := lastNamesCorpus[fnv1a(seed, []byte("last"))%uint64(len(lastNamesCorpus))]
	switch {
	case strings.Contains(lname, "first"):
		return first
	case strings.Contains(lname, "last"):
		return last
	default:
		return first + " " + last
	}
}

// genWords picks minWords..maxWords words (with replacement) from the
// adjective+noun corpora — used for both "title" (short, capitalized) and
// "description" (longer, lowercase) field kinds.
func genWords(seed uint64, minWords, maxWords int, capitalize bool) string {
	span := uint64(maxWords - minWords + 1)
	n := minWords + int(seed%span)
	pool := make([]string, 0, len(adjectiveCorpus)+len(nounCorpus))
	pool = append(pool, adjectiveCorpus...)
	pool = append(pool, nounCorpus...)

	words := make([]string, n)
	h := seed
	for i := range words {
		h = fnv1a(h, []byte{byte(i)})
		w := pool[h%uint64(len(pool))]
		if !capitalize {
			w = strings.ToLower(w)
		}
		words[i] = w
	}
	return strings.Join(words, " ")
}

func genSlug(seed uint64) string {
	words := strings.Fields(genWords(seed, 2, 3, false))
	return strings.Join(words, "-")
}

func genCode(seed uint64) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no ambiguous 0/O, 1/I
	const length = 6
	b := make([]byte, length)
	h := seed
	for i := range b {
		h = fnv1a(h, []byte{byte(i)})
		b[i] = alphabet[h%uint64(len(alphabet))]
	}
	return string(b)
}

func genColor(seed uint64) string {
	return fmt.Sprintf("#%06x", seed&0xFFFFFF)
}

// genBinaryPlaceholder stands in for format: binary/byte content — a full
// binary-format generator is out of P1b's scope (mirrors Body/finalize's
// own "mock-<mediatype>" placeholder in gen.go for unknown media types).
func genBinaryPlaceholder(seed uint64) string {
	return fmt.Sprintf("bin-%016x", seed)
}
