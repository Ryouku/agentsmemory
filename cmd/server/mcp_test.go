package main

import "testing"

// TestCLIWingDefaultsLikeARegistration.
//
// Reproduced live on the running server before this landed: `agentsmemory mcp
// search "deploy" --team <id>` returned eight hits from two OTHER projects'
// wings and none from the caller's, while the same query over MCP returned eight
// from the registration's wing. The header of cmd/server/mcp.go claims parity
// with the HTTP gate; it diverged on the one setting that keeps projects apart.
//
// The CLI has no registration to derive a wing from — no X-Agentsmemory-Wing
// header, no per-project token — so the honest fix is not to invent one but to
// let the operator state it, and to have it behave like a registration default
// once stated: an explicit `-a wing=` still wins, exactly as an explicit wing
// argument beats the registration default over MCP.
func TestCLIWingDefaultsLikeARegistration(t *testing.T) {
	t.Run("the flag supplies the wing when the call names none", func(t *testing.T) {
		got := parseArgsWithWing(nil, nil, "query", "wing_alpha")
		if got.str("wing") != "wing_alpha" {
			t.Errorf("wing = %q, want wing_alpha — without this, a CLI recall naming no wing "+
				"reads every project in the workspace", got.str("wing"))
		}
	})

	t.Run("an explicit argument wins, as it does over MCP", func(t *testing.T) {
		got := parseArgsWithWing([]string{"wing=wing_beta"}, nil, "query", "wing_alpha")
		if got.str("wing") != "wing_beta" {
			t.Errorf("wing = %q, want wing_beta — an argument is a decision and a default is only "+
				"a default", got.str("wing"))
		}
	})

	t.Run(`"*" stays the way to ask across wings`, func(t *testing.T) {
		got := parseArgsWithWing([]string{"wing=*"}, nil, "query", "wing_alpha")
		if got.str("wing") != "*" {
			t.Errorf(`wing = %q, want "*" — the cross-project escape hatch must survive`, got.str("wing"))
		}
	})

	t.Run("no flag leaves the behaviour it had", func(t *testing.T) {
		if got := parseArgsWithWing(nil, nil, "query", ""); got.str("wing") != "" {
			t.Errorf("wing = %q, want empty — an operator who names no wing has not asked to be "+
				"scoped, and inventing one would hide data they can legitimately read", got.str("wing"))
		}
	})
}
