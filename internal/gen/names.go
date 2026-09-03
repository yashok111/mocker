package gen

import "strings"

// names.go is the field-name realism corpus for leafValue (values.go).
// DESIGN §9 "Реалистичность": "format, затем имя поля: email, *_url/image/
// avatar/cover → URL, phone, *_at/date, first_name. В реальных спеках format
// почти не проставлен; без разбора имён фронт падает на
// new URL(\"fugiat cupidatat\")." — format is checked first (values.go), and
// falls back to matching the property's OWN name against this table.
//
// This file is DATA, not cleverness: fieldNameCorpus is a plain ordered list
// of (matcher, fieldKind) rows. Adding a name is a one-line addition to the
// table; classifyFieldName's matching loop never needs to change.

// fieldKind is the coarse realism category a property name maps to.
// fieldKindGeneric means "the name told us nothing" — the caller falls
// through to format-only or bare schema-driven generation.
type fieldKind int

const (
	fieldKindGeneric fieldKind = iota
	fieldKindEmail
	fieldKindURL // also covers image-ish names (photo/avatar/cover/...); isImageField picks the flavor at generation time
	fieldKindPhone
	fieldKindTimestamp
	fieldKindPersonName
	fieldKindTitle
	fieldKindDescription
	fieldKindStatus
	fieldKindCode
	fieldKindColor
	fieldKindSlug
	fieldKindCount
	fieldKindID
)

// matchKind is how one fieldNameRule tests a (lowercased) field name.
type matchKind int

const (
	mExact matchKind = iota
	mSuffix
	mPrefix
	mContains
)

// fieldNameRule is one row of the corpus. Rules are tried top to bottom;
// the first match wins — so a specific rule (an exact word, or a suffix
// anchored on "_") is listed before a broader "contains" rule it could
// otherwise be shadowed by.
type fieldNameRule struct {
	kind    matchKind
	pattern string
	result  fieldKind
}

// fieldNameCorpus: one line per name. mExact/mSuffix/mPrefix are anchored on
// word boundaries ("_") where it matters, so they cannot misfire on an
// unrelated word that merely contains the pattern (e.g. "id" as mExact/
// mSuffix("_id") never matches "valid" or "guide" the way a bare mContains
// "id" would). mContains is reserved for words specific enough that a
// substring hit is still meaningful (e.g. "email", "avatar").
var fieldNameCorpus = []fieldNameRule{
	// contact / media — DESIGN's own examples, first
	{mContains, "email", fieldKindEmail},
	{mContains, "phone", fieldKindPhone},
	{mContains, "mobile", fieldKindPhone},
	{mContains, "photo", fieldKindURL},
	{mContains, "image", fieldKindURL},
	{mContains, "avatar", fieldKindURL},
	{mContains, "cover", fieldKindURL},
	{mContains, "thumbnail", fieldKindURL},
	{mSuffix, "_url", fieldKindURL},
	{mSuffix, "_uri", fieldKindURL},
	{mSuffix, "_link", fieldKindURL},
	{mExact, "url", fieldKindURL},
	{mExact, "uri", fieldKindURL},
	{mExact, "link", fieldKindURL},

	// time — checked here only for the ORDINARY (seed-derived) case;
	// deadline-shaped names (exp, *_expires_at, ...) are intercepted
	// earlier, by isDeadlineField, before this classification is even
	// consulted for that purpose.
	{mSuffix, "_at", fieldKindTimestamp},
	{mSuffix, "_date", fieldKindTimestamp},
	{mPrefix, "date_", fieldKindTimestamp},
	{mPrefix, "time_", fieldKindTimestamp},
	{mExact, "date", fieldKindTimestamp},

	// identity / naming
	{mSuffix, "_name", fieldKindPersonName},
	{mExact, "name", fieldKindPersonName},
	{mExact, "username", fieldKindPersonName},
	{mExact, "login", fieldKindPersonName},

	// short text
	{mContains, "title", fieldKindTitle},
	{mContains, "description", fieldKindDescription},
	{mContains, "summary", fieldKindDescription},
	{mContains, "status", fieldKindStatus},
	{mContains, "state", fieldKindStatus},
	{mContains, "code", fieldKindCode},
	{mContains, "color", fieldKindColor},
	{mContains, "colour", fieldKindColor},
	{mContains, "slug", fieldKindSlug},

	// counters — mExact/mSuffix only: mContains("count") would misfire on
	// "discount", "account", etc.
	{mExact, "count", fieldKindCount},
	{mExact, "limit", fieldKindCount},
	{mSuffix, "_count", fieldKindCount},
	{mSuffix, "_limit", fieldKindCount},

	// identity keys — last, and deliberately mExact/mSuffix("_id") only:
	// a bare mContains("id") would misfire on "valid", "guide", "avoid".
	{mExact, "id", fieldKindID},
	{mSuffix, "_id", fieldKindID},
}

// classifyFieldName maps a property's own (already leaf-extracted) name to
// a fieldKind, per fieldNameCorpus. Matching is case-insensitive.
func classifyFieldName(name string) fieldKind {
	n := strings.ToLower(name)
	for _, r := range fieldNameCorpus {
		switch r.kind {
		case mExact:
			if n == r.pattern {
				return r.result
			}
		case mSuffix:
			if strings.HasSuffix(n, r.pattern) {
				return r.result
			}
		case mPrefix:
			if strings.HasPrefix(n, r.pattern) {
				return r.result
			}
		case mContains:
			if strings.Contains(n, r.pattern) {
				return r.result
			}
		}
	}
	return fieldKindGeneric
}

// fieldNameOf extracts the meaningful LEAF name from a dataPath such as
// "user.profile.avatar_url" (-> "avatar_url") or "items[3].name"
// (-> "name"). It restarts at the last '.' (dataPath already restarts at
// the item root inside a list item — see schema.go's joinPath) and, for a
// bare array-item path like "roles[2]" (no '.' before the bracket), strips
// the trailing "[N]" to recover the property name ("roles").
func fieldNameOf(dataPath string) string {
	if i := strings.LastIndexByte(dataPath, '.'); i >= 0 {
		dataPath = dataPath[i+1:]
	}
	if i := strings.IndexByte(dataPath, '['); i >= 0 {
		dataPath = dataPath[:i]
	}
	return dataPath
}

// isDeadlineField reports whether lname (already-lowercased leaf name) is
// one of DESIGN §9's deadline-shaped names — exp, *_expires_at, expires_in,
// not_after, *_valid_until — the ones that must derive from Options.Now()
// rather than the seed (see deadlineValue in values.go).
func isDeadlineField(lname string) bool {
	switch lname {
	case "exp", "not_after", "expires_in", "valid_until":
		return true
	}
	return strings.HasSuffix(lname, "_expires_at") || strings.HasSuffix(lname, "_valid_until")
}

// isImageField reports whether lname looks like it holds a picture rather
// than an arbitrary link — used only to pick a plausible file extension
// once fieldKindURL has already been decided (see genURL in values.go); it
// does not affect classification itself.
func isImageField(lname string) bool {
	for _, s := range [...]string{"photo", "image", "avatar", "cover", "thumbnail"} {
		if strings.Contains(lname, s) {
			return true
		}
	}
	return false
}

// --- small deterministic word/token corpora -------------------------------
//
// None of these claim to be a real faker library (out of scope: no new
// dependency, HARD RULE 2). They exist so a "name"/"title"/"description"
// field reads as a plausible word instead of the bare alphanumeric filler
// genString (schema.go) produces, which is exactly the realism gap DESIGN
// §9 calls out ("фронт падает на new URL(\"fugiat cupidatat\")" — a
// human-shaped placeholder beats a random token wherever the property name
// says what shape is expected).

var firstNamesCorpus = []string{
	"Alex", "Jamie", "Taylor", "Jordan", "Morgan", "Casey", "Riley", "Avery",
	"Quinn", "Reese", "Skyler", "Drew", "Rowan", "Emerson", "Hayden", "Kai",
	"Sage", "Blair", "Elliot", "Parker",
}

var lastNamesCorpus = []string{
	"Smith", "Johnson", "Brown", "Garcia", "Martinez", "Davis", "Rodriguez",
	"Wilson", "Anderson", "Thomas", "Moore", "Jackson", "Martin", "Lee",
	"Perez", "Clark", "Lewis", "Walker", "Young", "Allen",
}

var adjectiveCorpus = []string{
	"Rapid", "Bright", "Silent", "Golden", "Northern", "Quiet", "Bold",
	"Steady", "Modern", "Ancient", "Hidden", "Swift", "Gentle", "Vivid",
	"Distant", "Coastal", "Central", "Primary", "Formal", "Casual",
}

var nounCorpus = []string{
	"River", "Falcon", "Bridge", "Harbor", "Signal", "Orbit", "Canyon",
	"Meadow", "Compass", "Beacon", "Summit", "Terminal", "Horizon",
	"Archive", "Cascade", "Ledger", "Outpost", "Circuit", "Junction", "Relay",
}

var statusCorpus = []string{
	"active", "pending", "inactive", "completed", "cancelled", "draft",
	"archived", "failed", "processing", "approved",
}

var emailDomains = []string{
	"example.com", "mail.example.org", "corp.example.net", "inbox.example.io",
}

var urlHosts = []string{
	"cdn.example.com", "assets.example.org", "static.example.net", "media.example.io",
}

var imageExts = []string{"jpg", "png", "webp"}
