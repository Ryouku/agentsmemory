// Package chromemvec implements the store.VectorStore search index on top of
// chromem-go, an embedded pure-Go vector database that persists to a directory.
//
// It is the "no second service" index. Qdrant answers searches over the network
// and needs its own container; letting SQLite answer them (the sqlite backend)
// means reading and decoding every stored blob per query. chromem sits between
// the two: it runs inside this process, keeps the vectors in memory, and writes
// them beside the SQLite file — so a self-hosted install is one binary and one
// directory, which is what --local and the Docker stack default to.
//
// SQLite remains the source of truth (internal/store/sqlitevec). This index is
// derived and disposable: deleting its directory costs only a Rebuild from the
// stored vectors, never a re-embedding.
package chromemvec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	chromem "github.com/philippgille/chromem-go"
)

// compile-time proof the driver satisfies the seam store.Hybrid drives.
var _ store.VectorStore = (*Index)(nil)

// payloadKey holds a point's payload, JSON-encoded, inside chromem's document
// metadata. chromem metadata is map[string]string while the seam's payload is
// map[string]any, so the whole payload travels as one JSON string and is decoded
// back on Search — the driver round-trips it verbatim, as the seam promises.
const payloadKey = "_payload"

// schemaVersion identifies the metadata layout this driver writes. Bumping it
// makes New discard an index written by an older layout, which is safe precisely
// because this index is derived: the vectors are durable in SQLite and the boot
// reconcile replays them without re-embedding.
//
// v2 added the flat filter keys below; a v1 directory has payloads only, so a
// filtered search against it would match nothing and silently return an empty
// page — worse than the rebuild it now triggers instead.
const schemaVersion = "2"

// schemaFile records schemaVersion inside the index directory.
const schemaFile = ".schema"

// errPrecomputed rejects any attempt by chromem to embed text itself. Every
// vector reaching this index was produced by our Ollama embedder and is already
// durable in SQLite, so a call here would mean a document arrived without its
// embedding — worth surfacing as an error rather than silently embedding it with
// a different model and corrupting the index's vector space.
var errPrecomputed = errors.New("chromemvec: index stores precomputed embeddings only")

// Index is a chromem-go database used as the search index for one agentsmemory
// process. Namespaces (tenant IDs) map one-to-one onto chromem collections, each
// persisted in its own subdirectory of the database directory.
//
// It is safe for concurrent use: chromem guards its own maps, and mu closes the
// one gap that leaves (see collection).
type Index struct {
	db *chromem.DB

	// mu serializes get-or-create. chromem locks the lookup and the creation
	// separately, so two goroutines touching a brand-new namespace at once can
	// both create a collection and have the second overwrite the first — losing
	// whatever the first had already written.
	mu sync.Mutex
}

// New opens the chromem database in dir, creating the directory if it is absent
// and loading any collections already persisted there back into memory.
//
// Compression is off: chromem gzips each document file when it is on, which
// trades boot and write time for disk on data that is mostly incompressible
// float32 vectors. A personal palace is small enough that the space is not worth
// the CPU.
func New(dir string) (*Index, error) {
	if err := ensureSchema(dir); err != nil {
		return nil, err
	}
	db, err := chromem.NewPersistentDB(dir, false)
	if err != nil {
		return nil, fmt.Errorf("open chromem db at %q: %w", dir, err)
	}
	return &Index{db: db}, nil
}

// ensureSchema discards an index directory written by an older metadata layout
// and stamps the current one, so what is on disk always matches what this driver
// expects to read. Discarding is the right move rather than migrating in place:
// the index holds nothing the SQLite source of truth does not, so a rebuild costs
// a replay of stored vectors and no embedding at all — while a stale layout costs
// silently wrong search results.
func ensureSchema(dir string) error {
	stamp := filepath.Join(dir, schemaFile)
	switch current, err := os.ReadFile(stamp); {
	case err == nil && string(current) == schemaVersion:
		return nil // already the layout we write
	case err != nil && !os.IsNotExist(err):
		return fmt.Errorf("read chromem schema stamp %q: %w", stamp, err)
	}
	// Either no stamp (a pre-v2 directory, or a fresh one) or a different
	// version: start clean. RemoveAll on a missing directory is a no-op, so the
	// fresh-install path costs one syscall and no special case.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("discard stale chromem index at %q: %w", dir, err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create chromem index dir %q: %w", dir, err)
	}
	if err := os.WriteFile(stamp, []byte(schemaVersion), 0o644); err != nil {
		return fmt.Errorf("write chromem schema stamp %q: %w", stamp, err)
	}
	return nil
}

// collection returns the namespace's collection, creating it on first use.
func (i *Index) collection(namespace string) (*chromem.Collection, error) {
	i.mu.Lock()
	defer i.mu.Unlock()
	col, err := i.db.GetOrCreateCollection(namespace, nil, func(context.Context, string) ([]float32, error) {
		return nil, errPrecomputed
	})
	if err != nil {
		return nil, fmt.Errorf("chromem collection %q: %w", namespace, err)
	}
	return col, nil
}

// EnsureNamespace creates the namespace's collection if it is absent.
//
// dim is ignored: chromem takes the dimension from the vectors it is handed
// rather than being told a collection's width up front the way Qdrant is. A
// vector of the wrong length fails at query time, when it is actually compared.
func (i *Index) EnsureNamespace(_ context.Context, namespace string, _ int) error {
	_, err := i.collection(namespace)
	return err
}

// Upsert writes points into the namespace's collection, keyed by Point.ID so a
// repeated write replaces rather than duplicates.
func (i *Index) Upsert(ctx context.Context, namespace string, points []store.Point) error {
	if len(points) == 0 {
		return nil
	}
	col, err := i.collection(namespace)
	if err != nil {
		return err
	}
	docs := make([]chromem.Document, 0, len(points))
	for _, p := range points {
		payload, err := json.Marshal(p.Payload)
		if err != nil {
			return fmt.Errorf("encode payload of %q: %w", p.ID, err)
		}
		// The payload travels twice, for two different jobs: once as JSON so it
		// round-trips verbatim (values are map[string]any, chromem metadata is
		// map[string]string), and once flattened into string keys so chromem's
		// `where` can filter on them server-side. Only string values flatten —
		// anything else has no meaningful equality match here, and the blob still
		// carries it.
		meta := map[string]string{payloadKey: string(payload)}
		for k, v := range p.Payload {
			if str, ok := v.(string); ok && k != payloadKey {
				meta[k] = str
			}
		}
		docs = append(docs, chromem.Document{
			ID:        p.ID,
			Metadata:  meta,
			Embedding: p.Vector,
			// Content stays empty on purpose: the drawer text lives in SQLite,
			// and a second copy here would double the index's memory for no
			// query benefit — searches are vector-only, and callers read what
			// they need from the payload.
		})
	}
	// chromem persists one file per document, so spreading the batch over the
	// CPUs turns a bulk replay (Rebuild) from a serial write loop into a
	// parallel one.
	if err := col.AddDocuments(ctx, docs, runtime.NumCPU()); err != nil {
		return fmt.Errorf("chromem upsert %d point(s) into %q: %w", len(points), namespace, err)
	}
	return nil
}

// Search returns up to k nearest neighbours by cosine similarity, closest first.
func (i *Index) Search(ctx context.Context, namespace string, vector []float32, k int, filter store.Filter) ([]store.Hit, error) {
	if k <= 0 {
		return nil, nil
	}
	col, err := i.collection(namespace)
	if err != nil {
		return nil, err
	}
	// chromem rejects a query asking for more results than the collection holds,
	// while the seam promises that fewer than k hits simply means there were
	// fewer points. Clamping keeps that promise; an empty collection returns
	// early because chromem also rejects a zero result count.
	n := col.Count()
	if n == 0 {
		return nil, nil
	}
	if k > n {
		k = n
	}
	// chromem compares k against the collection's TOTAL size, not the filtered
	// subset, so the clamp above is still the right one — a filter simply yields
	// fewer than k hits, which the seam already allows.
	res, err := col.QueryEmbedding(ctx, vector, k, filter, nil)
	if err != nil {
		return nil, fmt.Errorf("chromem search %q: %w", namespace, err)
	}
	hits := make([]store.Hit, 0, len(res))
	for _, r := range res {
		var payload map[string]any
		if raw := r.Metadata[payloadKey]; raw != "" {
			if err := json.Unmarshal([]byte(raw), &payload); err != nil {
				return nil, fmt.Errorf("decode payload of %q: %w", r.ID, err)
			}
		}
		// Similarity is cosine in [-1, 1] — chromem normalizes both stored and
		// query vectors — which is exactly what store.Hit.Score means.
		hits = append(hits, store.Hit{ID: r.ID, Score: r.Similarity, Payload: payload})
	}
	return hits, nil
}

// Delete removes points by ID, ignoring IDs the namespace does not hold.
func (i *Index) Delete(ctx context.Context, namespace string, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	col, err := i.collection(namespace)
	if err != nil {
		return err
	}
	if err := col.Delete(ctx, nil, nil, ids...); err != nil {
		return fmt.Errorf("chromem delete from %q: %w", namespace, err)
	}
	return nil
}

// Count reports how many points the namespace's index holds.
//
// It exists for the boot-time reconcile in cmd/server: an index that is empty
// while the source of truth is not is the state a fresh chromem directory is in
// after an existing install switches to this backend, and the fix is to replay
// SQLite into it.
func (i *Index) Count(namespace string) (int, error) {
	col, err := i.collection(namespace)
	if err != nil {
		return 0, err
	}
	return col.Count(), nil
}
