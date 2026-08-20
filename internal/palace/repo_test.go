package palace

import (
	"context"
	"testing"
)

// TestWingIsEmptyCountsDrawers pins the question the handoff check asks: is this
// write the one that creates the wing? A wing that answers "not empty" when it
// holds nothing turns the check off silently, and one that answers "empty" for a
// populated wing refuses correct handoffs.
func TestWingIsEmptyCountsDrawers(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	empty, err := svc.WingIsEmpty(ctx, "team", "wing_never_written")
	if err != nil || !empty {
		t.Fatalf("WingIsEmpty on an unwritten wing = %v, %v; want true", empty, err)
	}

	if _, err := svc.Add(ctx, "team", AddInput{
		Wing: "wing_written", Room: "decisions", Content: "a decision worth keeping",
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	if empty, err := svc.WingIsEmpty(ctx, "team", "wing_written"); err != nil || empty {
		t.Errorf("WingIsEmpty after a write = %v, %v; want false", empty, err)
	}
	// Another team's drawer must not populate this team's wing — the check runs
	// before a write and a cross-tenant leak here would refuse a legitimate one.
	if empty, err := svc.WingIsEmpty(ctx, "other-team", "wing_written"); err != nil || !empty {
		t.Errorf("WingIsEmpty is not tenant-scoped: %v, %v", empty, err)
	}

	names, err := svc.WingNames(ctx, "team")
	if err != nil || len(names) != 1 || names[0] != "wing_written" {
		t.Errorf("WingNames = %v, %v; want [wing_written]", names, err)
	}
}
