package stream

import "context"

// push.go is P6c's push half (decisions.md mocker-p6c-live-conns D3, D4):
// an operator's frame, addressed to ONE live connection, queued into that
// connection's inbox and written by the connection's own serving loop —
// never by the pusher. The pusher waits for the ordinal the loop wrote the
// frame under, so a caller observes a delivery, not a promise.

// inboxDepth is how many pushed frames one connection holds unwritten
// (D3). A constant, not a MOCKER_* variable: sixteen frames at the 4 MiB
// frame cap is 64 MiB per connection in the worst case, and only an
// authenticated operator can fill it — one more push than this answers
// ErrInboxFull rather than blocking or dropping.
const inboxDepth = 16

// PushRequest is one queued frame as the serving loop reads it off
// [Conn.Inbox]: the event name (empty for the browser's default "message"
// event) and the already-compact JSON payload. The loop writes it, then
// calls Reply exactly once with the ordinal it carried or the error that
// stopped it.
type PushRequest struct {
	Event string
	Data  []byte
	reply chan pushReply
}

type pushReply struct {
	id  int64
	err error
}

// Reply hands the pusher the outcome. reply is buffered (one slot) so a
// loop replying to a pusher that already gave up (ErrPushTimeout) never
// blocks — the reply sits unread and is collected with the request.
func (r PushRequest) Reply(id int64, err error) {
	r.reply <- pushReply{id: id, err: err}
}

// Inbox is the loop's read side of the queue — one more case in the mock
// plane's select (D4). Nil for a connection with no inbox (the admin
// feed's), and a receive from a nil channel blocks forever, which is
// exactly what a select case that must never fire should do.
func (c *Conn) Inbox() <-chan PushRequest { return c.inbox }

// Push queues one frame for c and waits until the loop has written it.
//
// The queue step never blocks: a full inbox answers ErrInboxFull at once
// and nothing was queued. The wait ends with the loop's reply (the ordinal
// the frame carries on the wire), with the connection ending
// (ErrConnClosed — the request's own context, the lifetime, an operator's
// close or a registry shutdown all cancel c's context, and deregister
// cancels it too, so no pusher stays parked behind a loop that already
// returned), or with the caller's ctx ending (ErrPushTimeout — the frame
// stays queued and may still be written). On either of the last two the
// reply channel is checked once more without blocking, because a select
// with two ready cases picks at random and a frame that WAS written must
// not be reported as lost or timed out.
func (c *Conn) Push(ctx context.Context, event string, data []byte) (int64, error) {
	if c.inbox == nil {
		return 0, ErrConnClosed
	}
	req := PushRequest{Event: event, Data: data, reply: make(chan pushReply, 1)}
	select {
	case c.inbox <- req:
	default:
		return 0, ErrInboxFull
	}
	select {
	case r := <-req.reply:
		return r.id, r.err
	case <-c.ctx.Done():
		return settle(req, ErrConnClosed)
	case <-ctx.Done():
		return settle(req, ErrPushTimeout)
	}
}

// settle is the second, non-blocking look at the reply the doc comment on
// Push describes.
func settle(req PushRequest, fallback error) (int64, error) {
	select {
	case r := <-req.reply:
		return r.id, r.err
	default:
		return 0, fallback
	}
}
