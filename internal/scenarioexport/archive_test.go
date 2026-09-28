package scenarioexport

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestArchivePreservesArtifactsAndManifest(t *testing.T) {
	t.Parallel()
	rev := revisionFixture()
	rev.Version = 3
	rev.Document.Title = "../../Проверка 🦊"
	rev.Document.Contracts = []designscenario.Contract{{ID: "../../api", Document: []byte(apiDocument)}}
	items := []Request{{Format: Markdown}, {Format: OpenAPIJSON, ContractID: "../../api"}, {Format: Mermaid}, {Format: OpenAPIYAML, ContractID: "../../api"}}
	svc := New(validContract, 1<<20)
	archive, err := svc.ExportArchive(rev, items)
	if err != nil {
		t.Fatal(err)
	}
	repeated, err := svc.ExportArchive(rev, items)
	if err != nil || !reflect.DeepEqual(archive, repeated) {
		t.Fatalf("not deterministic: %v", err)
	}
	if archive.Filename != "scenario-7-r11.zip" || archive.MediaType != "application/zip" || archive.ScenarioID != 7 || archive.RevisionID != 11 || archive.SourceHash != rev.Hash {
		t.Fatalf("wrong archive metadata: %+v", archive)
	}
	raw, err := base64.StdEncoding.DecodeString(archive.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	if len(reader.File) != len(items)+1 {
		t.Fatal("wrong file count")
	}
	contents := map[string][]byte{}
	for _, file := range reader.File {
		if strings.ContainsAny(file.Name, "/\\") || file.Mode().Perm() != 0644 || file.Modified.Year() != 1980 {
			t.Fatalf("unsafe metadata: %+v", file.FileHeader)
		}
		stream, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(stream)
		stream.Close()
		if err != nil {
			t.Fatal(err)
		}
		contents[file.Name] = data
	}
	var manifest ArchiveManifest
	if err := json.Unmarshal(contents["manifest.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(manifest, archive.Manifest) || manifest.SchemaVersion != 1 || manifest.Kind != "mocker-scenario-artifacts" || manifest.Restorable || manifest.Scenario.Title != rev.Document.Title || manifest.Scenario.Version != 3 {
		t.Fatalf("wrong manifest: %+v", manifest)
	}
	for i, item := range items {
		expected, err := svc.Export(rev, item)
		if err != nil {
			t.Fatal(err)
		}
		file := manifest.Files[i]
		data := contents[file.Path]
		if string(data) != expected.Content || file.Path != expected.Filename || file.Format != string(item.Format) || file.ContractID != item.ContractID || file.MediaType != expected.MediaType || !reflect.DeepEqual(file.Diagnostics, expected.Diagnostics) || file.Bytes != int64(len(data)) || file.SHA256 != fmt.Sprintf("%x", sha256.Sum256(data)) {
			t.Fatalf("altered artifact: %+v", file)
		}
	}
}

func TestArchiveRejectsInvalidSelectionsAtomically(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		items []Request
		want  error
	}{
		{"empty", nil, ErrInvalidRequest},
		{"too many", make([]Request, 33), ErrInvalidRequest},
		{"duplicate", []Request{{Format: Mermaid}, {Format: Mermaid}}, ErrInvalidRequest},
		{"duplicate contract", []Request{{Format: OpenAPIJSON, ContractID: "a"}, {Format: OpenAPIJSON, ContractID: "a"}}, ErrInvalidRequest},
		{"browser image", []Request{{Format: "svg"}}, ErrUnsupportedFormat},
		{"browser png", []Request{{Format: "png"}}, ErrUnsupportedFormat},
		{"pdf", []Request{{Format: "pdf"}}, ErrUnsupportedFormat},
		{"zip", []Request{{Format: "zip"}}, ErrUnsupportedFormat},
		{"unknown", []Request{{Format: "../../x"}}, ErrUnsupportedFormat},
		{"contract filter", []Request{{Format: Mermaid, ContractID: "a"}}, ErrInvalidRequest},
		{"missing id", []Request{{Format: OpenAPIJSON}}, ErrInvalidRequest},
		{"unknown contract after success", []Request{{Format: Mermaid}, {Format: OpenAPIJSON, ContractID: "missing"}}, ErrContractNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := New(validContract, 1<<20).ExportArchive(revisionFixture(), tt.items)
			if !errors.Is(err, tt.want) || !reflect.DeepEqual(got, ArchiveArtifact{}) {
				t.Fatalf("partial or accepted archive: %+v %v", got, err)
			}
		})
	}
	rev := revisionFixture()
	rev.Document.Contracts = []designscenario.Contract{{ID: "a", Document: []byte(apiDocument)}}
	rev.FormDrafts["all"] = "{"
	got, err := New(validContract, 1<<20).ExportArchive(rev, []Request{{Format: Mermaid}, {Format: OpenAPIJSON, ContractID: "a"}})
	if _, ok := errors.AsType[*BlockedError](err); !ok || !reflect.DeepEqual(got, ArchiveArtifact{}) {
		t.Fatalf("partial blocked archive: %+v %v", got, err)
	}
}

func TestArchiveWireBudgetAndRawTotal(t *testing.T) {
	t.Parallel()
	rev := revisionFixture()
	items := []Request{{Format: Mermaid}, {Format: PlantUML}}
	archive, err := New(nil, 1<<20).ExportArchive(rev, items)
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(archive)
	if err != nil {
		t.Fatal(err)
	}

	rawZIP, err := base64.StdEncoding.DecodeString(archive.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := json.Marshal(archive.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	rawTotal := int64(len(manifest))
	for _, file := range archive.Manifest.Files {
		rawTotal += file.Bytes
	}
	zipLimit := int64(len(rawZIP) - 1)
	if rawTotal >= zipLimit {
		t.Fatal("fixture must fit raw budget but exceed ZIP budget")
	}
	if got, err := New(nil, zipLimit).ExportArchive(rev, items); !errors.Is(err, ErrTooLarge) || !reflect.DeepEqual(got, ArchiveArtifact{}) {
		t.Fatalf("ZIP byte limit: %+v %v", got, err)
	}
	for _, limit := range []int64{0, 1, int64(len(wire) - 1)} {
		got, err := New(nil, limit).ExportArchive(rev, items)
		if !errors.Is(err, ErrTooLarge) || !reflect.DeepEqual(got, ArchiveArtifact{}) {
			t.Fatalf("limit=%d: %+v %v", limit, got, err)
		}
	}
	if _, err := New(nil, int64(len(wire))).ExportArchive(rev, items); err != nil {
		t.Fatalf("exact budget: %v", err)
	}
	// Each export fits, but their combined raw content exceeds the shared budget.
	large := strings.Replace(apiDocument, "analyst", strings.Repeat("x", 10000), 1)
	rev.Document.Contracts = []designscenario.Contract{{ID: "a", Document: []byte(large)}, {ID: "b", Document: []byte(large)}}
	items = []Request{{Format: OpenAPIJSON, ContractID: "a"}, {Format: OpenAPIJSON, ContractID: "b"}}
	svc := New(validContract, 15000)
	for _, item := range items {
		if _, err := svc.Export(rev, item); err != nil {
			t.Fatalf("single must fit: %v", err)
		}
	}
	if got, err := svc.ExportArchive(rev, items); !errors.Is(err, ErrTooLarge) || !reflect.DeepEqual(got, ArchiveArtifact{}) {
		t.Fatalf("raw sum limit: %+v %v", got, err)
	}
}

func TestArchiveAccepts32UniqueFiles(t *testing.T) {
	t.Parallel()
	rev := revisionFixture()
	items := []Request{}
	for i := range 32 {
		id := fmt.Sprintf("api-%d", i)
		rev.Document.Contracts = append(rev.Document.Contracts, designscenario.Contract{ID: id, Document: []byte(apiDocument)})
		items = append(items, Request{Format: OpenAPIJSON, ContractID: id})
	}
	got, err := New(validContract, 1<<20).ExportArchive(rev, items)
	if err != nil || len(got.Manifest.Files) != 32 {
		t.Fatalf("32 unique files rejected: %v", err)
	}
}
