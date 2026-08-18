package mcpserver

import (
	"context"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/auth"
)

// TestWingForPrefersTheCaller: an argument is a decision, a registration default
// is only a default. A caller that names a wing must always win, or cross-project
// filing (a tunnel, a shared decision) becomes impossible from a scoped client.
func TestWingForPrefersTheCaller(t *testing.T) {
	ctx := auth.WithDefaultWing(context.Background(), "wing_project")
	got, err := wingFor(ctx, "wing_explicit")
	if err != nil || got != "wing_explicit" {
		t.Fatalf("wingFor = %q, %v; want wing_explicit", got, err)
	}
}

// TestWingForFallsBackToTheRegistration is the whole point: a write with no wing
// lands in the project this MCP was registered for, rather than failing or
// landing wherever the agent last remembered.
func TestWingForFallsBackToTheRegistration(t *testing.T) {
	ctx := auth.WithDefaultWing(context.Background(), "wing_project")
	got, err := wingFor(ctx, "")
	if err != nil || got != "wing_project" {
		t.Fatalf("wingFor = %q, %v; want wing_project", got, err)
	}
	// Blank-but-present is the same as absent — an agent passing "" means it has
	// no opinion, not that it wants an empty wing.
	if got, err = wingFor(ctx, "   "); err != nil || got != "wing_project" {
		t.Fatalf("wingFor(blank) = %q, %v; want wing_project", got, err)
	}
}

// TestWingForSanitizesTheRegistrationValue: the default arrives over the wire in
// a header, so it gets the same validation as anything else from a caller.
func TestWingForSanitizesTheRegistrationValue(t *testing.T) {
	ctx := auth.WithDefaultWing(context.Background(), "wing with spaces/and-slash")
	if _, err := wingFor(ctx, ""); err == nil {
		t.Fatal("an unsafe registration wing must be rejected, not filed")
	}
}

// TestWingForWithoutAnyWing names both fixes, because a caller in this state has
// two different ones available.
func TestWingForWithoutAnyWing(t *testing.T) {
	_, err := wingFor(context.Background(), "")
	if err == nil {
		t.Fatal("want an error when neither the call nor the registration names a wing")
	}
	for _, want := range []string{"wing is required", auth.WingHeader} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (haystack == needle || len(needle) == 0 ||
		indexOf(haystack, needle) >= 0)
}

func indexOf(h, n string) int {
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}
