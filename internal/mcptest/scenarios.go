package mcptest

import "testing"

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
