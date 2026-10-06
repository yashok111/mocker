package backendmodel

import (
	"context"
	"database/sql"
	"strconv"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
)

type portableAPIReader struct {
	*apidesign.Repo
	tx *sql.Tx
}

func (r portableAPIReader) ArtifactSnapshot(ctx context.Context, id, revision int64) (*apidesign.ArtifactSnapshot, error) {
	return r.Repo.ArtifactSnapshotTx(ctx, r.tx, id, revision)
}

type portableScenarioReader struct {
	*designscenario.Repo
	tx *sql.Tx
}

func (r portableScenarioReader) ArtifactInspectionSnapshot(ctx context.Context, id, revision int64) (*designscenario.ArtifactInspectionSnapshot, error) {
	return r.Repo.ArtifactInspectionSnapshotTx(ctx, r.tx, id, revision)
}
func (r *Repo) portableArtifactRequest(ctx context.Context, tx *sql.Tx) *EditorArtifactRequest {
	cfg := &config.Config{MaxBody: MaxRevisionBytes}
	api := apidesign.NewRepo(r.db, cfg)
	scenario := designscenario.NewRepo(r.db, cfg, api)
	return NewEditorArtifactRequest(ctx, portableAPIReader{api, tx}, portableScenarioReader{scenario, tx})
}
func (r *Repo) validatePortableMappingsTx(ctx context.Context, tx *sql.Tx, in PortableRemap) error {
	request := r.portableArtifactRequest(ctx, tx)
	origins := map[NamespacedArtifactPin]bool{}
	destinations := map[NamespacedArtifactPin]bool{}
	for _, pair := range in.Artifacts {
		if err := pair.Origin.Validate(); err != nil {
			return err
		}
		if origins[pair.Origin] || destinations[pair.Local] {
			return invalid("artifactMapping", "Mappings must be unique and injective")
		}
		origins[pair.Origin] = true
		destinations[pair.Local] = true
		if pair.Origin.Pin.Kind != pair.Local.Pin.Kind {
			return invalid("artifactMapping", "Owner kind differs")
		}
		if _, err := request.ResolveNamespacedPin(in.InstallationID, pair.Local); err != nil {
			return err
		}
	}
	return nil
}
func (r *Repo) resolvePortableContextTx(ctx context.Context, tx *sql.Tx, source *PortableSource) error {
	c, err := DecodeVersionedArtifactContext(source.ArtifactContext, nil)
	if err != nil {
		return err
	}
	if c.V3 == nil {
		return invalid("context", "Expected namespaced portable context")
	}
	installation, err := installationID(ctx, tx)
	if err != nil {
		return err
	}
	request := r.portableArtifactRequest(ctx, tx)
	if err := resolvePortableContext(c.V3, installation, request); err != nil {
		return err
	}
	source.ArtifactContext, err = EncodeArtifactContextV3(*c.V3)
	return err
}
func resolvePortableContext(c *ArtifactContextV3, installation string, request *EditorArtifactRequest) error {
	for i := range c.Groups {
		g := &c.Groups[i]
		if g.Namespace.Scope == "foreign" {
			continue
		}
		pins := map[ArtifactKey]ArtifactPin{}
		for _, p := range g.Pins {
			local, err := request.ResolveNamespacedPin(installation, NamespacedArtifactPin{Namespace: g.Namespace, Pin: p})
			if err != nil {
				return err
			}
			pins[ArtifactKey{Kind: local.Kind, ID: local.ID}] = local
		}
		for j := range g.APIBindings {
			b := &g.APIBindings[j]
			p, ok := pins[ArtifactKey{Kind: b.Ref.Kind, ID: b.Ref.ArtifactID}]
			if !ok {
				return invalid("binding", "Local API binding has no scoped pin")
			}
			object, err := request.ResolveAPIObject(p, b.Ref.Selector)
			if err != nil {
				return err
			}
			b.Ref.RevisionID, b.Ref.ContentHash, b.Ref.ObjectHash = p.RevisionID, p.ContentHash, object.ObjectHash
			b.Ref.ResolvedPointer = object.Pointer
		}
		for j := range g.EditorBindings {
			b := &g.EditorBindings[j]
			p, ok := pins[ArtifactKey{Kind: b.ArtifactKind, ID: b.ArtifactID}]
			if !ok {
				return invalid("binding", "Local editor binding has no scoped pin")
			}
			object, err := request.ResolveObject(p, b.Selector)
			if err != nil {
				return err
			}
			b.ObjectHash = object.ObjectHash
		}
	}
	return nil
}
func portableDecimal(v string) int64 { n, _ := strconv.ParseInt(v, 10, 64); return n }
