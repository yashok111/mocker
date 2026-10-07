package backendobservations

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"strings"
)

type otAttr struct {
	Key   string `json:"key"`
	Value struct {
		StringValue string `json:"stringValue"`
	} `json:"value"`
}
type otSpan struct {
	TraceID      string         `json:"traceId"`
	SpanID       string         `json:"spanId"`
	ParentSpanID string         `json:"parentSpanId"`
	Start        jsontext.Value `json:"startTimeUnixNano"`
	End          jsontext.Value `json:"endTimeUnixNano"`
	Kind         int            `json:"kind"`
	Status       struct {
		Code int `json:"code"`
	} `json:"status"`
	Links *[]struct {
		TraceID string `json:"traceId"`
		SpanID  string `json:"spanId"`
	} `json:"links"`
	Attributes []otAttr `json:"attributes"`
}

func otTime(v jsontext.Value) string {
	s := string(v)
	if len(s) > 1 && s[0] == '"' {
		var out string
		if json.Unmarshal(v, &out) == nil {
			return out
		}
	}
	return s
}
func adaptOTel(in AdaptInput, out *AdaptedBatch) ([]AdaptBatch, error) {
	var doc struct {
		ResourceSpans []struct {
			SchemaURL string `json:"schemaUrl"`
			Resource  struct {
				Attributes []otAttr `json:"attributes"`
			} `json:"resource"`
			ScopeSpans []struct {
				SchemaURL string   `json:"schemaUrl"`
				Spans     []otSpan `json:"spans"`
			} `json:"scopeSpans"`
		} `json:"resourceSpans"`
	}
	if json.Unmarshal([]byte(in.Data), &doc) != nil || len(doc.ResourceSpans) == 0 || len(doc.ResourceSpans) > 256 {
		return nil, invalid()
	}
	batches := []AdaptBatch{}
	for _, resource := range doc.ResourceSpans {
		if resource.SchemaURL != "" && resource.SchemaURL != "https://opentelemetry.io/schemas/1.27.0" {
			return nil, fault(422, "unsupported_schema")
		}
		c := in.Context
		c.Producer.AdapterVersion = in.Adapter
		attrs := map[string]string{}
		for _, a := range resource.Resource.Attributes {
			if _, ok := attrs[a.Key]; ok {
				return nil, invalid()
			}
			attrs[a.Key] = a.Value.StringValue
			out.Excluded["resource.attributes"]++
		}
		if service := attrs["service.name"]; service != "" && service != c.Source.ServiceID {
			c.Source = Source{Status: "unknown", ServiceID: service, Reason: "resource requires explicit build context"}
			out.Gaps = append(out.Gaps, "Separate resource context; build not matched")
		}
		if env := attrs["deployment.environment.name"]; env != "" {
			c.Environment.ID = env
		}
		if build := attrs["service.version"]; build != "" && build != c.Source.BuildID {
			c.Source = Source{Status: "unknown", ServiceID: c.Source.ServiceID, Reason: "resource build does not match declared context"}
		}
		batch := AdaptBatch{Context: c, Records: []Record{}}
		for _, scope := range resource.ScopeSpans {
			if scope.SchemaURL != "" && scope.SchemaURL != "https://opentelemetry.io/schemas/1.27.0" {
				return nil, fault(422, "unsupported_schema")
			}
			for _, s := range scope.Spans {
				if s.Kind < 1 || s.Kind > 3 || s.Status.Code < 0 || s.Status.Code > 2 {
					out.Unsupported = append(out.Unsupported, "unsupported span kind/status")
					continue
				}
				if s.Links != nil && len(*s.Links) > 32 {
					out.Unsupported = append(out.Unsupported, "span link limit exceeded; record omitted")
					continue
				}
				r := Record{Type: "span", ID: strings.ToLower(s.TraceID) + "/" + strings.ToLower(s.SpanID), ExecutionID: strings.ToLower(s.TraceID), TraceID: strings.ToLower(s.TraceID), SpanID: strings.ToLower(s.SpanID), ParentSpanID: strings.ToLower(s.ParentSpanID), StartTimeUnixNano: otTime(s.Start), EndTimeUnixNano: otTime(s.End), Kind: []string{"", "internal", "server", "client"}[s.Kind], Category: "internal", Status: []string{"unset", "ok", "error"}[s.Status.Code], Attrs: &Attributes{}}
				if strings.Trim(r.ParentSpanID, "0") == "" {
					r.ParentSpanID = ""
				}
				if s.Links != nil {
					// Links differing only in the attributes/tracestate dropped
					// here, and a link to the span itself, are not associations:
					// ValidateRecord rejects them, and one such link failed the
					// whole upload with a bare 422. Drop and count them instead
					// (review 2026-10-06, F160).
					links := []SpanLink{}
					seen := map[[2]string]bool{}
					for _, l := range *s.Links {
						link := SpanLink{TraceID: strings.ToLower(l.TraceID), SpanID: strings.ToLower(l.SpanID), Relation: "association"}
						key := [2]string{link.TraceID, link.SpanID}
						if seen[key] || link.TraceID == r.TraceID && link.SpanID == r.SpanID {
							out.Excluded["links.duplicate/self"]++
							continue
						}
						seen[key] = true
						links = append(links, link)
					}
					r.Links = &links
					out.Excluded["links.attributes/tracestate"] += len(links)
				}
				for _, a := range s.Attributes {
					switch a.Key {
					case "mocker.operation":
						r.Attrs.Operation = a.Value.StringValue
					case "mocker.query.fingerprint":
						r.Attrs.Fingerprint = a.Value.StringValue
					case "db.system", "db.system.name":
						r.Category = "sql"
					case "http.request.method":
						if r.Category == "internal" {
							r.Category = "http"
						}
					default:
						out.Excluded["span.attributes"]++
					}
				}
				out.Excluded["span.names/bodies/events/status.message"]++
				batch.Records = append(batch.Records, r)
			}
		}
		batches = append(batches, batch)
	}
	out.Gaps = append(out.Gaps, "Parents and links may refer to missing exact sets; generic links are associations only")
	return batches, nil
}
