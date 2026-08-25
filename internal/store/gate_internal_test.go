package store

import (
	"context"
	"sync"
	"testing"
	"time"
)

// internalSoT and internalIndex are minimal fakes for the gate's UNEXPORTED
// machinery (afterWrite, countPairFor, the cached pair). The public-behaviour
// tests live in hybrid_gate_test.go with richer fakes; these exist so the
// cap/single-flight behaviour can be asserted from inside the package.
type internalSoT struct {
	mu         sync.Mutex
	count      int
	countHook  func() // called at the top of Count, without the lock
	countCalls int
}

func (f *internalSoT) EnsureNamespace(context.Context, string, int) error { return nil }
func (f *internalSoT) Upsert(context.Context, string, []Point) error      { return nil }
func (f *internalSoT) Search(context.Context, string, []float32, int, Filter) (SearchResult, error) {
	return SearchResult{}, nil
}
func (f *internalSoT) Count(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	f.countCalls++
	hook := f.countHook
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.count, nil
}
func (f *internalSoT) Delete(context.Context, string, []string) error { return nil }
func (f *internalSoT) PointsByIDs(context.Context, string, []string) ([]Point, error) {
	return nil, nil
}
func (f *internalSoT) SetPayload(context.Context, string, []string, map[string]string) error {
	return nil
}
func (f *internalSoT) AllPoints(context.Context, string) ([]Point, error) { return nil, nil }
func (f *internalSoT) Namespaces(context.Context) ([]string, error)       { return nil, nil }

type internalIndex struct {
	mu          sync.Mutex
	count       int
	exactCalls  int
	approxCalls int
}

func (f *internalIndex) EnsureNamespace(context.Context, string, int) error { return nil }
func (f *internalIndex) Upsert(context.Context, string, []Point) error      { return nil }
func (f *internalIndex) Search(context.Context, string, []float32, int, Filter) (SearchResult, error) {
	return SearchResult{}, nil
}
func (f *internalIndex) Count(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exactCalls++
	return f.count, nil
}
func (f *internalIndex) ApproximateCount(_ context.Context, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approxCalls++
	return f.count, nil
}
func (f *internalIndex) Delete(context.Context, string, []string) error { return nil }
func (f *internalIndex) PointsByIDs(context.Context, string, []string) ([]Point, error) {
	return nil, nil
}
func (f *internalIndex) SetPayload(context.Context, string, []string, map[string]string) error {
	return nil
}
func (f *internalIndex) AllPoints(context.Context, string) ([]Point, error) { return nil, nil }
func (f *internalIndex) Namespaces(context.Context) ([]string, error)       { return nil, nil }

// TestAfterWriteHonorsTheExactCountCap: the write path must not pay the exact
// index count above the cap that the read path was engineered to avoid. The
// watermark needs a population, not a precise one — an approximate read above
// the cap is the same currency the read path spends.
func TestAfterWriteHonorsTheExactCountCap(t *testing.T) {
	sot := &internalSoT{count: ExactCountCap + 1}
	idx := &internalIndex{count: ExactCountCap + 1}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: ExactCountCap + 1, indexed: ExactCountCap + 1, at: time.Now()}
	h.gate.mu.Unlock()

	h.afterWrite(context.Background(), "ns", true)

	if idx.exactCalls != 0 {
		t.Errorf("afterWrite read an exact index count %d time(s) above the cap; want 0", idx.exactCalls)
	}
	if idx.approxCalls == 0 {
		t.Error("afterWrite never used the approximate count above the cap")
	}
}

// TestAfterWriteCountsExactlyBelowTheCap: below the cap the write path keeps
// the exact count — the approximate path only exists to dodge a cost that does
// not exist at that size.
func TestAfterWriteCountsExactlyBelowTheCap(t *testing.T) {
	sot := &internalSoT{count: 2}
	idx := &internalIndex{count: 2}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: 2, indexed: 2, at: time.Now()}
	h.gate.mu.Unlock()

	h.afterWrite(context.Background(), "ns", true)

	if idx.approxCalls != 0 {
		t.Errorf("afterWrite used the approximate count %d time(s) below the cap; want 0", idx.approxCalls)
	}
	if idx.exactCalls == 0 {
		t.Error("afterWrite never read the exact index count below the cap")
	}
}

// TestCountPairRefreshIsSingleFlight: N concurrent queries on an expired pair
// must issue one count refresh, not N — the count pair is exactly the cost the
// cache exists to amortize, and a stampede at TTL expiry would pay it once per
// query.
func TestCountPairRefreshIsSingleFlight(t *testing.T) {
	sot := &internalSoT{count: 1}
	idx := &internalIndex{count: 1}
	h := NewHybridWithConfig(sot, idx, DefaultGateConfig())
	ttl := DefaultGateConfig().CountTTL
	h.gate.mu.Lock()
	h.gate.pair["ns"] = countPair{expected: 1, indexed: 1, at: time.Now().Add(-2 * ttl)}
	h.gate.mu.Unlock()

	// The leader's truth count blocks until release; the second goroutine must
	// be waiting on the in-flight refresh, not issuing its own count.
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	sot.countHook = func() {
		once.Do(func() { close(entered) })
		<-release
	}

	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = h.countPairFor(context.Background(), "ns")
		}(i)
	}
	<-entered      // the leader is inside the truth count
	close(release) // let it finish; the second goroutine is already waiting
	wg.Wait()
	if sot.countCalls != 1 {
		t.Errorf("concurrent refreshes ran the truth count %d time(s); want 1", sot.countCalls)
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d: %v", i, err)
		}
	}
}
