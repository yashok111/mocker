package backendmaterialize

import (
	"encoding/json/jsontext"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	bm "github.com/yashok111/mocker/internal/backendmodel"
)

func TestMaterializationNoopKeepsDraftAndPublishedState(t *testing.T) {
	t.Parallel()
	s, pid, in := fixture(t)
	api, err := s.apis.Create(t.Context(), apidesign.CreateInput{Name: "No-op", Document: apiDoc, Source: "ui"})
	must(t, err)
	installation, err := s.models.InstallationID(t.Context())
	must(t, err)
	in.Targets[0].Pin = &bm.NamespacedArtifactPin{Namespace: bm.ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: bm.ArtifactPin{Kind: "api_design", ID: strconv.FormatInt(api.Design.ID, 10), RevisionID: strconv.FormatInt(api.Draft.ID, 10), ContentHash: api.Draft.Hash}}
	in.Targets[0].ExpectedVersion = api.Design.Version
	in.Targets[0].Commands[0].APIDocument = api.Draft.Document
	var before, after int64
	must(t, s.db.R.QueryRow(`SELECT revision FROM workspaces WHERE id=?`, api.Design.DraftWorkspaceID).Scan(&before))
	request := applyInput(t, s, pid, in, "noop")
	receipt, err := s.Apply(t.Context(), pid, request)
	must(t, err)
	if receipt.Owners[0].Pin.Pin.RevisionID != strconv.FormatInt(api.Draft.ID, 10) || receipt.Owners[0].Version != api.Design.Version {
		t.Fatal("no-op created revision")
	}
	must(t, s.db.R.QueryRow(`SELECT revision FROM workspaces WHERE id=?`, api.Design.DraftWorkspaceID).Scan(&after))
	if before != after {
		t.Fatal("no-op changed draft mock")
	}
	var publishedSpec *int64
	must(t, s.db.R.QueryRow(`SELECT spec_id FROM workspaces WHERE id=?`, api.Design.PublishedWorkspaceID).Scan(&publishedSpec))
	if publishedSpec != nil {
		t.Fatal("implicit publication")
	}
}

func TestMaterializationLimitsAndUnsupportedOwnerCommands(t *testing.T) {
	t.Parallel()
	s, pid, base := fixture(t)
	for _, name := range []string{"targets", "commands", "bytes", "native"} {
		t.Run(name, func(t *testing.T) {
			in, err := cloneInput(base)
			must(t, err)
			switch name {
			case "targets":
				for len(in.Targets) < 6 {
					in.Targets = append(in.Targets, in.Targets[0])
				}
			case "commands":
				for len(in.Targets[0].Commands) < 101 {
					in.Targets[0].Commands = append(in.Targets[0].Commands, in.Targets[0].Commands[0])
				}
			case "bytes":
				in.Reason = strings.Repeat("x", MaxBytes+1)
			case "native":
				in.Targets[0].Commands[0].Type = "compile_sql"
			}
			if _, err := s.Preview(t.Context(), pid, in); err == nil {
				t.Fatal("unsupported/quota input accepted")
			}
		})
	}
}

func TestMaterializationUnsupportedSourceConstructs(t *testing.T) {
	for _, step := range []string{"loop", "parallel", "join", "opaque", "query", "transform"} {
		if !unsupportedSourceConstruct("step", map[string]jsontext.Value{"stepKind": jsontext.Value(`"` + step + `"`)}) {
			t.Fatal("native construct admitted", step)
		}
	}
	if !unsupportedSourceConstruct("consumer", nil) || unsupportedSourceConstruct("handler", nil) {
		t.Fatal("source coverage classification")
	}
}
