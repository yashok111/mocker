// Traffic handlers implement DESIGN §14's traffic screen (screen 4): what the
// mock plane actually answered, listable, pollable and clearable from the
// admin API. Every read here goes through a [traffic.Repo] this package
// builds internally over db, exactly like specsRepo and overridesRepo
// (server.go's own comment on those fields is the rule: New's signature is
// shared with cmd/mocker/main.go, so a stateless reader over the database
// never needs to arrive as a New parameter). The RECORDER is different — it
// is the same live *traffic.Recorder the mock plane writes through, so it
// arrives via [Server.SetTraffic], a setter, exactly like live state.
package admin

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/traffic"
)

// trafficDefaultLimit and trafficMaxLimit bound the "limit" query parameter
// on both GET /traffic and GET /traffic/poll: a caller asking for more than
// the ceiling gets the ceiling, never an error — DESIGN §18 wants this
// screen cheap to poll, not a new way to fail a request.
const (
	trafficDefaultLimit = 100
	trafficMaxLimit     = 500
)

// TrafficControl is *traffic.Recorder as this package needs it: Flush, so
// DELETE can force the queue out before it deletes (otherwise the next
// periodic flush resurrects rows the operator just cleared — a bug an
// operator could never explain), and Dropped, so both GET routes can report
// how many events were lost to a full queue rather than silently omitting
// that fact.
type TrafficControl interface {
	Flush(ctx context.Context) error
	Dropped() int64
}

// SetTraffic wires rec as the Server's [TrafficControl]. Like SetLiveState,
// this is a setter rather than a New parameter for the same reason: New's
// signature is shared with cmd/mocker/main.go's five call sites. cmd/mocker
// wires the SAME *traffic.Recorder instance the mock plane records through —
// this package never constructs its own recorder, only reads the table it
// writes to (via trafficRepo) and forces its queue out (via this interface).
//
// A Server whose SetTraffic is never called still serves every traffic
// route: GET and poll simply report dropped=0 (there is no recorder to ask),
// and DELETE still deletes rows but cannot flush first — logged, not
// refused, since "the table right now" is still a legitimate thing to clear
// even with no recorder wired (a test harness with no reason to wire one,
// above all).
func (s *Server) SetTraffic(rec TrafficControl) {
	s.traffic = rec
}

// trafficDropped reports the recorder's lifetime dropped-event count, or 0
// when no recorder is wired — never a panic on a nil interface.
func (s *Server) trafficDropped() int64 {
	if s.traffic == nil {
		return 0
	}
	return s.traffic.Dropped()
}

// trafficListView is GET /traffic's wire shape: rows newest first, plus the
// two counters DESIGN §14 screen 4 wants alongside them.
type trafficListView struct {
	Rows    []traffic.Row `json:"rows"`
	Rate1m  int           `json:"rate1m"`
	Dropped int64         `json:"dropped"`
}

// trafficPollView is GET /traffic/poll's wire shape: rows oldest first, plus
// lastId so a poller can chain — "since" is a traffic ROW ID, never a
// timestamp (traffic_ws is indexed (workspace_id, id DESC) and ts ties
// inside a millisecond).
type trafficPollView struct {
	Rows    []traffic.Row `json:"rows"`
	LastID  int64         `json:"lastId"`
	Dropped int64         `json:"dropped"`
}

// trafficClearedView is DELETE /traffic's wire shape.
type trafficClearedView struct {
	Deleted int64 `json:"deleted"`
}

// handleListTraffic answers GET /api/workspaces/{id}/traffic?limit=: up to
// limit rows, NEWEST first, plus rate1m (§14 screen 4's "сюда пришло N
// запросов за минуту") — reported here and only here, never by poll, which a
// UI hits several times a minute and which has no use for a per-minute rate
// on every tick.
func (s *Server) handleListTraffic(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	limit := parseTrafficLimit(r)
	rows, err := s.trafficRepo.List(r.Context(), ws.ID, limit)
	if err != nil {
		s.log.Error("list traffic", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to list traffic")
		return
	}
	rate1m, err := s.trafficRepo.Rate1m(r.Context(), ws.ID, time.Now())
	if err != nil {
		s.log.Error("traffic rate1m", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to compute request rate")
		return
	}

	httpx.JSON(w, http.StatusOK, trafficListView{Rows: rows, Rate1m: rate1m, Dropped: s.trafficDropped()})
}

// handlePollTraffic answers GET /api/workspaces/{id}/traffic/poll?since=&limit=:
// rows with id strictly greater than "since", OLDEST first, so a poller
// applying them in order sees the same sequence they happened in. When there
// are no new rows, lastId echoes the "since" it was given — returning 0
// would make a chained poller replay the whole table on every quiet tick.
func (s *Server) handlePollTraffic(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	since := parseTrafficSince(r)
	limit := parseTrafficLimit(r)
	rows, err := s.trafficRepo.Since(r.Context(), ws.ID, since, limit)
	if err != nil {
		s.log.Error("poll traffic", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to poll traffic")
		return
	}

	lastID := since
	if n := len(rows); n > 0 {
		lastID = rows[n-1].ID
	}
	httpx.JSON(w, http.StatusOK, trafficPollView{Rows: rows, LastID: lastID, Dropped: s.trafficDropped()})
}

// handleDeleteTraffic answers DELETE /api/workspaces/{id}/traffic. It
// flushes the recorder BEFORE deleting: a delete that ran first would be
// silently undone the moment the recorder's next periodic tick writes out
// whatever was still sitting in its queue when the operator clicked clear.
func (s *Server) handleDeleteTraffic(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.requireUser(w, r); !ok {
		return
	}
	id, ok := parseWorkspaceID(w, r)
	if !ok {
		return
	}
	ws, ok := s.loadWorkspace(w, r, id)
	if !ok {
		return
	}

	if s.traffic != nil {
		if err := s.traffic.Flush(r.Context()); err != nil {
			// A flush failure does not block the clear that follows: the
			// operator asked to clear what is in the table NOW, and that is
			// still a legitimate request even if the in-flight queue could
			// not be forced out. Logged so the "rows came back after I
			// cleared them" report has an explanation on file.
			s.log.Error("traffic: flush before clear failed", "workspace", ws.Slug, "err", err)
		}
	} else {
		s.log.Warn("traffic: clearing with no recorder wired; anything still queued elsewhere could resurrect rows later", "workspace", ws.Slug)
	}

	n, err := s.trafficRepo.Clear(r.Context(), ws.ID)
	if err != nil {
		s.log.Error("clear traffic", "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to clear traffic")
		return
	}
	httpx.JSON(w, http.StatusOK, trafficClearedView{Deleted: n})
}

// parseTrafficLimit reads "limit" off r's query, defaulting to
// trafficDefaultLimit and clamping to trafficMaxLimit. Anything that is not
// a positive integer (missing, zero, negative, unparsable) falls back to the
// default rather than answering 400 — DESIGN §18 wants this screen cheap to
// poll, not a new way for a UI's stray query param to fail a request.
func parseTrafficLimit(r *http.Request) int {
	limit := trafficDefaultLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > trafficMaxLimit {
		limit = trafficMaxLimit
	}
	return limit
}

// parseTrafficSince reads "since" off r's query. A missing or negative value
// is 0 (the beginning of the table) — never an error: "since" is a cursor a
// poller echoes back verbatim, and a malformed one just means "start over",
// not a request worth failing.
func parseTrafficSince(r *http.Request) int64 {
	v := r.URL.Query().Get("since")
	if v == "" {
		return 0
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
