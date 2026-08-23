package palace

import (
	"context"
	"fmt"
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

// TestInboxCountCountsOnlyTheInboxRoom: the wake-up hint is read off this
// number, so a count that includes the wing's other rooms sends every session
// chasing an inbox that is empty, and the hint stops meaning anything.
func TestInboxCountCountsOnlyTheInboxRoom(t *testing.T) {
	svc := newTestService(t)
	ctx := context.Background()

	// Distinct content per drawer: re-adding identical text is idempotent by
	// design, so two identical inbox items would collapse into one and this test
	// would be measuring the dedup rather than the count.
	for i, room := range []string{"inbox", "inbox", "decisions", "diary"} {
		if _, err := svc.Add(ctx, "team", AddInput{
			Wing: "wing_x", Room: room, Content: fmt.Sprintf("drawer %d filed into %s", i, room),
		}); err != nil {
			t.Fatalf("add %s: %v", room, err)
		}
	}

	if n, err := svc.InboxCount(ctx, "team", "wing_x", "inbox"); err != nil || n != 2 {
		t.Errorf("InboxCount = %d, %v; want 2 — the decisions and diary drawers are not handoffs", n, err)
	}
	if n, err := svc.InboxCount(ctx, "team", "wing_other", "inbox"); err != nil || n != 0 {
		t.Errorf("InboxCount for another wing = %d, %v; want 0", n, err)
	}
	if n, err := svc.InboxCount(ctx, "other-team", "wing_x", "inbox"); err != nil || n != 0 {
		t.Errorf("InboxCount is not tenant-scoped: %d, %v", n, err)
	}
}
