package backendobservations

import (
	"encoding/xml"
	"errors"
	"io"
	"math/big"
	"strconv"
	"strings"

	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func adaptJUnit(in AdaptInput, out *AdaptedBatch) ([]AdaptBatch, error) {
	d := xml.NewDecoder(strings.NewReader(in.Data))
	c := in.Context
	lo, _, _ := window(c)
	j := junitCases{records: []Record{}, lo: lo, reportHash: p.HashBytes([]byte(in.Data)), sourceHash: c.Source.SourceFilesHash, out: out}
	if j.sourceHash == "" {
		j.sourceHash = p.HashBytes([]byte("unknown"))
	}
	depth := 0
	for {
		tok, e := d.Token()
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return nil, invalid()
		}
		switch v := tok.(type) {
		case xml.Directive:
			return nil, fault(422, "xml_directive")
		case xml.StartElement:
			depth++
			if depth > 64 {
				return nil, fault(413, "xml_depth")
			}
			if e = j.start(v); e != nil {
				return nil, e
			}
		case xml.EndElement:
			depth--
			if v.Name.Local == "testcase" {
				if e = j.endCase(); e != nil {
					return nil, e
				}
			}
		case xml.CharData:
			if len(strings.TrimSpace(string(v))) > 0 {
				out.Excluded["xml.text"]++
			}
		}
	}
	if depth != 0 || j.current != nil || len(j.records) == 0 {
		return nil, invalid()
	}
	out.Gaps = append(out.Gaps, "JUnit labels and output excluded; timestamp uses declared window start; no span timing inferred")
	return []AdaptBatch{{c, j.records}}, nil
}

// junitCases accumulates one test record per <testcase>; outcome elements
// nested in the open case amend it.
type junitCases struct {
	records    []Record
	current    *Record
	duration   string
	index      int
	lo         int64
	reportHash string
	sourceHash string
	out        *AdaptedBatch
}

func (j *junitCases) start(v xml.StartElement) error {
	switch v.Name.Local {
	case "testcase":
		if j.current != nil || len(j.records) >= 100000 {
			return invalid()
		}
		j.index++
		a := []Assertion{}
		id := j.reportHash + "/" + strconv.Itoa(j.index)
		j.current = &Record{Type: "test", ID: id, ExecutionID: j.reportHash, SuiteID: "junit", CaseID: strconv.Itoa(j.index), Outcome: "passed", Timestamp: strconv.FormatInt(j.lo, 10), DurationNs: "0", Assertions: &a, RunPins: &RunPins{RunID: j.reportHash, ReportHash: j.reportHash, TriggerVerdict: "not_applicable", SourceHash: j.sourceHash}}
		j.duration = "0"
		for _, at := range v.Attr {
			if at.Name.Local == "time" {
				j.duration = at.Value
			} else {
				j.out.Excluded["testcase.attributes"]++
			}
		}
	case "skipped":
		if j.current != nil {
			j.current.Outcome = "skipped"
		}
	case "failure", "error":
		if j.current != nil {
			j.current.Outcome = "failed"
		}
		j.out.Excluded["failure/error"]++
	}
	return nil
}

func (j *junitCases) endCase() error {
	if j.current == nil {
		return invalid()
	}
	ns, e := junitDurationNs(j.duration)
	if e != nil {
		return e
	}
	j.current.DurationNs = ns
	j.records = append(j.records, *j.current)
	j.current = nil
	return nil
}

// junitDurationNs converts a JUnit time in plain decimal seconds (review
// 2026-10-06, F159) to whole nanoseconds that fit an int64.
func junitDurationNs(duration string) (string, error) {
	if !plainDecimal(duration) {
		return "", invalid()
	}
	n, ok := new(big.Rat).SetString(duration)
	if !ok || n.Sign() < 0 {
		return "", invalid()
	}
	n.Mul(n, big.NewRat(1000000000, 1))
	if !n.IsInt() || !n.Num().IsInt64() {
		return "", invalid()
	}
	return n.Num().String(), nil
}
