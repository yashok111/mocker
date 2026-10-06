package backendmodel

import (
	"encoding/json/v2"
	"strings"
	"testing"
)

func TestFoundationNamespaceHashAndCollision(t *testing.T) {
	pin := ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
	local := ArtifactNamespace{Scope: "local", InstallationID: "11111111-1111-4111-8111-111111111111"}
	foreign := ArtifactNamespace{Scope: "foreign", InstallationID: local.InstallationID}
	c := ArtifactContextV3{DocumentVersion: ArtifactContextV3Version, SourceContentHash: strings.Repeat("b", 64), SourceSemanticHash: strings.Repeat("c", 64), Groups: []ArtifactNamespaceGroup{{Namespace: local, Pins: []ArtifactPin{pin}, APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}}}}
	a, err := ArtifactContextV3Hash(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Groups[0].Namespace = foreign
	b, err := ArtifactContextV3Hash(c)
	if err != nil || a == b {
		t.Fatal("namespace missing from hash", err)
	}
	if err := foreign.CheckLocal(local.InstallationID); err == nil {
		t.Fatal("foreign numeric collision admitted")
	}
	if err := local.CheckLocal("22222222-2222-4222-8222-222222222222"); err == nil {
		t.Fatal("wrong installation admitted")
	}
	if err := local.CheckLocal(local.InstallationID); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeArtifactContextV3(c)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := DecodeVersionedArtifactContext(raw, nil)
	if err != nil || decoded.V3 == nil || decoded.Legacy != nil {
		t.Fatal(decoded, err)
	}
	for _, raw := range []string{strings.Replace(string(raw), `"scope":"foreign"`, `"scope":"foreign","extra":1`, 1), strings.Replace(string(raw), ArtifactContextV3Version, "future", 1), strings.Replace(string(raw), `"groups":[`, `"groups":null,"other":[`, 1)} {
		if _, err := DecodeVersionedArtifactContext([]byte(raw), nil); err == nil {
			t.Fatal("open wire", raw)
		}
	}
	c.Groups = append(c.Groups, c.Groups[0])
	if _, err := EncodeArtifactContextV3(c); err == nil {
		t.Fatal("duplicate namespace")
	}
}

func TestFoundationV2BytesUnchanged(t *testing.T) {
	f := newEditorProjectionFixture(t)
	raw, err := EncodeArtifactContext(f.context, f.state.Revision.ArtifactPins)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeVersionedArtifactContext(raw, f.state.Revision.ArtifactPins)
	if err != nil || got.Legacy == nil || got.V3 != nil {
		t.Fatal(got, err)
	}
	after, err := EncodeArtifactContext(*got.Legacy, f.state.Revision.ArtifactPins)
	if err != nil || string(raw) != string(after) {
		t.Fatal("v2 bytes changed", err)
	}
	var value map[string]any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
}

func TestFoundationV3CanonicalAndLocalGate(t *testing.T) {
	a := ArtifactNamespaceGroup{Namespace: ArtifactNamespace{Scope: "foreign", InstallationID: "11111111-1111-4111-8111-111111111111"}, Pins: []ArtifactPin{{Kind: "api_design", ID: "1", RevisionID: "2", ContentHash: strings.Repeat("a", 64)}}}
	b := a
	b.Namespace.InstallationID = "22222222-2222-4222-8222-222222222222"
	c := ArtifactContextV3{DocumentVersion: ArtifactContextV3Version, SourceContentHash: strings.Repeat("b", 64), SourceSemanticHash: strings.Repeat("c", 64), Groups: []ArtifactNamespaceGroup{a, b}}
	h, err := ArtifactContextV3Hash(c)
	if err != nil {
		t.Fatal(err)
	}
	c.Groups = []ArtifactNamespaceGroup{b, a}
	other, err := ArtifactContextV3Hash(c)
	if err != nil || h != other {
		t.Fatal("group order changes identity", err)
	}
	pin := NamespacedArtifactPin{Namespace: a.Namespace, Pin: a.Pins[0]}
	if _, err := pin.LocalPin(a.Namespace.InstallationID); err == nil {
		t.Fatal("foreign owner lookup admitted")
	}
	pin.Namespace.Scope = "local"
	got, err := pin.LocalPin(a.Namespace.InstallationID)
	if err != nil || got != a.Pins[0] {
		t.Fatal(got, err)
	}
	pin.Pin.ContentHash = ""
	if _, err := pin.LocalPin(a.Namespace.InstallationID); err == nil {
		t.Fatal("unpinned lookup")
	}
}

func TestFoundationV3RejectsReusedIncompletePin(t *testing.T) {
	p := NamespacedArtifactPin{Namespace: ArtifactNamespace{Scope: "local", InstallationID: "11111111-1111-4111-8111-111111111111"}, Pin: ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "2", ContentHash: strings.Repeat("a", 64)}}
	raw := []byte(`{"namespace":{"scope":"local","installationId":"11111111-1111-4111-8111-111111111111"},"pin":{"kind":"api_design","id":"1"}}`)
	if err := json.Unmarshal(raw, &p); err == nil {
		t.Fatal("inherited exact pin from reused decoder receiver")
	}
}
