package repohygiene

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// docCitedADRExemptions are the places a doc names a record NUMBER without
// pointing at a record, with the reason each one is not a broken pointer.
//
// ⚠ A MENTION IS NOT A POINTER, and this is the whole difficulty of gating prose.
// `TestEveryCitedADRResolves` needs no such list because Go source has no reason to
// discuss a number it is not referring to. Docs do: a Numbering line says which
// numbers are taken, and a record about the citation gate has to show what a
// failing citation looks like. Measured 2026-08-28: 1,219 ADR references across 234
// tracked docs, four unresolved, and ALL FOUR are mentions. A gate shipped without
// this list would have been 4/4 false alarms on day one — which is how a gate gets
// switched off, and this repository has already had one such incident.
//
// Keyed by file, valued by reason. TestDocCitedADRExemptionsAreJustified refuses an
// empty reason and an entry that no longer earns its place.
var docCitedADRExemptions = map[string]string{
	"docs/adr/ADR-026-a-history-you-cannot-query.md": "its Numbering line states which numbers an " +
		"open PR still claims — a statement about allocation, not a reference to a record",
	"docs/adr/ADR-037-the-why-travels-with-the-code.md": "it shows a deliberately unresolvable " +
		"record number as the failing example, in the record that introduced the citation gate: the " +
		"number has to resolve to nothing for the point to land",
	"docs/adr/ADR-037-the-why-travels-with-the-code/tasks/T1-every-cited-adr-resolves.md": "the same " +
		"unresolvable-number fixture, in that task's Risks table about the regex over-matching",
}

// TestEveryCitedADRResolvesInDocsToo extends the citation gate's universe from Go
// source to the tracked documentation corpus.
//
// ⚠ THE GO GATE'S UNIVERSE IS `.go` AND ONLY `.go` (`citation_test.go:179`), so a
// record renamed or withdrawn is caught where a doc comment cites it and missed
// where an ADR, a task file, the README or the backlog does — which is where most
// of this corpus's citations live: 1,219 across 234 docs against a few hundred in
// source. A pointer to nothing reads as provenance wherever it is written.
//
// This is a sibling rather than a widening of the existing gate on purpose:
// `AGENTS.md` describes that gate's universe, its Go-only scope is what makes it
// need no exemptions, and a gate whose name claims more than it covers is worse
// than a narrower one.
func TestEveryCitedADRResolvesInDocsToo(t *testing.T) {
	root := repoRoot(t)
	checkDocCitations(t, root, gitignoreMatcher(t, root), recordNumbers(t, root))
}

// checkDocCitations is the whole verdict path, taking a testing.TB so the
// falsifiability half below can substitute one. Pinning only a helper leaves the
// walk uncovered — the trap this package has now hit twice.
func checkDocCitations(tb testing.TB, root string, ignored func(string, bool) bool, records map[string]bool) {
	tb.Helper()
	var offenders []citation
	seen := map[string]bool{}
	total := 0
	for _, path := range walk(tb, root, ignored) {
		if !strings.HasSuffix(path, ".md") {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		if _, exempt := docCitedADRExemptions[filepath.ToSlash(rel)]; exempt {
			continue
		}
		src, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		found := citationsIn(filepath.ToSlash(rel), string(src))
		total += len(found)
		for _, c := range found {
			seen[c.number] = true
		}
		offenders = append(offenders, unresolved(found, records)...)
	}

	// A scan that found nothing to check is a gate that cannot fail. This corpus
	// carries over a thousand doc citations; zero means the walk or the pattern
	// broke, not that the docs went quiet.
	if total == 0 {
		tb.Fatal("no ADR-NNN citation found in any tracked .md file — this corpus carries " +
			"more than a thousand, so zero means the walk or the pattern broke and the gate is " +
			"passing vacuously")
	}

	for _, o := range offenders {
		tb.Errorf("%s:%d cites %s, and no record %s/%s-*.md exists.\n"+
			"  A citation is the only route from this prose to the reasoning behind it. Either the "+
			"record was renamed or withdrawn and the prose was left behind, or the number is a typo. "+
			"If the doc is NAMING the number rather than pointing at a record, add it to "+
			"docCitedADRExemptions with the reason.",
			o.file, o.line, o.number, adrCorpusDir, o.number)
	}
	if len(offenders) == 0 {
		tb.Logf("%d ADR citations across %d distinct records in the doc corpus, all resolved", total, len(seen))
	}
}

// TestDocCitedADRExemptionsAreJustified refuses an exemption without a written
// reason, and one that has stopped earning its place.
//
// The escape hatch is where a gate goes to die: an entry added to silence a finding,
// with no reason, outlives whatever justified it. The reason is the review.
func TestDocCitedADRExemptionsAreJustified(t *testing.T) {
	root := repoRoot(t)
	records := recordNumbers(t, root)
	for file, reason := range docCitedADRExemptions {
		if strings.TrimSpace(reason) == "" {
			t.Errorf("%s is exempt from the doc citation gate with no reason given", file)
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Errorf("%s is exempt but does not exist: %v\nRemove the entry.", file, err)
			continue
		}
		if len(unresolved(citationsIn(file, string(body)), records)) == 0 {
			t.Errorf("%s no longer cites an unresolved record, so its exemption is dead weight.\n"+
				"Remove it — an exemption nobody needs is one nobody re-reads.", file)
		}
	}
}

// selfCiteRE is built per file: a doc citing its OWN path with a line number.
func selfCiteRE(base string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(base) + `:(\d+)\b`)
}

// TestNoDocCitesItsOwnLineNumbers bans the one citation form this corpus has proved
// cannot survive its own file being edited.
//
// ⚠ AN INSERTION ABOVE A LINE NUMBER INVALIDATES IT, AND AN APPEND-HEAVY FILE GETS
// INSERTIONS CONSTANTLY. One backlog entry cited a sibling bullet in the same file
// and the citation drifted `:690` → `:716` → `:744` → `:763` across four review
// rounds — each correction wrong again by the next round, because the entry doing
// the citing was itself inserting lines above the target. A second instance sat in
// `ADR-038` pointing at `:665` for a receipt that had moved to `:778`, stale before
// anyone noticed and widened by every commit since.
//
// The fix is not a better line number, it is not writing one: cite the heading or
// quote the text. That survives the next insertion, which no number does.
//
// The corpus holds ZERO of these today — they were converted to quoted anchors
// while this gate was being written — so this is a gate against recurrence rather
// than a cleanup. That is the profile worth having: silent now, loud on the next
// one. It never asks whether a line number is RIGHT, only whether one was written
// at all, so it cannot cry wolf.
func TestNoDocCitesItsOwnLineNumbers(t *testing.T) {
	root := repoRoot(t)
	checkSelfCitations(t, root, gitignoreMatcher(t, root))
}

func checkSelfCitations(tb testing.TB, root string, ignored func(string, bool) bool) {
	tb.Helper()
	scanned, problems := 0, 0
	for _, path := range walk(tb, root, ignored) {
		if !strings.HasSuffix(path, ".md") {
			continue
		}
		scanned++
		body, err := os.ReadFile(path)
		if err != nil {
			tb.Fatalf("read %s: %v", path, err)
		}
		rel, _ := filepath.Rel(root, path)
		text := string(body)
		for _, m := range selfCiteRE(filepath.Base(path)).FindAllStringSubmatchIndex(text, -1) {
			problems++
			tb.Errorf("%s:%d cites its own file by line number (%s)\n"+
				"  A line number in the file doing the citing is invalidated by the next insertion "+
				"above it, and this corpus has watched one drift 690 → 716 → 744 → 763 across four "+
				"review rounds. Cite the heading, or quote the sentence — both survive an insert.",
				filepath.ToSlash(rel), countNewlines(text[:m[0]]), text[m[0]:m[1]])
		}
	}
	if scanned == 0 {
		tb.Fatal("scanned no .md files — the walk or the suffix filter broke, and a green run " +
			"here would mean nothing")
	}
	if problems == 0 {
		tb.Logf("%d tracked docs, none citing its own line numbers", scanned)
	}
}

// countNewlines reports the 1-based line a byte offset falls on.
func countNewlines(s string) int { return strings.Count(s, "\n") + 1 }

// TestADocCitingNoRecordIsReported is the falsifiability half for the doc citation
// gate, and TestADocCitingItsOwnLinesIsReported for the self-citation ban.
//
// Both corpora are clean, so neither gate's reporting branch is reachable from the
// real tree — the gates would pass identically with their bodies deleted. Each
// drives the REAL function over a fixture built to be wrong, through a substituted
// testing.TB, because a test cannot pin its own reporting. A half that
// reimplements the check instead of calling it pins nothing: this package has hit
// that twice, once in `citation_test.go` and once in `specbinding_test.go`.
func TestADocCitingNoRecordIsReported(t *testing.T) {
	root := t.TempDir()
	corpus := filepath.Join(root, adrCorpusDir)
	if err := os.MkdirAll(corpus, 0o755); err != nil {
		t.Fatal(err)
	}
	// One real record, so `records` is non-empty and the vacuity guard does not fire.
	if err := os.WriteFile(filepath.Join(corpus, "ADR-001-a-fixture.md"), []byte("# ADR-001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A real record number against a corpus that deliberately does not hold it — a
	// fixture naming a number nobody wrote would be flagged by the Go citation gate,
	// since this file is Go source like any other.
	doc := "# notes\n\nThis follows ADR-001, and supersedes ADR-002.\n"
	if err := os.WriteFile(filepath.Join(corpus, "notes.md"), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	none := func(string, bool) bool { return false }

	rec := &recordingTB{}
	checkDocCitations(rec, root, none, map[string]bool{"ADR-001": true})
	if rec.errors == 0 {
		t.Error("a doc citing a record the corpus does not hold was not reported.\n" +
			"Without this the gate above passes over a clean corpus whatever its body says.")
	}

	// The negative half, without which "reports" is satisfied by reporting always.
	clean := "# notes\n\nThis follows ADR-001 and nothing else.\n"
	if err := os.WriteFile(filepath.Join(corpus, "notes.md"), []byte(clean), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := &recordingTB{}
	checkDocCitations(ok, root, none, map[string]bool{"ADR-001": true})
	if ok.errors != 0 {
		t.Errorf("a doc citing only records that exist was reported anyway (%d error(s)) — "+
			"a gate that fires on everything is one people switch off", ok.errors)
	}
}

func TestADocCitingItsOwnLinesIsReported(t *testing.T) {
	root := t.TempDir()
	none := func(string, bool) bool { return false }

	// The exact shape that drifted four times: an entry pointing at a sibling
	// bullet in the file doing the pointing.
	bad := "# backlog\n\n- a finding\n- see the bullet at BACKLOG.md:3 for why\n"
	if err := os.WriteFile(filepath.Join(root, "BACKLOG.md"), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := &recordingTB{}
	checkSelfCitations(rec, root, none)
	if rec.errors == 0 {
		t.Error("a doc citing its own file by line number was not reported.\n" +
			"That citation is invalidated by the next insertion above it, which is how one " +
			"drifted 690 → 716 → 744 → 763 across four review rounds.")
	}

	// A line citation into ANOTHER file is legitimate and must stay quiet — the ban
	// is on self-reference, not on line numbers as such.
	good := "# backlog\n\n- a finding\n- see `internal/palace/repo.go:797` for why\n" +
		"- and the bullet under *\"A heading Somebody Wrote\"* for the rest\n"
	if err := os.WriteFile(filepath.Join(root, "BACKLOG.md"), []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	ok := &recordingTB{}
	checkSelfCitations(ok, root, none)
	if ok.errors != 0 {
		t.Errorf("a doc citing another file by line, and its own content by heading, was "+
			"reported anyway (%d error(s)) — this gate bans SELF-reference, and a gate that "+
			"fires on legitimate citations is one people switch off", ok.errors)
	}
}
