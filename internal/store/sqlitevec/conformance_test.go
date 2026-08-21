package sqlitevec

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest"
)

// TestSQLiteVecRunsTheConformanceSuite runs the shared suite against the real
// migrated schema, which is what the source of truth actually stores into.
func TestSQLiteVecRunsTheConformanceSuite(t *testing.T) {
	storetest.RunPointsConformance(t, "sqlitevec", func(t *testing.T) store.VectorStore {
		return newTestStore(t)
	})
}
