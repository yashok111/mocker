package ordersreference

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"strconv"
	"time"
	"uuid"

	o "github.com/yashok111/mocker/internal/backendobservations"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func (s *Service) mutate(ctx context.Context, ep p.Endpoint, f p.Fence, body any) (int, []byte, error) {
	hash, err := p.RequestHash(ep, body)
	if err != nil {
		return 0, nil, &protocolError{400, "invalid_request"}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	clock := &observationClock{trace: randomHex(16), root: randomHex(8), execution: f.RunID, records: []o.Record{}}
	started := time.Now()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	var oldHash string
	var status int
	var raw []byte
	err = tx.QueryRow("SELECT hash,status,receipt FROM requests WHERE run_id=? AND request_key=?", f.RunID, f.RequestKey).Scan(&oldHash, &status, &raw)
	if err == nil {
		if oldHash != hash {
			return 0, nil, conflict("idempotency_conflict")
		}
		return status, raw, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, err
	}
	if f.IdentityHash != s.identityHash {
		return 0, nil, conflict("identity_mismatch")
	}
	current, err := epoch(tx)
	if err != nil {
		return 0, nil, err
	}
	if f.Epoch != current {
		return 0, nil, conflict("epoch_mismatch")
	}
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM requests WHERE run_id=? AND step_id=?", f.RunID, f.StepID).Scan(&n); err != nil {
		return 0, nil, err
	}
	if n != 0 {
		return 0, nil, conflict("idempotency_conflict")
	}
	r := p.Receipt{Fence: f, Endpoint: ep, RequestHash: hash, HTTPStatus: 200, ResultEpoch: current}
	if ep == p.ResetEndpoint {
		req := body.(p.ResetRequest)
		a := req.Authorization
		if a.TargetID != s.config.TargetID || a.ConfigVersion != s.config.ConfigVersion || a.IsolationID != s.config.IsolationID {
			return 0, nil, &protocolError{403, "forbidden"}
		}
		if err = tx.QueryRow("SELECT count(*) FROM runs WHERE id=?", f.RunID).Scan(&n); err != nil {
			return 0, nil, err
		}
		if n != 0 {
			return 0, nil, conflict("run_conflict")
		}
		if current == math.MaxInt64 {
			return 0, nil, conflict("epoch_mismatch")
		}
		idRaw, e := encode(s.identity)
		if e != nil {
			return 0, nil, e
		}
		authRaw, e := encode(a)
		if e != nil {
			return 0, nil, e
		}
		if _, err = tx.Exec("INSERT INTO runs(id,epoch,identity,authorization) VALUES(?,?,?,?)", f.RunID, current+1, idRaw, authRaw); err != nil {
			return 0, nil, err
		}
		if _, err = tx.Exec("UPDATE metadata SET epoch=? WHERE singleton=1", current+1); err != nil {
			return 0, nil, err
		}
		r.ResultEpoch = current + 1
		r.Outcome = "reset"
		err = event(tx, &r, "reset", "", 0)
	} else {
		j, e := readJournal(tx, f.RunID)
		if e != nil {
			return 0, nil, e
		}
		if j.Epoch != current {
			return 0, nil, conflict("epoch_mismatch")
		}
		if j.IdentityHash != s.identityHash {
			return 0, nil, conflict("identity_mismatch")
		}
		if ep == p.FailureEndpoint {
			if err = tx.QueryRow("SELECT count(*) FROM arms WHERE run_id=?", f.RunID).Scan(&n); err != nil {
				return 0, nil, err
			}
			if n != 0 || len(j.Receipts) != 1 {
				return 0, nil, conflict("run_conflict")
			}
			if _, err = tx.Exec("INSERT INTO arms VALUES(?,0)", f.RunID); err != nil {
				return 0, nil, err
			}
			r.Outcome = "armed"
			err = event(tx, &r, "armed", "", 0)
		} else {
			err = s.order(tx, body.(p.OrderRequest), j, &r, clock)
		}
	}
	if err != nil {
		return 0, nil, err
	}
	r.Counters, err = counters(tx, f.RunID)
	if err != nil {
		return 0, nil, err
	}
	if err = r.Validate(); err != nil {
		return 0, nil, err
	}
	raw, err = encode(r)
	if err != nil {
		return 0, nil, err
	}
	if _, err = tx.Exec("INSERT INTO requests VALUES(?,?,?,?,?,?)", f.RunID, f.RequestKey, f.StepID, hash, r.HTTPStatus, raw); err != nil {
		return 0, nil, err
	}
	if err = tx.Commit(); err != nil {
		return 0, nil, err
	}
	if ep == p.OrderEndpoint {
		ended := time.Now()
		requestBytes, _ := encode(body)
		status := "ok"
		if r.HTTPStatus >= 400 {
			status = "error"
		}
		clock.records = append(clock.records, o.Record{Type: "span", ID: clock.trace + "/" + clock.root, ExecutionID: f.RunID, TraceID: clock.trace, SpanID: clock.root, StartTimeUnixNano: strconv.FormatInt(started.UnixNano(), 10), EndTimeUnixNano: strconv.FormatInt(ended.UnixNano(), 10), Kind: "server", Category: "http", Status: status, Attrs: &o.Attributes{Operation: "orders/create", RequestBytes: new(int64(len(requestBytes))), ResponseBytes: new(int64(len(raw)))}})
		if _, exists := s.observations[f.RunID]; exists || len(s.observations) < 1000 {
			if len(s.observations[f.RunID])+len(clock.records) <= 800 {
				s.observations[f.RunID] = append(s.observations[f.RunID], clock.records...)
			} else {
				s.observationTruncated[f.RunID] = true
			}
		}
	}
	return r.HTTPStatus, raw, nil
}

func event(tx *sql.Tx, r *p.Receipt, kind, object string, amount int64) error {
	if err := tx.QueryRow("SELECT coalesce(max(sequence),0)+1 FROM events WHERE run_id=?", r.RunID).Scan(&r.Sequence); err != nil {
		return err
	}
	e := p.Event{Sequence: r.Sequence, Kind: kind, StepID: r.StepID, RequestKey: r.RequestKey, BusinessKey: r.BusinessKey, ObjectID: object, AmountMinor: amount}
	return insertDocument(tx, "INSERT INTO events VALUES(?,?,?)", e, r.RunID, r.Sequence)
}
func counters(tx *sql.Tx, run string) (p.Counters, error) {
	var c p.Counters
	err := tx.QueryRow(`SELECT (SELECT count(*) FROM orders WHERE run_id=?), (SELECT count(*) FROM charges WHERE run_id=?), (SELECT count(*) FROM events WHERE run_id=? AND json_extract(document,'$.kind')='attempt'), (SELECT coalesce(sum(consumed),0) FROM arms WHERE run_id=?)`, run, run, run, run).Scan(&c.Orders, &c.Charges, &c.Attempts, &c.Triggers)
	return c, err
}

func (s *Service) order(tx *sql.Tx, req p.OrderRequest, j p.Journal, r *p.Receipt, clock *observationClock) error {
	var consumed int
	err := clock.span("sql", "client", "orders/read-failure-arm", func() error {
		return tx.QueryRow("SELECT consumed FROM arms WHERE run_id=?", req.RunID).Scan(&consumed)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return conflict("run_conflict")
	}
	if err != nil {
		return err
	}
	attempts := []p.Receipt{}
	for _, previous := range j.Receipts {
		if previous.Endpoint == p.OrderEndpoint {
			attempts = append(attempts, previous)
		}
	}
	if req.Attempt == 1 {
		if len(attempts) != 0 || consumed != 0 {
			return conflict("run_conflict")
		}
	} else {
		if len(attempts) != 1 || attempts[0].Outcome != "persistence_failed" || attempts[0].BusinessKey != req.BusinessKey || consumed != 1 {
			return conflict("run_conflict")
		}
	}
	if req.Attempt > 1 {
		if err = clock.span("retry", "internal", "orders/explicit-retry", func() error { return nil }); err != nil {
			return err
		}
	}
	r.BusinessKey = req.BusinessKey
	r.Attempt = req.Attempt
	if err = event(tx, r, "attempt", "", 0); err != nil {
		return err
	}
	if err = clock.span("http", "client", "mocked/payment", func() error { return s.measuredPayment(tx, req, r, clock) }); err != nil {
		return err
	}
	if _, err = tx.Exec("SAVEPOINT order_persistence"); err != nil {
		return err
	}
	order := p.OrderRecord{ID: uuid.New().String(), BusinessKey: req.BusinessKey, Order: req.Order}
	if err = clock.span("sql", "client", "orders/write-order", func() error {
		return insertDocument(tx, "INSERT INTO orders VALUES(?,?,?)", order, req.RunID, req.BusinessKey)
	}); err != nil {
		return err
	}
	if consumed == 0 {
		if _, err = tx.Exec("ROLLBACK TO order_persistence"); err != nil {
			return err
		}
		if _, err = tx.Exec("RELEASE order_persistence"); err != nil {
			return err
		}
		if _, err = tx.Exec("UPDATE arms SET consumed=1 WHERE run_id=?", req.RunID); err != nil {
			return err
		}
		r.HTTPStatus = 503
		r.Outcome = "persistence_failed"
		return event(tx, r, "failure_triggered", "", 0)
	}
	if _, err = tx.Exec("RELEASE order_persistence"); err != nil {
		return err
	}
	r.HTTPStatus = 201
	r.Outcome = "persisted"
	r.OrderID = order.ID
	return event(tx, r, "order_persisted", order.ID, 0)
}

func (s *Service) measuredPayment(tx *sql.Tx, req p.OrderRequest, r *p.Receipt, clock *observationClock) error {
	// The payment substitute is durable SQLite state in the outer transaction.
	paymentKey := req.BusinessKey
	if s.identity.Variant == "buggy" {
		paymentKey = req.RequestKey
	}
	var raw []byte
	var charge p.ChargeRecord
	err := clock.span("sql", "client", "orders/read-charge", func() error {
		return tx.QueryRow("SELECT document FROM charges WHERE run_id=? AND payment_key=?", req.RunID, paymentKey).Scan(&raw)
	})
	switch {
	case err == nil:
		if err = p.Decode(raw, &charge, p.BodyLimit); err != nil {
			return err
		}
		r.ChargeID = charge.ID
		if err = event(tx, r, "payment_reused", charge.ID, charge.AmountMinor); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		charge = p.ChargeRecord{ID: uuid.New().String(), BusinessKey: req.BusinessKey, AmountMinor: req.Order.AmountMinor, Currency: req.Order.Currency, Scope: "mocked"}
		r.ChargeID = charge.ID
		if err = clock.span("sql", "client", "orders/write-charge", func() error {
			return insertDocument(tx, "INSERT INTO charges VALUES(?,?,?)", charge, req.RunID, paymentKey)
		}); err != nil {
			return err
		}
		if err = event(tx, r, "payment_charged", charge.ID, charge.AmountMinor); err != nil {
			return err
		}
	default:
		return err
	}

	return nil
}
