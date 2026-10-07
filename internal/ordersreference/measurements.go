package ordersreference

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strconv"
	"time"

	o "github.com/yashok111/mocker/internal/backendobservations"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

type ReadItem struct {
	ID       int    `json:"id"`
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}
type ReadMeasurement struct {
	Variant  string     `json:"variant"`
	Identity p.Identity `json:"identity"`
	Items    []ReadItem `json:"items"`
	Records  []o.Record `json:"records"`
	Start    string     `json:"start"`
	End      string     `json:"end"`
}

// PrepareReadFixture seeds ONLY this service's private database. Setup/control SQL
// is outside the measured business operation and is never imported as its trace.
func (s *Service) PrepareReadFixture(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, ddl := range []string{"CREATE TABLE IF NOT EXISTS read_products(id INTEGER PRIMARY KEY,sku TEXT NOT NULL)", "CREATE TABLE IF NOT EXISTS read_items(order_id INTEGER NOT NULL,product_id INTEGER NOT NULL,quantity INTEGER NOT NULL,PRIMARY KEY(order_id,product_id))"} {
		if _, err = tx.ExecContext(ctx, ddl); err != nil {
			return err
		}
	}
	for i := 1; i <= 50; i++ {
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO read_products VALUES(?,?)", i, fmt.Sprintf("sku-%d", i)); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO read_items VALUES(1,?,?)", i, i); err != nil {
			return err
		}
	}
	return tx.Commit()
}
func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

type observationClock struct {
	trace, root, execution string
	records                []o.Record
}

func (c *observationClock) span(category, kind, operation string, fn func() error) error {
	start := time.Now()
	err := fn()
	end := time.Now()
	status := "ok"
	if err != nil {
		status = "error"
	}
	id := randomHex(8)
	c.records = append(c.records, o.Record{Type: "span", ID: c.trace + "/" + id, ExecutionID: c.execution, TraceID: c.trace, SpanID: id, ParentSpanID: c.root, StartTimeUnixNano: strconv.FormatInt(start.UnixNano(), 10), EndTimeUnixNano: strconv.FormatInt(end.UnixNano(), 10), Kind: kind, Category: category, Status: status, Attrs: &o.Attributes{Operation: operation}})
	return err
}

// MeasureReadOrder runs actual SQLite operations. Both variants read the same
// private fixture; the branch changes query shape, never injected metric values.
func (s *Service) MeasureReadOrder(ctx context.Context, variant, execution string) (ReadMeasurement, error) {
	out := ReadMeasurement{Variant: variant, Identity: s.identity, Items: []ReadItem{}, Records: []o.Record{}}
	if variant != "n_plus_one" && variant != "batched" || execution == "" || len(execution) > 256 {
		return out, fmt.Errorf("invalid measurement selection")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	c := observationClock{trace: randomHex(16), root: randomHex(8), execution: execution, records: []o.Record{}}
	start := time.Now()
	out.Start = start.UTC().Format(time.RFC3339Nano)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	quantities := map[int]int{}
	ids := []int{}
	err = c.span("sql", "client", "orders/read-items", func() error {
		rows, e := tx.QueryContext(ctx, "SELECT product_id,quantity FROM read_items WHERE order_id=? ORDER BY product_id", 1)
		if e != nil {
			return e
		}
		defer rows.Close()
		for rows.Next() {
			var id, q int
			if e = rows.Scan(&id, &q); e != nil {
				return e
			}
			ids = append(ids, id)
			quantities[id] = q
		}
		return rows.Err()
	})
	if err != nil {
		return out, err
	}
	if variant == "n_plus_one" {
		for _, id := range ids {
			var sku string
			err = c.span("sql", "client", "orders/read-product", func() error {
				return tx.QueryRowContext(ctx, "SELECT sku FROM read_products WHERE id=?", id).Scan(&sku)
			})
			if err != nil {
				return out, err
			}
			out.Items = append(out.Items, ReadItem{id, sku, quantities[id]})
		}
	} else {
		err = c.span("sql", "client", "orders/read-products-batched", func() error {
			rows, e := tx.QueryContext(ctx, "SELECT p.id,p.sku FROM read_products p JOIN read_items i ON i.product_id=p.id WHERE i.order_id=? ORDER BY p.id", 1)
			if e != nil {
				return e
			}
			defer rows.Close()
			for rows.Next() {
				var id int
				var sku string
				if e = rows.Scan(&id, &sku); e != nil {
					return e
				}
				out.Items = append(out.Items, ReadItem{id, sku, quantities[id]})
			}
			return rows.Err()
		})
		if err != nil {
			return out, err
		}
	}
	if err = tx.Commit(); err != nil {
		return out, err
	}
	// The executed fixture notification crosses an in-memory channel. These roles
	// witness send/receive events, not producer-span completion or a real broker.
	message := randomHex(16)
	hash := sha256.Sum256([]byte(message))
	messageHash := hex.EncodeToString(hash[:])
	ch := make(chan string, 1)
	sendStart := time.Now()
	ch <- message
	sendEnd := time.Now()
	sendID := randomHex(8)
	recvStart := time.Now()
	received := <-ch
	recvEnd := time.Now()
	if received != message {
		return out, fmt.Errorf("notification mismatch")
	}
	recvID := randomHex(8)
	peerTrace := randomHex(16)
	send := o.Record{Type: "span", ID: c.trace + "/" + sendID, ExecutionID: execution, TraceID: c.trace, SpanID: sendID, ParentSpanID: c.root, StartTimeUnixNano: strconv.FormatInt(sendStart.UnixNano(), 10), EndTimeUnixNano: strconv.FormatInt(sendEnd.UnixNano(), 10), Kind: "internal", Category: "internal", Status: "ok", Attrs: &o.Attributes{Operation: "fixture/read-notification", MessageRole: "send", MessageIDHash: messageHash}}
	receive := o.Record{Type: "span", ID: peerTrace + "/" + recvID, ExecutionID: execution, TraceID: peerTrace, SpanID: recvID, StartTimeUnixNano: strconv.FormatInt(recvStart.UnixNano(), 10), EndTimeUnixNano: strconv.FormatInt(recvEnd.UnixNano(), 10), Kind: "internal", Category: "internal", Status: "ok", Attrs: &o.Attributes{Operation: "fixture/read-notification", MessageRole: "receive", MessageIDHash: messageHash}, Links: &[]o.SpanLink{{TraceID: c.trace, SpanID: sendID, Relation: "follows_from", Proof: &o.LinkProof{Profile: "orders-message-causal-v1", MessageIDHash: messageHash, PredecessorEvent: "send", SuccessorEvent: "receive"}}}}
	c.records = append(c.records, send, receive)
	encoded, err := encode(out.Items)
	if err != nil {
		return out, err
	}
	end := time.Now()
	out.End = end.UTC().Format(time.RFC3339Nano)
	root := o.Record{Type: "span", ID: c.trace + "/" + c.root, ExecutionID: execution, TraceID: c.trace, SpanID: c.root, StartTimeUnixNano: strconv.FormatInt(start.UnixNano(), 10), EndTimeUnixNano: strconv.FormatInt(end.UnixNano(), 10), Kind: "server", Category: "http", Status: "ok", Attrs: &o.Attributes{Operation: "orders/read-order"}}
	c.records = append(c.records, root, o.Record{Type: "measurement", ID: c.trace + "/response-bytes", ExecutionID: execution, Timestamp: end.UTC().Format(time.RFC3339Nano), Metric: "response_bytes", Value: strconv.Itoa(len(encoded)), Unit: "bytes", Basis: "encoded-json", Scope: "read-order-response"})
	out.Records = c.records
	return out, nil
}

// RecordedBusiness returns a defensive snapshot of executed business spans only.
// SQL scope is partial: protocol journal/fences/savepoints are excluded. Payment
// is an in-process substitute, explicitly marked mocked/payment, not network I/O.
func (s *Service) RecordedBusiness(run string) ([]o.Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := encode(s.observations[run])
	if err != nil {
		return nil, err
	}
	var out []o.Record
	if err = p.Decode(raw, &out, 4<<20); err != nil {
		return nil, err
	}
	return out, nil
}

func (s *Service) ObservationRetention(run string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.observations[run]; !ok {
		return "not retained: no business samples or process retention limit"
	}
	if s.observationTruncated[run] {
		return "truncated: process retention limit"
	}
	return "retained within process; SQL instrumentation remains partial"
}
