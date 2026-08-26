package palace

import (
	"context"
	"fmt"
	"testing"
)

// TestFactLookupCostIsBounded measures how many graph queries a single recall
// issues, because factsFor runs one KGQuery per candidate entity and candidates
// come from BOTH the vector matches AND every loaded drawer's extracted terms.
//
// This is a search-path hot loop: every am_search pays it. The number is measured
// rather than reasoned about, and the ceiling is asserted so a later change that
// multiplies it fails here instead of showing up as a slow palace.
func TestFactLookupCostIsBounded(t *testing.T) {
	ctx := context.Background()
	const team = "t-cost"
	svc := newTestService(t)

	// A page's worth of drawers, each carrying several extracted proper nouns.
	for i := 0; i < 10; i++ {
		content := fmt.Sprintf("Ledger and Atlas and Vault and Beacon and Harbor interact in system %d. Ledger calls Atlas. Vault stores Beacon.", i)
		if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: content}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := svc.BackfillEntityLabels(ctx, team); err != nil {
		t.Fatalf("backfill: %v", err)
	}

	rows, err := svc.repo.IDsBySource(ctx, team, "wing_acme", "decisions", "")
	if err != nil {
		t.Fatalf("ids: %v", err)
	}
	loaded := map[string]Drawer{}
	drawers, err := svc.repo.DrawersByIDs(ctx, team, rows)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for _, d := range drawers {
		loaded[d.ID] = d
	}

	// The query count equals the number of distinct candidate entities, which is
	// what the implementation iterates.
	vec, err := svc.embed.EmbedOne(ctx, "what does Ledger call")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	matches, err := svc.entityMatches(ctx, team, vec, factEntityMatches)
	if err != nil {
		t.Fatalf("matches: %v", err)
	}
	seen := map[string]bool{}
	for _, m := range matches {
		seen[m.ID] = true
	}
	for _, d := range loaded {
		for _, term := range d.Entities {
			if id := normalizeEntityID(term); id != "" {
				seen[id] = true
			}
		}
	}

	t.Logf("page of %d drawers -> %d candidate entities -> %d KGQuery calls per recall",
		len(loaded), len(seen), len(seen))

	// The ceiling is deliberately generous and deliberately PRESENT. A recall that
	// issues a query per extracted noun across a whole page is a cost that grows
	// with page size and with how chatty the extractor is, and neither is bounded
	// by anything else in the system.
	const ceiling = 60
	if len(seen) > ceiling {
		t.Errorf("a single recall would issue %d graph queries (ceiling %d); factsFor is on the search hot path and this grows with page size times entities per drawer",
			len(seen), ceiling)
	}

	// The case above is FAVOURABLE: every drawer names the same five things, so
	// dedup collapses them and the count stays near the entity count rather than
	// the drawer count. A page whose drawers share no vocabulary is the real
	// ceiling, and measuring only the flattering case is how a cost estimate
	// becomes a reassurance.
	t.Run("a page that shares no vocabulary", func(t *testing.T) {
		const team = "t-cost-worst"
		svc := newTestService(t)
		// Ten drawers, each naming three things NO other drawer names. Getting
		// this fixture to actually exercise the worst case took three attempts,
		// and each failed version passed:
		//
		//   1. each noun named once   -> extractEntities is frequency-based and
		//                                stamped nothing; 0 candidate entities
		//   2. nouns suffixed 0..9    -> the extractor strips digits, so Alpha0
		//                                and Alpha9 are both "Alpha"; all ten
		//                                drawers shared one vocabulary
		//
		// A cost measurement over a fixture that cannot produce the cost is a
		// reassurance, not a measurement.
		vocab := [][3]string{
			{"Kestrel", "Meridian", "Foundry"}, {"Lantern", "Quarry", "Thicket"},
			{"Beacon", "Harrow", "Vellum"}, {"Cinder", "Marrow", "Pallet"},
			{"Dovetail", "Nimbus", "Rampart"}, {"Ember", "Orchard", "Sable"},
			{"Fathom", "Plinth", "Tessera"}, {"Girder", "Quill", "Umber"},
			{"Halyard", "Rookery", "Verdant"}, {"Ingot", "Sextant", "Willow"},
		}
		for _, v := range vocab {
			content := fmt.Sprintf("%s calls %s. %s stores in %s. %s reads %s. %s and %s share %s.",
				v[0], v[1], v[0], v[2], v[1], v[2], v[0], v[1], v[2])
			if _, err := svc.Add(ctx, team, AddInput{Wing: "wing_acme", Room: "decisions", Content: content}); err != nil {
				t.Fatalf("add %s: %v", v[0], err)
			}
		}
		ids, err := svc.repo.IDsBySource(ctx, team, "wing_acme", "decisions", "")
		if err != nil {
			t.Fatalf("ids: %v", err)
		}
		ds, err := svc.repo.DrawersByIDs(ctx, team, ids)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		worst := map[string]bool{}
		for _, d := range ds {
			for _, term := range d.Entities {
				if id := normalizeEntityID(term); id != "" {
					worst[id] = true
				}
			}
		}
		t.Logf("page of %d drawers sharing no vocabulary -> %d candidate entities -> %d KGQuery calls",
			len(ds), len(worst), len(worst))
		if len(worst) > ceiling {
			t.Errorf("worst realistic page issues %d graph queries (ceiling %d) on every am_search", len(worst), ceiling)
		}
	})
}
