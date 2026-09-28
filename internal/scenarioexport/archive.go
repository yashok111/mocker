package scenarioexport

import (
	"archive/zip"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"

	"github.com/yashok111/mocker/internal/designscenario"
)

const MaxArchiveItems = 32

type ArchiveRequest struct {
	Items []Request `json:"items"`
}

type ArchiveScenario struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	RevisionID int64  `json:"revisionId"`
	Version    int64  `json:"version"`
	SourceHash string `json:"sourceHash"`
}

type ArchiveFile struct {
	Path        string       `json:"path"`
	Format      string       `json:"format"`
	MediaType   string       `json:"mediaType"`
	Bytes       int64        `json:"bytes"`
	SHA256      string       `json:"sha256"`
	Diagnostics []Diagnostic `json:"diagnostics"`
	ContractID  string       `json:"contractId,omitempty"`
}

type ArchiveManifest struct {
	SchemaVersion int             `json:"schemaVersion"`
	Kind          string          `json:"kind"`
	Restorable    bool            `json:"restorable"`
	Scenario      ArchiveScenario `json:"scenario"`
	Files         []ArchiveFile   `json:"files"`
}

type ArchiveArtifact struct {
	ScenarioID    int64           `json:"scenarioId"`
	RevisionID    int64           `json:"revisionId"`
	SourceHash    string          `json:"sourceHash"`
	Filename      string          `json:"filename"`
	MediaType     string          `json:"mediaType"`
	ContentBase64 string          `json:"contentBase64"`
	Manifest      ArchiveManifest `json:"manifest"`
}

// ValidateArchiveRequests rejects invalid or repeated selections before rendering.
func ValidateArchiveRequests(items []Request) error {
	if len(items) == 0 || len(items) > MaxArchiveItems {
		return ErrInvalidRequest
	}
	seen := make(map[Request]bool, len(items))
	for _, item := range items {
		switch item.Format {
		case PlantUML, Mermaid, Postman, CURL, Markdown, HTML:
			if item.ContractID != "" {
				return ErrInvalidRequest
			}
		case OpenAPIJSON, OpenAPIYAML:
			if item.ContractID == "" {
				return ErrInvalidRequest
			}
		default:
			return ErrUnsupportedFormat
		}
		if seen[item] {
			return ErrInvalidRequest
		}
		seen[item] = true
	}
	return nil
}

// ExportArchive packages the exact existing exports of one immutable revision.
// All allocations are bounded by the raw, ZIP and JSON response budgets. A
// failed item returns no archive; ZIP contents never leave this method early.
func (s *Service) ExportArchive(rev designscenario.Revision, items []Request) (ArchiveArtifact, error) {
	if err := ValidateArchiveRequests(items); err != nil {
		return ArchiveArtifact{}, err
	}
	result := ArchiveArtifact{
		ScenarioID: rev.ScenarioID, RevisionID: rev.ID, SourceHash: rev.Hash,
		Filename: fmt.Sprintf("scenario-%d-r%d.zip", rev.ScenarioID, rev.ID), MediaType: "application/zip",
		Manifest: ArchiveManifest{SchemaVersion: 1, Kind: "mocker-scenario-artifacts", Scenario: ArchiveScenario{ID: rev.ScenarioID, Title: rev.Document.Title, RevisionID: rev.ID, Version: rev.Version, SourceHash: rev.Hash}, Files: make([]ArchiveFile, 0, len(items))},
	}
	output := &boundedBuffer{limit: s.maxBytes}
	writer := zip.NewWriter(output)
	remaining := s.maxBytes
	for _, item := range items {
		artifact, err := s.Export(rev, item)
		if err != nil {
			return ArchiveArtifact{}, err
		}
		size := int64(len(artifact.Content))
		if size > remaining {
			return ArchiveArtifact{}, ErrTooLarge
		}
		remaining -= size
		hash := sha256.Sum256([]byte(artifact.Content))
		result.Manifest.Files = append(result.Manifest.Files, ArchiveFile{Path: artifact.Filename, Format: string(item.Format), MediaType: artifact.MediaType, Bytes: size, SHA256: fmt.Sprintf("%x", hash), Diagnostics: artifact.Diagnostics, ContractID: item.ContractID})
		if err := s.CheckResponse(result.Manifest); err != nil {
			return ArchiveArtifact{}, err
		}
		if err := writeArchiveFile(writer, artifact.Filename, artifact.Content); err != nil {
			return ArchiveArtifact{}, err
		}
	}
	manifest, err := json.Marshal(result.Manifest)
	if err != nil {
		return ArchiveArtifact{}, err
	}
	if int64(len(manifest)) > remaining {
		return ArchiveArtifact{}, ErrTooLarge
	}
	if err := writeArchiveFile(writer, "manifest.json", string(manifest)); err != nil {
		return ArchiveArtifact{}, err
	}
	if err := writer.Close(); err != nil {
		return ArchiveArtifact{}, err
	}
	// Count the full DTO (including an empty string's quotes) before allocating
	// base64. Its alphabet needs no JSON escaping, so this check is exact.
	budget := jsonBudget{remaining: s.maxBytes}
	if err := budget.value(reflect.ValueOf(result)); err != nil {
		return ArchiveArtifact{}, err
	}
	encodedSize := (int64(output.Len()) + 2) / 3 * 4
	if err := budget.take(encodedSize); err != nil {
		return ArchiveArtifact{}, err
	}
	result.ContentBase64 = base64.StdEncoding.EncodeToString(output.Bytes())
	return result, nil
}

func writeArchiveFile(writer *zip.Writer, name, content string) error {
	// Names only come from Export's numeric IDs and fixed suffixes, never titles
	// or contract IDs. DOS metadata avoids an extra wall-clock timestamp field.
	header := &zip.FileHeader{Name: name, Method: zip.Store, ModifiedDate: 33}
	header.SetMode(0644)
	file, err := writer.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = io.WriteString(file, content)
	return err
}
