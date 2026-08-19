// Package qdrant is a thin REST client for the Qdrant vector store. It is the
// only vector backend agentsmemory ships (Ollama + Qdrant from day one — no
// Chroma, no local ONNX), so there is no pluggable backend registry like the
// Python tool had; just this concrete client behind a small interface defined
// at its consumers.
//
// Tenancy is physical: each team gets its own collection (decided 2026-06-26),
// named deterministically from the team id. That keeps one team's vectors in a
// separate Qdrant collection from every other team's — a missing query filter
// can never leak across the boundary because the data is not even colocated.
package qdrant

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// CollectionName returns the deterministic Qdrant collection name for a team.
//
// Format: "mempalace_<sha256(teamID)[:16]>_drawers". The 16-hex-char team hash
// mirrors the Python palace-hash scheme (sha256(palace.id)[:16]) so the naming
// is familiar, opaque, and URL-safe. It is a pure function so it can be unit
// tested and called anywhere without a client.
func CollectionName(teamID string) string {
	sum := sha256.Sum256([]byte(teamID))
	return "mempalace_" + hex.EncodeToString(sum[:])[:16] + "_drawers"
}

// Client is a minimal Qdrant REST client built on net/http (no SDK, matching
// the Python urllib client). It is safe for concurrent use.
type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

// New constructs a Client for the given Qdrant base URL. apiKey may be empty for
// an unauthenticated local Qdrant.
func New(baseURL, apiKey string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		http:    &http.Client{Timeout: timeout},
	}
}

// EnsureCollection creates the team's collection if it does not already exist,
// configured for cosine distance at the embedder's dimension (1024 for bge-m3).
// It is idempotent: an existing collection of the right shape is left alone.
func (c *Client) EnsureCollection(ctx context.Context, teamID string, dim int) error {
	name := CollectionName(teamID)

	// A HEAD-equivalent GET tells us whether the collection already exists.
	exists, err := c.collectionExists(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		body := map[string]any{
			"vectors": map[string]any{"size": dim, "distance": "Cosine"},
		}
		if err := c.do(ctx, http.MethodPut, "/collections/"+name, body, nil); err != nil {
			return err
		}
	}
	// Indexes are ensured even on an existing collection, so a palace created
	// before this existed gets them on the next boot rather than staying slow
	// forever with nothing to indicate why.
	return c.ensurePayloadIndexes(ctx, name)
}

// filterKeys are the payload fields searches filter on. They mirror the payload
// written at upsert time (internal/palace writes {wing, room}).
var filterKeys = []string{"wing", "room"}

// ensurePayloadIndexes creates a keyword index for each filterable payload key.
//
// Without one, Qdrant can still answer a filtered search — by scanning. The
// filter is applied to points as they are visited rather than used to narrow
// what gets visited, so the cost grows with the COLLECTION rather than with the
// matching subset. That is invisible on a small palace and is exactly the shape
// of problem that only shows up once someone depends on it: per-project wings
// mean every search is filtered, so this is the difference between a filter that
// helps and one that quietly costs.
//
// Creating an index that already exists is a no-op for Qdrant, so this runs on
// every boot without a guard.
func (c *Client) ensurePayloadIndexes(ctx context.Context, collection string) error {
	for _, key := range filterKeys {
		body := map[string]any{"field_name": key, "field_schema": "keyword"}
		if err := c.do(ctx, http.MethodPut, "/collections/"+collection+"/index?wait=true", body, nil); err != nil {
			return fmt.Errorf("ensure payload index %q on %s: %w", key, collection, err)
		}
	}
	return nil
}

// DeleteCollection drops a team's collection entirely — points and config. A
// collection that does not exist is treated as already-deleted, not an error, so
// the call is idempotent. Used by `sync --recreate` to rebuild a tenant from a
// clean slate, pruning points that no longer exist in the source of truth.
func (c *Client) DeleteCollection(ctx context.Context, teamID string) error {
	name := CollectionName(teamID)
	req, err := c.newRequest(ctx, http.MethodDelete, "/collections/"+name, nil)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNotFound:
		return nil
	default:
		return fmt.Errorf("qdrant: unexpected status %d deleting collection", resp.StatusCode)
	}
}

// collectionExists reports whether a collection is present, treating 404 as a
// clean "no" rather than an error.
func (c *Client) collectionExists(ctx context.Context, name string) (bool, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/collections/"+name, nil)
	if err != nil {
		return false, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body) // drain so the connection can be reused
	switch {
	case resp.StatusCode == http.StatusOK:
		return true, nil
	case resp.StatusCode == http.StatusNotFound:
		return false, nil
	default:
		return false, fmt.Errorf("qdrant: unexpected status %d checking collection", resp.StatusCode)
	}
}

// newRequest builds a JSON request with the optional api-key header set.
func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("api-key", c.apiKey)
	}
	return req, nil
}

// do performs a JSON request, decoding the response into out when non-nil. A
// non-2xx status is turned into an error carrying the response body for
// diagnosis.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var rdr io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(raw)
	}
	req, err := c.newRequest(ctx, method, path, rdr)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("qdrant: %s %s -> %d: %s", method, path, resp.StatusCode, string(data))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}
