package backendobservations

import (
	"context"
	"database/sql"
	"errors"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"reflect"
	"slices"
	"strings"
)

// AnalysisPin addresses immutable evidence; Side belongs to the analysis, not the set.
type AnalysisPin struct {
	ObservationSetID   string `json:"observationSetId"`
	Version            int64  `json:"version"`
	ContentHash        string `json:"contentHash"`
	CorrelationVersion int64  `json:"correlationVersion"`
	CorrelationHash    string `json:"correlationHash"`
	Side               string `json:"side"`
}
type SourcePin struct {
	RevisionID      string `json:"revisionId"`
	SemanticHash    string `json:"semanticHash"`
	TargetGraphHash string `json:"targetGraphHash"`
}
type PinRequest struct {
	Pins              []AnalysisPin
	Targets           map[string]SourcePin
	DiagramScope      *bm.DiagramScope
	RequireCompatible bool
	MaxRecords        int
	MaxBytes          int
}
type PinnedSet struct {
	Pin         AnalysisPin         `json:"pin"`
	Version     Version             `json:"version"`
	Correlation CorrelationSnapshot `json:"correlation"`
	Records     []Record            `json:"records"`
}
type PinnedObservations struct {
	Sets []PinnedSet `json:"sets"`
}

func (v *AnalysisPin) UnmarshalJSON(raw []byte) error {
	type wire AnalysisPin
	if err := decode(raw, (*wire)(v)); err != nil {
		return err
	}
	return v.Validate()
}
func (v AnalysisPin) Validate() error {
	if !p.ValidID(v.ObservationSetID) || v.Version < 1 || v.CorrelationVersion < 1 || !p.ValidHash(v.ContentHash) || !p.ValidHash(v.CorrelationHash) || !slices.Contains([]string{"before", "after"}, v.Side) {
		return invalid()
	}
	return nil
}
func NormalizeAnalysisPins(pins []AnalysisPin) ([]AnalysisPin, error) {
	if len(pins) < 1 || len(pins) > 20 {
		return nil, invalid()
	}
	out := slices.Clone(pins)
	for _, pin := range out {
		if err := pin.Validate(); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(out, func(a, b AnalysisPin) int {
		x, _ := canonical(a)
		y, _ := canonical(b)
		return strings.Compare(string(x), string(y))
	})
	for i := 1; i < len(out); i++ {
		if out[i] == out[i-1] {
			return nil, invalid()
		}
	}
	return out, nil
}

// ResolveAnalysisPins verifies all owner boundaries and bounds frozen data. It
// never lists sets or follows head pointers, so append/reimport cannot enrich a job.
func (s *Service) ResolveAnalysisPins(ctx context.Context, pid string, in PinRequest) (*PinnedObservations, error) {
	pins, err := NormalizeAnalysisPins(in.Pins)
	if err != nil {
		return nil, err
	}
	if !p.ValidID(pid) || in.MaxRecords < 1 || in.MaxRecords > 20000 || in.MaxBytes < 1 || in.MaxBytes > 2<<20 {
		return nil, invalid()
	}
	out := &PinnedObservations{Sets: []PinnedSet{}}
	records, bytes := 0, 0
	for _, pin := range pins {
		target, ok := in.Targets[pin.Side]
		if !ok || !p.ValidID(target.RevisionID) || !p.ValidHash(target.SemanticHash) || !p.ValidHash(target.TargetGraphHash) {
			return nil, fault(422, "analysis_target_mismatch")
		}
		ver, e := s.Repo.Version(ctx, pid, pin.ObservationSetID, pin.Version)
		if e != nil {
			return nil, analysisReadError(e)
		}
		if ver.ContentHash != pin.ContentHash {
			return nil, fault(422, "analysis_pin_mismatch")
		}
		if ver.RecordCount > in.MaxRecords-records || ver.LogicalBytes > int64(in.MaxBytes-bytes) {
			return nil, fault(413, "analysis_input_limit")
		}
		var correlationBytes int
		if e := s.Repo.db.R.QueryRowContext(ctx, `SELECT length(CAST(document AS BLOB)) FROM backend_observation_correlations WHERE project_id=? AND set_id=? AND version=?`, pid, pin.ObservationSetID, pin.CorrelationVersion).Scan(&correlationBytes); e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return nil, fault(422, "analysis_pin_missing")
			}
			return nil, e
		}
		if correlationBytes > in.MaxBytes-bytes {
			return nil, fault(413, "analysis_input_limit")
		}
		corr, e := s.Repo.Correlation(ctx, pid, pin.ObservationSetID, pin.CorrelationVersion)
		if e != nil {
			return nil, analysisReadError(e)
		}
		if corr.ContentHash != pin.CorrelationHash || corr.Input.Observation != ver.VersionReceipt || corr.Input.RevisionID != target.RevisionID || corr.Input.SourceHash != target.SemanticHash || corr.Input.TargetGraphHash != target.TargetGraphHash || corr.Input.Policy != CorrelationPolicy || corr.Input.ServiceID != ver.Context.Source.ServiceID {
			return nil, fault(422, "analysis_pin_mismatch")
		}
		if in.RequireCompatible && !corr.SourceCompatible {
			return nil, fault(422, "analysis_source_incompatible")
		}
		if in.DiagramScope != nil && (corr.DiagramScope == nil || !reflect.DeepEqual(corr.DiagramScope, in.DiagramScope)) {
			return nil, fault(422, "analysis_diagram_mismatch")
		}
		set := PinnedSet{Pin: pin, Version: *ver, Correlation: *corr, Records: []Record{}}
		raw, e := canonical(set)
		if e != nil {
			return nil, e
		}
		bytes += len(raw)
		if bytes > in.MaxBytes {
			return nil, fault(413, "analysis_input_limit")
		}
		for cursor := ""; ; {
			page, e := s.Repo.Records(ctx, pid, pin.ObservationSetID, pin.Version, 500, cursor)
			if e != nil {
				return nil, e
			}
			for _, rec := range page.Items {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				raw, e := canonical(rec)
				if e != nil {
					return nil, e
				}
				bytes += len(raw) + 1
				records++
				if records > in.MaxRecords || bytes > in.MaxBytes {
					return nil, fault(413, "analysis_input_limit")
				}
				set.Records = append(set.Records, rec)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		out.Sets = append(out.Sets, set)
	}
	return out, nil
}
func analysisReadError(err error) error {
	var f *bm.FaultError
	if errors.As(err, &f) && f.Status == 404 {
		return fault(422, "analysis_pin_missing")
	}
	return err
}
