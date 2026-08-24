package palace

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// countingEmbedder records what the service asked it to embed, so a test can
// assert not only that an oversized update was refused but that the refusal
// happened BEFORE the embedder was called. A version that embeds first and
// checks afterwards passes a refusal test while still paying the cost — and,
// against a real server, still receiving the truncated vector the bound exists
// to prevent.
type countingEmbedder struct {
	fakeEmbedder
	oneCalls int
	longest  int
}

func (c *countingEmbedder) EmbedOne(ctx context.Context, input string) ([]float32, error) {
	c.oneCalls++
	if n := len([]rune(input)); n > c.longest {
		c.longest = n
	}
	return c.fakeEmbedder.EmbedOne(ctx, input)
}

// TestUpdateRefusesContentTheEmbedderWouldSilentlyTruncate is the gate for the
// invariant teiembed.go states in prose: nothing reaches the embedder as one
// piece above what the model can represent.
//
// It was prose, and a live path violated it. ChunkText bounds Add; Update
// re-embeds a whole memory with EmbedOne and never chunks, so a memory created
// small and grown in place was the one unbounded input — and because the client
// asks for truncation rather than an error, an oversized update returned a
// vector for the prefix and reported success. The memory still read back whole
// from am_get_drawer while being unfindable past the cut.
func TestUpdateRefusesContentTheEmbedderWouldSilentlyTruncate(t *testing.T) {
	emb := &countingEmbedder{}
	svc := newTestServiceWith(t, emb)
	ctx := context.Background()

	const team = "team-1"
	added, err := svc.Add(ctx, team, AddInput{
		Wing: "wing_a", Room: "decisions", SourceFile: "seed", Content: "the original short memory",
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if len(added.Drawers) != 1 {
		t.Fatalf("seed produced %d chunks, want 1 — this test needs a single-chunk memory", len(added.Drawers))
	}
	id := added.Drawers[0].ID

	// Add embeds through the BATCH path, so EmbedOne is untouched so far and any
	// call counted below is one this update made.
	if emb.oneCalls != 0 {
		t.Fatalf("Add called EmbedOne %d times; the baseline for this test is 0", emb.oneCalls)
	}

	oversized := strings.Repeat("x", MaxEmbedRunes+1)
	if _, err := svc.Update(ctx, team, id, DrawerPatch{Content: &oversized}); err == nil {
		t.Fatal("an update larger than the embedder can hold in one piece was accepted; " +
			"the text past the model's window would be stored and never findable")
	} else if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("refusal must be ErrInvalidInput so the MCP layer reports it as a bad request, got %v", err)
	} else {
		// The caller cannot act on "too long" without both numbers and a way out.
		for _, want := range []string{"4001", "4000", "add_drawer"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("refusal does not mention %q, so the caller cannot tell what to do: %v", want, err)
			}
		}
	}

	if emb.oneCalls != 0 {
		t.Errorf("the embedder was called %d time(s) for content that was then refused — "+
			"the check must come BEFORE the embed, or the truncated vector has already been fetched",
			emb.oneCalls)
	}

	// A refused update must leave the memory exactly as it was: this path writes
	// the vector before the row, so a half-applied refusal is a real possibility.
	unchanged, err := svc.Get(ctx, team, id)
	if err != nil {
		t.Fatalf("get after refusal: %v", err)
	}
	if unchanged.Content != "the original short memory" {
		t.Errorf("a refused update changed the drawer: %q", unchanged.Content)
	}

	// The boundary belongs in the test, not in a reader's head: exactly the limit
	// is allowed, so the comparison is > and not >=.
	atLimit := strings.Repeat("y", MaxEmbedRunes)
	if _, err := svc.Update(ctx, team, id, DrawerPatch{Content: &atLimit}); err != nil {
		t.Fatalf("content of exactly MaxEmbedRunes was refused, so the bound is off by one: %v", err)
	}
	if emb.longest != MaxEmbedRunes {
		t.Errorf("longest string handed to the embedder was %d, want exactly the limit %d",
			emb.longest, MaxEmbedRunes)
	}
}

// TestMaxEmbedRunesIsSmallerThanTheSmallestBackend pins the reasoning rather than
// the number. The bound was first written from bge-m3's 8192-token window, which
// is the LARGEST backend; agentsmemory also embeds through ollama, whose models
// commonly run a far smaller context, and an operator chooses at deploy time. A
// limit only the roomiest backend satisfies does not bound anything — it moves
// the silent truncation to whoever configured the other one.
//
// So this asserts headroom, not equality: raising MaxEmbedRunes towards a
// specific model's window should have to argue with a failing test first.
func TestMaxEmbedRunesIsSmallerThanTheSmallestBackend(t *testing.T) {
	// A deliberately conservative floor on what any supported embedding model
	// holds, expressed in characters because the palace cannot ask a tokenizer
	// and the chars-per-token ratio is script-dependent.
	const smallestSupportedWindowRunes = 4096

	if MaxEmbedRunes > smallestSupportedWindowRunes {
		t.Fatalf("MaxEmbedRunes is %d, above the %d this repo is willing to assume of every "+
			"configurable backend. Raising it needs evidence about ollama, not only about bge-m3",
			MaxEmbedRunes, smallestSupportedWindowRunes)
	}
	if MaxEmbedRunes <= ChunkSize {
		t.Fatalf("MaxEmbedRunes (%d) is at or below ChunkSize (%d), which would make every "+
			"multi-chunk-sized live document unupdatable and defeat the point of the bound",
			MaxEmbedRunes, ChunkSize)
	}
}
