// Package testspec is the one shared place every phase's acceptance test
// takes the acceptance document from: testdata/acceptance.json, a
// 110-path, 130-operation, 179-schema OpenAPI 3.0.3 corpus embedded into
// the test binary. It exists so "which document do the acceptance tests
// run against" is answered once instead of once per package:
// internal/specs/acceptance_test.go had its own private resolver first
// (P1a); this package is that resolver, promoted and exported.
//
// The corpus is the public, sanitised form of the document the project was
// built against — an internal API definition that could not ship. It is
// STRUCTURE-PRESERVING: every path, operation, schema, $ref, parameter,
// property, enum, status code and format of the original survives one for
// one (so every count and quirk the acceptance tests exercise is
// unchanged), while the prose, the vendor extensions and the product's own
// vocabulary do not. Until 2026-09-03 the document was read from the
// module root under a gitignored name and every test depending on it
// SKIPPED on a fresh clone; the golden guard (internal/gen) refused to run
// at all. Embedding it is what makes `make test` green on a clone.
package testspec

import (
	_ "embed"
	"testing"
)

//go:embed testdata/acceptance.json
var acceptanceJSON []byte

// --- P3a: the resource-derivation fixture -----------------------------
//
// DerivationDoc is a small, hand-built OpenAPI 3.0 document — never the
// gitignored real acceptance spec, so every test that uses it runs
// unconditionally in a fresh clone and in CI, unlike [Bytes] — that
// carries exactly the families clause 8 (mocker-p3a-resources decisions.md
// §D13) names, one route family per shape, so a single Index over it
// exercises every branch [specs]' deriveSuggestions must accept or refuse:
//
//   - FamilyWidgets: the positive control. A WRAPPED 200 ({"items":[...],
//     "total": ...}), integer id, and — deliberately — a "total" property
//     declared with NO "type" key at all (clause 49: proves countType is
//     read with [gen.SchemaType], which falls back to "string" for an
//     untyped leaf, not with [gen.PrimaryIDType], which would answer "").
//   - FamilyBareItems: the other positive control, a BARE-ARRAY 200 (no
//     wrapper object at all) with a STRING id — paired with Widgets'
//     integer id so clause 48 ("one collection, one id type") has both an
//     integer case and a string case, and both a wrapped and a bare-array
//     shape, without needing a fifth family.
//   - /orgs/{orgId}/departments (+ /{id}): a family whose canonical path
//     itself carries a "{}" segment (nested under {orgId}) — R2's own
//     restriction, carved out of [router.ListFamily]'s plain "detail route
//     exists" predicate. Must NOT derive.
//   - /orphans/{id}: a detail route with no matching collection GET. Must
//     NOT derive.
//   - /lonely: a collection GET with no matching detail route. Must NOT
//     derive.
//   - /noiditems (+ /{id}): item schema (identical for the list and the
//     detail variant here) declares no "id" property at all. Must NOT
//     derive.
//   - /ambiguous (+ /{id}): a wrapped 200 declaring TWO array-typed
//     properties ("primary" and "secondary") — arrayKey would be a guess.
//     Must NOT derive.
//   - /textdetail (+ /{id}): the collection is a plain JSON array, but the
//     detail GET's 200 is declared under "text/plain", not JSON. Must NOT
//     derive.
//   - /scalarpage (+ /{id}): the collection's 200 is a plain object with
//     no array-typed property at all — not a list by any reading. Must
//     NOT derive.
func DerivationDoc() []byte {
	return []byte(derivationDocJSON)
}

// The positive-control route families DerivationDoc declares, exported so
// a test asserting the exact derived set never repeats these path literals.
const (
	FamilyWidgets   = "/widgets"
	FamilyBareItems = "/bareitems"
)

const derivationDocJSON = `{
  "openapi": "3.0.3",
  "info": {"title": "P3a derivation fixture", "version": "1.0.0"},
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "listWidgets",
        "responses": {
          "200": {
            "description": "a page of widgets",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/WidgetList"}}
            }
          }
        }
      }
    },
    "/widgets/{id}": {
      "get": {
        "operationId": "getWidget",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "one widget",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/Widget"}}
            }
          }
        }
      }
    },
    "/bareitems": {
      "get": {
        "operationId": "listBareItems",
        "responses": {
          "200": {
            "description": "a bare array of items",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/BareItem"}}}
            }
          }
        }
      }
    },
    "/bareitems/{id}": {
      "get": {
        "operationId": "getBareItem",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {
          "200": {
            "description": "one item",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/BareItem"}}
            }
          }
        }
      }
    },
    "/orgs/{orgId}/departments": {
      "get": {
        "operationId": "listOrgDepartments",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "departments of one org",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Department"}}}
            }
          }
        }
      }
    },
    "/orgs/{orgId}/departments/{id}": {
      "get": {
        "operationId": "getOrgDepartment",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one department",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/Department"}}
            }
          }
        }
      }
    },
    "/orphans/{id}": {
      "get": {
        "operationId": "getOrphan",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "an orphan detail route with no collection",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/Orphan"}}
            }
          }
        }
      }
    },
    "/lonely": {
      "get": {
        "operationId": "listLonely",
        "responses": {
          "200": {
            "description": "a collection with no detail route",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Lonely"}}}
            }
          }
        }
      }
    },
    "/noiditems": {
      "get": {
        "operationId": "listNoIDItems",
        "responses": {
          "200": {
            "description": "a page of items with no id property",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/NoIDItemList"}}
            }
          }
        }
      }
    },
    "/noiditems/{id}": {
      "get": {
        "operationId": "getNoIDItem",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "one item, still no id property",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/NoIDItem"}}
            }
          }
        }
      }
    },
    "/ambiguous": {
      "get": {
        "operationId": "listAmbiguous",
        "responses": {
          "200": {
            "description": "a wrapper with two array-typed properties",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/AmbiguousList"}}
            }
          }
        }
      }
    },
    "/ambiguous/{id}": {
      "get": {
        "operationId": "getAmbiguous",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "one item",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/AmbiguousItem"}}
            }
          }
        }
      }
    },
    "/textdetail": {
      "get": {
        "operationId": "listTextDetail",
        "responses": {
          "200": {
            "description": "a JSON array collection",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/TextItem"}}}
            }
          }
        }
      }
    },
    "/textdetail/{id}": {
      "get": {
        "operationId": "getTextDetail",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "a non-JSON detail response",
            "content": {
              "text/plain": {"schema": {"type": "string"}}
            }
          }
        }
      }
    },
    "/scalarpage": {
      "get": {
        "operationId": "listScalarPage",
        "responses": {
          "200": {
            "description": "an object with no array-typed property",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/ScalarPage"}}
            }
          }
        }
      }
    },
    "/scalarpage/{id}": {
      "get": {
        "operationId": "getScalarPage",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "one item",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/ScalarPageItem"}}
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Widget": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "WidgetList": {
        "type": "object",
        "properties": {
          "items": {"type": "array", "items": {"$ref": "#/components/schemas/Widget"}},
          "total": {}
        }
      },
      "BareItem": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "label": {"type": "string"}
        }
      },
      "Department": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "Orphan": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"}
        }
      },
      "Lonely": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"}
        }
      },
      "NoIDItem": {
        "type": "object",
        "properties": {
          "name": {"type": "string"}
        }
      },
      "NoIDItemList": {
        "type": "object",
        "properties": {
          "items": {"type": "array", "items": {"$ref": "#/components/schemas/NoIDItem"}},
          "total": {"type": "integer"}
        }
      },
      "AmbiguousItem": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"}
        }
      },
      "AmbiguousList": {
        "type": "object",
        "properties": {
          "primary": {"type": "array", "items": {"$ref": "#/components/schemas/AmbiguousItem"}},
          "secondary": {"type": "array", "items": {"$ref": "#/components/schemas/AmbiguousItem"}}
        }
      },
      "TextItem": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"}
        }
      },
      "ScalarPage": {
        "type": "object",
        "properties": {
          "message": {"type": "string"}
        }
      },
      "ScalarPageItem": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"}
        }
      }
    }
  }
}`

// --- P3e: the nested-family fixture ------------------------------------
//
// NestedDerivationDoc is a SECOND, separate hand-built document — never
// added to [DerivationDoc] itself (D4.3): that document's own test asserts
// its derived set EXACTLY, including the negative control
// "/orgs/{}/departments" in mustNotAppear, and adding a real "/orgs" family
// beside it would make that family derivable, turning both of
// [DerivationDoc]'s own assertions red against a CORRECT implementation.
//
// It declares:
//
//   - /orgs (+ /{orgId}): the top-level parent family, itself an ordinary
//     derivable resource — nesting under a family that is not itself
//     confirmable is the case D4.1's parent check refuses, so a positive
//     nested-family fixture needs a real parent to nest under.
//   - /orgs/{orgId}/departments (+ /{id}): the first of TWO children of
//     that one parent — proving "one parent, two children" has a fixture
//     at all, not just "a parent has A child".
//   - /orgs/{orgId}/users (+ /{organizationId}/users/{id}): the second
//     child, and the one shape here that is a REQUIREMENT rather than a
//     convenience (D4.3): its outer path parameter is spelled DIFFERENTLY
//     on the collection route ("orgId") and the detail route
//     ("organizationId"). [router.CanonicalPath] erases parameter names,
//     so both still resolve to the one family "/orgs/{}/users" — and that
//     asymmetry is the only thing that can tell a POSITIONAL scope read
//     (D3.1: the outer parameter's ORDER, not its name) apart from a
//     by-name one. Acceptance property P15 rests on it; without this
//     asymmetry that property would be vacuous.
//
// Exported (P3e) beside DerivationDoc for every package this slice's
// nested properties touch to import the SAME fixture rather than
// hand-rolling its own nested spec (D4.3's own "the second document is not
// a derivation control only" note). As of the P3e acceptance run's own
// fixes that is internal/specs (the derivation control itself,
// derive_test.go), internal/mockplane (serving, P15 —
// resource_integration_test.go) and internal/admin (confirm/decline over
// HTTP, resource_handlers_test.go); internal/resources and
// internal/checkpoints still roll their own local nested fixtures
// (nested_test.go's own nestedFixtureDoc, and checkpoints' own round-trip
// fixture for P9) — an earlier version of this comment claimed both
// already imported this one, and nothing had enforced that claim.
func NestedDerivationDoc() []byte {
	return []byte(nestedDerivationDocJSON)
}

// The route families NestedDerivationDoc declares, exported for the same
// reason [FamilyWidgets]/[FamilyBareItems] are: a test asserting the
// derived set never repeats these path literals. Family names are already
// in [router.CanonicalPath]'s "{}"-erased form, matching what
// [deriveSuggestions] itself returns.
const (
	FamilyOrgs           = "/orgs"
	FamilyOrgDepartments = "/orgs/{}/departments"
	FamilyOrgUsers       = "/orgs/{}/users"
)

const nestedDerivationDocJSON = `{
  "openapi": "3.0.3",
  "info": {"title": "P3e nested-derivation fixture", "version": "1.0.0"},
  "paths": {
    "/orgs": {
      "get": {
        "operationId": "listOrgs",
        "responses": {
          "200": {
            "description": "a page of orgs",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Org"}}}
            }
          }
        }
      }
    },
    "/orgs/{orgId}": {
      "get": {
        "operationId": "getOrg",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "one org",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/Org"}}
            }
          }
        }
      }
    },
    "/orgs/{orgId}/departments": {
      "get": {
        "operationId": "listOrgDepartments",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "departments of one org",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Department"}}}
            }
          }
        }
      }
    },
    "/orgs/{orgId}/departments/{id}": {
      "get": {
        "operationId": "getOrgDepartment",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one department",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/Department"}}
            }
          }
        }
      }
    },
    "/orgs/{orgId}/users": {
      "get": {
        "operationId": "listOrgUsers",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "users of one org",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/User"}}}
            }
          }
        }
      }
    },
    "/orgs/{organizationId}/users/{id}": {
      "get": {
        "operationId": "getOrgUser",
        "parameters": [
          {"name": "organizationId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one user",
            "content": {
              "application/json": {"schema": {"$ref": "#/components/schemas/User"}}
            }
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Org": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "Department": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "User": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      }
    }
  }
}`

// --- P3g: the deep-nesting fixture --------------------------------------
//
// DeepNestingDoc is a THIRD, separate hand-built document — never added to
// [DerivationDoc] or [NestedDerivationDoc] (D4.3): both of those assert
// their own derived sets EXACTLY, so widening either one's paths would turn
// a correct implementation red against an assertion this slice has no
// business moving.
//
// It declares a four-family positive chain reaching [maxNestingDepth]'s own
// ceiling and three negative controls, each proving a DIFFERENT way a
// shallow-checking implementation can pass a one-level fixture and still be
// wrong at depth:
//
//   - /orgs (+ /{orgId}, POST): the depth-0 root.
//   - /orgs/{orgId}/teams (+ /{teamId}, POST): depth 1.
//   - /orgs/{orgId}/teams/{teamId}/users (+ detail, POST): depth 2 — the
//     positive control for two levels, and the family P8/P9/P17's serving
//     properties use.
//   - .../users/{userId}/badges (+ detail, no POST): depth 3, the positive
//     control sitting exactly at the ceiling.
//
// Every family of that chain except the deepest (badges) declares POST —
// D4.3's own requirement, not a convenience: P13's reseed property needs a
// LIVE row written into the root and into a middle scope before a reseed,
// so each level's live keys provably differ from what the reseed re-mints,
// and a family with no POST route has no such write on the mock plane.
// Nothing is ever scoped under badges, so it needs none.
//
//   - .../badges/{badgeId}/history (+ detail): depth 4, ONE past the
//     ceiling. Must derive NOTHING — this is what makes P2 (D13) discriminate
//     an implementation with a real ceiling from one that "just loops".
//   - /regions/{regionId}/cities (+ detail): depth 1, whose parent
//     "/regions" this document does NOT declare — the negative control that
//     a chain broken AT ITS TOP derives nothing, carried the same way
//     [DerivationDoc]'s own "/orgs/{}/departments" already is.
//   - .../cities/{cityId}/streets (+ detail): depth 2, whose immediate
//     parent "/regions/{}/cities" is a shape-legal family (it has a matching
//     detail route) that itself failed to derive because ITS parent is
//     absent. This family must ALSO derive nothing, and it is the control
//     that separates a loop checking the WHOLE chain (D4.1's "derived by
//     pass k-1") from one that only checks the immediate parent's SHAPE —
//     a single-level fixture cannot tell the two apart, because at depth 1
//     "parent is shape-legal" and "parent was derived" coincide.
//
// The depth-2 family (users) spells BOTH of its outer path parameters
// differently between its collection and detail routes
// ("/orgs/{orgId}/teams/{teamId}/users" beside
// "/orgs/{organizationId}/teams/{team}/users/{id}") — [NestedDerivationDoc]'s
// own requirement carried one level deeper: [router.CanonicalPath] erases
// parameter names, so only a differing spelling can tell a POSITIONAL scope
// read (D3.1) apart from a by-name one, and a fixture differing in one
// position only cannot fail an implementation that reads position 0
// positionally and position 1 by name — both positions have to disagree for
// that property (P17) to mean anything.
func DeepNestingDoc() []byte {
	return []byte(deepNestingDocJSON)
}

// The route families DeepNestingDoc declares, exported for the same reason
// [FamilyWidgets]/[FamilyOrgs] are: a test asserting the derived (or
// excluded) set never repeats these path literals. "Deep" distinguishes
// them from [NestedDerivationDoc]'s own FamilyOrgs/FamilyOrgUsers, which
// name a different document's families under overlapping path prefixes.
const (
	FamilyDeepOrgs          = "/orgs"
	FamilyDeepTeams         = "/orgs/{}/teams"
	FamilyDeepUsers         = "/orgs/{}/teams/{}/users"
	FamilyDeepBadges        = "/orgs/{}/teams/{}/users/{}/badges"
	FamilyDeepBadgeHistory  = "/orgs/{}/teams/{}/users/{}/badges/{}/history" // depth 4: must NOT derive (ceiling)
	FamilyDeepRegionCities  = "/regions/{}/cities"                           // parent "/regions" absent: must NOT derive
	FamilyDeepRegionStreets = "/regions/{}/cities/{}/streets"                // parent shape-legal but undeclared-chain: must NOT derive
)

const deepNestingDocJSON = `{
  "openapi": "3.0.3",
  "info": {"title": "P3g deep-nesting fixture", "version": "1.0.0"},
  "paths": {
    "/orgs": {
      "get": {
        "operationId": "listOrgs",
        "responses": {
          "200": {
            "description": "a page of orgs",
            "content": {
              "application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Org"}}}
            }
          }
        }
      },
      "post": {
        "operationId": "createOrg",
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Org"}}}
        },
        "responses": {
          "201": {
            "description": "created org",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Org"}}}
          }
        }
      }
    },
    "/orgs/{orgId}": {
      "get": {
        "operationId": "getOrg",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "one org",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Org"}}}
          }
        }
      }
    },
    "/orgs/{orgId}/teams": {
      "get": {
        "operationId": "listOrgTeams",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "teams of one org",
            "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Team"}}}}
          }
        }
      },
      "post": {
        "operationId": "createOrgTeam",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Team"}}}
        },
        "responses": {
          "201": {
            "description": "created team",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Team"}}}
          }
        }
      }
    },
    "/orgs/{orgId}/teams/{teamId}": {
      "get": {
        "operationId": "getOrgTeam",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "teamId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one team",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Team"}}}
          }
        }
      }
    },
    "/orgs/{orgId}/teams/{teamId}/users": {
      "get": {
        "operationId": "listTeamUsers",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "teamId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "users of one team",
            "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/User"}}}}
          }
        }
      },
      "post": {
        "operationId": "createTeamUser",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "teamId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "requestBody": {
          "required": true,
          "content": {"application/json": {"schema": {"$ref": "#/components/schemas/User"}}}
        },
        "responses": {
          "201": {
            "description": "created user",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/User"}}}
          }
        }
      }
    },
    "/orgs/{organizationId}/teams/{team}/users/{id}": {
      "get": {
        "operationId": "getTeamUser",
        "parameters": [
          {"name": "organizationId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "team", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one user",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/User"}}}
          }
        }
      }
    },
    "/orgs/{orgId}/teams/{teamId}/users/{userId}/badges": {
      "get": {
        "operationId": "listUserBadges",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "teamId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "userId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "badges of one user",
            "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Badge"}}}}
          }
        }
      }
    },
    "/orgs/{orgId}/teams/{teamId}/users/{userId}/badges/{badgeId}": {
      "get": {
        "operationId": "getUserBadge",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "teamId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "userId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "badgeId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one badge",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Badge"}}}
          }
        }
      }
    },
    "/orgs/{orgId}/teams/{teamId}/users/{userId}/badges/{badgeId}/history": {
      "get": {
        "operationId": "listBadgeHistory",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "teamId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "userId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "badgeId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "history of one badge — depth 4, past the ceiling",
            "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/History"}}}}
          }
        }
      }
    },
    "/orgs/{orgId}/teams/{teamId}/users/{userId}/badges/{badgeId}/history/{historyId}": {
      "get": {
        "operationId": "getBadgeHistory",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "teamId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "userId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "badgeId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "historyId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one history entry",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/History"}}}
          }
        }
      }
    },
    "/regions/{regionId}/cities": {
      "get": {
        "operationId": "listRegionCities",
        "parameters": [{"name": "regionId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {
          "200": {
            "description": "cities of a region never declared as its own family",
            "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/City"}}}}
          }
        }
      }
    },
    "/regions/{regionId}/cities/{cityId}": {
      "get": {
        "operationId": "getRegionCity",
        "parameters": [
          {"name": "regionId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "cityId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one city",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/City"}}}
          }
        }
      }
    },
    "/regions/{regionId}/cities/{cityId}/streets": {
      "get": {
        "operationId": "listCityStreets",
        "parameters": [
          {"name": "regionId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "cityId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "streets of a city whose own parent chain is broken above it",
            "content": {"application/json": {"schema": {"type": "array", "items": {"$ref": "#/components/schemas/Street"}}}}
          }
        }
      }
    },
    "/regions/{regionId}/cities/{cityId}/streets/{streetId}": {
      "get": {
        "operationId": "getCityStreet",
        "parameters": [
          {"name": "regionId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "cityId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "streetId", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {
          "200": {
            "description": "one street",
            "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Street"}}}
          }
        }
      }
    }
  },
  "components": {
    "schemas": {
      "Org": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "Team": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "User": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "Badge": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "History": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "note": {"type": "string"}
        }
      },
      "City": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      },
      "Street": {
        "type": "object",
        "properties": {
          "id": {"type": "integer"},
          "name": {"type": "string"}
        }
      }
    }
  }
}`

// Bytes returns the acceptance document. It takes *testing.TB only for
// symmetry with the fixtures above and the callers written when the
// document could be absent: since 2026-09-03 the corpus is embedded and
// there is no path on which this function skips or fails.
func Bytes(t testing.TB) []byte {
	t.Helper()
	return append([]byte(nil), acceptanceJSON...)
}
