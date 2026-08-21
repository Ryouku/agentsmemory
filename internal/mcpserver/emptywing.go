package mcpserver

import (
	"context"
	"fmt"
	"strings"
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
	if err != nil || !empty {
		return "", nil // fails OPEN: a lookup failure must not turn a real page into a warning
	}
	names, err := wings.WingNames(ctx, teamID)
	if err != nil || len(names) == 0 {
		return fmt.Sprintf("the wing %q holds no memories at all, so this is not a miss — nothing "+
			"has ever been filed there.", wing), nil
	}
	note := fmt.Sprintf("the wing %q holds no memories at all, so this is not a miss — nothing has "+
		"ever been filed there. Wings that do hold memories: %s.", wing, strings.Join(names, ", "))
	if near := nearestWing(wing, names); near != "" {
		note += fmt.Sprintf(" Did you mean %q? Pass wing:\"*\" to search every wing.", near)
	} else {
		note += " Pass wing:\"*\" to search every wing."
	}
	return note, names
}

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

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}
