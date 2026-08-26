package palace

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/chromemvec"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/qdrant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/sqlitevec"
)

// Every backend a deployment can actually select must be able to name itself,
// or am.vector_backend is emitted in tests and absent in production for exactly
// the deployments where it matters most. No behavioural test can see this: a
// service holding a store that DOES describe itself works perfectly, and the
// palace's own fixture uses sqlitevec.
var (
	_ VectorDescriber = (*sqlitevec.Store)(nil)
	_ VectorDescriber = (*qdrant.Client)(nil)
	_ VectorDescriber = (*chromemvec.Index)(nil)
	_ VectorDescriber = (*store.Hybrid)(nil)
)

// TestAHybridNamesBOTHHalves: the pair is the interesting fact. A Hybrid serves
// reads from the index while the source of truth holds every point, so "which
// backend answered" has two answers, and a trace naming one of them cannot be
// read against a divergence between them — which is the whole failure mode a
// hybrid deployment has and a single-store one does not.
func TestAHybridNamesBOTHHalves(t *testing.T) {
	got := store.NewHybrid(sqlitevec.New(nil), qdrant.New("http://x", "", 0)).DescribeVectorStore()
	if got != "hybrid(sqlitevec->qdrant)" {
		t.Errorf("DescribeVectorStore() = %q, want %q — a hybrid that names only one half "+
			"reports the same string whether or not its two stores agree", got, "hybrid(sqlitevec->qdrant)")
	}
}
