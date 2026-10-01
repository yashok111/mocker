package admin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendProposalRoutePolicy(t *testing.T) {
	want := map[string]checkpointPolicy{
		"GET /api/backend-projects/{id}/proposals":                                    cpRead,
		"POST /api/backend-projects/{id}/proposals":                                   cpAnotherLayer,
		"GET /api/backend-projects/{id}/proposals/{pid}":                              cpRead,
		"POST /api/backend-projects/{id}/proposals/{pid}/preview":                     cpNeverTouchesLayer,
		"POST /api/backend-projects/{id}/proposals/{pid}/commands":                    cpAnotherLayer,
		"GET /api/backend-projects/{id}/proposals/{pid}/revisions/{prid}/nodes/{nid}": cpRead,
		"GET /api/backend-projects/{id}/proposals/{pid}/revisions/{prid}/evidence":    cpRead,
		"GET /api/backend-projects/{id}/proposals/{pid}/revisions/{prid}/coverage":    cpRead,
	}
	for _, route := range (&Server{}).routes() {
		if policy, ok := want[route.pattern]; ok {
			if route.checkpoint != policy || route.mcp != mcpAllow {
				t.Fatalf("wrong proposal policy: %+v", route)
			}
			delete(want, route.pattern)
		}
	}
	if len(want) != 0 {
		t.Fatalf("missing routes: %+v", want)
	}
}

func proposalTransportFixture(t *testing.T, s *Server, dialect string) (*backendmodel.ImportCommitResult, map[string]string) {
	t.Helper()
	r := s.backendRepo
	p, err := r.Create(t.Context(), backendmodel.CreateInput{Name: dialect, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	in := transportImportFixture(*p)
	in.Profile = backendmodel.RelationalProfile
	in.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile}
	in.Manifest.Snapshot.Files = []backendmodel.ManifestFile{}
	in.Manifest.Snapshot.CapturedAt = time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)
	for _, name := range []string{"schema.sql", "models.go", "migrations/001_initial.sql", "migrations/002_unsupported.sql"} {
		raw, err := os.ReadFile(filepath.Join("../backendmodel/testdata/relational/orders", dialect, "v1", name))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(raw)
		in.Manifest.Snapshot.Files = append(in.Manifest.Snapshot.Files, backendmodel.ManifestFile{Path: dialect + "/v1/" + name, ContentHash: hex.EncodeToString(hash[:]), FileType: strings.TrimPrefix(filepath.Ext(name), "."), AnalysisStatus: "analyzed"})
	}
	in.Inventory[0].KnownCount = 4
	in.Inventory[0].Denominator = new(int64(4))
	session, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join("../backendmodel/testdata/relational/orders", dialect, "v1", "commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.NewReplacer("@repositoryId@", session.RepositoryID, "@snapshotId@", session.SnapshotID).Replace(string(raw)))
	var commands []backendmodel.ImportCommand
	if err := json.Unmarshal(raw, &commands); err != nil {
		t.Fatal(err)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := r.PutImportBatch(t.Context(), p.ID, session.ID, "source", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, session.ID, backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("source preview: %+v %v", preview, err)
	}
	out, err := r.CommitImport(t.Context(), p.ID, session.ID, backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "source-commit"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, id := range batch.Identities {
		ids[id.ExternalKey] = id.ID
	}
	return out, ids
}

func TestBackendProposalRESTLifecycle(t *testing.T) {
	for _, dialect := range []string{"postgresql", "sqlite"} {
		t.Run(dialect, func(t *testing.T) {
			s := loopbackTestServer(t, nil)
			source, ids := proposalTransportFixture(t, s, dialect)
			base := "/api/backend-projects/" + source.Project.ID
			call := func(method, path string, in any, want int, out any) []byte {
				t.Helper()
				var raw []byte
				var err error
				if input, ok := in.(string); ok {
					raw = []byte(input)
				} else if in != nil {
					raw, err = json.Marshal(in)
					if err != nil {
						t.Fatal(err)
					}
				}
				status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, base+path, raw)
				if err != nil || status != want {
					t.Fatalf("%s %s: %d %s %v", method, path, status, data, err)
				}
				if want == 200 {
					validateBackendImportResponse(t, method, base+path, data)
				}
				if out != nil {
					if err := json.Unmarshal(data, out); err != nil {
						t.Fatal(err)
					}
				}
				return data
			}
			create := backendmodel.CreateProposalInput{Name: "Require user", BaseRevisionID: source.Revision.ID, RepositoryID: source.Project.Repositories[0].ID, DatastoreID: ids["database:orders"], FacetKey: "sql", IdempotencyKey: "proposal"}
			var d backendmodel.ProposalDetail
			created := call("POST", "/proposals", create, 200, &d)
			path := "/proposals/" + d.Proposal.ID
			call("GET", "/proposals?status=draft&limit=1", nil, 200, nil)
			call("GET", path+"?proposalRevisionId="+d.Revision.ID, nil, 200, nil)
			command := backendmodel.ProposalCommand{Type: "alter_column", CommandID: "required", Reason: "Every order has a user", ColumnID: ids["column:orders:user_id"], Nullable: new(false)}
			previewIn := backendmodel.PreviewProposalInput{ExpectedVersion: d.Proposal.Version, DraftRevisionID: d.Revision.ID, Commands: []backendmodel.ProposalCommand{command}}
			var preview backendmodel.ProposalPreview
			call("POST", path+"/preview", previewIn, 200, &preview)
			if preview.CandidateHash == nil {
				t.Fatalf("preview: %+v", preview)
			}
			apply := backendmodel.ApplyProposalInput{ExpectedVersion: previewIn.ExpectedVersion, DraftRevisionID: previewIn.DraftRevisionID, Commands: previewIn.Commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: "save"}
			var saved backendmodel.ProposalApplyResult
			receipt := call("POST", path+"/commands", apply, 200, &saved)
			if replay := call("POST", path+"/commands", apply, 200, nil); string(replay) != string(receipt) {
				t.Fatal("receipt changed")
			}
			if replay := call("POST", "/proposals", create, 200, nil); string(replay) != string(created) {
				t.Fatal("create receipt changed")
			}
			target := &backendmodel.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: saved.Revision.ID}
			call("POST", "/graph/query", backendmodel.GraphQueryInput{Proposal: target, RecordType: "nodes", ID: command.ColumnID}, 200, nil)
			call("POST", "/database/query", backendmodel.DatabaseQueryInput{Proposal: target, DatastoreID: create.DatastoreID, FacetKey: "sql", RecordType: "relationships"}, 200, nil)
			alias := path + "/revisions/" + saved.Revision.ID
			call("GET", alias+"/nodes/"+command.ColumnID, nil, 200, nil)
			call("GET", alias+"/evidence?subjectId="+command.ColumnID, nil, 200, nil)
			call("GET", alias+"/coverage", nil, 200, nil)
			// A source revision is not a proposal revision, including on aliases.
			call("GET", path+"/revisions/"+source.Revision.ID+"/nodes/"+command.ColumnID, nil, 404, nil)
			criteria := backendmodel.ProposalCommand{Type: "set_criteria", CommandID: "rollout", Reason: "Plan rollout", Criteria: []backendmodel.ProposalCriterionInput{{Key: "rollback", Kind: "migration_plan", TargetIDs: []string{ids["table:orders"]}, Description: "Review rollout and rollback"}}}
			criteriaInput := backendmodel.PreviewProposalInput{ExpectedVersion: saved.Proposal.Version, DraftRevisionID: saved.Revision.ID, Commands: []backendmodel.ProposalCommand{criteria}}
			call("POST", path+"/preview", criteriaInput, 200, nil)
			encodedCriteria, err := json.Marshal(criteriaInput)
			if err != nil {
				t.Fatal(err)
			}
			for _, malformed := range []string{
				strings.Replace(string(encodedCriteria), `"kind":"migration_plan"`, `"kind":"migration_plan","kind":"writers"`, 1),
				strings.Replace(string(encodedCriteria), `"description":"Review rollout and rollback"`, `"description":null`, 1),
				strings.Replace(string(encodedCriteria), `"description":"Review rollout and rollback"`, `"description":"Review rollout and rollback","status":"verified"`, 1),
			} {
				call("POST", path+"/preview", malformed, 400, nil)
			}
			for _, suffix := range []string{"?status=ready", "?status=", "?limit=0", "?status=draft&status=draft", "?baseRevisionId=null"} {
				call("GET", "/proposals"+suffix, nil, 400, nil)
			}
			for _, suffix := range []string{"?proposalRevisionId=", "?proposalRevisionId=" + d.Revision.ID + "&proposalRevisionId=" + d.Revision.ID, "?limit=501"} {
				call("GET", path+suffix, nil, 400, nil)
			}
			for _, raw := range []string{`null`, `{}`, `{"expectedVersion":null}`, `{"expectedVersion":1,"expectedVersion":1}`, `{"unknown":true}`} {
				call("POST", path+"/preview", raw, 400, nil)
			}
			call("POST", path+"/commands", strings.Repeat(" ", backendmodel.MaxImportBatchBytes+1), 413, nil)
			conflict := apply
			conflict.IdempotencyKey = "new-key"
			call("POST", path+"/commands", conflict, 409, nil)
			apply.Commands[0].Reason = "changed"
			call("POST", path+"/commands", apply, 409, nil)
			project, err := s.backendRepo.Get(t.Context(), source.Project.ID)
			if err != nil || project.Version != source.Project.Version || project.CurrentRevisionID != source.Revision.ID {
				t.Fatal("proposal advanced source")
			}
		})
	}
}
