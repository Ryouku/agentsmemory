// Package datasetdoc turns a project's JSONL datasets into memories the palace
// can recall: a measured profile of each file, joined to the explanation a person
// wrote beside it, emitted in the bundle format the importer already accepts.
//
// The split between those two halves is the whole design. What can be MEASURED
// is measured on every run and therefore cannot drift from the data — field
// names, types, row counts, the values a small field actually takes. What cannot
// be measured is the part worth recalling: what the dataset is for, why it looks
// like this, what a field means that its name does not say. That half is written
// by hand and carried verbatim.
//
// Rows themselves stay out. They are already in whatever database the same JSONL
// builds, which answers questions about rows better than a vector search will,
// and filing tens of thousands of them would push every other memory in the wing
// further down its own recall.
//
// And the values inside those rows stay out unless the mapping file NAMES the
// field they came from. A profile is filed into a wing every agent recalls from,
// so quoting a column nobody asked it to quote — an email, a phone number, a
// hostname — publishes that column to every future session. Counting a column is
// a fact about the data; quoting it is the data.
package datasetdoc

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// maxLineBytes bounds one JSONL record. It is generous because a seed row with
// an embedded document is ordinary, and a line over it is REPORTED as malformed
// rather than truncated: a silently shortened row would be profiled as if the
// data really ended there, which is a wrong answer wearing a right one's clothes.
const maxLineBytes = 8 << 20 // 8 MiB

// maxDistinct is how many values a field may take before it stops being
// reportable as a value set. Past this the list would be data rather than
// schema, and the profile's job is schema.
//
// It is a constant rather than a knob because it is no longer what decides how
// much data escapes: a field is quoted only when the mapping file names it, so
// this bounds a disclosure someone already chose to make. As a threshold on its
// own it would be the whole control, and 25 would be a number to argue about.
const maxDistinct = 25

// maxValueBytes bounds ONE value inside a reported set. maxDistinct bounds how
// MANY values a field may contribute and says nothing about their size, so a
// named field holding a long blob in each of twenty-five rows would carry the
// whole column into a drawer — and an embedder truncates at its own limit, which
// would leave everything after that value stored and unfindable.
//
// 256 bytes is chosen to hold the values a value set is for — codes, statuses,
// currencies, short names — and to cut the ones it is not.
const maxValueBytes = 256

// Profile is what one JSONL file says about itself when nobody is asked.
//
// Every field is derived from the file on the run that produced it, so a Profile
// is a snapshot with a date rather than a standing claim. It describes what was
// in THIS file: a value set here is what the file contained, never a constraint
// the domain guarantees.
type Profile struct {
	// Rows is how many lines parsed as JSON objects.
	Rows int
	// Malformed is how many lines did not — unparseable, too long, or not an
	// object. Reported rather than skipped silently, because a file that is half
	// malformed is a finding about the export that produced it.
	Malformed int
	// Fields are the keys observed, in a stable order so two runs over unchanged
	// data produce identical text and a re-import is a no-op.
	Fields []Field
	// Withheld is how many fields were counted but not quoted, because the
	// mapping file did not name them. Reported so the memory can say that values
	// are missing by choice — an unexplained absence reads as "this field has no
	// interesting values", which is the opposite of what it means.
	Withheld int
	// Truncated reports that reading stopped before the end of the file, because
	// a line exceeded maxLineBytes and a bufio.Scanner cannot resume past a token
	// it could not hold. Keeping the rows already read is the right call — the
	// alternative is returning nothing because row 40,000 was oversized — but
	// nothing about those counts says they are partial, so "one bad line among
	// 100,000" and "we never saw rows 11 onwards" both surface as Malformed: 1.
	// This is the difference, and the memory prints it.
	Truncated bool
}

// Field is one key observed across the file, with what was seen of it.
type Field struct {
	// Name is the JSON key.
	Name string
	// Types are the distinct JSON types observed, sorted. More than one is worth
	// seeing: it usually means an optional field encoded as null in some rows and
	// a string in others, which is exactly what breaks a loader downstream.
	Types []string
	// Present is how many rows carried this key with a non-null value. Compared
	// against Profile.Rows it says whether the field is optional in practice.
	Present int
	// Values is the sorted value set, and is non-nil only when the mapping file
	// named this field AND it took at most maxDistinct distinct values.
	//
	// Naming is required because a threshold cannot tell an enumeration from a
	// small population: `status` and `country` and `manager_email` all look like
	// twelve distinct strings from here, and only the person who wrote the
	// dataset knows which of them may be quoted into a wing everyone recalls.
	Values []string
	// Distinct is how many distinct values were seen, capped: once the count
	// passes maxDistinct it is reported as maxDistinct+1 and Values is dropped,
	// because counting further would mean holding the column in memory. It is 0
	// and meaningless when Compound is set.
	Distinct int
	// Compound marks a field that held an object or an array. Such a field has no
	// value set and no distinct count worth reporting — it is not a column of
	// values — and saying "more than 25 distinct values" about it, which is what
	// sharing the overflow flag used to do, is a false statement about the data
	// rather than a missing one.
	Compound bool
	// Earliest and Latest bound the field when every observed value parsed as a
	// date or timestamp; both are empty otherwise. A date range is the fact most
	// often wanted about seed data and the one nobody records.
	Earliest string
	Latest   string
}

// dateLayouts are the forms a value must match to be treated as a date. Kept
// deliberately short: guessing at ambiguous numeric formats would produce a
// confident range from a field that is not a date at all.
var dateLayouts = []string{time.RFC3339, "2006-01-02", "2006-01-02 15:04:05"}

// fieldStat accumulates one field while the file streams past.
type fieldStat struct {
	types     map[string]bool
	present   int
	values    map[string]bool
	overflow  bool // more than maxDistinct distinct values seen
	compound  bool // held an object or an array, so there is no value set at all
	allDates  bool
	anyDate   bool
	earliest  time.Time
	latest    time.Time
	firstSeen int // source order, so the report reads like the file
}

// ProfileJSONL reads JSONL from r and reports what it contains.
//
// showValues names the fields whose values may be quoted in the result. Every
// field is measured the same either way — types, presence, distinct count, date
// range — but a field nobody named has its values counted and then dropped, so a
// Profile cannot hand a caller a column it was not asked to disclose. The filter
// is here rather than at rendering time for exactly that reason: a value that is
// never carried out cannot be printed by the next caller who forgets.
//
// It streams, so file size costs memory in one row rather than in the whole
// file. A read error is returned; malformed LINES are counted into the Profile
// instead, because one bad row should not deny a reader everything the other
// thousands say.
func ProfileJSONL(r io.Reader, showValues []string) (Profile, error) {
	quotable := make(map[string]bool, len(showValues))
	for _, name := range showValues {
		quotable[name] = true
	}

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), maxLineBytes)

	var p Profile
	stats := map[string]*fieldStat{}
	order := 0

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var row map[string]any
		// A bare `null` decodes into a NIL map without an error: it is valid JSON
		// and not a row, so counting it as one would inflate every "present in N of
		// M rows" reading below it. Arrays, numbers and strings fail the decode and
		// land here too.
		if err := json.Unmarshal([]byte(line), &row); err != nil || row == nil {
			p.Malformed++
			continue
		}
		p.Rows++
		for k, v := range row {
			st := stats[k]
			if st == nil {
				st = &fieldStat{types: map[string]bool{}, values: map[string]bool{}, allDates: true, firstSeen: order}
				stats[k] = st
				order++
			}
			observe(st, v)
		}
	}
	if err := sc.Err(); err != nil {
		// A line over the cap surfaces here as bufio.ErrTooLong. Report it as a
		// malformed line and keep what was already read: the alternative is
		// returning nothing at all because row 40,000 was oversized.
		if !strings.Contains(err.Error(), "token too long") {
			return Profile{}, fmt.Errorf("read jsonl: %w", err)
		}
		p.Malformed++
		// Everything after that line is unread, and a row count that stops at 10
		// looks exactly like a file with 10 rows. Say so rather than let the
		// profile pass off a prefix as the whole.
		p.Truncated = true
	}

	p.Fields, p.Withheld = summarise(stats, quotable)
	return p, nil
}

// observe folds one value into a field's running statistics.
func observe(st *fieldStat, v any) {
	st.types[jsonType(v)] = true
	if v == nil {
		st.allDates = false
		return
	}
	st.present++

	s, ok := scalarString(v)
	if !ok {
		// Objects and arrays have no useful value set or date reading; recording
		// their rendered form would put row data in a schema report. Reported
		// through a second return rather than an empty string, because "" is also a
		// perfectly good string value — sharing the sentinel made a column of empty
		// strings indistinguishable from a column of arrays.
		st.allDates = false
		st.compound = true
		st.values = nil
		return
	}
	if !st.overflow {
		// Clipped on the way IN. maxDistinct bounds how many values are held, and
		// nothing bounded how big one of them was, so twenty-five multi-megabyte
		// cells were twenty-five multi-megabyte cells — held in memory, written
		// into a drawer, and then silently cut off by the embedder's own limit
		// halfway through, leaving every field after them stored and unfindable.
		st.values[clip(s)] = true
		if len(st.values) > maxDistinct {
			st.overflow = true
			st.values = nil
		}
	}
	if st.allDates {
		if t, ok := parseDate(s); ok {
			st.anyDate = true
			if st.earliest.IsZero() || t.Before(st.earliest) {
				st.earliest = t
			}
			if t.After(st.latest) {
				st.latest = t
			}
		} else {
			st.allDates = false
		}
	}
}

// summarise turns the accumulated stats into the reported, sorted Fields, and
// reports how many fields were counted without being quoted.
//
// This is the ONE place a measured value becomes a reported one, which is why
// the allowlist is applied here and nowhere else: a disclosure with a single
// gate is a disclosure a test can hold shut.
func summarise(stats map[string]*fieldStat, quotable map[string]bool) ([]Field, int) {
	out := make([]Field, 0, len(stats))
	withheld := 0
	for name, st := range stats {
		f := Field{Name: name, Present: st.present}
		for t := range st.types {
			f.Types = append(f.Types, t)
		}
		sort.Strings(f.Types)
		switch {
		case st.compound:
			// No value set, no count: the interior of a nested value is out of scope
			// for this profile, so there is nothing here to disclose or to withhold.
			f.Compound = true
		case st.overflow:
			f.Distinct = maxDistinct + 1
		default:
			f.Distinct = len(st.values)
			if quotable[name] {
				for v := range st.values {
					f.Values = append(f.Values, v)
				}
				sortValues(f.Values)
			} else {
				withheld++
			}
		}
		if st.allDates && st.anyDate {
			f.Earliest = st.earliest.Format(time.RFC3339)
			f.Latest = st.latest.Format(time.RFC3339)
		}
		out = append(out, f)
	}
	// Sorted by name, not by first appearance: JSON object key order is not
	// stable across encoders, so ordering by observation would make the same data
	// produce different text on different exports — and a re-import that changes
	// nothing must be a no-op.
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, withheld
}

// sortValues orders a value set for reading: numerically when every value is a
// number, lexically otherwise.
//
// Lexical order on numbers is actively misleading — a set printed as
// "1200, 450, 90" reads as unsorted noise and hides the range a reader is
// looking for. Sorting is still total and deterministic either way, which is
// what keeps a re-import over unchanged data a no-op.
func sortValues(vs []string) {
	for _, v := range vs {
		if _, err := strconv.ParseFloat(v, 64); err != nil {
			sort.Strings(vs)
			return
		}
	}
	// Parsed inside the comparator, against the live slice. A parallel []float64
	// built beforehand would be indexed by the ORIGINAL positions while sort
	// permutes vs underneath it, and the comparison would read another value's
	// number — an ordering bug that still produces plausible-looking output.
	sort.Slice(vs, func(i, j int) bool {
		a, _ := strconv.ParseFloat(vs[i], 64)
		b, _ := strconv.ParseFloat(vs[j], 64)
		return a < b
	})
}

// jsonType names the JSON type of a decoded value, using JSON's own vocabulary
// rather than Go's — a reader of the profile is looking at JSON.
func jsonType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// scalarString renders a scalar for value-set and date purposes. The second
// return is false for anything compound — an object or an array — and is what
// distinguishes those from a genuine empty string.
func scalarString(v any) (string, bool) {
	switch t := v.(type) {
	case bool:
		if t {
			return "true", true
		}
		return "false", true
	case float64:
		// %v keeps integers integral rather than rendering 1 as 1e+00, which is
		// what a reader of an id or a minor-unit column expects to see.
		return fmt.Sprintf("%v", t), true
	case string:
		return t, true
	default:
		return "", false
	}
}

// clip shortens one value to maxValueBytes on a rune boundary, marking that it
// was shortened.
//
// The mark matters as much as the cap: an unmarked cut reads as the value the
// column really holds, and a reader would take a truncated URL or identifier for
// the whole of it. Cutting on a rune boundary keeps the memory valid UTF-8,
// which a byte slice of a multi-byte character would not.
func clip(s string) string {
	if len(s) <= maxValueBytes {
		return s
	}
	cut := maxValueBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…(clipped)"
}

// parseDate reports whether s is one of the accepted date forms.
func parseDate(s string) (time.Time, bool) {
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
