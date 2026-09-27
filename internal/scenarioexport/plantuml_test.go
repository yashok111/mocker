package scenarioexport

import (
	"context"
	"encoding/xml"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestPlantUMLRendererAcceptsBlankLabels(t *testing.T) {
	plantuml, err := exec.LookPath("plantuml")
	if err != nil {
		t.Skip("install PlantUML to run renderer compatibility checks")
	}
	for _, field := range []string{"title", "participant", "both"} {
		t.Run(field, func(t *testing.T) {
			rev := revisionFixture()
			if field != "participant" {
				rev.Document.Title = ""
			}
			if field != "title" {
				rev.Document.Participants[0].Name = ""
			}
			artifact, err := New(nil, 1<<20).Export(rev, Request{Format: PlantUML})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, plantuml, "-charset", "UTF-8", "-tsvg", "-pipe", "-failfast2")
			cmd.Env = append(os.Environ(), "PLANTUML_SECURITY_PROFILE=SANDBOX")
			cmd.Stdin = strings.NewReader(artifact.Content)
			svg, err := cmd.Output()
			if err != nil {
				t.Fatalf("PlantUML rejected %s: %v\n%s", field, err, svg)
			}
			var result struct{ XMLName xml.Name }
			if err := xml.Unmarshal(svg, &result); err != nil || result.XMLName.Local != "svg" {
				t.Fatalf("invalid SVG: %v", err)
			}
		})
	}
}
