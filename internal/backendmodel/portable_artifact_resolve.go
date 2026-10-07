package backendmodel

import (
	"context"
	"database/sql"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
)

// txAPIReader and txScenarioReader read every owner snapshot the editor
// request asks for on one caller-held transaction. A caller holding the
// single writer (portable import) or a read snapshot (frozen preview replay)
// that read owners through the reader pool instead took a second connection
// per owner: pool-width callers each waited for one, and a writer holder
// waited on readers whose holders waited for the writer (review 2026-10-06,
// F3/F183).
type txAPIReader struct {
	*apidesign.Repo
	tx *sql.Tx
}

func (r txAPIReader) ArtifactSnapshot(ctx context.Context, id, revision int64) (*apidesign.ArtifactSnapshot, error) {
	return r.ArtifactSnapshotTx(ctx, r.tx, id, revision)
}

type txScenarioReader struct {
	*designscenario.Repo
	tx *sql.Tx
}

func (r txScenarioReader) ArtifactSnapshot(ctx context.Context, id, revision int64) (*designscenario.ArtifactSnapshot, error) {
	return r.ArtifactSnapshotTx(ctx, r.tx, id, revision)
}
func (r txScenarioReader) ArtifactInspectionSnapshot(ctx context.Context, id, revision int64) (*designscenario.ArtifactInspectionSnapshot, error) {
	return r.ArtifactInspectionSnapshotTx(ctx, r.tx, id, revision)
}

// ownerReaders builds the owner adapters with the materialization input cap.
// With a nil tx they read the pool; otherwise every snapshot reads tx.
func (r *Repo) ownerReaders(tx *sql.Tx) (APIArtifactReader, ScenarioArtifactReader) {
	cfg := &config.Config{MaxBody: MaxRevisionBytes}
	api := apidesign.NewRepo(r.db, cfg)
	scenario := designscenario.NewRepo(r.db, cfg, api)
	if tx == nil {
		return api, scenario
	}
	return txAPIReader{api, tx}, txScenarioReader{scenario, tx}
}
func (r *Repo) portableArtifactRequest(ctx context.Context, tx *sql.Tx) *EditorArtifactRequest {
	api, scenario := r.ownerReaders(tx)
	return NewEditorArtifactRequest(ctx, api, scenario)
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
