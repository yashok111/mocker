package stream

import (
	"strings"
	"testing"
)

func TestEncodeFrame(t *testing.T) {
	got := string(encodeFrame(42, []byte(`{"rows":[],"lastId":42,"dropped":0}`)))
	want := "id: 42\nevent: traffic\ndata: {\"rows\":[],\"lastId\":42,\"dropped\":0}\n\n"
	if got != want {
		t.Fatalf("encodeFrame =\n%q\nwant\n%q", got, want)
	}
}

func TestEncodeFrame_negativeAndZeroID(t *testing.T) {
	// since=0 is the "no cursor yet" case D7 hands the handler; the encoder
	// must not special-case it away.
	for _, id := range []int64{0, -1} {
		got := string(encodeFrame(id, []byte("{}")))
		if !strings.HasPrefix(got, "id: ") {
			t.Fatalf("id %d: frame missing id line: %q", id, got)
		}
		if !strings.HasSuffix(got, "\n\n") {
			t.Fatalf("id %d: frame not blank-line terminated: %q", id, got)
		}
	}
}

func TestPingFrame_isSSEComment(t *testing.T) {
	// A line starting with ":" is an SSE comment: EventSource's parser skips
	// it and fires no "message"/"traffic" event — that is the whole point of
	// using it as the keepalive rather than a zero-length data frame.
	if !strings.HasPrefix(string(pingFrame), ":") {
		t.Fatalf("pingFrame does not start with ':': %q", pingFrame)
	}
	if !strings.HasSuffix(string(pingFrame), "\n\n") {
		t.Fatalf("pingFrame not blank-line terminated: %q", pingFrame)
	}
}
