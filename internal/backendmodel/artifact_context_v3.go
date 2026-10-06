package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"strings"
)

const ArtifactContextV3Version = "artifact-context-v3"

// ArtifactNamespace is an explicit lookup boundary, not a hint. A foreign
// namespace remains foreign even if its installation or numeric IDs collide.
type ArtifactNamespace struct {
	Scope          string `json:"scope"`
	InstallationID string `json:"installationId"`
}

func (n ArtifactNamespace) Validate() error {
	if (n.Scope != "local" && n.Scope != "foreign") || !ValidID(n.InstallationID) {
		return invalid("namespace", "Expected local/foreign scope and canonical installation UUID")
	}
	return nil
}
func (n *ArtifactNamespace) UnmarshalJSON(raw []byte) error {
	*n = ArtifactNamespace{}
	type plain ArtifactNamespace
	if err := strictAPIObject(raw, []string{"scope", "installationId"}, nil, (*plain)(n)); err != nil {
		return err
	}
	return n.Validate()
}

// CheckLocal must run before ANY owner read. Remapping creates a new context;
// changing the current installation ID can never authorize foreign lookup.
func (n ArtifactNamespace) CheckLocal(installationID string) error {
	if err := n.Validate(); err != nil {
		return err
	}
	if n.Scope != "local" || n.InstallationID != installationID {
		return &FaultError{Status: 422, Code: "backend_artifact_foreign_unresolved", Message: "Explicit exact local artifact mapping is required"}
	}
	return nil
}

// Groups preserve the complete existing API/editor binding vocabulary without
// adding optional namespace fields to legacy pins (which would change hashes).
// Identity is namespace + owner kind + ID; revisions/hashes remain exact pins.
type ArtifactNamespaceGroup struct {
	Namespace      ArtifactNamespace    `json:"namespace"`
	Pins           []ArtifactPin        `json:"pins"`
	APIBindings    []APIArtifactBinding `json:"apiBindings"`
	EditorBindings []EditorBinding      `json:"editorBindings"`
}

func (g *ArtifactNamespaceGroup) UnmarshalJSON(raw []byte) error {
	*g = ArtifactNamespaceGroup{}
	type plain ArtifactNamespaceGroup
	return strictAPIObject(raw, []string{"namespace", "pins", "apiBindings", "editorBindings"}, nil, (*plain)(g))
}

type ArtifactContextV3 struct {
	DocumentVersion    string                   `json:"documentVersion"`
	SourceContentHash  string                   `json:"sourceContentHash"`
	SourceSemanticHash string                   `json:"sourceSemanticHash"`
	Groups             []ArtifactNamespaceGroup `json:"groups"`
}

func (c *ArtifactContextV3) UnmarshalJSON(raw []byte) error {
	*c = ArtifactContextV3{}
	type plain ArtifactContextV3
	if len(raw) > MaxEditorArtifactContextBytes {
		return EditorArtifactContextLimit(int64(len(raw)))
	}
	if err := strictAPIObject(raw, []string{"documentVersion", "sourceContentHash", "sourceSemanticHash", "groups"}, nil, (*plain)(c)); err != nil {
		return err
	}
	_, err := canonicalArtifactContextV3(*c)
	return err
}

func canonicalArtifactContextV3(c ArtifactContextV3) (ArtifactContextV3, error) {
	if c.DocumentVersion != ArtifactContextV3Version || !validHash(c.SourceContentHash) || !validHash(c.SourceSemanticHash) {
		return c, invalid("context", "Invalid v3 version or frozen source anchors")
	}
	if len(c.Groups) > MaxAPIArtifactPins {
		return c, invalid("groups", "Too many artifact namespaces")
	}
	c.Groups = slices.Clone(c.Groups)
	seen := map[ArtifactNamespace]bool{}
	pins, bindings := 0, 0
	for i, g := range c.Groups {
		if err := g.Namespace.Validate(); err != nil {
			return c, err
		}
		if seen[g.Namespace] {
			return c, invalid("namespace", "Duplicate artifact namespace")
		}
		seen[g.Namespace] = true
		pins += len(g.Pins)
		bindings += len(g.APIBindings) + len(g.EditorBindings)
		if pins > MaxAPIArtifactPins || bindings > MaxAPIArtifactBindings {
			return c, invalid("groups", "Full artifact vector exceeds limits")
		}
		// Unlike legacy opaque groups, v3 admits only owners with known validators.
		for _, p := range g.Pins {
			if err := (ArtifactKey{Kind: p.Kind, ID: p.ID}).Validate(); err != nil {
				return c, err
			}
		}
		if err := ValidateArtifactVector(g.Pins, g.APIBindings, g.EditorBindings); err != nil {
			return c, err
		}
		g.Pins = canonicalAPIPins(g.Pins)
		g.APIBindings = slices.Clone(g.APIBindings)
		if g.APIBindings == nil {
			g.APIBindings = []APIArtifactBinding{}
		}
		slices.SortFunc(g.APIBindings, func(a, b APIArtifactBinding) int { return strings.Compare(a.SourceNodeID, b.SourceNodeID) })
		var err error
		g.EditorBindings, err = CanonicalEditorBindings(g.EditorBindings)
		if err != nil {
			return c, err
		}
		c.Groups[i] = g
	}
	if c.Groups == nil {
		c.Groups = []ArtifactNamespaceGroup{}
	}
	slices.SortFunc(c.Groups, func(a, b ArtifactNamespaceGroup) int {
		return strings.Compare(a.Namespace.Scope+":"+a.Namespace.InstallationID, b.Namespace.Scope+":"+b.Namespace.InstallationID)
	})
	return c, nil
}

func EncodeArtifactContextV3(c ArtifactContextV3) ([]byte, error) {
	c, err := canonicalArtifactContextV3(c)
	if err != nil {
		return nil, err
	}
	w := editorArtifactCountingWriter{}
	if err := json.MarshalWrite(&w, c); err != nil {
		return nil, err
	}
	return json.Marshal(c)
}

// ArtifactContextV3Hash is the complete canonical context identity, including
// labels, attribution and namespace. It does not replace legacy semantic hashes.
func ArtifactContextV3Hash(c ArtifactContextV3) (string, error) {
	raw, err := EncodeArtifactContextV3(c)
	if err != nil {
		return "", err
	}
	return hashBytes(raw), nil
}

// VersionedArtifactContext makes dropping the namespace an explicit type error.
// Existing readers stay on DecodeArtifactContext until their v3 resolver is wired.
type VersionedArtifactContext struct {
	Legacy *ArtifactContext
	V3     *ArtifactContextV3
}

func DecodeVersionedArtifactContext(raw []byte, legacyPins []ArtifactPin) (*VersionedArtifactContext, error) {
	m, err := relationalObject(raw)
	if err != nil {
		return nil, invalid("context", "Expected artifact context object")
	}
	var version string
	if v, ok := m["documentVersion"]; ok {
		if err := json.Unmarshal(v, &version); err != nil {
			return nil, err
		}
	}
	if version == ArtifactContextV3Version {
		if len(legacyPins) != 0 {
			return nil, invalid("pins", "V3 pins must be namespaced inside context")
		}
		var c ArtifactContextV3
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		return &VersionedArtifactContext{V3: &c}, nil
	}
	c, err := DecodeArtifactContext(raw, legacyPins)
	if err != nil {
		return nil, err
	}
	return &VersionedArtifactContext{Legacy: c}, nil
}

// NamespacedArtifactPin is the portable owner address; never pass Pin alone to
// existing owner adapters. LocalPin is the required gate before that conversion.
type NamespacedArtifactPin struct {
	Namespace ArtifactNamespace `json:"namespace"`
	Pin       ArtifactPin       `json:"pin"`
}

func (p NamespacedArtifactPin) Validate() error {
	if err := p.Namespace.Validate(); err != nil {
		return err
	}
	if err := (ArtifactKey{Kind: p.Pin.Kind, ID: p.Pin.ID}).Validate(); err != nil {
		return err
	}
	if !ValidAPIArtifactID(p.Pin.RevisionID) || !validHash(p.Pin.ContentHash) {
		return invalid("pin", "Expected exact owner revision and hash")
	}
	return nil
}
func (p *NamespacedArtifactPin) UnmarshalJSON(raw []byte) error {
	type plain NamespacedArtifactPin
	var decoded plain
	if err := strictAPIObject(raw, []string{"namespace", "pin"}, nil, &decoded); err != nil {
		return err
	}
	// ArtifactPin is a legacy DTO; require all v3 fields without changing its codec.
	fields, err := relationalObject(raw)
	if err != nil {
		return err
	}
	var pin ArtifactPin
	if err := strictAPIObject(fields["pin"], []string{"kind", "id", "revisionId", "contentHash"}, nil, &pin); err != nil {
		return err
	}
	decoded.Pin = pin
	result := NamespacedArtifactPin(decoded)
	if err := result.Validate(); err != nil {
		return err
	}
	*p = result
	return nil
}

func (p NamespacedArtifactPin) LocalPin(installationID string) (ArtifactPin, error) {
	if err := p.Validate(); err != nil {
		return ArtifactPin{}, err
	}
	if err := p.Namespace.CheckLocal(installationID); err != nil {
		return ArtifactPin{}, err
	}
	return p.Pin, nil
}

// ArtifactContextV3SemanticHash binds the complete namespace/context identity in
// a separate domain; legacy semantic hashes are never reused for a remapped row.
func ArtifactContextV3SemanticHash(c ArtifactContextV3) (string, error) {
	identity, err := ArtifactContextV3Hash(c)
	if err != nil {
		return "", err
	}
	return sourceDomainHash("backend-artifact-context-v3-semantic-v1", identity)
}
