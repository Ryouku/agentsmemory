package mcpserver

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atvirokodosprendimai/agentsmemory/internal/telemetry"

	"go.opentelemetry.io/otel/attribute"
)

// emptyWingNote explains a zero-hit page when the WING itself holds nothing,
// which is a different fact from "nothing matched your query".
//
// Measured 2026-08-21 against 32 real production queries: wing scoping produced
// 4 of the 4 hard failures. A query scoped to a wing the palace has never held
// returns {"count":0,"hits":[]} in under a second — byte-identical to a genuine
// miss against a well-stocked wing. In one case the same query against the
// correctly-spelled wing returned the exact answer at rank 2. The agent has no
// way to tell a typo from an absence, so it concludes the memory does not exist
// and stops asking.
//
// This is the read-path twin of the handoff refusal, which has guarded the WRITE
// path against the same mistake since ADR-005. Both use the same two primitives;
// only the write path had a consumer.
//
// It returns "" whenever the wing genuinely holds memories, so a real miss reads
// exactly as it did before.
func emptyWingNote(ctx context.Context, wings wingReader, teamID, wing string) (string, []string) {
	wing = strings.TrimSpace(wing)
	if wing == "" || wing == "*" {
		return "", nil
	}
	empty, err := wings.WingIsEmpty(ctx, teamID, wing)
	if err != nil {
		// Fails OPEN — a lookup failure must not turn a real page into a warning —
		// but the two causes are not the same event and must not read as one. This
		// branch means "we could not tell"; the branch below means "the wing has
		// content". Collapsing them into one silent `return "", nil` is why a
		// zero-hit page could lose its explanation with nothing anywhere saying so.
		telemetry.Annotate(ctx, attribute.Bool("am.emptiness_lookup_failed", true))
		slog.Warn("empty-wing lookup failed; a zero-hit page will carry no explanation",
			"error", err, "wing", wing)
		return "", nil
	}
	if !empty {
		return "", nil // the wing genuinely holds memories: a real miss, no note
	}
	names, err := wings.WingNames(ctx, teamID)
	if err != nil || len(names) == 0 {
		return fmt.Sprintf("the wing %q holds no memories, so this is not a miss: there is nothing "+
			"there to match.", wing), nil
	}
	// The suggestion searches every name; the NOTE lists a bounded few. This
	// string goes into an agent's context window, and a workspace holding
	// thousands of wings would spend the whole page on a list nobody reads.
	shown := names
	extra := ""
	if len(shown) > maxWingsInNote {
		extra = fmt.Sprintf(" (+%d more)", len(shown)-maxWingsInNote)
		shown = shown[:maxWingsInNote]
	}
	note := fmt.Sprintf("the wing %q holds no memories, so this is not a miss: there is nothing there "+
		"to match. Wings that do hold memories: %s%s.", wing, strings.Join(shown, ", "), extra)
	if near := nearestWing(wing, names); near != "" {
		note += fmt.Sprintf(" Did you mean %q? Pass wing:\"*\" to search every wing.", near)
	} else {
		note += " Pass wing:\"*\" to search every wing."
	}
	return note, shown
}

// maxWingsInNote bounds how many wing names the note spells out. The note is
// delivered to an agent, so its cost is context, and an unbounded list is a
// diagnostic that crowds out the thing it is diagnosing.
const maxWingsInNote = 20

// nearestWing suggests the closest existing wing name, or "" when nothing is
// close enough to be worth guessing. A wrong suggestion is worse than none: it
// sends an agent to a wing that has nothing to do with its question.
func nearestWing(want string, names []string) string {
	want = strings.ToLower(strings.TrimPrefix(want, "wing_"))
	best, bestScore := "", 0
	for _, n := range names {
		got := strings.ToLower(strings.TrimPrefix(n, "wing_"))
		score := commonPrefix(want, got)
		if strings.Contains(got, want) || strings.Contains(want, got) {
			score += 3
		}
		if score > bestScore {
			best, bestScore = n, score
		}
	}
	// Three characters of agreement is the floor. Below it the "suggestion" is
	// just the alphabetically luckiest wing.
	if bestScore < 3 {
		return ""
	}
	return best
}

// commonPrefix counts how many leading CHARACTERS two names share.
//
// Runes, not bytes: the floor above is stated in characters, and a byte count
// let a single three-byte rune clear a three-character bar — "wing_猫x" was
// offered "wing_猫y" as a confident suggestion on one shared character. A wrong
// suggestion sends an agent to a wing with nothing to do with its question,
// which is worse than no suggestion at all.
func commonPrefix(a, b string) int {
	ar, br := []rune(a), []rune(b)
	n := 0
	for n < len(ar) && n < len(br) && ar[n] == br[n] {
		n++
	}
	return n
}
