package palace

import (
	_ "embed"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Entity extraction for mining, ported from the frozen miner
// (_extract_entities_for_metadata) + entity_detector. The goal is the same: pull
// the proper-noun-ish tokens out of a chunk's content so co-occurrence within a
// wing can later materialise hallways, while filtering out ordinary capitalized
// words (sentence starters, common content words). Two vendored data files drive
// the filtering — a COCA common-word list and a known-systems compound list —
// embedded so the binary is self-contained.

//go:embed data/coca_content_words.json
var cocaJSON []byte

//go:embed data/known_systems.json
var knownSystemsJSON []byte

const (
	// entityExtractWindow bounds how much of a chunk is scanned for entities, so a
	// huge drawer cannot make extraction quadratic (frozen _ENTITY_EXTRACT_WINDOW).
	entityExtractWindow = 5000
	// entityMetadataLimit caps how many entities ride a drawer's metadata, sorted
	// so the cap drops whole names, never a partial (frozen _ENTITY_METADATA_LIMIT).
	entityMetadataLimit = 25
	// entityMinFreq / entityMinLen are the inclusion thresholds: a candidate must
	// appear at least twice and be longer than two characters, matching frozen.
	entityMinFreq = 2
	entityMinLen  = 2 // strictly-greater test below, so effective minimum length is 3
)

// entityStoplist is the lexicon of ordinary English words that are never
// entities, keyed LOWERCASE and consulted case-insensitively.
//
// Both of those properties are corrections ADR-016's T1 measurement forced, and
// the second matters more than the first. The list used to be matched as-found,
// so it held "And" and let "AND" through — and an agent's memory is full of
// SHOUTED emphasis, because in a note capitalisation marks stress where in prose
// it marks a proper noun.
//
// Case is not the whole story though, and measuring said so: of 163 ordinary
// words checked, 47 survived shouted and 46 survived in Title Case, so folding
// case alone would have fixed exactly one word ("AND"). What actually
// discriminates a name from a shouted word is a LEXICON, and the vendored COCA
// list cannot be it on its own: it is 1,016 CONTENT words, so it structurally
// omits the closed classes (articles, pronouns, auxiliaries, conjunctions,
// prepositions) and it holds lemmas rather than inflections. This list covers
// exactly what COCA cannot reach — see the three groups in entityStopwords —
// and ordinary is the general test that consults both.
var entityStoplist = map[string]struct{}{}

// candidateWordRE matches an entity candidate: an uppercase letter followed by
// letters. \p{Lu}/\p{L} keep it Unicode-aware (ASCII, accented Latin, Cyrillic),
// the faithful-enough stand-in for the frozen per-locale candidate patterns; the
// full i18n pattern set is a later refinement. Digits/hyphens are not part of a
// single-word candidate — multi-token names like "Claude Code" or "GPT-4o" are
// caught by the known-systems prepass instead.
//
// It deliberately still admits an ALL-CAPS token, even though all-caps is how
// emphasis is written in the corpus this repo measured. Narrowing the regex was
// the obvious repair and it is the wrong one: HTTP, MCP, ADR, TEI and RRF are
// all-caps and all entities, and no rule over a token's SHAPE separates them
// from AND, WAS and MISSING — the shouted survivors run 3 to 11 characters, the
// acronyms 3 to 12. The separation is lexical, so it is made in ordinary below,
// where a wrong answer costs one word rather than every acronym.
var candidateWordRE = regexp.MustCompile(`\p{Lu}[\p{L}]*`)

// entityData is the lazily-loaded, parsed form of the two vendored files: the COCA
// words as a lowercased set, and the known-systems compounds as boundary-anchored
// case-insensitive matchers ordered longest-first so the longest compound wins.
type entityData struct {
	coca         map[string]struct{}
	knownSystems []knownSystem
}

// knownSystem pairs a compound's canonical name with its compiled matcher.
type knownSystem struct {
	name string
	re   *regexp.Regexp
}

var (
	entityOnce sync.Once
	entityDB   entityData
)

// loadEntityData parses the embedded JSON once. Like the frozen loader it degrades
// gracefully: a malformed or missing file yields an empty set rather than a panic,
// so extraction still runs (just without that filter/list).
func loadEntityData() entityData {
	entityOnce.Do(func() {
		var coca struct {
			Words []string `json:"words"`
		}
		_ = json.Unmarshal(cocaJSON, &coca)
		set := make(map[string]struct{}, len(coca.Words))
		for _, w := range coca.Words {
			set[strings.ToLower(w)] = struct{}{}
		}

		var known struct {
			Compounds []string `json:"compounds"`
		}
		_ = json.Unmarshal(knownSystemsJSON, &known)
		// Longest-first so "Claude Sonnet 4.5" masks before "Claude", matching the
		// frozen sort(key=len, reverse=True) longest-match-wins behaviour.
		valid := make([]string, 0, len(known.Compounds))
		for _, c := range known.Compounds {
			if strings.TrimSpace(c) != "" {
				valid = append(valid, c)
			}
		}
		sort.SliceStable(valid, func(i, j int) bool { return len(valid[i]) > len(valid[j]) })
		systems := make([]knownSystem, 0, len(valid))
		for _, c := range valid {
			// (?i) case-insensitive, \b word boundaries (ASCII — sufficient for these
			// English compounds). RE2 has no lookbehind, so \b stands in for the
			// frozen (?<!\w)…(?!\w); all compounds begin and end with a word char.
			re, err := regexp.Compile(`(?i)\b` + regexp.QuoteMeta(c) + `\b`)
			if err != nil {
				continue
			}
			systems = append(systems, knownSystem{name: c, re: re})
		}

		entityDB = entityData{coca: set, knownSystems: systems}
	})
	return entityDB
}

// entityFreq is the shared counting pass behind both drawer-entity and
// closet-entity extraction: it scans a bounded window, masks known-system
// compounds (counting each as one), then counts capitalized candidates that
// survive the stoplist and COCA filters. Returning the raw frequency map lets the
// two consumers apply their own thresholds and ordering without re-scanning.
func entityFreq(content string) map[string]int {
	db := loadEntityData()

	// Bound the scan by runes so multibyte content is windowed at a character
	// count, not a byte count (mirrors Python slicing the str).
	runes := []rune(content)
	if len(runes) > entityExtractWindow {
		runes = runes[:entityExtractWindow]
	}
	window := string(runes)

	// Known-systems prepass: mask each compound's matches to spaces (preserving
	// length so later indices stay valid) and remember how often each occurred, so
	// a compound counts as one entity and its constituent words are not recounted.
	freq := map[string]int{}
	for _, ks := range db.knownSystems {
		locs := ks.re.FindAllStringIndex(window, -1)
		if len(locs) == 0 {
			continue
		}
		freq[ks.name] += len(locs)
		b := []byte(window)
		for _, loc := range locs {
			for i := loc[0]; i < loc[1]; i++ {
				b[i] = ' '
			}
		}
		window = string(b)
	}

	// Capitalized single-word candidates from the masked window.
	for _, w := range candidateWordRE.FindAllString(window, -1) {
		if db.ordinary(w) {
			continue
		}
		freq[w]++
	}
	return freq
}

// ordinary reports whether w is an ordinary English word rather than a name.
//
// This is the whole of what separates an entity from a capitalised word, and it
// runs case-insensitively so a shouted word is judged as the word it is. Three
// tests, cheapest first: the stoplist of what COCA structurally omits, COCA
// itself, and finally COCA again through the regular inflections — because COCA
// holds "ship" and "change" and an agent writes SHIPPED and CHANGED.
//
// A false positive here costs one entity; a false negative fills the derived
// graph with hallways between shouted conjunctions, which is worse than no graph.
// The bias is therefore toward filtering, and the two batteries in
// entity_test.go are where that bias is held to an actual number.
func (db entityData) ordinary(w string) bool {
	lw := strings.ToLower(w)
	if _, stop := entityStoplist[lw]; stop {
		return true
	}
	if _, common := db.coca[lw]; common {
		return true
	}
	for _, base := range inflectionBases(lw) {
		if _, common := db.coca[base]; common {
			return true
		}
	}
	return false
}

// inflectionBases returns the dictionary forms a regularly inflected English word
// could have come from — every one of them, because the reduction is ambiguous
// ("changes" is change+s and "passes" is pass+es) and a lexicon lookup is cheap
// enough to try both rather than guess.
//
// It handles only REGULAR inflection. The irregulars COCA's lemmas cannot be
// reached from ("was", "took", "written") are listed in entityStoplist instead,
// which is why the two exist together.
func inflectionBases(w string) []string {
	var out []string
	add := func(s string) {
		// Below three letters nothing survives entityMinLen anyway, and a
		// two-letter stem matches far too much.
		if len(s) >= 3 {
			out = append(out, s)
		}
	}
	switch {
	case strings.HasSuffix(w, "ies"):
		add(w[:len(w)-3] + "y") // queries -> query
	case strings.HasSuffix(w, "es"):
		add(w[:len(w)-2]) // passes -> pass
		add(w[:len(w)-1]) // changes -> change
	case strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
		add(w[:len(w)-1]) // runs -> run
	}
	switch {
	case strings.HasSuffix(w, "ied"):
		add(w[:len(w)-3] + "y") // tried -> try
	case strings.HasSuffix(w, "ed"):
		add(w[:len(w)-2])           // worked -> work
		add(w[:len(w)-1])           // changed -> change
		add(undouble(w[:len(w)-2])) // shipped -> ship
	case strings.HasSuffix(w, "ing"):
		add(w[:len(w)-3])           // counting -> count
		add(w[:len(w)-3] + "e")     // writing -> write
		add(undouble(w[:len(w)-3])) // running -> run
	case strings.HasSuffix(w, "ly"):
		add(w[:len(w)-2]) // quickly -> quick
	}
	return out
}

// undouble drops the doubled final consonant English adds before -ed/-ing
// ("shipp" -> "ship"). It only fires on an ASCII letter so a multibyte tail is
// left alone rather than sliced mid-rune.
func undouble(s string) string {
	n := len(s)
	if n < 3 || s[n-1] != s[n-2] || s[n-1] < 'a' || s[n-1] > 'z' {
		return s
	}
	return s[:n-1]
}

// extractEntities returns the entities stamped on a mined drawer's metadata: every
// candidate seen at least twice and longer than two characters, sorted
// alphabetically and capped at entityMetadataLimit (the cap drops whole names).
func extractEntities(content string) []string {
	freq := entityFreq(content)
	matched := make([]string, 0, len(freq))
	for w, c := range freq {
		if c >= entityMinFreq && len([]rune(w)) > entityMinLen {
			matched = append(matched, w)
		}
	}
	sort.Strings(matched)
	if len(matched) > entityMetadataLimit {
		matched = matched[:entityMetadataLimit]
	}
	return matched
}

// closetEntities returns the few most salient entities for a closet line: the
// candidates seen at least twice, ordered by descending frequency (ties broken
// alphabetically for determinism) and capped at closetEntityLimit — the frozen
// closet builder's top-N-by-frequency selection, distinct from the drawer's
// alphabetical, length-filtered list.
func closetEntities(content string) []string {
	freq := entityFreq(content)
	matched := make([]string, 0, len(freq))
	for w, c := range freq {
		if c >= entityMinFreq {
			matched = append(matched, w)
		}
	}
	sort.Slice(matched, func(i, j int) bool {
		if freq[matched[i]] != freq[matched[j]] {
			return freq[matched[i]] > freq[matched[j]]
		}
		return matched[i] < matched[j]
	})
	if len(matched) > closetEntityLimit {
		matched = matched[:closetEntityLimit]
	}
	return matched
}

// entityStopwords is what the COCA content-word list cannot reach, in four
// groups. Each group has a rule, so the list can be extended by argument rather
// than by taste, and the batteries in entity_test.go are the specification the
// list is written against.
//
//  1. Closed-class function words. COCA is a CONTENT-word list by construction,
//     so no article, pronoun, auxiliary, modal, conjunction or preposition can
//     ever be in it. This is the group that let AND, WAS, ARE and BEEN become
//     entities.
//  2. Irregular verb forms. COCA holds the lemma ("take", "write", "break") and
//     inflectionBases only knows regular endings, so TOOK, WROTE and BROKEN
//     survive both filters with nothing to reduce them to.
//  3. Participles and adjectives an agent uses as a status marker, which COCA
//     simply omits — MISSING and BROKEN and STALE are what a memory about a
//     defect is made of, and they were among the most frequent candidates the
//     live corpus produced.
//  4. Days, months and conversational roles, kept verbatim from the frozen
//     miner's _ENTITY_STOPLIST.
//
// Deliberately NOT here: ordinary words that are also product names. "Atlas",
// "Vault", "Delta", "Spark" and "Sentry" are entities in somebody's memory, and
// a lexicon aggressive enough to catch every shouted adjective would take them
// with it. TestAcronymsAndNamesStayEntities pins that boundary.
var entityStopwords = []string{
	// 1 — closed classes.
	"a", "an", "the", "and", "but", "or", "nor", "yet", "so", "if", "then",
	"else", "than", "that", "this", "these", "those", "as", "because",
	"although", "though", "while", "whereas", "unless", "until", "since",
	"whether", "either", "neither", "both", "each", "every", "any", "all",
	"none", "some", "such", "no", "not", "only", "own", "same", "other",
	"another", "more", "most", "less", "least", "much", "many", "few",
	"several", "enough", "i", "me", "my", "mine", "myself", "we", "us", "our",
	"ours", "ourselves", "you", "your", "yours", "yourself", "yourselves",
	"he", "him", "his", "himself", "she", "her", "hers", "herself", "it",
	"its", "itself", "they", "them", "their", "theirs", "themselves", "who",
	"whom", "whose", "which", "what", "where", "when", "why", "how", "here",
	"there", "everyone", "everything", "someone", "something", "anyone",
	"anything", "nobody", "nothing", "everybody", "somebody", "anybody",
	"am", "is", "are", "was", "were", "be", "been", "being", "have", "has",
	"had", "having", "do", "does", "did", "doing", "will", "would", "shall",
	"should", "can", "could", "may", "might", "must", "ought",
	"of", "in", "on", "at", "by", "for", "with", "from", "to", "into", "onto",
	"upon", "about", "above", "below", "under", "over", "between", "among",
	"through", "during", "before", "after", "against", "without", "within",
	"across", "behind", "beyond", "beside", "besides", "toward", "towards",
	"per", "via", "off", "out", "up", "down", "back", "again", "once", "twice",
	"ever", "never", "always", "often", "sometimes", "already", "still",
	"just", "even", "also", "too", "very", "quite", "rather", "almost",
	"indeed", "therefore", "however", "instead", "otherwise", "meanwhile",
	"thus", "hence", "yes", "ok", "okay", "maybe",

	// 2 — irregular verb forms whose lemma COCA holds.
	"got", "gotten", "went", "gone", "came", "took", "taken", "gave", "given",
	"saw", "seen", "knew", "known", "made", "found", "said", "told", "thought",
	"brought", "bought", "caught", "taught", "ran", "wrote", "written",
	"broke", "spoke", "spoken", "chose", "chosen", "drove", "driven", "froze",
	"frozen", "threw", "thrown", "grew", "grown", "flew", "flown", "drew",
	"drawn", "blew", "blown", "shown", "held", "kept", "left", "meant", "sent",
	"spent", "lost", "felt", "built", "dealt", "slept", "swept", "heard",
	"sold", "paid", "laid", "began", "begun", "drank", "sang", "sung", "rang",
	"rung", "swam", "stood", "understood", "won", "sat", "met", "led", "fed",
	"fell", "fallen", "rose", "risen", "wore", "worn", "tore", "torn", "bore",

	// 3 — status participles and adjectives COCA omits.
	"missing", "missed", "broken", "dead", "stale", "fake", "actual",
	"silent", "unreachable", "deliberate", "worse", "worst", "wrong",
	"stuck", "flaky", "noisy", "obvious", "unrelated", "identical",
	"critical",

	// 4 — days, months and conversational roles (frozen _ENTITY_STOPLIST).
	"user", "assistant", "system", "tool",
	"monday", "tuesday", "wednesday", "thursday", "friday", "saturday",
	"sunday", "january", "february", "march", "april", "may", "june", "july",
	"august", "september", "october", "november", "december",
}

func init() {
	for _, w := range entityStopwords {
		entityStoplist[w] = struct{}{}
	}
}
