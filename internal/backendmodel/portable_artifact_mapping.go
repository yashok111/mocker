package backendmodel

import (
	"slices"
	"strings"
)

func (m *portableMapper) scopedPin(pin NamespacedArtifactPin) NamespacedArtifactPin {
	if m.collect {
		return pin
	}
	if err := pin.Validate(); err != nil {
		m.err = err
		return pin
	}
	for _, mapping := range m.artifacts {
		if mapping.Origin == pin {
			if _, err := mapping.Local.LocalPin(m.installation); err != nil {
				m.err = err
				return pin
			}
			if mapping.Local.Pin.Kind != pin.Pin.Kind {
				m.err = invalid("artifactMapping", "Owner kind cannot change")
				return pin
			}
			return mapping.Local
		}
	}
	// A local source owner is foreign in the destination even on same-installation
	// import. Only an explicit pair can authorize retaining a local owner address.
	pin.Namespace.Scope = "foreign"
	return pin
}
func (m *portableMapper) context(c *VersionedArtifactContext, pins []ArtifactPin) ArtifactContextV3 {
	out := ArtifactContextV3{DocumentVersion: ArtifactContextV3Version, Groups: []ArtifactNamespaceGroup{}}
	if c.V3 != nil {
		out = *c.V3
	} else if c.Legacy != nil {
		old := c.Legacy
		out.SourceContentHash, out.SourceSemanticHash = old.SourceContentHash, old.SourceSemanticHash
		out.Groups = []ArtifactNamespaceGroup{{Namespace: ArtifactNamespace{Scope: "local", InstallationID: m.originInstallation}, Pins: pins, APIBindings: old.APIBindings, EditorBindings: old.EditorBindings}}
	}
	groups := []ArtifactNamespaceGroup{}
	for _, g := range out.Groups {
		for _, pin := range g.Pins {
			scoped := m.scopedPin(NamespacedArtifactPin{Namespace: g.Namespace, Pin: pin})
			i := slices.IndexFunc(groups, func(v ArtifactNamespaceGroup) bool { return v.Namespace == scoped.Namespace })
			if i < 0 {
				groups = append(groups, ArtifactNamespaceGroup{Namespace: scoped.Namespace, Pins: []ArtifactPin{}, APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}})
				i = len(groups) - 1
			}
			group := &groups[i]
			group.Pins = append(group.Pins, scoped.Pin)
			for _, binding := range g.APIBindings {
				if binding.Ref.Kind != pin.Kind || binding.Ref.ArtifactID != pin.ID {
					continue
				}
				b := binding
				m.id("node", &b.SourceNodeID)
				b.Ref.ArtifactID, b.Ref.RevisionID, b.Ref.ContentHash = scoped.Pin.ID, scoped.Pin.RevisionID, scoped.Pin.ContentHash
				group.APIBindings = append(group.APIBindings, b)
			}
			for _, binding := range g.EditorBindings {
				if binding.ArtifactKind != pin.Kind || binding.ArtifactID != pin.ID {
					continue
				}
				b := binding
				b.SourceNodeIDs = slices.Clone(b.SourceNodeIDs)
				m.list("node", b.SourceNodeIDs)
				b.ArtifactID = scoped.Pin.ID
				group.EditorBindings = append(group.EditorBindings, b)
			}
		}
	}
	out.Groups = groups
	return out
}

// PortableArtifactPins is the complete external owner reference roster; exported
// bundles contain these immutable descriptors, never credentials or live owners.
func PortableArtifactPins(model PortableModel, installation string) ([]NamespacedArtifactPin, error) {
	seen := map[NamespacedArtifactPin]bool{}
	add := func(c *VersionedArtifactContext, pins []ArtifactPin) {
		if c.V3 != nil {
			for _, g := range c.V3.Groups {
				for _, p := range g.Pins {
					seen[NamespacedArtifactPin{Namespace: g.Namespace, Pin: p}] = true
				}
			}
			return
		}
		for _, p := range pins {
			seen[NamespacedArtifactPin{Namespace: ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: p}] = true
		}
	}
	for _, s := range model.Sources {
		if len(s.ArtifactContext) > 0 {
			c, err := DecodeVersionedArtifactContext(s.ArtifactContext, s.Revision.ArtifactPins)
			if err != nil {
				return nil, err
			}
			add(c, s.Revision.ArtifactPins)
		}
	}
	addCriterion := func(c ChangeCriterion) {
		if c.NamespacedArtifact != nil {
			seen[*c.NamespacedArtifact] = true
		} else if c.Artifact != nil {
			seen[NamespacedArtifactPin{Namespace: ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: *c.Artifact}] = true
		}
		if a := c.Attachment; a != nil {
			if a.NamespacedArtifact != nil {
				seen[*a.NamespacedArtifact] = true
			} else if a.Artifact != nil {
				seen[NamespacedArtifactPin{Namespace: ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: *a.Artifact}] = true
			}
		}
	}
	for _, p := range model.Proposals {
		for _, v := range p.FullRevisions {
			add(&VersionedArtifactContext{Legacy: &v.ArtifactContext, V3: v.ArtifactContextV3}, v.ArtifactPins)
			for _, c := range v.Criteria {
				addCriterion(c)
			}
		}
	}
	out := []NamespacedArtifactPin{}
	for pin := range seen {
		if err := pin.Validate(); err != nil {
			return nil, err
		}
		out = append(out, pin)
	}
	slices.SortFunc(out, func(a, b NamespacedArtifactPin) int {
		aa, _ := requestDigest(a)
		bb, _ := requestDigest(b)
		return strings.Compare(aa, bb)
	})
	return out, nil
}

func PortableProposalSourceDependencies(p PortableProposal) []string {
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" {
			seen[id] = true
		}
	}
	for _, v := range p.FullRevisions {
		add(v.BaseRevisionID)
		for _, c := range v.Criteria {
			if c.Attachment != nil && c.Attachment.Kind == "source" {
				add(c.Attachment.RevisionID)
			}
		}
		for _, c := range v.Delta.Created {
			if c.Origin.BaseRef != nil {
				add(c.Origin.BaseRef.RevisionID)
			}
		}
		for _, c := range v.Delta.Properties {
			if c.Origin.BaseRef != nil {
				add(c.Origin.BaseRef.RevisionID)
			}
		}
		for _, c := range v.Delta.Removed {
			if c.Origin.BaseRef != nil {
				add(c.Origin.BaseRef.RevisionID)
			}
		}
		for _, c := range v.Delta.CarriedIdentities {
			add(c.Basis.RevisionID)
		}
		for _, c := range v.Delta.IdentityIntents {
			if c.Target.Basis != nil {
				add(c.Target.Basis.RevisionID)
			}
		}
		if v.Rebase != nil {
			add(v.Rebase.Input.NewBaseRevisionID)
		}
	}
	for _, v := range p.LegacyRevisions {
		add(v.BaseRevisionID)
		for _, o := range v.Overlays {
			if o.Base != nil {
				add(o.Base.RevisionID)
			}
		}
	}
	out := []string{}
	for id := range seen {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}
