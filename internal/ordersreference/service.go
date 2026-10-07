// Package ordersreference implements the trusted, isolated Orders fixture.
package ordersreference

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"uuid"

	_ "modernc.org/sqlite"

	o "github.com/yashok111/mocker/internal/backendobservations"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

//go:embed schema.sql
var schema string

// Build is supplied by the build tool, never by an HTTP request.
type Build struct{ Variant, ServiceVersion, SourceTreeHash, BuildHash string }
type Config struct {
	Addr, DBPath, Token, IsolationID, TargetID string
	ConfigVersion                              int64
}

func ConfigFromEnv() (Config, error) {
	c := Config{Addr: os.Getenv("ORDERS_REFERENCE_ADDR"), DBPath: os.Getenv("ORDERS_REFERENCE_DB"), Token: os.Getenv("ORDERS_REFERENCE_TOKEN"), IsolationID: os.Getenv("ORDERS_REFERENCE_ISOLATION_ID"), TargetID: os.Getenv("ORDERS_REFERENCE_TARGET_ID")}
	var err error
	c.ConfigVersion, err = strconv.ParseInt(os.Getenv("ORDERS_REFERENCE_CONFIG_VERSION"), 10, 64)
	if err != nil {
		return c, fmt.Errorf("invalid config version")
	}
	host, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return c, fmt.Errorf("loopback address required")
	}
	ip := net.ParseIP(host)
	n, e := strconv.Atoi(port)
	if ip == nil || !ip.IsLoopback() || e != nil || n < 1 || n > 65535 {
		return c, fmt.Errorf("loopback literal and port required")
	}
	return c, c.validate()
}
func (c Config) validate() error {
	if !filepath.IsAbs(c.DBPath) || len(c.Token) < 32 || strings.TrimSpace(c.Token) != c.Token || !p.ValidID(c.IsolationID) || c.TargetID == "" || c.ConfigVersion < 1 {
		return fmt.Errorf("invalid Orders configuration")
	}
	return nil
}

type Service struct {
	observations         map[string][]o.Record
	observationTruncated map[string]bool
	mu                   sync.Mutex
	db                   *sql.DB
	config               Config
	identity             p.Identity
	identityHash         string
	lock                 *os.File
}

// Open uses a dedicated DB and a lifetime exclusive file lock. It never opens Mocker's store.
func Open(c Config, b Build) (*Service, error) {
	if err := c.validate(); err != nil {
		return nil, err
	}
	id := p.Identity{Protocol: p.Version, Service: "orders-reference", ServiceVersion: b.ServiceVersion, TestOnly: true, IsolationID: c.IsolationID, InstanceID: uuid.New().String(), Variant: b.Variant, BuildHash: b.BuildHash, SourceTreeHash: b.SourceTreeHash, SourcePolicy: p.ManifestPolicy, FixtureHash: p.FixtureHash()}
	hash, err := p.IdentityHash(id)
	if err != nil {
		return nil, err
	}
	lock, err := lockDB(c.DBPath)
	if err != nil {
		return nil, err
	}
	cleanup := func() { unlockDB(lock) }
	// Refuse symlinks and non-regular files; do not chmod an unrelated existing database.
	info, err := os.Lstat(c.DBPath)
	if err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0) {
		cleanup()
		return nil, fmt.Errorf("database must be a private regular file")
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanup()
		return nil, err
	}
	f, err := os.OpenFile(c.DBPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		cleanup()
		return nil, err
	}
	if err = f.Close(); err != nil {
		cleanup()
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: c.DBPath}
	q := u.Query()
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(3000)")
	q.Add("_pragma", "synchronous(FULL)")
	u.RawQuery = q.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		cleanup()
		return nil, err
	}
	db.SetMaxOpenConns(1)
	s := &Service{observations: map[string][]o.Record{}, observationTruncated: map[string]bool{}, db: db, config: c, identity: id, identityHash: hash, lock: lock}
	// Open runs before any request exists; its setup has no caller context.
	ctx := context.Background()
	fail := func(e error) (*Service, error) { _ = s.Close(); return nil, e }
	// Reject foreign databases before installing the private schema.
	var count int
	if err = db.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count); err != nil {
		return fail(err)
	}
	if count > 0 {
		var isolation string
		if err = db.QueryRowContext(ctx, "SELECT isolation FROM metadata WHERE singleton=1").Scan(&isolation); err != nil || isolation != c.IsolationID {
			return fail(fmt.Errorf("database isolation mismatch"))
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fail(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err = tx.ExecContext(ctx, schema); err != nil {
		_ = tx.Rollback()
		return fail(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO metadata VALUES(1,?,0)", c.IsolationID); err != nil {
		_ = tx.Rollback()
		return fail(err)
	}
	if err = tx.Commit(); err != nil {
		return fail(err)
	}
	return s, nil
}
func (s *Service) Close() error { err := s.db.Close(); unlockDB(s.lock); return err }

type protocolError struct {
	status int
	code   string
}

func (e *protocolError) Error() string { return e.code }
func conflict(code string) error       { return &protocolError{409, code} }

func encode(v any) ([]byte, error) { return p.Encode(v) }
func insertDocument(ctx context.Context, tx *sql.Tx, query string, v any, args ...any) error {
	b, err := encode(v)
	if err != nil {
		return err
	}
	args = append(args, b)
	_, err = tx.ExecContext(ctx, query, args...)
	return err
}
func epoch(ctx context.Context, tx *sql.Tx) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx, "SELECT epoch FROM metadata WHERE singleton=1").Scan(&n)
	return n, err
}

func (s *Service) journal(ctx context.Context, run string) (p.Journal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return p.Journal{}, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := readJournal(ctx, tx, run)
	if err != nil {
		return j, err
	}
	return j, tx.Commit()
}
func readJournal(ctx context.Context, tx *sql.Tx, run string) (p.Journal, error) {
	j := p.Journal{RunID: run, FixtureHash: p.FixtureHash(), Complete: true, PendingKeys: []string{}, Receipts: []p.Receipt{}, Events: []p.Event{}, Orders: []p.OrderRecord{}, Charges: []p.ChargeRecord{}}
	var raw []byte
	err := tx.QueryRowContext(ctx, "SELECT epoch,identity FROM runs WHERE id=?", run).Scan(&j.Epoch, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return j, &protocolError{404, "not_found"}
	}
	if err != nil {
		return j, err
	}
	if err = p.Decode(raw, &j.Identity, p.BodyLimit); err != nil {
		return j, err
	}
	j.IdentityHash, err = p.IdentityHash(j.Identity)
	if err != nil {
		return j, err
	}
	j.CurrentEpoch, err = epoch(ctx, tx)
	if err != nil {
		return j, err
	}
	if err = readDocuments(ctx, tx, "SELECT receipt FROM requests WHERE run_id=? ORDER BY rowid", run, &j.Receipts); err != nil {
		return j, err
	}
	if err = readDocuments(ctx, tx, "SELECT document FROM events WHERE run_id=? ORDER BY sequence", run, &j.Events); err != nil {
		return j, err
	}
	if err = readDocuments(ctx, tx, "SELECT document FROM orders WHERE run_id=? ORDER BY rowid", run, &j.Orders); err != nil {
		return j, err
	}
	if err = readDocuments(ctx, tx, "SELECT document FROM charges WHERE run_id=? ORDER BY rowid", run, &j.Charges); err != nil {
		return j, err
	}
	if len(j.Events) > 0 {
		j.HighWater = j.Events[len(j.Events)-1].Sequence
	}
	return j, j.Validate()
}
func readDocuments[T any](ctx context.Context, tx *sql.Tx, query, run string, out *[]T) error {
	rows, err := tx.QueryContext(ctx, query, run)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var raw []byte
		if err = rows.Scan(&raw); err != nil {
			return err
		}
		var v T
		if err = p.Decode(raw, &v, p.BodyLimit); err != nil {
			return err
		}
		*out = append(*out, v)
	}
	return rows.Err()
}
