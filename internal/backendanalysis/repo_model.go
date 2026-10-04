package backendanalysis

import (
	"context"
	"database/sql"
)

type PreparedStart struct {
	ProjectID, InputHash, RequestHash, Key string
	InputJSON                              []byte
	OutputReservation                      int64
}
type PreparedSnapshot struct {
	Chunks   []ResultChunk
	Manifest ResultManifest
	Progress Progress
	// Encoded outside the writer; immutable sequence/content retries are checked.
	ManifestJSON []byte
	chunkCounts  []int
}
type TerminalSnapshot struct {
	Status     string
	Snapshot   PreparedSnapshot
	Diagnostic *Diagnostic
}
type ClaimedJob struct {
	Job   Job
	Input ImmutableInput
	Token string
}
type Receipt struct {
	ProjectID, Action, Key, JobID, RequestHash string
	Response                                   []byte
}
type AdmissionCheck func(context.Context, *sql.Tx) error
