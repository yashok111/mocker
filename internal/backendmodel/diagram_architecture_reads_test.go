package backendmodel

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// Count the actual mutation's reads, including its locked reload. Counting only
// loadArchitectureGraph would miss a caller accidentally using the full resolver.
type diagramReadConnector struct {
	driver driver.Driver
	dsn    string
	reads  atomic.Int64
}

func (c *diagramReadConnector) Connect(context.Context) (driver.Conn, error) {
	conn, err := c.driver.Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &diagramReadConn{Conn: conn, counter: &c.reads}, nil
}

func (c *diagramReadConnector) Driver() driver.Driver { return c.driver }

type diagramReadConn struct {
	driver.Conn
	counter *atomic.Int64
}

func (c *diagramReadConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.counter.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *diagramReadConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *diagramReadConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opts)
}

func diagramCountReads(t *testing.T, r *Repo) (reader, writer *diagramReadConnector) {
	t.Helper()
	oldReader, oldWriter := r.db.R, r.db.W
	open := func(original *sql.DB, isWriter bool) (*sql.DB, *diagramReadConnector) {
		params := url.Values{"_pragma": {"foreign_keys(ON)", "busy_timeout(5000)"}}
		if isWriter {
			params.Set("_txlock", "immediate")
		}
		file := &url.URL{Path: r.db.Path()}
		counter := &diagramReadConnector{driver: original.Driver(), dsn: "file:" + file.EscapedPath() + "?" + params.Encode()}
		pool := sql.OpenDB(counter)
		pool.SetMaxOpenConns(1)
		return pool, counter
	}
	r.db.R, reader = open(oldReader, false)
	r.db.W, writer = open(oldWriter, true)
	t.Cleanup(func() {
		_ = r.db.R.Close()
		_ = r.db.W.Close()
		r.db.R, r.db.W = oldReader, oldWriter
	})
	return reader, writer
}

func diagramProofRef(g *EffectiveGraphSnapshot, proof Evidence) DiagramRef {
	kind := "edge"
	for _, node := range g.State.Nodes {
		if node.ID == proof.SubjectID {
			kind = "node"
			break
		}
	}
	return DiagramRef{Kind: "record", RecordType: kind, ID: proof.SubjectID}
}

func TestArchitectureSource5MutationHasBoundedReads(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveFiveRelationalFixture(t)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	doc := diagramTestDocument(base.Revision.ID)
	proof := graph.State.Evidence[0]
	doc.Payload.Elements[0].Refs = []DiagramRef{diagramProofRef(graph, proof)}
	doc.Payload.Elements[0].Origin = DiagramOrigin{Kind: "source_assertion", Evidence: []DiagramEvidenceRef{{RevisionID: base.Revision.ID, EvidenceID: proof.ID, SubjectID: proof.SubjectID}}}
	reader, writer := diagramCountReads(t, r)
	var version *DiagramVersion
	for _, op := range []string{"create", "save", "fork"} {
		t.Run(op, func(t *testing.T) {
			reader.reads.Store(0)
			writer.reads.Store(0)
			switch op {
			case "create":
				version, err = r.CreateDiagram(t.Context(), base.Project.ID, DiagramCreateInput{Document: doc, IdempotencyKey: op})
			case "save":
				doc.Payload.Elements[0].Label = "Updated responsibility"
				version, err = r.SaveDiagram(t.Context(), base.Project.ID, version.Pin.ID, DiagramSaveInput{Document: doc, ExpectedVersion: version.Pin.Version, IdempotencyKey: op})
			case "fork":
				version, err = r.ForkDiagram(t.Context(), base.Project.ID, DiagramForkInput{Source: version.Pin, Target: doc.Target, Reason: "Independent mapping", IdempotencyKey: op})
			}
			if err != nil {
				t.Fatal(err)
			}
			if version.TargetHash != graph.Pins.TargetHash || len(version.Gaps) != 0 {
				t.Fatalf("native source pin/evidence changed: %+v", version)
			}
			for pool, count := range map[string]int64{"reader": reader.reads.Load(), "writer": writer.reads.Load()} {
				limit := int64(16)
				if pool == "writer" {
					// The writer also probes receipt/blob/catalog quotas and sealing.
					limit = 28
				}
				if count == 0 || count > limit {
					t.Errorf("%s issued %d SQL reads; expected 1..%d bulk/metadata reads without per-evidence bootstrap", pool, count, limit)
				}
				t.Logf("%s SQL reads: %d", pool, count)
			}
		})
	}
}

func TestArchitectureMutationMatchesFullResolver(t *testing.T) {
	t.Parallel()
	for _, schema := range []string{"source5", "source5-artifacts", "source6", "proposal"} {
		t.Run(schema, func(t *testing.T) {
			r, base, _ := effectiveFiveRelationalFixture(t)
			if schema == "source6" {
				r, base, _ = effectiveRepresentationFixture(t)
			}
			if schema == "source5-artifacts" {
				service, old, _, _ := artifactServiceFixture(t)
				base, _ = upgradeEventsArtifactFixture(t, service, old)
				r = service.repo
			}
			target := BackendReadTarget{RevisionID: base.Revision.ID}
			if schema == "proposal" {
				draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Diagram mutation parity", BaseRevisionID: base.Revision.ID, IdempotencyKey: "parity"})
				if err != nil {
					t.Fatal(err)
				}
				target = BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}}
			}
			full, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, target)
			if err != nil {
				t.Fatal(err)
			}
			doc := diagramTestDocument(base.Revision.ID)
			doc.Target = target
			proof := full.State.Evidence[0]
			doc.Payload.Elements[0].Refs = []DiagramRef{diagramProofRef(full, proof)}
			doc.Payload.Elements[0].Origin = DiagramOrigin{Kind: "source_assertion", Evidence: []DiagramEvidenceRef{{RevisionID: base.Revision.ID, EvidenceID: proof.ID, SubjectID: proof.SubjectID}}}
			want, err := resolveDiagramEvidence(t.Context(), full, doc, nil)
			if err != nil {
				t.Fatal(err)
			}
			got, err := r.CreateDiagram(t.Context(), base.Project.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
			if err != nil {
				t.Fatal(err)
			}
			if got.TargetHash != full.Pins.TargetHash || !reflect.DeepEqual(got.Gaps, want) {
				t.Fatalf("mutation changed full-resolver validation: got %+v; expected gaps %+v", got, want)
			}
			before := diagramTableCounts(t, r.db)
			for _, invalidPart := range []string{"record", "proof", "flow"} {
				bad, err := normalizeDiagram(doc)
				if err != nil {
					t.Fatal(err)
				}
				const foreign = "10000000-0000-4000-8000-000000000099"
				switch invalidPart {
				case "record":
					bad.Payload.Elements[0].Refs[0].ID = foreign
				case "proof":
					bad.Payload.Elements[0].Origin.Evidence[0].EvidenceID = foreign
				case "flow":
					bad.Payload.Elements[0].Navigation = []ArchitectureNavigation{{Format: ArchitectureNavigationVersion, Kind: "flow", Label: "Foreign flow", FlowID: foreign, Target: &target}}
				}
				if err := bad.Validate(); err != nil {
					t.Fatalf("rejection fixture must pass wire validation: %v", err)
				}
				if _, err := r.CreateDiagram(t.Context(), base.Project.ID, DiagramCreateInput{Document: bad, IdempotencyKey: invalidPart}); err == nil {
					t.Fatalf("foreign %s accepted", invalidPart)
				}
			}
			if before != diagramTableCounts(t, r.db) {
				t.Fatal("invalid mutation wrote diagram rows")
			}
		})
	}
}

func TestArchitectureMutationReloadRejectsChangedPin(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveFiveRelationalFixture(t)
	doc := diagramTestDocument(base.Revision.ID)
	graph, err := r.readArchitectureGraph(t.Context(), base.Project.ID, doc.Target)
	if err != nil {
		t.Fatal(err)
	}
	// A stale outer read must not authorize a write even when the document is
	// otherwise valid. The locked reload must compute the actual target hash.
	graph.Pins.TargetHash = strings.Repeat("0", 64)
	digest, err := requestDigest(doc)
	if err != nil {
		t.Fatal(err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := r.writeDiagramMutation(t.Context(), tx, diagramMutation{pid: base.Project.ID, op: "create", key: "stale-pin", digest: digest, document: doc, graph: graph})
		return err
	})
	assertFault(t, err, "backend_diagram_pin_mismatch")
	if diagramTableCounts(t, r.db) != [5]int{} {
		t.Fatal("stale target pin wrote diagram rows")
	}
}

func TestArchitectureSource5MutationValidatesPinnedArtifacts(t *testing.T) {
	t.Parallel()
	service, base, ids, scenario := artifactServiceFixture(t)
	up, _ := upgradeEventsArtifactFixture(t, service, base)
	pinned, _ := applyArtifactTest(t, service, base.Project.ID, scenarioSet(up, ids, scenario), "source5-diagram-artifact")
	if pinned.Revision.SchemaVersion != EventsSchemaVersion {
		t.Fatal("fixture must exercise the source5 fast path")
	}
	page, err := service.Query(t.Context(), base.Project.ID, ArtifactQueryInput{RevisionID: pinned.Revision.ID, Artifact: ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(scenario.Scenario.ID, 10)}, View: "sequence", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	refs := []DiagramRef{}
	for _, item := range page.Items {
		if validHash(item.ObjectHash) {
			refs = append(refs, DiagramRef{Kind: "artifact", Locator: new(item.Locator), RowID: item.ID})
		}
	}
	if len(refs) < 2 {
		t.Fatal("fixture requires distinct exact artifact rows")
	}
	doc := diagramTestDocument(pinned.Revision.ID)
	doc.Payload.Elements[0].Refs = refs[:2]
	full, err := service.repo.ResolveEffectiveGraph(t.Context(), base.Project.ID, doc.Target)
	if err != nil {
		t.Fatal(err)
	}
	want, err := resolveDiagramEvidence(service.DiagramContext(t.Context()), full, doc, nil)
	if err != nil {
		t.Fatal(err)
	}
	counter := &diagramCountingScenarioReader{ScenarioArtifactReader: service.scenarios}
	reader := NewArtifactService(service.repo, service.api, counter)
	got, err := service.repo.CreateDiagram(reader.DiagramContext(t.Context()), base.Project.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create-artifact"})
	if err != nil {
		t.Fatal(err)
	}
	if got.TargetHash != full.Pins.TargetHash || !reflect.DeepEqual(got.Gaps, want) || counter.inspections != 1 {
		t.Fatalf("pinned artifact validation changed: gaps=%+v expected=%+v owner reads=%d", got.Gaps, want, counter.inspections)
	}
	before := diagramTableCounts(t, service.repo.db)
	for _, part := range []string{"row", "pin"} {
		bad, err := normalizeDiagram(doc)
		if err != nil {
			t.Fatal(err)
		}
		if part == "row" {
			bad.Payload.Elements[0].Refs[0].RowID += "-foreign"
		} else {
			bad.Payload.Elements[0].Refs[0].Locator.Pin.ContentHash = strings.Repeat("0", 64)
		}
		if err := bad.Validate(); err != nil {
			t.Fatalf("rejection fixture must pass wire validation: %v", err)
		}
		if _, err := service.repo.CreateDiagram(reader.DiagramContext(t.Context()), base.Project.ID, DiagramCreateInput{Document: bad, IdempotencyKey: "foreign-artifact-" + part}); err == nil {
			t.Fatalf("foreign artifact %s accepted", part)
		}
	}
	if before != diagramTableCounts(t, service.repo.db) {
		t.Fatal("foreign artifact reference wrote diagram data")
	}
}
