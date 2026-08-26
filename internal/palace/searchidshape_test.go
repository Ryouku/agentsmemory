package palace

import (
	"strings"
	"testing"
)

// TestEveryMintedSearchIDIsAcceptedByItsOwnValidator ties the minter to the
// validator, which nothing did before.
//
// A review on 2026-08-26 demonstrated the gap by mutation rather than by
// reading: changing randomID to emit strings.ToUpper(hex...) compiles and leaves
// every package green, while every freshly minted search_id then fails
// validation, is annotated am.search_id_rejected, and ADR-028's deferral trigger
// — "the first week am_get_drawer receives a non-empty search_id from a client
// that is not a test" — reads as "no client ever sent one" at the exact moment
// every client is sending one. The validator's own comment named that hazard for
// the LENGTH half and nothing covered the alphabet or encoding half.
//
// Many iterations because randomID is random: a single sample can pass while a
// subset of outputs does not.
func TestEveryMintedSearchIDIsAcceptedByItsOwnValidator(t *testing.T) {
	for i := 0; i < 2000; i++ {
		id := randomID()
		if !ValidSearchID(id) {
			t.Fatalf("randomID() minted %q, which its own ValidSearchID rejects.\n"+
				"A rejected id is not counted as adoption, so this reads as "+
				"'no client ever sent one' while every client is sending one.", id)
		}
	}
}

// TestTheClockFallbackShapeIsAccepted covers randomID's other branch. It is
// unreachable in a test — it fires only when the entropy source fails — so the
// shape is asserted directly, and this test is the reason the "t"+digits arm of
// ValidSearchID exists at all.
func TestTheClockFallbackShapeIsAccepted(t *testing.T) {
	// The exact form randomID builds: fmt.Sprintf("t%d", time.Now().UnixNano()).
	for _, id := range []string{"t1", "t1756192921000000000", "t" + strings.Repeat("9", 19)} {
		if !ValidSearchID(id) {
			t.Errorf("ValidSearchID(%q) = false; randomID's entropy-failure fallback would be rejected", id)
		}
	}
}
