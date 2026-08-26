package repohygiene

import "testing"

// TestADR036FixturesCarryNoPrivatePalaceContent is ADR-036 T1's privacy gate.
//
// ADR-003 T2 carries a PERMANENT boundary: "Committing case files or full
// results JSON to the repo — they carry queries and drawer ids from a private
// palace; the cells file is the redacted record the evidence directory holds."
//
// ADR-036's first draft proposed committing a frozen case set whose questions
// came from real search_events rows, with gold triple ids, plus a real client
// transcript. That was written while FIXING an unrelated finding — a hermetic
// gate over a real-data requirement — and it would have walked straight through
// a boundary an accepted ADR had already closed. Nothing sweeps a `permanent`,
// so nothing would have resurfaced it.
//
// This test is the mechanical version of that boundary: tracked ADR-036 fixture
// files may carry counts, hashes, provenance and a tokenizer name, and must
// carry no case text, no drawer ids and no triple ids.
func TestADR036FixturesCarryNoPrivatePalaceContent(t *testing.T) {
	t.Fatal("ADR-036 T1 not implemented: tracked testdata fixtures carry only redacted aggregates — counts, hashes, provenance, tokenizer — and no case text, drawer id or triple id")
}
