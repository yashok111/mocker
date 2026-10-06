// Package backendblob stores immutable, domain-bound exact historical bytes.
// It depends only on database/sql and never on store or a domain repository.
package backendblob

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io"
)

type Domain struct{ Owner, Schema, RecordType string }
type Blob struct {
	Key    string
	Domain Domain
	Bytes  []byte
}

func field(h hash.Hash, b []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(b)))
	_, _ = h.Write(size[:])
	_, _ = h.Write(b)
}
func stringField(h hash.Hash, s string) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(s)))
	_, _ = h.Write(size[:])
	_, _ = io.WriteString(h, s)
}

// Key hashes length-prefixed raw bytes without canonicalizing JSON or numbers.
func Key(d Domain, raw []byte) string {
	h := sha256.New()
	for _, s := range []string{"backend-raw-blob-v1", d.Owner, d.Schema, d.RecordType} {
		stringField(h, s)
	}
	field(h, raw)
	return hex.EncodeToString(h.Sum(nil))
}
