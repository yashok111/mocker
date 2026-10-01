package store

// TransientReservation accounts for an operation's temporary semantic payload.
// Repositories sharing a DB share its budget. Reservations are process-local;
// durable staging is counted by the caller inside the serialized writer.
type TransientReservation struct {
	db    *DB
	scope string
	bytes int64
}

// ReserveTransient reserves bytes when the total stays within available.
// Call inside DB.Write when available depends on durable staging, so a staging
// mutation cannot race the reservation. Release on every exit path.
func (db *DB) ReserveTransient(scope string, bytes, available int64) (*TransientReservation, bool) {
	db.transientMu.Lock()
	defer db.transientMu.Unlock()
	if bytes < 0 || available < bytes || db.transientBytes[scope] > available-bytes {
		return nil, false
	}
	if db.transientBytes == nil {
		db.transientBytes = make(map[string]int64)
	}
	db.transientBytes[scope] += bytes
	return &TransientReservation{db: db, scope: scope, bytes: bytes}, true
}

// TransientBytes returns outstanding reservations for scope.
func (db *DB) TransientBytes(scope string) int64 {
	db.transientMu.Lock()
	defer db.transientMu.Unlock()
	return db.transientBytes[scope]
}

// Resize reconciles the reservation with its measured payload. Call inside
// DB.Write when available includes durable staging.
func (r *TransientReservation) Resize(bytes, available int64) bool {
	r.db.transientMu.Lock()
	defer r.db.transientMu.Unlock()
	if bytes < 0 || available < bytes || r.db.transientBytes[r.scope]-r.bytes > available-bytes {
		return false
	}
	r.db.transientBytes[r.scope] += bytes - r.bytes
	r.bytes = bytes
	return true
}

// Release is safe to call more than once.
func (r *TransientReservation) Release() {
	r.db.transientMu.Lock()
	defer r.db.transientMu.Unlock()
	r.db.transientBytes[r.scope] -= r.bytes
	r.bytes = 0
	if r.db.transientBytes[r.scope] == 0 {
		delete(r.db.transientBytes, r.scope)
	}
}
