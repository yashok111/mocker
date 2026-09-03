package authpreset

import "strings"

// authTriggerSegments is DESIGN §10 line 455's exact trigger list, matched
// case-insensitively on WHOLE path segments — never a substring. That
// whole-segment requirement is what keeps "/api/v1/ledger/tokens" (segment
// "tokens", not "token") and "/api/v1/id/{token}" ("token" sits inside the
// param name "{token}", never its own segment) out: the acceptance document
// carries exactly that trap, still live today.
var authTriggerSegments = map[string]bool{
	"auth":     true,
	"login":    true,
	"token":    true,
	"refresh":  true,
	"oauth":    true,
	"userinfo": true,
	"me":       true,
}

// IsAuthPath reports whether path (relative, as operations.path stores it)
// matches DESIGN §10's trigger list.
func IsAuthPath(path string) bool {
	for seg := range strings.SplitSeq(path, "/") {
		if authTriggerSegments[strings.ToLower(seg)] {
			return true
		}
	}
	return false
}

// tokenFieldName reports whether name is one of DESIGN §10's "this field IS
// the token" spellings on an auth path, and whether it is specifically
// refresh_token — the one that gets its own, longer ttl (a refresh token
// that expires with the access token it refreshes is useless).
func tokenFieldName(name string) (isRefresh, ok bool) {
	switch name {
	case "refresh_token":
		return true, true
	case "token", "access_token", "id_token", "jwt":
		return false, true
	default:
		return false, false
	}
}

// refreshTTLMultiplier is how much longer a refresh token lives than the
// access token it refreshes. DESIGN §9 requires SOME multiplier without
// pinning an exact number; 24x (an hour-scale access token behind a
// day-scale refresh token) is this slice's concrete choice — spelled out in
// every refresh binding's Reason so an operator sees it and can edit it
// before anything is applied.
const refreshTTLMultiplier = 24

// expiryFieldName reports whether name is one of DESIGN §10's expiry-shaped
// property names, and whether it names a DURATION (expires_in) rather than
// a point in time. expire_count is deliberately not matched here: it is a
// COUNT, not a time, and it is neither equal to "expires_in" nor a suffix
// match for "_expires_at" — the plain enumeration below already excludes
// it without needing a special-case, which is the point: a substring
// heuristic ("contains 'expire'") would have caught it, and this table is
// built precisely to not be that heuristic.
func expiryFieldName(name string) (isExpiry, isDuration bool) {
	switch name {
	case "exp", "expiresAt", "expires_at", "not_after", "valid_until":
		return true, false
	case "expires_in":
		return true, true
	}
	if strings.HasSuffix(name, "_expires_at") {
		return true, false
	}
	return false, false
}

// gatedIdentityAliasField holds the identity-alias spellings that are
// GENERIC enough to appear on an arbitrary, unrelated entity — not just the
// logged-in user. Binding them unconditionally is exactly the "40 copies of
// one row" failure DESIGN §10 warns about for a bare "id", and measuring
// against the real acceptance document proved it is not hypothetical for
// "name" either: without this gate, response.items[*].district.name (a
// CITY'S district) gets bound to identity.name right alongside a genuine
// profile's own name field, which is precisely the preset an operator has
// to undo by hand rather than one that helps. "id" and the name/email
// family are gated here; "full_name"/"fullName"/"display_name" are grouped
// with the bare "name" they are synonyms of, for the same reason "name"
// itself is gated, not because DESIGN singles them out individually.
//
// A gated field is bound only on an auth path, or on a "profile-shaped"
// response — see hasProfileAlias — that is not itself inside a list.
var gatedIdentityAliasField = map[string]string{
	"id":           "id",
	"name":         "name",
	"full_name":    "name",
	"fullName":     "name",
	"display_name": "name",
	"email":        "email",
	"login":        "email",
}

// explicitIdentityAliasField holds the identity-alias spellings self-
// describing enough to bind unconditionally, everywhere they appear: they
// either say "user"/"org" outright (user_id, uid, sub, organization_id,
// org_id, app_id — not the generic word an unrelated entity would also
// carry) or are low-visual-impact values where a mismatch reads as odd
// rather than as broken data (roles, permissions — an array of strings, not
// a name or id a UI renders as THE identity of the row).
var explicitIdentityAliasField = map[string]string{
	"user_id":         "id",
	"uid":             "id",
	"sub":             "id",
	"roles":           "roles",
	"permissions":     "roles",
	"organization_id": "org.id",
	"org_id":          "org.id",
	"app_id":          "org.id",
}

// profileAliasNames are the sibling names that mark an object as a user
// profile for a gated alias's judgement call: DESIGN §10's "a schema whose
// properties also carry a name/email/roles alias". The org/id-family
// aliases are deliberately excluded — an object with an organization_id
// sibling is not thereby a USER profile.
var profileAliasNames = map[string]bool{
	"name":         true,
	"full_name":    true,
	"fullName":     true,
	"display_name": true,
	"email":        true,
	"login":        true,
	"roles":        true,
	"permissions":  true,
}

// hasProfileAlias reports whether siblings (the full property set at the
// same object level as the gated field being judged, which includes that
// field's own name) contains at least TWO distinct name/email/roles
// aliases. One is not enough: measured against the acceptance document,
// ReminderInfo (id, name, send_datetime, status, avatar_urls, org_id,
// receiver_count) and OrganizationGeneralInvite (id, roles, type, creator,
// organization, expire_count, ...) each carry exactly ONE alias — "name" on
// a reminder, "roles" GRANTED by an invite — and neither is a user profile;
// binding "id" there overwrote the created resource's own id with the
// caller's. A genuine profile corroborates with at least two (id+name+email,
// fullName+email, ...), which is what the two-alias floor requires.
func hasProfileAlias(siblings map[string]bool) bool {
	matched := 0
	for n := range siblings {
		if profileAliasNames[n] {
			matched++
			if matched >= 2 {
				return true
			}
		}
	}
	return false
}
