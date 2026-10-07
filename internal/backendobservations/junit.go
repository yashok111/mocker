package backendobservations

import (
	"encoding/xml"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"io"
	"math/big"
	"strconv"
	"strings"
)

func adaptJUnit(in AdaptInput, out *AdaptedBatch) ([]AdaptBatch, error) {
	d := xml.NewDecoder(strings.NewReader(in.Data))
	c := in.Context
	lo, _, _ := window(c)
	records := []Record{}
	depth := 0
	var current *Record
	var duration string
	reportHash := p.HashBytes([]byte(in.Data))
	sourceHash := c.Source.SourceFilesHash
	if sourceHash == "" {
		sourceHash = p.HashBytes([]byte("unknown"))
	}
	index := 0
	for {
		tok, e := d.Token()
		if e == io.EOF {
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
			switch v.Name.Local {
			case "testcase":
				if current != nil || len(records) >= 100000 {
					return nil, invalid()
				}
				index++
				a := []Assertion{}
				id := reportHash + "/" + strconv.Itoa(index)
				current = &Record{Type: "test", ID: id, ExecutionID: reportHash, SuiteID: "junit", CaseID: strconv.Itoa(index), Outcome: "passed", Timestamp: strconv.FormatInt(lo, 10), DurationNs: "0", Assertions: &a, RunPins: &RunPins{RunID: reportHash, ReportHash: reportHash, TriggerVerdict: "not_applicable", SourceHash: sourceHash}}
				duration = "0"
				for _, at := range v.Attr {
					if at.Name.Local == "time" {
						duration = at.Value
					} else {
						out.Excluded["testcase.attributes"]++
					}
				}
			case "skipped":
				if current != nil {
					current.Outcome = "skipped"
				}
			case "failure", "error":
				if current != nil {
					current.Outcome = "failed"
				}
				out.Excluded["failure/error"]++
			}
		case xml.EndElement:
			depth--
			if v.Name.Local == "testcase" {
				if current == nil {
					return nil, invalid()
				}
				// Plain decimal seconds only (review 2026-10-06, F159).
				if !plainDecimal(duration) {
					return nil, invalid()
				}
				n, ok := new(big.Rat).SetString(duration)
				if !ok || n.Sign() < 0 {
					return nil, invalid()
				}
				n.Mul(n, big.NewRat(1000000000, 1))
				if !n.IsInt() || !n.Num().IsInt64() {
					return nil, invalid()
				}
				current.DurationNs = n.Num().String()
				records = append(records, *current)
				current = nil
			}
		case xml.CharData:
			if len(strings.TrimSpace(string(v))) > 0 {
				out.Excluded["xml.text"]++
			}
		}
	}
	if depth != 0 || current != nil || len(records) == 0 {
		return nil, invalid()
	}
	out.Gaps = append(out.Gaps, "JUnit labels and output excluded; timestamp uses declared window start; no span timing inferred")
	return []AdaptBatch{{c, records}}, nil
}
