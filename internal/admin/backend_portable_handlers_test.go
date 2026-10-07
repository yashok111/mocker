package admin

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendportable"
)

func TestBackendPortableSVGAttachment(t *testing.T) {
	w := httptest.NewRecorder()
	a := &backendportable.SVGArtifact{Filename: "backend-view-example-v2.svg", ContentType: "image/svg+xml", Body: []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)}
	writeBackendSVG(w, a)
	if !strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Type") != "image/svg+xml" || w.Body.String() != string(a.Body) {
		t.Fatal(w.Header(), w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Content-Security-Policy"), "default-src 'none'") {
		t.Fatal("missing restrictive CSP")
	}
}
