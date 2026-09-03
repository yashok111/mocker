package domain

import (
	"strconv"
	"testing"
)

// TestValidateBasePath_NoParameter is the common case (every workspace
// before P3h): a basePath with k = 0 must validate regardless of shape
// quirks NormalizeBasePath already irons out.
func TestValidateBasePath_NoParameter(t *testing.T) {
	for _, bp := range []string{"", "/", "/orgs", "/api/v1/orgs"} {
		if err := ValidateBasePath(bp); err != nil {
			t.Errorf("ValidateBasePath(%q) = %v, want nil", bp, err)
		}
	}
}

// TestValidateBasePathValues_NoParameter_AnyDeclaredState is D4.1's own
// promise: a basePath with no parameter validates under BOTH the nil state
// every stored settings blob has today and the explicit empty slice — this
// validator runs on every settings write, including ones that never touch
// basePath at all.
func TestValidateBasePathValues_NoParameter_AnyDeclaredState(t *testing.T) {
	for _, values := range [][]string{nil, {}} {
		if err := ValidateBasePathValues("/orgs", values); err != nil {
			t.Errorf("ValidateBasePathValues(%q) = %v, want nil", values, err)
		}
	}
}

// TestValidateBasePathValues_NoParameter_NonEmptyRefused is D4.3's last
// bullet: any element at all when k = 0 is refused, because a basePath
// with no tuple to declare has nothing a values list could legally name.
func TestValidateBasePathValues_NoParameter_NonEmptyRefused(t *testing.T) {
	err := ValidateBasePathValues("/orgs", []string{"7"})
	if err == nil {
		t.Fatal("want refusal for a non-empty basePathValues beside a parameterless basePath")
	}
}

// TestValidateBasePath_UnbalancedBrace covers D4.3's first bullet.
func TestValidateBasePath_UnbalancedBrace(t *testing.T) {
	for _, bp := range []string{"/orgs/{orgId", "/orgs/orgId}"} {
		if err := ValidateBasePath(bp); err == nil {
			t.Errorf("ValidateBasePath(%q) = nil, want refusal", bp)
		}
	}
}

// TestValidateBasePath_BraceNotWholeSegment covers D4.3's second bullet —
// the shape router.compilePattern would silently treat as a LITERAL
// segment, not a parameter, so a validator that accepted it would disagree
// with the matcher about what a parameter is.
func TestValidateBasePath_BraceNotWholeSegment(t *testing.T) {
	for _, bp := range []string{"/v{n}", "/a{orgId}c"} {
		if err := ValidateBasePath(bp); err == nil {
			t.Errorf("ValidateBasePath(%q) = nil, want refusal", bp)
		}
	}
}

// TestValidateBasePath_EmptyParameterName covers D4.3's third bullet.
func TestValidateBasePath_EmptyParameterName(t *testing.T) {
	if err := ValidateBasePath("/orgs/{}"); err == nil {
		t.Fatal("want refusal for an empty parameter name")
	}
}

// TestValidateBasePath_DuplicateParameterName covers D4.3's fourth bullet:
// two base parameters sharing a name would collapse to one entry in
// router.Match.Params.
func TestValidateBasePath_DuplicateParameterName(t *testing.T) {
	if err := ValidateBasePath("/orgs/{id}/teams/{id}"); err == nil {
		t.Fatal("want refusal for a repeated parameter name")
	}
}

// TestValidateBasePathValues_ElementComponentCount is D4.3's fifth bullet,
// and the exact case the document spells out: "7" is legal at k = 1 and
// refused at k = 2; "7/eu" is legal at k = 2 and refused at k = 1.
func TestValidateBasePathValues_ElementComponentCount(t *testing.T) {
	if err := ValidateBasePathValues("/orgs/{orgId}", []string{"7"}); err != nil {
		t.Errorf("k=1, element %q: got %v, want nil", "7", err)
	}
	if err := ValidateBasePathValues("/orgs/{orgId}/teams/{teamId}", []string{"7"}); err == nil {
		t.Errorf("k=2, element %q: want refusal", "7")
	}
	if err := ValidateBasePathValues("/orgs/{orgId}/teams/{teamId}", []string{"7/eu"}); err != nil {
		t.Errorf("k=2, element %q: got %v, want nil", "7/eu", err)
	}
	if err := ValidateBasePathValues("/orgs/{orgId}", []string{"7/eu"}); err == nil {
		t.Errorf("k=1, element %q: want refusal", "7/eu")
	}
}

// TestValidateBasePathValues_EmptyComponentRefused covers the rest of
// D4.3's fifth bullet: "7//eu", "/7" and "7/" are refused at every k
// because at least one component is empty.
func TestValidateBasePathValues_EmptyComponentRefused(t *testing.T) {
	for _, element := range []string{"7//eu", "/7", "7/"} {
		if err := ValidateBasePathValues("/orgs/{orgId}/teams/{teamId}", []string{element}); err == nil {
			t.Errorf("ValidateBasePathValues(k=2, %q) = nil, want refusal", element)
		}
	}
	if err := ValidateBasePathValues("/orgs/{orgId}", []string{""}); err == nil {
		t.Fatal(`ValidateBasePathValues(k=1, "") = nil, want refusal`)
	}
}

// TestValidateBasePathValues_DuplicateElementRefused covers D4.3's sixth
// bullet.
func TestValidateBasePathValues_DuplicateElementRefused(t *testing.T) {
	err := ValidateBasePathValues("/orgs/{orgId}", []string{"7", "8", "7"})
	if err == nil {
		t.Fatal("want refusal for a duplicated basePathValues element")
	}
}

// TestValidateBasePathValues_TwoParameterPairs is the two-parameter case
// named by the task: a basePath declaring k = 2 parameters validates a set
// of distinct "/"-joined pairs and refuses one that repeats.
func TestValidateBasePathValues_TwoParameterPairs(t *testing.T) {
	const basePath = "/orgs/{orgId}/teams/{teamId}"
	if err := ValidateBasePathValues(basePath, []string{"7/eu", "7/us", "8/eu"}); err != nil {
		t.Fatalf("distinct pairs: got %v, want nil", err)
	}
	if err := ValidateBasePathValues(basePath, []string{"7/eu", "7/eu"}); err == nil {
		t.Fatal("repeated pair: want refusal")
	}
}

// TestValidateBasePathValues_ElementCountIsCapped: a confirm and a reseed
// populate one row set per declared value, so the declared set is bounded
// here rather than by the row cap after every body has been generated.
func TestValidateBasePathValues_ElementCountIsCapped(t *testing.T) {
	values := make([]string, MaxBasePathValues+1)
	for i := range values {
		values[i] = "v" + strconv.Itoa(i)
	}
	if err := ValidateBasePathValues("/t/{id}", values); err == nil {
		t.Fatalf("%d values accepted, want a refusal past %d", len(values), MaxBasePathValues)
	}
	if err := ValidateBasePathValues("/t/{id}", values[:MaxBasePathValues]); err != nil {
		t.Fatalf("exactly %d values refused: %v", MaxBasePathValues, err)
	}
}
