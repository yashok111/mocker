package backendblob

import (
	"bytes"
	"context"
	"database/sql"
	_ "modernc.org/sqlite"
	"testing"
)

func fixture(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	if _, err = db.Exec(Schema); err != nil {
		t.Fatal(err)
	}
	return db
}
func TestRawBlobDomainAndBytes(t *testing.T) {
	d := Domain{"graph", "6", "node"}
	raw := []byte(`{"n":9007199254740993}`)
	// Independent Python hashlib + struct.pack('>Q') witness, not a codec round trip.
	if got := Key(d, raw); got != "4cfda54966fac03cd0425c653cad7e6211b7808bb45394b3405b612abd796aea" {
		t.Fatalf("key = %s", got)
	}
	for _, other := range []Domain{{"other", "6", "node"}, {"graph", "5", "node"}, {"graph", "6", "edge"}} {
		if Key(d, raw) == Key(other, raw) {
			t.Fatal("domain collapsed")
		}
	}
	if Key(d, raw) == Key(d, []byte(`{ "n":9007199254740993}`)) {
		t.Fatal("lost raw identity")
	}
}
func TestPutDeduplicatesAndRejectsCorruption(t *testing.T) {
	db := fixture(t)
	ctx := t.Context()
	d := Domain{"graph", "6", "node"}
	raw := []byte(" {\"n\":9007199254740993}\n")
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	key, err := Put(ctx, tx, d, raw)
	if err != nil {
		t.Fatal(err)
	}
	again, err := Put(ctx, tx, d, raw)
	if err != nil || again != key {
		t.Fatalf("dedup: %s %v", again, err)
	}
	got, err := Get(ctx, tx, key)
	if err != nil || !bytes.Equal(got, raw) {
		t.Fatalf("bytes: %q %v", got, err)
	}
	var n int
	if err = tx.QueryRow("SELECT count(*) FROM backend_payload_blobs").Scan(&n); err != nil || n != 1 {
		t.Fatalf("count %d %v", n, err)
	}
	if _, err = tx.Exec("UPDATE backend_payload_blobs SET payload=x'00'"); err == nil {
		t.Fatal("mutable blob")
	}
	if _, err = tx.Exec("DELETE FROM backend_payload_blobs"); err == nil {
		t.Fatal("deletable blob")
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = tx.Exec("INSERT INTO backend_payload_blobs VALUES(?,?,?,?,?,?)", key, d.Owner, d.Schema, d.RecordType, 2, []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err = Put(ctx, tx, d, raw); err == nil {
		t.Fatal("corruption accepted")
	}
	if _, err = Get(context.Background(), tx, key); err == nil {
		t.Fatal("corrupt read accepted")
	}
}
