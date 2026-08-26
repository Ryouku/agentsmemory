package mcpserver

import "testing"

// TestOnlyASearchIDShapedValueReachesTheSpan: am.search_id is the one am.*
// string in the tree that a CLIENT supplies, and ADR-025 keeps query text off
// spans. A caller can put anything in this field, so the shape is checked before
// the value reaches a collector.
//
// The rejection is annotated rather than dropped in silence because ADR-028
// defers on "the first week a non-test client sends one": clients sending
// malformed ids must not read as no adoption at all.
func TestOnlyASearchIDShapedValueReachesTheSpan(t *testing.T) {
	for _, tc := range []struct {
		name string
		sid  string
		want bool
	}{
		{"what randomID actually mints, 24 hex", "0123456789abcdef01234567", true},
		{"the 16-hex shape older fixtures use", "cbbf4b5f8aacdf20", true},
		{"the clock fallback", "t1756192921000000000", true},
		{"a query pretending to be an id", "how do I configure the reranker", false},
		{"uppercase hex is not what randomID mints", "0123456789ABCDEF01234567", false},
		{"right alphabet, far too short to be an id", "0123abcd", false},
		{"a long string that would carry content", "t" + string(make([]rune, 40)), false},
		{"the bare fallback prefix", "t", false},
		{"hex-ish but with a stray character", "0123456789abcdef0123456g", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validSearchID(tc.sid); got != tc.want {
				t.Errorf("validSearchID(%q) = %v, want %v", tc.sid, got, tc.want)
			}
		})
	}
}
