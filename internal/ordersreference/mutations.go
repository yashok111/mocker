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
	defer func() { _ = tx.Rollback() }()
	if status, raw, found, err := priorRequest(ctx, tx, f, hash); err != nil || found {
		return status, raw, err
	}
	current, err := s.admit(ctx, tx, f)
	if err != nil {
		return 0, nil, err
	}
	r := p.Receipt{Fence: f, Endpoint: ep, RequestHash: hash, HTTPStatus: 200, ResultEpoch: current}
	if ep == p.ResetEndpoint {
		err = s.reset(ctx, tx, body.(p.ResetRequest), &r, current)
	} else {
		err = s.applyToRun(ctx, tx, ep, body, &r, current, clock)
	}
	if err != nil {
		return 0, nil, err
	}
	raw, err := recordRequest(ctx, tx, &r)
	if err != nil {
		return 0, nil, err
	}
	if err = tx.Commit(); err != nil {
		return 0, nil, err
	}
	if ep == p.OrderEndpoint {
		s.retainOrderSpans(clock, f.RunID, body, raw, r.HTTPStatus, started)
	}
	return r.HTTPStatus, raw, nil
}

// priorRequest replays an already answered request key: the same request gets
// its stored receipt back, a different one under the same key a conflict.
func priorRequest(ctx context.Context, tx *sql.Tx, f p.Fence, hash string) (status int, raw []byte, found bool, err error) {
	var oldHash string
	err = tx.QueryRowContext(ctx, "SELECT hash,status,receipt FROM requests WHERE run_id=? AND request_key=?", f.RunID, f.RequestKey).Scan(&oldHash, &status, &raw)
	if err == nil {
		if oldHash != hash {
			return 0, nil, true, conflict("idempotency_conflict")
		}
		return status, raw, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, nil, false, err
	}
	return 0, nil, false, nil
}

// admit checks a new request's fence against this instance and the current
// epoch, and that its step has not been answered under another key.
func (s *Service) admit(ctx context.Context, tx *sql.Tx, f p.Fence) (int64, error) {
	if f.IdentityHash != s.identityHash {
		return 0, conflict("identity_mismatch")
	}
	current, err := epoch(ctx, tx)
	if err != nil {
		return 0, err
	}
	if f.Epoch != current {
		return 0, conflict("epoch_mismatch")
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM requests WHERE run_id=? AND step_id=?", f.RunID, f.StepID).Scan(&n); err != nil {
		return 0, err
	}
	if n != 0 {
		return 0, conflict("idempotency_conflict")
	}
	return current, nil
}

// reset opens a new run under the next epoch, for an authorization naming
// exactly this target configuration.
func (s *Service) reset(ctx context.Context, tx *sql.Tx, req p.ResetRequest, r *p.Receipt, current int64) error {
	a := req.Authorization
	if a.TargetID != s.config.TargetID || a.ConfigVersion != s.config.ConfigVersion || a.IsolationID != s.config.IsolationID {
		return &protocolError{403, "forbidden"}
	}
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM runs WHERE id=?", r.RunID).Scan(&n); err != nil {
		return err
	}
	if n != 0 {
		return conflict("run_conflict")
	}
	if current == math.MaxInt64 {
		return conflict("epoch_mismatch")
	}
	idRaw, err := encode(s.identity)
	if err != nil {
		return err
	}
	authRaw, err := encode(a)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO runs(id,epoch,identity,authorization) VALUES(?,?,?,?)", r.RunID, current+1, idRaw, authRaw); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE metadata SET epoch=? WHERE singleton=1", current+1); err != nil {
		return err
	}
	r.ResultEpoch = current + 1
	r.Outcome = "reset"
	return event(ctx, tx, r, "reset", "", 0)
}

// applyToRun handles the two endpoints that act inside an existing run of
// the current epoch and this instance: arming the failure and ordering.
func (s *Service) applyToRun(ctx context.Context, tx *sql.Tx, ep p.Endpoint, body any, r *p.Receipt, current int64, clock *observationClock) error {
	j, err := readJournal(ctx, tx, r.RunID)
	if err != nil {
		return err
	}
	if j.Epoch != current {
		return conflict("epoch_mismatch")
	}
	if j.IdentityHash != s.identityHash {
		return conflict("identity_mismatch")
	}
	if ep != p.FailureEndpoint {
		return s.order(ctx, tx, body.(p.OrderRequest), j, r, clock)
	}
	var n int
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM arms WHERE run_id=?", r.RunID).Scan(&n); err != nil {
		return err
	}
	if n != 0 || len(j.Receipts) != 1 {
		return conflict("run_conflict")
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO arms VALUES(?,0)", r.RunID); err != nil {
		return err
	}
	r.Outcome = "armed"
	return event(ctx, tx, r, "armed", "", 0)
}

// recordRequest completes the receipt with the run's counters, validates it
// and stores it as the answer to its request key.
func recordRequest(ctx context.Context, tx *sql.Tx, r *p.Receipt) ([]byte, error) {
	var err error
	r.Counters, err = counters(ctx, tx, r.RunID)
	if err != nil {
		return nil, err
	}
	if err = r.Validate(); err != nil {
		return nil, err
	}
	raw, err := encode(*r)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO requests VALUES(?,?,?,?,?,?)", r.RunID, r.RequestKey, r.StepID, r.RequestHash, r.HTTPStatus, raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// retainOrderSpans keeps a committed order's spans for the observations
// route, within the process-lifetime retention limits.
func (s *Service) retainOrderSpans(clock *observationClock, run string, body any, raw []byte, httpStatus int, started time.Time) {
	ended := time.Now()
	requestBytes, _ := encode(body)
	status := "ok"
	if httpStatus >= 400 {
		status = "error"
	}
	clock.records = append(clock.records, o.Record{Type: "span", ID: clock.trace + "/" + clock.root, ExecutionID: run, TraceID: clock.trace, SpanID: clock.root, StartTimeUnixNano: strconv.FormatInt(started.UnixNano(), 10), EndTimeUnixNano: strconv.FormatInt(ended.UnixNano(), 10), Kind: "server", Category: "http", Status: status, Attrs: &o.Attributes{Operation: "orders/create", RequestBytes: new(int64(len(requestBytes))), ResponseBytes: new(int64(len(raw)))}})
	if _, exists := s.observations[run]; exists || len(s.observations) < 1000 {
		if len(s.observations[run])+len(clock.records) <= 800 {
			s.observations[run] = append(s.observations[run], clock.records...)
		} else {
			s.observationTruncated[run] = true
		}
	}
}

func event(ctx context.Context, tx *sql.Tx, r *p.Receipt, kind, object string, amount int64) error {
	if err := tx.QueryRowContext(ctx, "SELECT coalesce(max(sequence),0)+1 FROM events WHERE run_id=?", r.RunID).Scan(&r.Sequence); err != nil {
		return err
	}
	e := p.Event{Sequence: r.Sequence, Kind: kind, StepID: r.StepID, RequestKey: r.RequestKey, BusinessKey: r.BusinessKey, ObjectID: object, AmountMinor: amount}
	return insertDocument(ctx, tx, "INSERT INTO events VALUES(?,?,?)", e, r.RunID, r.Sequence)
}
func counters(ctx context.Context, tx *sql.Tx, run string) (p.Counters, error) {
	var c p.Counters
	err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM orders WHERE run_id=?), (SELECT count(*) FROM charges WHERE run_id=?), (SELECT count(*) FROM events WHERE run_id=? AND json_extract(document,'$.kind')='attempt'), (SELECT coalesce(sum(consumed),0) FROM arms WHERE run_id=?)`, run, run, run, run).Scan(&c.Orders, &c.Charges, &c.Attempts, &c.Triggers)
	return c, err
}

func (s *Service) order(ctx context.Context, tx *sql.Tx, req p.OrderRequest, j p.Journal, r *p.Receipt, clock *observationClock) error {
	var consumed int
	err := clock.span("sql", "client", "orders/read-failure-arm", func() error {
		return tx.QueryRowContext(ctx, "SELECT consumed FROM arms WHERE run_id=?", req.RunID).Scan(&consumed)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return conflict("run_conflict")
	}
	if err != nil {
		return err
	}
	if !attemptAdmitted(req, j, consumed) {
		return conflict("run_conflict")
	}
	if req.Attempt > 1 {
		if err = clock.span("retry", "internal", "orders/explicit-retry", func() error { return nil }); err != nil {
			return err
		}
	}
	r.BusinessKey = req.BusinessKey
	r.Attempt = req.Attempt
	if err = event(ctx, tx, r, "attempt", "", 0); err != nil {
		return err
	}
	if err = clock.span("http", "client", "mocked/payment", func() error { return s.measuredPayment(ctx, tx, req, r, clock) }); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "SAVEPOINT order_persistence"); err != nil {
		return err
	}
	order := p.OrderRecord{ID: uuid.New().String(), BusinessKey: req.BusinessKey, Order: req.Order}
	if err = clock.span("sql", "client", "orders/write-order", func() error {
		return insertDocument(ctx, tx, "INSERT INTO orders VALUES(?,?,?)", order, req.RunID, req.BusinessKey)
	}); err != nil {
		return err
	}
	if consumed == 0 {
		return failPersistence(ctx, tx, req, r)
	}
	if _, err = tx.ExecContext(ctx, "RELEASE order_persistence"); err != nil {
		return err
	}
	r.HTTPStatus = 201
	r.Outcome = "persisted"
	r.OrderID = order.ID
	return event(ctx, tx, r, "order_persisted", order.ID, 0)
}

// attemptAdmitted is the retry protocol: attempt 1 runs on a fresh arm, and
// any later attempt only as the single retry of a persistence failure for the
// same business key.
func attemptAdmitted(req p.OrderRequest, j p.Journal, consumed int) bool {
	attempts := []p.Receipt{}
	for _, previous := range j.Receipts {
		if previous.Endpoint == p.OrderEndpoint {
			attempts = append(attempts, previous)
		}
	}
	if req.Attempt == 1 {
		return len(attempts) == 0 && consumed == 0
	}
	return len(attempts) == 1 && attempts[0].Outcome == "persistence_failed" && attempts[0].BusinessKey == req.BusinessKey && consumed == 1
}

// failPersistence fires the armed failure: the order write is rolled back to
// its savepoint while the charge before it stays, and the arm is consumed.
func failPersistence(ctx context.Context, tx *sql.Tx, req p.OrderRequest, r *p.Receipt) error {
	if _, err := tx.ExecContext(ctx, "ROLLBACK TO order_persistence"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "RELEASE order_persistence"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE arms SET consumed=1 WHERE run_id=?", req.RunID); err != nil {
		return err
	}
	r.HTTPStatus = 503
	r.Outcome = "persistence_failed"
	return event(ctx, tx, r, "failure_triggered", "", 0)
}

func (s *Service) measuredPayment(ctx context.Context, tx *sql.Tx, req p.OrderRequest, r *p.Receipt, clock *observationClock) error {
	// The payment substitute is durable SQLite state in the outer transaction.
	paymentKey := req.BusinessKey
	if s.identity.Variant == "buggy" {
		paymentKey = req.RequestKey
	}
	var raw []byte
	var charge p.ChargeRecord
	err := clock.span("sql", "client", "orders/read-charge", func() error {
		return tx.QueryRowContext(ctx, "SELECT document FROM charges WHERE run_id=? AND payment_key=?", req.RunID, paymentKey).Scan(&raw)
	})
	switch {
	case err == nil:
		if err = p.Decode(raw, &charge, p.BodyLimit); err != nil {
			return err
		}
		r.ChargeID = charge.ID
		if err = event(ctx, tx, r, "payment_reused", charge.ID, charge.AmountMinor); err != nil {
			return err
		}
	case errors.Is(err, sql.ErrNoRows):
		charge = p.ChargeRecord{ID: uuid.New().String(), BusinessKey: req.BusinessKey, AmountMinor: req.Order.AmountMinor, Currency: req.Order.Currency, Scope: "mocked"}
		r.ChargeID = charge.ID
		if err = clock.span("sql", "client", "orders/write-charge", func() error {
			return insertDocument(ctx, tx, "INSERT INTO charges VALUES(?,?,?)", charge, req.RunID, paymentKey)
		}); err != nil {
			return err
		}
		if err = event(ctx, tx, r, "payment_charged", charge.ID, charge.AmountMinor); err != nil {
			return err
		}
	default:
		return err
	}

	return nil
}
