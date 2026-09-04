// ws.go is P6d's serving half (decisions.md mocker-p6d-websocket D4, D6,
// D7, D8, D9; DESIGN §30.3, §30.4, §30.8, §30.11, §30.12): a custom endpoint
// of kind "ws" answered as a WebSocket on the mock plane, with all four
// behaviours — the two server-driven ones P6b built (timeline, tick, reused
// through the same streamLoop pieces) and the two that read an inbound frame
// (reactive, echo).
//
// The shape §30.8 prescribes and this file keeps: ONE extra goroutine per
// connection, the reader, because Go cannot select on a socket read; it
// reads, matches and queues, and never writes. The handler's own loop is the
// ONLY writer, one select over the timeline timer, the tick ticker, the
// ping, the lifetime, the admin inbox (P6c), the reply queue, the reader's
// exit and the connection context. The reader is unblocked by closing the
// connection and JOINED before the handler returns.
//
// The socket itself is internal/wsmock's; this file never imports the
// library (wsmock's boundary test keeps that true).
package mockplane

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/luafn"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/workspaces"
	"github.com/yashok111/mocker/internal/wsmock"
)

// codeOriginRefused is D6's refusal, before the upgrade.
const codeOriginRefused = "origin_refused"

// gapMarkerKey is the one key of D7's synthetic frame `{"$gap": N}` — a
// name no application object is likely to carry, so a JSON-object protocol
// can filter it by key.
const gapMarkerKey = "$gap"

// wsSocket is what the loop and the reader need of a connection — the
// four operations of wsmock.Conn — as an interface so that a white-box
// test can stand a blocking writer in for the socket (A5's budget test)
// without a slow peer.
type wsSocket interface {
	Read(ctx context.Context) (wsmock.MessageType, []byte, error)
	Write(ctx context.Context, typ wsmock.MessageType, p []byte) error
	Ping(ctx context.Context) error
	Close(code wsmock.StatusCode, reason string) error
	CloseNow() error
}

// originAllowed is D6: with no list every origin is allowed; with a list, a
// request with NO Origin header (every non-browser client) is allowed and a
// present Origin must match one element exactly on scheme://host[:port],
// case-insensitively — the elements were normalised by internal/config.
func originAllowed(origin string, allowed []string) bool {
	if len(allowed) == 0 || origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return false
	}
	got := strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
	for _, a := range allowed {
		if a == got {
			return true
		}
	}
	return false
}

// serveWS is D7's branch: everything before it (route_off, the live effect,
// the pause, the delay, the status) already ran in serveCustom.
func (p *Plane) serveWS(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, row *customep.Row, base resources.ScopeKey) {
	if p.streams == nil {
		httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable", "no stream registry is wired in this deployment")
		return
	}
	def := row.Stream
	if def == nil {
		p.log.Error("ws endpoint row carries no stream document", "workspace", ws.Slug, "endpoint", row.ID)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}
	// D6: the origin check comes first — a refused origin must not consume
	// a cap slot or be recorded as a stream.
	if !originAllowed(r.Header.Get("Origin"), p.streamOpts.Origins) {
		httpx.Err(w, http.StatusForbidden, codeOriginRefused, "this Origin is not in MOCKER_STREAM_ORIGINS")
		return
	}
	// D8/§30.6: a writer that cannot be hijacked is refused by name BEFORE
	// the library touches the response — the same code SSE answers for a
	// writer that cannot flush.
	if !wsmock.CanHijack(w) {
		httpx.Err(w, http.StatusNotImplemented, codeStreamingUnsupported, "this response writer cannot be hijacked (no WebSocket upgrade)")
		return
	}

	conn, err := p.streams.Open(r.Context(), ws.ID)
	if err != nil {
		switch {
		case errors.Is(err, stream.ErrCapExceeded):
			httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable", "this workspace's stream connection cap is reached (MOCKER_STREAM_MAX_CONNS)")
		case errors.Is(err, stream.ErrClosed):
			httpx.Err(w, http.StatusServiceUnavailable, "service_unavailable", "the server is shutting down")
		default:
			p.log.Error("ws: open", "workspace", ws.Slug, "endpoint", row.ID, "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "failed to open the stream")
		}
		return
	}
	defer conn.Release()
	conn.SetInfo(stream.Info{
		EndpointID: row.ID,
		Path:       row.Path,
		Kind:       row.Kind,
		Peer:       httpx.ResolvePeer(r, p.cfg.TrustProxy).String(),
	})

	// The handshake's query and headers are captured ONCE, here, and are
	// what a rule's query/header conditions see for the connection's whole
	// life (D4, §30.3).
	handshake := overrides.Input{Query: r.URL.Query(), Header: r.Header.Clone()}

	sock, err := wsmock.Accept(w, r, wsmock.AcceptOptions{MaxFrame: p.streamOpts.MaxFrame})
	if err != nil {
		// The library already wrote its own 4xx (a malformed handshake);
		// nothing streams and the row records that status.
		p.log.Debug("ws: accept", "workspace", ws.Slug, "endpoint", row.ID, "err", err)
		return
	}
	markStream(r, customep.KindWS)

	// A14: one frame log each way, one text frame per line — a WebSocket
	// payload has no delimiter of its own.
	attachStreamLogs(r, p.newFrameLog([]byte("\n")), p.newFrameLog([]byte("\n")))
	loop := newStreamLoop(def, p.tickSource(rt, row, p.newLuaHost(rt, ws, base, nil)), p.streamOpts)
	loop.hooks = streamHooks{
		onFrame: func(frame []byte) {
			noteStreamFrame(r, frame)
			conn.RecordFrame()
		},
		onPushed: func() {
			noteStreamPushed(r)
			conn.RecordPushed()
		},
		onSkip: func() {
			noteStreamSkipped(r)
			conn.RecordSkipped()
		},
		onErr: func(err error) {
			p.log.Debug("ws: tick body", "workspace", ws.Slug, "endpoint", row.ID, "err", err)
		},
	}
	wl := &wsLoop{
		streamLoop: loop,
		sock:       sock,
		conn:       conn,
		handshake:  handshake,
		queue:      newReplyQueue(p.streamOpts.SendBudget),
		onFrameIn: func(typ wsmock.MessageType, payload []byte) {
			conn.RecordFrameIn()
			noteStreamFrameIn(r, typ, payload)
		},
		onDropped: func(n int) { noteStreamRepliesDropped(r, n) },
		// A18 D10.2. The host is built once per connection from the same
		// runtime the tick's is — a hook and a tick on one endpoint reach
		// the same workspace's rows under the same base scope.
		onFrame: def.OnFrame,
		host:    p.newLuaHost(rt, ws, base, nil),
		onHookErr: func(err error) {
			noteOnFrameError(r)
			p.log.Debug("ws: onFrame hook", "workspace", ws.Slug, "endpoint", row.ID, "err", luafn.Note(err))
		},
	}
	code := wl.run(conn.Context()) //nolint:contextcheck // conn.Context() is Registry.Open's child of r.Context(); the registry's cancel (shutdown, an operator's close) is what must end the loop, and a context derived from r.Context() alone would not carry it
	noteStreamClose(r, int(code))
	if n := wl.queue.unreported(); n > 0 {
		noteStreamRepliesDropped(r, n)
	}
	if conn.ClosedByAdmin() {
		markStreamClosedByAdmin(r)
	}
}

// replyItem is one queued outbound frame from the reader: a reply, an
// echo, or the terminal close a rule asked for (D3, D7).
type replyItem struct {
	typ     wsmock.MessageType
	payload []byte
	close   *customep.RuleClose
}

// replyQueue is D7's byte-bounded queue between the reader and the loop.
// Data items count against budget; a terminal item never does and is the
// last item the reader ever enqueues (it stops reading after it). Overflow
// drops and counts; the loop emits one `{"$gap": N}` before its next write
// from the queue.
type replyQueue struct {
	mu      sync.Mutex
	items   []replyItem
	bytes   int64
	budget  int64
	dropped int // since the last marker
	notify  chan struct{}
}

func newReplyQueue(budget int64) *replyQueue {
	return &replyQueue{budget: budget, notify: make(chan struct{}, 1)}
}

// offer queues a data item or drops it; returns whether it was queued.
func (q *replyQueue) offer(it replyItem) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if it.close == nil && q.bytes+int64(len(it.payload)) > q.budget {
		q.dropped++
		return false
	}
	q.items = append(q.items, it)
	if it.close == nil {
		q.bytes += int64(len(it.payload))
	}
	select {
	case q.notify <- struct{}{}:
	default:
	}
	return true
}

// take removes the next item and returns it with the number of replies
// dropped before it (the gap the loop must announce first).
func (q *replyQueue) take() (it replyItem, gap int, ok bool) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.items) == 0 {
		return replyItem{}, 0, false
	}
	it = q.items[0]
	q.items = q.items[1:]
	if it.close == nil {
		q.bytes -= int64(len(it.payload))
	}
	gap, q.dropped = q.dropped, 0
	return it, gap, true
}

func (q *replyQueue) pending() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items) > 0
}

// unreported returns and clears the replies dropped since the last marker
// — what the loop reads on its way out, so drops after the LAST write from
// the queue still reach the row's replies_dropped (a gap marker announces
// them only when a write follows; at the end nothing does).
func (q *replyQueue) unreported() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := q.dropped
	q.dropped = 0
	return n
}

// wsLoop is one WebSocket connection: the P6b loop state (ordinal, timeline
// cursor, tick, hooks) plus the socket, the reader's channels and the reply
// queue.
type wsLoop struct {
	*streamLoop
	sock      wsSocket
	conn      *stream.Conn
	handshake overrides.Input
	queue     *replyQueue
	onFrameIn func(wsmock.MessageType, []byte)
	onDropped func(int)
	// A18 D10.2: onFrame is the Lua hook that REPLACES reactive and echo,
	// host is what its mock.* helpers reach, and onHookErr counts a hook
	// that failed. The counter is its OWN note token (on_frame_errors:K) and
	// deliberately not replies_dropped: that one means "the send budget was
	// full", and overloading it would hide broken code behind a full budget.
	onFrame   string
	host      luafn.Host
	onHookErr func(error)

	readerDone chan error // one slot: the reader's terminal error
	readerExit chan struct{}
	closeCode  wsmock.StatusCode // what the peer sent, when it closed first
}

// closeReason pairs D8's code with its reason word.
type closeReason struct {
	code   wsmock.StatusCode
	reason string
}

// run drives the connection to its end and returns the close code the row
// records: the one the server sent, or the peer's when the peer closed
// first. Every socket operation runs under a context derived from connCtx
// and bounded by the frame timeout (D7), so a registry shutdown aborts a
// blocked write within one timeout.
func (l *wsLoop) run(connCtx context.Context) wsmock.StatusCode { //nolint:gocyclo // the connection's whole life: one select per producer, one exit per D8 reason
	// wsCtx is the OWNED connection context (D7): cancelled by the reader's
	// exit, by this loop's exit, and — through connCtx — by an operator's
	// close or a registry shutdown.
	wsCtx, wsCancel := context.WithCancel(connCtx)
	defer wsCancel()
	// The READER's context is its own, cancelled only after the closing
	// handshake: the library closes the socket outright when the context
	// of a Read expires, so a reader under wsCtx would tear the connection
	// down the instant an operator's close or a shutdown cancelled it —
	// before the 1001 close frame D8 promises could be written and before
	// the peer's own close frame (which this reader is what reads) could
	// complete the handshake.
	readCtx, readCancel := context.WithCancel(context.WithoutCancel(connCtx))
	defer readCancel()
	l.readerDone = make(chan error, 1)
	l.readerExit = make(chan struct{})
	go l.read(readCtx, wsCancel)

	l.stopped = func() bool { return wsCtx.Err() != nil }
	lifetime := time.NewTimer(l.opts.Lifetime)
	defer lifetime.Stop()
	ping := time.NewTicker(l.opts.Ping)
	defer ping.Stop()
	l.armTimeline()
	defer func() {
		if l.timeline != nil {
			l.timeline.Stop()
		}
	}()
	var tickC <-chan time.Time
	tickActive := l.def.Tick != nil && l.tick != nil
	if tickActive {
		t := time.NewTicker(time.Duration(l.def.Tick.IntervalMs) * time.Millisecond)
		defer t.Stop()
		tickC = t.C
	}

	end := l.loop(wsCtx, lifetime.C, ping.C, tickC, tickActive)

	// Exit, in D7's order: the closing handshake (its peer half is read by
	// the reader, still running), then the socket, then the reader's
	// context, then the join; serveWS writes the row after this returns.
	if end.code == closedByPeer {
		// The peer closed first; the library completed the handshake
		// inside Read. Release the socket without a second handshake.
		_ = l.sock.CloseNow()
		readCancel()
		<-l.readerExit
		return l.closeCode
	}
	l.closeBounded(end)
	readCancel()
	<-l.readerExit
	return end.code
}

// closeBounded runs the closing handshake under the frame timeout
// (diff-review finding 1): the library's Close waits for the peer's close
// frame under its OWN fixed deadline (five seconds), which a registry
// shutdown cannot cancel and which exceeds FrameTimeout. The handshake runs
// on a helper goroutine; if it has not returned within one frame timeout
// the socket is closed outright (CloseNow), which makes the helper return,
// and the helper is JOINED before this returns — no goroutine outlives the
// handler (§30.8).
func (l *wsLoop) closeBounded(end closeReason) {
	done := make(chan error, 1)
	go func() { done <- l.sock.Close(end.code, end.reason) }()
	select {
	case err := <-done:
		if err != nil {
			_ = l.sock.CloseNow()
		}
	case <-time.After(l.opts.FrameTimeout):
		_ = l.sock.CloseNow()
		<-done
	}
}

// writeFailed classifies a failed write (diff-review finding 2): a peer
// close that arrived while the loop was in another case makes the very
// next write fail, and that connection ended by the PEER — its code, not
// "write failed", is what the row must record. readerDone is checked
// without blocking before the generic answer.
func (l *wsLoop) writeFailed() closeReason {
	select {
	case err := <-l.readerDone:
		return l.peerEnded(err)
	default:
		return closeReason{wsmock.StatusGoingAway, "write failed"}
	}
}

// closedByPeer is a sentinel code for "the peer closed first" inside run;
// never written to the wire.
const closedByPeer wsmock.StatusCode = -2

// loop is run's select; it returns how the connection ends.
func (l *wsLoop) loop(wsCtx context.Context, lifetimeC, pingC, tickC <-chan time.Time, tickActive bool) closeReason { //nolint:gocyclo // one case per producer is the specification (D7)
	for {
		select {
		case <-wsCtx.Done():
			if l.conn.ClosedByAdmin() {
				return closeReason{wsmock.StatusGoingAway, "closed by operator"}
			}
			select {
			case err := <-l.readerDone:
				return l.peerEnded(err)
			default:
			}
			return closeReason{wsmock.StatusGoingAway, "shutting down"}
		case err := <-l.readerDone:
			return l.peerEnded(err)
		case <-lifetimeC:
			return closeReason{wsmock.StatusNormalClosure, "lifetime"}
		case <-pingC:
			if err := l.ping(wsCtx); err != nil {
				return closeReason{wsmock.StatusGoingAway, "no pong"}
			}
		case <-l.timelineC:
			if !l.writeTimeline(wsCtx, tickActive) {
				if l.timelineDone() && !tickActive && l.def.ClosesWhenDone() && wsCtx.Err() == nil {
					return closeReason{wsmock.StatusNormalClosure, "done"}
				}
				return l.writeFailed()
			}
		case <-tickC:
			if !l.writeTickFrame(wsCtx) {
				return l.writeFailed()
			}
		case req := <-l.conn.Inbox():
			if !l.writePushed(wsCtx, req) {
				return l.writeFailed()
			}
		case <-l.queue.notify:
			// ONE item per wakeup, then re-arm the slot if more is queued:
			// a bare "for pending()" drain never re-entered this select, so
			// an echo peer that talks continuously starved the lifetime,
			// the ping, the tick and the admin inbox for as long as it kept
			// talking (the same shape internal/stream's drain yields for).
			it, gap, ok := l.queue.take()
			if !ok {
				continue
			}
			if gap > 0 {
				l.onDropped(gap)
				marker, _ := jsonx.Marshal(map[string]int{gapMarkerKey: gap})
				if !l.writeData(wsCtx, wsmock.Text, marker) {
					return l.writeFailed()
				}
			}
			if len(it.payload) > 0 {
				if !l.writeData(wsCtx, it.typ, it.payload) {
					return l.writeFailed()
				}
			}
			if it.close != nil {
				return closeReason{wsmock.StatusCode(it.close.Code), it.close.Reason}
			}
			if l.queue.pending() {
				select {
				case l.queue.notify <- struct{}{}:
				default:
				}
			}
		}
	}
}

// peerEnded turns the reader's terminal error into the loop's exit: a
// close from the peer (mirrored, recorded), or a read failure.
func (l *wsLoop) peerEnded(err error) closeReason {
	if code := wsmock.CloseStatus(err); code != wsmock.NoStatus {
		l.closeCode = code
		return closeReason{closedByPeer, ""}
	}
	l.closeCode = wsmock.StatusGoingAway
	return closeReason{closedByPeer, ""}
}

// frameCtx bounds one socket operation by the frame timeout (D7).
func (l *wsLoop) frameCtx(wsCtx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(wsCtx, l.opts.FrameTimeout)
}

// writeData writes one DATA frame under the next ordinal (D7's ordinal
// policy: only data frames the loop writes consume one).
func (l *wsLoop) writeData(wsCtx context.Context, typ wsmock.MessageType, payload []byte) bool {
	if l.stopped() {
		return false
	}
	ctx, cancel := l.frameCtx(wsCtx)
	defer cancel()
	l.id++
	if err := l.sock.Write(ctx, typ, payload); err != nil {
		return false
	}
	l.hooks.onFrame(payload)
	return true
}

func (l *wsLoop) writeTimeline(wsCtx context.Context, tickActive bool) bool {
	fr := l.def.Timeline.Frames[l.next]
	if !l.writeData(wsCtx, wsmock.Text, compactJSON(fr.Data)) {
		return false
	}
	l.next++
	if l.next >= len(l.def.Timeline.Frames) && l.def.Timeline.Loop {
		l.next = 0
	}
	if l.timelineDone() && !tickActive && l.def.ClosesWhenDone() {
		return false
	}
	l.armTimeline()
	return true
}

func (l *wsLoop) writeTickFrame(wsCtx context.Context) bool {
	if l.stopped() {
		return false
	}
	l.tickN++
	body, err := l.tick(wsCtx, l.tickN)
	switch {
	case errors.Is(err, errTickDeclined):
		// A18 D10.1 / clause 41, the same reading streamLoop.writeTick
		// makes: a Lua tick that returned nil sent nothing and is neither a
		// skip nor an error.
		return true
	case err != nil:
		l.hooks.onSkip()
		l.hooks.onErr(err)
		return true
	}
	return l.writeData(wsCtx, wsmock.Text, body)
}

// writePushed is P6c's admin push over WebSocket (D9): the data as one text
// frame; the event was refused by the admin handler, so it is empty here.
func (l *wsLoop) writePushed(wsCtx context.Context, req stream.PushRequest) bool {
	if l.stopped() {
		req.Reply(0, stream.ErrConnClosed)
		return false
	}
	if !l.writeData(wsCtx, wsmock.Text, req.Data) {
		req.Reply(0, stream.ErrConnClosed)
		return false
	}
	l.hooks.onPushed()
	req.Reply(l.id, nil)
	return true
}

func (l *wsLoop) ping(wsCtx context.Context) error {
	ctx, cancel := l.frameCtx(wsCtx)
	defer cancel()
	return l.sock.Ping(ctx)
}

// read is the reader goroutine (D7): read, count, match, queue; never
// write. On any error it records, cancels the owned context, reports on
// readerDone and exits; readerExit is closed last so run can join it.
func (l *wsLoop) read(readCtx context.Context, cancel context.CancelFunc) {
	defer close(l.readerExit)
	terminal := false
	for {
		typ, payload, err := l.sock.Read(readCtx)
		if err != nil {
			l.readerDone <- err
			cancel()
			return
		}
		l.onFrameIn(typ, payload)
		if terminal {
			// A rule already closed the connection (D7): nothing after it
			// is MATCHED, but the reader keeps draining, because the
			// peer's half of the closing handshake arrives on this same
			// read and nobody else reads it. A18 D10.2 puts the Lua hook
			// under exactly this rule: after a close, onFrame stops being
			// called and the draining continues.
			continue
		}
		terminal = l.react(readCtx, typ, payload)
	}
}

// reactLua is D10.2: the hook answers one inbound frame, and it REPLACES the
// reactive/echo path rather than running beside it — two producers for one
// frame is the precedence question D5 refuses to document.
//
// It runs on the READER goroutine, which is what makes reply ORDER follow
// frame order; a slow hook blocks only this connection's reads, up to the
// per-call timeout. Nothing here writes: the reply is ENQUEUED and the writer
// loop performs every write, the close included — P6d's discipline verbatim,
// and the reason D10.2 spells it out is that "the reader returns close" reads
// otherwise.
func (l *wsLoop) reactLua(ctx context.Context, typ wsmock.MessageType, payload []byte) bool {
	// Whether the frame is an OBJECT is decided the same way the reactive
	// matcher decides it, on the same bytes: a TEXT frame that decodes as a
	// JSON object. Two answers to that question would be two contracts for
	// one wire.
	isObject := false
	if typ == wsmock.Text {
		var obj map[string]any
		if err := jsonx.Unmarshal(payload, &obj); err == nil && obj != nil {
			isObject = true
		}
	}

	act, err := luafn.RunOnFrame(ctx, l.onFrame, payload, isObject, l.host)
	if err != nil {
		// D10.2: the reply is dropped, counted in on_frame_errors, and the
		// hook KEEPS being called for later frames — a broken hook must not
		// silently turn the connection into a sink.
		l.onHookErr(err)
		return false
	}
	switch act.Verb {
	case luafn.FrameReply:
		l.queue.offer(replyItem{typ: wsmock.Text, payload: act.Data})
		return false
	case luafn.FrameClose:
		// Terminal, outside the send budget, exactly as a reactive rule's
		// own close is: offer never drops it and the reader stops matching.
		l.queue.offer(replyItem{close: &customep.RuleClose{Code: act.Code, Reason: act.Reason}})
		return true
	default:
		return false
	}
}

// react matches one inbound frame (D4) and queues the answer; it returns
// true when the matched rule closes the connection.
func (l *wsLoop) react(ctx context.Context, typ wsmock.MessageType, payload []byte) bool {
	if l.onFrame != "" {
		return l.reactLua(ctx, typ, payload)
	}
	in := l.handshake
	if typ == wsmock.Text {
		var obj map[string]any
		if err := jsonx.Unmarshal(payload, &obj); err == nil && obj != nil {
			in.Body, in.BodyOK = obj, true
		}
	}
	if in.BodyOK {
		for _, rule := range l.def.Reactive {
			if !overrides.MatchAll(rule.When, in) {
				continue
			}
			it := replyItem{typ: wsmock.Text, close: rule.Close}
			if len(rule.Data) > 0 {
				it.payload = compactJSON(rule.Data)
			}
			if rule.Close != nil {
				l.queue.offer(it) // terminal: never dropped
				return true
			}
			l.queue.offer(it)
			return false
		}
	}
	if l.def.Echo {
		l.queue.offer(replyItem{typ: typ, payload: payload})
	}
	return false
}

// wsCloseNote renders the close code for the traffic notes.
func wsCloseNote(code int) string { return "close:" + strconv.Itoa(code) }
