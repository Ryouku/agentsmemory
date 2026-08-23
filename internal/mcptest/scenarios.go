package mcptest

import (
	"strconv"
	"strings"
	"testing"
)

// Scenario is one end-to-end exercise of the tool surface.
//
// Tools is what the scenario CLAIMS to exercise, and it is checked against what
// the run actually invoked rather than trusted: a claim nobody verifies is how a
// coverage list drifts from the code it describes. The gate fails a scenario
// claiming a tool it never called, so the two can never disagree silently.
type Scenario struct {
	Name  string
	Tools []string
	Run   func(t *testing.T, h *Harness)
}

// ExemptionReasons are the external dependencies that make a tool genuinely
// unobservable in an in-process harness. Anything else is a reason to write a
// scenario, not a reason one cannot exist.
var ExemptionReasons = []string{"qdrant", "tei", "oauth", "ollama", "reranker", "network"}

// ValidExemption reports why an exemption is not acceptable, or "" when it is.
//
// It takes the entry rather than being inlined in a loop for the reason the
// catalogue guard was extracted: the exemption list is EMPTY today, so a loop
// over it executes zero times and the check passes no matter what it says. A
// rule that cannot run is not a rule.
func ValidExemption(u Unobservable) string {
	if strings.TrimSpace(u.Tool) == "" {
		return "an exemption with no tool name"
	}
	if strings.TrimSpace(u.Why) == "" {
		return u.Tool + " is exempt with no explanation"
	}
	needs := strings.ToLower(strings.TrimSpace(u.Needs))
	if needs == "" {
		return u.Tool + " is exempt with no dependency named"
	}
	for _, a := range ExemptionReasons {
		if strings.Contains(needs, a) {
			return ""
		}
	}
	return u.Tool + " is exempt for " + strconv.Quote(u.Needs) +
		", which names no external dependency — that is a reason to write a scenario, not a reason one cannot exist"
}

// Unobservable names a tool whose effect this in-process harness cannot see, and
// why.
//
// The reason must name an external dependency. Without that rule the list is a
// parking space for anything awkward, and "we did not get to it" becomes
// indistinguishable from "it cannot be done here" — which is the difference
// between a gap somebody will close and one nobody will ever look at again.
type Unobservable struct {
	Tool string
	// Needs is the external dependency: a live Qdrant, a TEI endpoint, an OAuth
	// issuer. The gate rejects a reason that names none.
	Needs string
	Why   string
}
