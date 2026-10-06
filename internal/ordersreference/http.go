package ordersreference

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"io"
	"mime"
	"net/http"
	"strings"
)

func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	want := sha256.Sum256([]byte("Bearer " + s.config.Token))
	if len(r.Header.Values("Authorization")) != 1 || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
		s.fail(w, &protocolError{401, "unauthorized"})
		return
	}
	if r.URL.RawQuery != "" || r.URL.ForceQuery || r.URL.RawPath != "" {
		s.fail(w, &protocolError{400, "invalid_request"})
		return
	}
	if r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/__mocker_test/runs/") && strings.HasSuffix(r.URL.Path, "/observations") {
		parts := strings.Split(r.URL.Path, "/")
		if len(parts) != 5 || !p.ValidID(parts[3]) || r.ContentLength != 0 || len(r.TransferEncoding) > 0 {
			s.fail(w, &protocolError{400, "invalid_request"})
			return
		}
		records, e := s.RecordedBusiness(parts[3])
		if e != nil {
			s.fail(w, e)
			return
		}
		s.respond(w, 200, struct {
			Identity    p.Identity `json:"identity"`
			Records     any        `json:"records"`
			Retention   string     `json:"retention"`
			Limitations []string   `json:"limitations"`
		}{s.identity, records, s.ObservationRetention(parts[3]), []string{"process-lifetime observations; unavailable after restart", "SQL is partial business scope; reset/control/journal/savepoints excluded", "mocked/payment is in-process, not real payment network traffic"}}, p.JournalLimit)
		return
	}
	ep, run := route(r)
	if ep == "" {
		s.fail(w, &protocolError{404, "not_found"})
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, p.BodyLimit+1))
	if err != nil || len(raw) > p.BodyLimit {
		s.fail(w, &protocolError{400, "invalid_request"})
		return
	}
	if r.Method == http.MethodGet {
		if len(raw) != 0 {
			s.fail(w, &protocolError{400, "invalid_request"})
			return
		}
		if ep == p.IdentityEndpoint {
			s.mu.Lock()
			var n int64
			err = s.db.QueryRowContext(r.Context(), "SELECT epoch FROM metadata WHERE singleton=1").Scan(&n)
			s.mu.Unlock()
			if err != nil {
				s.fail(w, err)
				return
			}
			s.respond(w, 200, p.IdentityResponse{Identity: s.identity, IdentityHash: s.identityHash, Epoch: n}, p.BodyLimit)
			return
		}
		j, e := s.journal(r.Context(), run)
		if e != nil {
			s.fail(w, e)
			return
		}
		s.respond(w, 200, j, p.JournalLimit)
		return
	}
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || media != "application/json" {
		s.fail(w, &protocolError{400, "invalid_request"})
		return
	}
	var body any
	var fence p.Fence
	switch ep {
	case p.ResetEndpoint:
		var v p.ResetRequest
		err = p.Decode(raw, &v, p.BodyLimit)
		body = v
		fence = v.Fence
	case p.FailureEndpoint:
		var v p.FailureRequest
		err = p.Decode(raw, &v, p.BodyLimit)
		body = v
		fence = v.Fence
	case p.OrderEndpoint:
		var v p.OrderRequest
		err = p.Decode(raw, &v, p.BodyLimit)
		body = v
		fence = v.Fence
	}
	if err != nil || (ep != p.OrderEndpoint && run != fence.RunID) {
		s.fail(w, &protocolError{400, "invalid_request"})
		return
	}
	status, data, err := s.mutate(r.Context(), ep, fence, body)
	if err != nil {
		s.fail(w, err)
		return
	}
	write(w, status, data)
}
func route(r *http.Request) (p.Endpoint, string) {
	run := ""
	parts := strings.Split(r.URL.Path, "/")
	if len(parts) == 5 && parts[1] == "__mocker_test" && parts[2] == "runs" {
		run = parts[3]
	}
	for _, ep := range []p.Endpoint{p.IdentityEndpoint, p.ResetEndpoint, p.FailureEndpoint, p.OrderEndpoint, p.JournalEndpoint} {
		id := run
		if ep == p.OrderEndpoint {
			id = "11111111-1111-4111-8111-111111111111"
		}
		method, path, err := ep.Route(id)
		if err == nil && method == r.Method && path == r.URL.Path {
			return ep, run
		}
	}
	return "", ""
}
func (s *Service) respond(w http.ResponseWriter, status int, v any, limit int) {
	raw, err := p.Encode(v)
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(raw) > limit {
		s.fail(w, &protocolError{413, "journal_limit"})
		return
	}
	write(w, status, raw)
}
func (s *Service) fail(w http.ResponseWriter, err error) {
	status, code := 500, "internal_error"
	if e, ok := errors.AsType[*protocolError](err); ok {
		status, code = e.status, e.code
	}
	raw, _ := p.Encode(p.ErrorResponse{Protocol: p.Version, Code: code, Message: code})
	write(w, status, raw)
}
func write(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}
