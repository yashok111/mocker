package domain

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// slugPattern is the contract from DESIGN §15. A slug becomes a DNS label
// (<slug>.mock.corp.internal), so it must survive as a hostname: lowercase
// letters, digits and hyphens, never leading with a hyphen.
var slugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,29}[a-z0-9])?$`)

// ErrSlugReserved is returned for names that would collide with infrastructure.
var ErrSlugReserved = errors.New("slug is reserved")

// reservedSlugs would shadow real hostnames or well-known paths inside the
// contour. Keeping them out is cheaper than debugging why "admin.mock.corp"
// serves somebody's mock.
var reservedSlugs = map[string]bool{
	"admin": true, "api": true, "assets": true, "health": true, "healthz": true,
	"localhost": true, "mail": true, "mock": true, "mocker": true, "ns": true,
	"proxy": true, "readyz": true, "root": true, "static": true, "status": true,
	"test": true, "www": true,
}

// ValidateSlug checks a workspace slug against the pattern and the reserved
// list. The error text is aimed at the person typing it, not at a log.
func ValidateSlug(slug string) error {
	switch {
	case slug == "":
		return errors.New("slug must not be empty")
	case !slugPattern.MatchString(slug):
		return fmt.Errorf("slug %q is invalid: use 2-31 characters of a-z, 0-9 and -, starting with a letter or digit", slug)
	case reservedSlugs[slug]:
		return fmt.Errorf("%w: %q", ErrSlugReserved, slug)
	}
	return nil
}

// Slugify turns a human name into a candidate slug. Cyrillic is transliterated
// rather than dropped: the team types Russian names, and a silent drop would
// leave everyone sharing the slug "-".
//
// The result may still be invalid (empty, too short, reserved) — callers must
// run it through [ValidateSlug] or [UniqueSlug].
func Slugify(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	prevHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		lat, isCyrillic := cyrillic[r]
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevHyphen = false
		case isCyrillic:
			// The soft and hard signs map to nothing on purpose; they must not
			// turn into a separator ("объект" is "obekt", not "ob-ekt").
			b.WriteString(lat)
			prevHyphen = prevHyphen && lat == ""
		default:
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 31 {
		out = strings.TrimRight(out[:31], "-")
	}
	return out
}

// UniqueSlug returns the first free slug derived from name, trying base,
// base-2, base-3 and so on. taken reports whether a candidate is already used.
//
// The chosen slug is deterministic and must be SHOWN to the user (DESIGN §14):
// silently handing "alex-2" to somebody who typed "alex" is how people end up
// pointing their frontend at a colleague's workspace.
func UniqueSlug(name string, taken func(string) (bool, error)) (string, error) {
	base := Slugify(name)
	if len(base) < 2 {
		// "ws" alone for a name with no usable letter at all: "ws-" would
		// be a label ending in a hyphen, which RFC 1123 (and slugPattern)
		// refuse.
		base = strings.TrimRight("ws-"+base, "-")
	}
	if len(base) > 29 {
		base = strings.TrimRight(base[:29], "-")
	}
	for i := 1; i <= 200; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", base, i)
		}
		if err := ValidateSlug(candidate); err != nil {
			if errors.Is(err, ErrSlugReserved) {
				continue // "admin" is reserved, "admin-2" is not
			}
			return "", err
		}
		used, err := taken(candidate)
		if err != nil {
			return "", err
		}
		if !used {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("no free slug for %q after 200 attempts", name)
}

// cyrillic maps Russian letters to a URL-safe transliteration.
var cyrillic = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "e",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "h", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "sch",
	'ъ': "", 'ы': "y", 'ь': "", 'э': "e", 'ю': "yu", 'я': "ya",
}
