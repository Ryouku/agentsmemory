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
	db, err := chromem.NewPersistentDB(dir, false)
	if err != nil {
		return nil, fmt.Errorf("open chromem db at %q: %w", dir, err)
	}
	return &Index{db: db}, nil
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
		docs = append(docs, chromem.Document{
			ID:        p.ID,
			Metadata:  map[string]string{payloadKey: string(payload)},
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
func (i *Index) Search(ctx context.Context, namespace string, vector []float32, k int) ([]store.Hit, error) {
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
	res, err := col.QueryEmbedding(ctx, vector, k, nil, nil)
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
