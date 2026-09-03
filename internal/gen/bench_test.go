package gen_test

import (
	"net/url"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/gen"
)

// BenchmarkBody_fullCorpus is the number CLAUDE.md quotes ("generating all
// 419 bodies") made repeatable: every operation's primary variant of the
// acceptance document, one Generator, one seed. Run with
// `go test ./internal/gen -run xxx -bench Body -benchmem`.
func BenchmarkBody_fullCorpus(b *testing.B) {
	fx := loadAcceptance(b)
	g := gen.New(fx.resolver, gen.Options{Seed: 42})
	type job struct {
		v   gen.ResponseVariant
		req gen.Request
	}
	var jobs []job
	for _, op := range fx.ops {
		v, ok := pickPrimaryVariant(fx.variants[op.ID])
		if !ok {
			continue
		}
		jobs = append(jobs, job{v, gen.Request{
			Method: strings.ToUpper(op.Method), CanonicalPath: op.CanonicalPath,
			PathParams: samplePathParams(op.Path), Query: url.Values{}, Status: v.HTTPStatus,
		}})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		for _, j := range jobs {
			if _, err := g.Body(j.v, j.req); err != nil {
				b.Fatal(err)
			}
		}
	}
}
