package palace

import (
	"strings"
	"testing"
)

// has reports whether entities contains name.
func has(entities []string, name string) bool {
	for _, e := range entities {
		if e == name {
			return true
		}
	}
	return false
}

// extractsAsEntity reports whether tok, repeated so it clears the frequency
// floor, comes back from the extractor.
func extractsAsEntity(tok string) bool {
	return has(extractEntities(tok+" zzz "+tok+" zzz"), tok)
}

// shoutedProse is ordinary English an agent writes in a memory. None of it names
// anything, and all of it used to become an entity.
//
// The list is not decoration: measured 2026-08-21 for ADR-016 T2, 47 of these
// survived the extractor in ALL CAPS and 46 survived in Title Case, so a live
// palace's most frequent "entities" were words somebody had shouted. Every group
// the ADR named is represented — conjunctions and auxiliaries, past-tense verbs,
// participles, status adjectives — because the fix is a lexicon and a lexicon is
// only as good as the classes it was tested against.
//
// The two halves of that lexicon are load-bearing separately, which is the point
// of listing this many words: 46 of the 62 are caught by entityStopwords, and
// the other 16 — SHIPPED, ADDED, REMOVED, CHANGED, MOVED, CALLED, WANTED, TRIED,
// FAILED, WORKED, RETURNED, TESTING, WORKING, READING, WRITING, COUNTING — are
// caught ONLY by the inflection reduction. Delete either mechanism and a
// different, disjoint set of rows goes red.
var shoutedProse = []string{
	"and", "was", "were", "been", "being", "are", "has", "have", "would",
	"must", "not", "never", "always", "only", "still", "also", "unless",
	"whereas", "nobody", "twice", "everything", "something", "nothing",
	"missing", "missed", "broken", "dead", "fake", "stale", "actual",
	"silent", "wrong", "unreachable", "deliberate", "critical", "worse", "worst",
	"shipped", "added", "removed", "changed", "moved", "called", "wanted",
	"tried", "failed", "worked", "returned",
	"took", "gave", "ran", "wrote", "broke", "kept", "left", "meant", "sent",
	"testing", "working", "reading", "writing", "counting",
}

// namesWorthKeeping is what the fix must not cost.
//
// The acronyms are the reason the repair is NOT a rule about a token's shape:
// HTTP and MCP are all-caps and are entities, AND and WAS are all-caps and are
// not, and nothing about the characters separates them. The product names are
// the reason the lexicon stops where it does — Vault, Delta and Sentry are
// ordinary English words that are also somebody's system, so a list aggressive
// enough to catch every shouted adjective would quietly eat them.
var namesWorthKeeping = []string{
	"MCP", "ADR", "HTTP", "HTTPS", "API", "CLI", "SQL", "JSON", "YAML", "TEI",
	"RRF", "IDF", "AAAK", "CQRS", "TDD", "UUID", "GRPC", "HTML", "CSS", "GDPR",
	"TOTP", "SSH", "ASCII", "CRUD", "LLM", "JWT", "DNS", "TLS", "PWD", "MRR",
	"QDRANT", "OLLAMA", "SQLITE", "REDIS", "POSTGRES", "NGINX",
	"Atlas", "Vault", "Sentry", "Envoy", "Kong", "Nomad", "Consul", "Delta",
	"Spark", "Arrow", "Grafana", "Prometheus", "Zephyr", "Nimbus", "Mongo",
	"Istio", "Helm", "Argo",
	// The same names shouted. If these went the way of AND and WAS, the filter
	// would be judging case instead of judging the word.
	"ATLAS", "VAULT", "DELTA", "SPARK", "ARROW", "MONGO", "HELM",
}

// TestEmphasisIsNotAnEntity: capitalisation marks stress in an agent's memory
// where it marks a proper noun in prose, so a shouted ordinary word must not
// reach the derived graph — in EITHER case, since measurement found the same 46
// words surviving Title Case as All Caps.
//
// Known survivors, recorded rather than hidden: "RANKING" and "OPTIONAL" still
// extract. COCA holds neither "rank" nor a route to "optional", and both are
// real names elsewhere ("Ranking" is a config surface here, "Optional" is a type
// in three languages), so they are left in rather than guessed out.
func TestEmphasisIsNotAnEntity(t *testing.T) {
	for _, w := range shoutedProse {
		title := strings.ToUpper(w[:1]) + w[1:]
		for _, tok := range []string{strings.ToUpper(w), title} {
			if extractsAsEntity(tok) {
				t.Errorf("%q is ordinary English, not a name — it must not become an entity", tok)
			}
		}
	}
}

// TestAcronymsAndNamesStayEntities: the other half of the same trade. A filter
// that silences emphasis by silencing every capitalised token would leave the
// graph empty in a new way, so the names have to be pinned beside the noise.
func TestAcronymsAndNamesStayEntities(t *testing.T) {
	for _, tok := range namesWorthKeeping {
		if !extractsAsEntity(tok) {
			t.Errorf("%q names something and must stay an entity", tok)
		}
	}
}

func TestExtractEntitiesFrequencyThreshold(t *testing.T) {
	// "Zephyr" appears twice (an entity); "Nimbus" once (below threshold).
	got := extractEntities("Zephyr launched the run. Later Zephyr paged Nimbus once.")
	if !has(got, "Zephyr") {
		t.Fatalf("Zephyr (x2) should be an entity, got %v", got)
	}
	if has(got, "Nimbus") {
		t.Fatalf("Nimbus (x1) is below the freq>=2 threshold, got %v", got)
	}
}

func TestExtractEntitiesDropsStoplistAndCommon(t *testing.T) {
	// "The" is stoplisted; "Action"/"After" are common (COCA / stoplist) — none
	// should survive even though each appears twice and is capitalized.
	got := extractEntities("The cache. The cache. Action here. Action there. After this. After that.")
	for _, bad := range []string{"The", "Action", "After"} {
		if has(got, bad) {
			t.Fatalf("%q should be filtered (stoplist/COCA), got %v", bad, got)
		}
	}
}

func TestExtractEntitiesShortDropped(t *testing.T) {
	// "Hi" repeats but is only two characters (needs > 2), so it is not an entity.
	if got := extractEntities("Hi Hi Hi there"); has(got, "Hi") {
		t.Fatalf("two-char token should be dropped, got %v", got)
	}
}

func TestExtractEntitiesKnownSystemCompound(t *testing.T) {
	// A multi-word known system is recognised as ONE entity when it recurs, and its
	// constituent words are masked (not separately counted).
	got := extractEntities("We shipped with Claude Code today. Then Claude Code again tomorrow.")
	if !has(got, "Claude Code") {
		t.Fatalf("recurring known system should be an entity, got %v", got)
	}
	if has(got, "Claude") {
		t.Fatalf("known-system constituent should be masked, not counted: %v", got)
	}
}

func TestExtractEntitiesSortedAndCapped(t *testing.T) {
	// Many distinct recurring entities must come back sorted and capped at the limit.
	var sb strings.Builder
	for i := 0; i < entityMetadataLimit+10; i++ {
		// Each name appears twice; names are Aaa00..Aaa34-ish, clearly proper nouns.
		name := "Zz" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		sb.WriteString(name + " " + name + ". ")
	}
	got := extractEntities(sb.String())
	if len(got) > entityMetadataLimit {
		t.Fatalf("entities should be capped at %d, got %d", entityMetadataLimit, len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("entities should be sorted, got %v", got)
		}
	}
}
