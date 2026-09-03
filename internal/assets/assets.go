// Package assets stores the files an operator uploads into a workspace so
// that a mock can serve them: DESIGN §32 (v11), slice A6. One table, one
// BLOB per row, addressed by (workspace_id, name) and never by id on the
// wire — a name survives the delete-and-reupload repair this product
// prescribes for a wrong object everywhere else, an id does not.
//
// The package is a LEAF over internal/store: it knows nothing about
// variants, recipes or the mock plane. Two other packages read ONE thing
// from it besides the repository — [ValidName], the single owner of what an
// asset name may look like, because the admin handler, the MCP tool and
// overrides.ValidateVariant's bodyRef check all refuse the same shape and a
// second regexp anywhere would be the divergent copy the one-owner rule
// exists to prevent (mocker-a6-assets D2).
package assets

import (
	"errors"
	"regexp"
	"time"
)

// Meta is everything about an asset except its bytes — what the list route
// answers, what the ETag path reads, what a bodyRef resolution needs
// before it decides to load the BLOB at all.
type Meta struct {
	Name      string
	MediaType string
	SizeBytes int64
	SHA256    string // lower-case hex of the stored bytes; the mock route's strong ETag
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Errors the repository returns by identity.
var (
	// ErrNotFound: no asset of that name in that workspace.
	ErrNotFound = errors.New("assets: asset not found")
	// ErrWorkspaceNotFound: the workspace itself does not exist (a write
	// against a workspace that is gone, never "no asset").
	ErrWorkspaceNotFound = errors.New("assets: workspace not found")
	// ErrInvalidName wraps every refusal of a name (see ValidName).
	ErrInvalidName = errors.New("assets: invalid asset name")
	// ErrTooLarge: one file over MaxAssetBytes.
	ErrTooLarge = errors.New("assets: asset exceeds the per-file cap")
	// ErrQuota: the workspace's total would exceed MaxTotalBytes.
	ErrQuota = errors.New("assets: workspace asset quota exceeded")
	// ErrConfirmSlug: Delete's confirmSlug does not name the workspace.
	ErrConfirmSlug = errors.New("assets: confirmSlug does not match the workspace")
)

// validName is the ONE regexp an asset name is held to: a single path
// segment of the characters a URL never needs to escape, at most 128 of
// them. No slash (the mock route takes exactly one segment after
// "assets"), no percent (the name is written into an asset_url verbatim
// after url.PathEscape, and a name that already carried an escape would
// double-encode), no whitespace, no dot-segments (refused separately below:
// "." and ".." match the character class but are not names).
var validName = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// ValidName reports whether name is an acceptable asset name. It is the
// single owner of the rule: the admin handler, the MCP tool and
// overrides.ValidateVariant's bodyRef check all call it rather than carry a
// copy (D2).
func ValidName(name string) bool {
	if name == "." || name == ".." {
		return false
	}
	return validName.MatchString(name)
}

// BodyRefPrefix is what a pinned variant's bodyRef starts with:
// "asset:<name>". The prefix leaves room for a second kind of reference
// later and makes a bare name in body and a reference in bodyRef visibly
// different things (D12).
const BodyRefPrefix = "asset:"

// NameFromBodyRef returns the asset name a bodyRef carries, and whether the
// value is a well-formed reference at all — the prefix followed by a name
// ValidName accepts.
func NameFromBodyRef(ref string) (string, bool) {
	if len(ref) <= len(BodyRefPrefix) || ref[:len(BodyRefPrefix)] != BodyRefPrefix {
		return "", false
	}
	name := ref[len(BodyRefPrefix):]
	if !ValidName(name) {
		return "", false
	}
	return name, true
}
