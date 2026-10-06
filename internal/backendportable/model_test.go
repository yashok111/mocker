package backendportable

import (
	"encoding/json/jsontext"
	"testing"
)

func TestPortableEnvelopeHashPathAndQuotas(t *testing.T) {
	for _, p := range []string{"/etc/passwd", "../x", "a/./b", `C:/x`, `a\b`, "a\x00b", "//server/x", "a//b"} {
		if ValidatePath(p) == nil {
			t.Errorf("accepted %q", p)
		}
	}
	if err := ValidatePath("src/main.go"); err != nil {
		t.Fatal(err)
	}
	h, err := DocumentHash(jsontext.Value(`{"z":9007199254740993,"a":{"y":2,"x":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	h2, err := DocumentHash(jsontext.Value(`{"a":{"x":1,"y":2},"z":9007199254740993}`))
	if err != nil || h != h2 {
		t.Fatal("canonical hash", err)
	}
	if _, err := DocumentHash(jsontext.Value(`{"x":1,"x":2}`)); err == nil {
		t.Fatal("duplicate key")
	}
	r := Record{Kind: "diagram_version", Identity: Identity{InstallationID: pid, ProjectID: pid, Kind: "diagram_version", ID: did, Version: "3"}, Document: jsontext.Value(`{"test":true}`)}
	r.ContentHash, _ = DocumentHash(r.Document)
	body, desc, err := EncodeChunk(0, []Record{r})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeChunk(desc, body); err != nil {
		t.Fatal(err)
	}
	body[len(body)-2] = '!'
	if _, err = DecodeChunk(desc, body); err == nil {
		t.Fatal("tamper accepted")
	}
	rs := make([]Record, 501)
	if _, _, err = EncodeChunk(0, rs); err == nil {
		t.Fatal("quota accepted")
	}
	r.Identity.Version = "03"
	if err = r.Validate(); err == nil {
		t.Fatal("noncanonical version")
	}
}

func TestPortableMemberIdentityIsDiagramScoped(t *testing.T) {
	parent := Identity{pid, pid, "diagram", did, "0"}
	makeRecord := func(parent Identity) Record {
		raw, _ := canonical(struct {
			Parent Identity `json:"parent"`
		}{parent})
		r := Record{Kind: "diagram_element", Identity: Identity{pid, pid, "diagram_element", nid, "0"}, Document: raw}
		r.ContentHash, _ = DocumentHash(r.Document)
		return r
	}
	a := makeRecord(parent)
	parent.ID = vid
	b := makeRecord(parent)
	body, c, err := EncodeChunk(0, []Record{a, b})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeChunk(c, body); err != nil {
		t.Fatal("same member UUID across fork diagrams must remain distinct", err)
	}
	body, c, err = EncodeChunk(0, []Record{a, a})
	if err == nil {
		_, err = DecodeChunk(c, body)
	}
	if err == nil {
		t.Fatal("duplicate same-parent member")
	}
}
