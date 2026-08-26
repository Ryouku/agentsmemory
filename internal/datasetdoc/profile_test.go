package datasetdoc

import (
	"strings"
	"testing"
)

// sample is a small dataset with every property the profile exists to report:
// an enumeration, an optional field, a date column, a high-cardinality id, and a
// field whose type is inconsistent across rows.
const sample = `{"id":"inv-1","status":"paid","amount":1200,"due":"2026-01-15","note":"first"}
{"id":"inv-2","status":"open","amount":90,"due":"2026-03-02","note":null}
{"id":"inv-3","status":"paid","amount":"450","due":"2026-02-20"}
`

func TestProfileMeasuresWhatTheFileActuallyContains(t *testing.T) {
	p, err := ProfileJSONL(strings.NewReader(sample))
	if err != nil {
		t.Fatalf("ProfileJSONL: %v", err)
	}
	if p.Rows != 3 || p.Malformed != 0 {
		t.Fatalf("rows=%d malformed=%d, want 3 and 0", p.Rows, p.Malformed)
	}

	byName := map[string]Field{}
	for _, f := range p.Fields {
		byName[f.Name] = f
	}

	// An enumeration is the fact most worth having: it says which of the twelve
	// documented statuses this data actually exercises.
	if got := byName["status"]; got.Distinct != 2 || strings.Join(got.Values, ",") != "open,paid" {
		t.Errorf("status: distinct=%d values=%v, want 2 and [open paid]", got.Distinct, got.Values)
	}

	// A type that is not consistent across rows is the thing that breaks the
	// loader downstream, so it must be visible rather than averaged away.
	if got := byName["amount"]; strings.Join(got.Types, ",") != "number,string" {
		t.Errorf("amount types=%v, want [number string] — a field encoded two ways is the "+
			"defect a profile exists to surface", got.Types)
	}

	// Optionality is Present against Rows, not a guess from the name.
	if got := byName["note"]; got.Present != 1 {
		t.Errorf("note present=%d of 3 rows, want 1 — null in one row and absent in another", got.Present)
	}

	// The date range on seed data is the question everyone asks and nobody records.
	got := byName["due"]
	if !strings.HasPrefix(got.Earliest, "2026-01-15") || !strings.HasPrefix(got.Latest, "2026-03-02") {
		t.Errorf("due range %s..%s, want 2026-01-15..2026-03-02", got.Earliest, got.Latest)
	}

	// And a field that is not a date must not acquire a range, or every id column
	// would grow a confident and meaningless one.
	if id := byName["id"]; id.Earliest != "" || id.Latest != "" {
		t.Errorf("id acquired a date range %s..%s from values that are not dates", id.Earliest, id.Latest)
	}
}

// TestProfileCountsMalformedRowsRatherThanFailing: one bad line must not deny a
// reader everything the other rows say — but it must not vanish either, because
// a file that is half malformed is a finding about the export that produced it.
func TestProfileCountsMalformedRowsRatherThanFailing(t *testing.T) {
	in := "{\"a\":1}\nnot json at all\n[1,2,3]\n\n{\"a\":2}\n"
	p, err := ProfileJSONL(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ProfileJSONL: %v", err)
	}
	if p.Rows != 2 {
		t.Errorf("rows=%d, want 2", p.Rows)
	}
	// The bare array is malformed FOR THIS PURPOSE: JSONL here means one object
	// per line, and a top-level array has no fields to profile.
	if p.Malformed != 2 {
		t.Errorf("malformed=%d, want 2 (a non-JSON line and a non-object line)", p.Malformed)
	}
}

// TestHighCardinalityFieldsReportNoValueSet: past the cap a value list would be
// DATA rather than schema, and the profile's job is schema. The cap must also be
// visible in the count, so a reader can tell "25 distinct" from "at least 26".
func TestHighCardinalityFieldsReportNoValueSet(t *testing.T) {
	var b strings.Builder
	for i := range maxDistinct + 5 {
		b.WriteString("{\"id\":\"row-")
		b.WriteString(strings.Repeat("x", i%3))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(itoa(i))
		b.WriteString("\"}\n")
	}
	p, err := ProfileJSONL(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("ProfileJSONL: %v", err)
	}
	f := p.Fields[0]
	if f.Values != nil {
		t.Errorf("a %d-value field reported a value set of %d entries; past the cap the list is "+
			"row data, not schema", f.Distinct, len(f.Values))
	}
	if f.Distinct != maxDistinct+1 {
		t.Errorf("distinct=%d, want %d — the capped count must say 'more than the cap' rather "+
			"than an exact number nobody counted", f.Distinct, maxDistinct+1)
	}
}

// TestNumericValueSetsSortNumerically: lexical order on numbers is actively
// misleading — "1200, 450, 90" reads as unsorted noise and hides the range a
// reader came for. Found in the real output, not in a unit test, which is why
// the round trip was worth running.
//
// Enough values to catch a parallel-array sort, where a []float64 built before
// sorting is indexed by the ORIGINAL positions while sort permutes the strings
// underneath it: with three values that bug can still produce sorted output by
// luck, so this uses a set where it cannot.
func TestNumericValueSetsSortNumerically(t *testing.T) {
	in := "{\"n\":90}\n{\"n\":1200}\n{\"n\":450}\n{\"n\":7}\n{\"n\":33}\n{\"n\":8000}\n"
	p, err := ProfileJSONL(strings.NewReader(in))
	if err != nil {
		t.Fatalf("ProfileJSONL: %v", err)
	}
	if got := strings.Join(p.Fields[0].Values, ","); got != "7,33,90,450,1200,8000" {
		t.Errorf("numeric values sorted as %q, want 7,33,90,450,1200,8000", got)
	}

	// A mixed field must fall back to lexical rather than silently dropping the
	// non-numeric values or panicking on them.
	mixed, err := ProfileJSONL(strings.NewReader("{\"n\":10}\n{\"n\":\"n/a\"}\n{\"n\":2}\n"))
	if err != nil {
		t.Fatalf("ProfileJSONL: %v", err)
	}
	if got := len(mixed.Fields[0].Values); got != 3 {
		t.Errorf("mixed field kept %d of 3 values", got)
	}
}

// TestFieldOrderIsStable: the same data must produce the same text on every run,
// or a re-import that changes nothing would replace every drawer and look like a
// change. JSON object key order is not stable across encoders, so ordering by
// observation would not survive a different exporter.
func TestFieldOrderIsStable(t *testing.T) {
	forward := "{\"a\":1,\"b\":2,\"c\":3}\n"
	reverse := "{\"c\":3,\"b\":2,\"a\":1}\n"
	p1, err := ProfileJSONL(strings.NewReader(forward))
	if err != nil {
		t.Fatalf("ProfileJSONL: %v", err)
	}
	p2, err := ProfileJSONL(strings.NewReader(reverse))
	if err != nil {
		t.Fatalf("ProfileJSONL: %v", err)
	}
	var n1, n2 []string
	for _, f := range p1.Fields {
		n1 = append(n1, f.Name)
	}
	for _, f := range p2.Fields {
		n2 = append(n2, f.Name)
	}
	if strings.Join(n1, ",") != strings.Join(n2, ",") {
		t.Errorf("key order changed the report: %v vs %v", n1, n2)
	}
}

// itoa avoids importing strconv into a test that needs one integer rendered.
func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
