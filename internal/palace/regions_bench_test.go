package palace

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkSnippetRegionsCoverage measures the coverage selector on a long
// memory — the S3 path. Coverage runs up to maxRegions rounds over every
// candidate window, so the cost of anything recomputed per round is multiplied
// by the round count.
//
//	go test ./internal/palace/ -run '^$' -bench SnippetRegionsCoverage -benchmem
func BenchmarkSnippetRegionsCoverage(b *testing.B) {
	var body strings.Builder
	for i := 0; body.Len() < 100_000; i++ {
		fmt.Fprintf(&body, "line %d: the rerank pool is read from the environment and the compose "+
			"file advertised a value nobody read, while the widening loop asks the index again. ", i)
	}
	content := body.String()
	const query = "rerank pool environment compose widening index"

	b.Run("coverage-on", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = snippetRegions(content, query, ChunkSize, maxMemoryEvidenceRegions, true)
		}
	})
	b.Run("coverage-off", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = snippetRegions(content, query, ChunkSize, maxMemoryEvidenceRegions, false)
		}
	})
}
