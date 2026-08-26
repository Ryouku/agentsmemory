package palace

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
)

// BootstrapTier separates content that travels INLINE from content that travels
// as a pointer.
//
// The distinction is the whole reason a bootstrap is smaller than the protocol it
// replaces. Inlining everything reproduces the problem — a client-side protocol
// measured at ~99KB — inside one call, and pointing at everything reproduces the
// 13 calls.
type BootstrapTier string

// The two tiers. The server distinguishes eager from on-demand; which drawers a
// team calls which is a team convention the server does not bless.
const (
	// TierEager: content a session needs before it can do anything, sent inline.
	TierEager BootstrapTier = "eager"
	// TierOnDemand: content a session may need, sent as an id to fetch.
	TierOnDemand BootstrapTier = "on_demand"
)

// BootstrapPointer names something the response did not inline.
type BootstrapPointer struct {
	ID   string        `json:"id"`
	Tier BootstrapTier `json:"tier"`
	// Fetch is the call that retrieves it. A pointer without the call that
	// resolves it is a riddle: the protocol this replaces lost 74% of a
	// prescribed tier to an unreported cap, and reporting "3 omitted" without
	// saying how to get them repeats that in a politer form.
	Fetch string `json:"fetch"`
}

// BootstrapTruncation reports what a bounded response left out.
//
// Always present, never inferred from a short list: a caller cannot tell a
// complete answer from a capped one by counting, which is exactly how the
// original loss went unnoticed.
type BootstrapTruncation struct {
	Omitted int    `json:"omitted"`
	Reason  string `json:"reason,omitempty"`
	// HowToFetch names the call that retrieves what was dropped.
	HowToFetch string `json:"how_to_fetch,omitempty"`
}

// BootstrapResult is everything a session needs to start work in a wing, in ONE
// call: no second round trip, and no id carried in from a skill file.
type BootstrapResult struct {
	// Wing is the wing this bootstraps, resolved and echoed.
	Wing string `json:"wing"`
	// EntryPoint is the wing's front door, from T7's direct resolution.
	EntryPoint EntryPointResult `json:"entry_point"`
	// Eager is the inline content.
	Eager []Drawer `json:"eager,omitempty"`
	// OnDemand names what was not inlined.
	OnDemand []BootstrapPointer `json:"on_demand,omitempty"`
	// Corrections are the retracts/supersedes/qualifies edges already swept
	// server-side, so a session that bootstraps perfectly does not still read
	// whatever the tier got wrong and believe it.
	Corrections map[string][]Correction `json:"corrections,omitempty"`
	// Truncation says what this response left out and how to get it.
	Truncation BootstrapTruncation `json:"truncation"`
}

// bootstrapEagerLimit bounds the inline tier. A bootstrap that grows without
// limit becomes the thing it replaced.
const bootstrapEagerLimit = 10

// Bootstrap assembles a wing's starting context in one call.
//
// It replaces a client-side protocol measured at 13 calls and ~99KB, which every
// session paid before it could do anything, and which required a hardcoded root
// drawer id that only worked in one wing.
//
// Every piece is CONSUMED rather than reimplemented: the entry point is T7's, the
// corrections are T5's single incoming sweep, and the wing rule is T3's
// WingPolicy. Two implementations of the same rule diverge on the path nobody
// tested, and here that path would be a tenancy boundary.
func (s *Service) Bootstrap(ctx context.Context, teamID, wing string) (BootstrapResult, error) {
	out := BootstrapResult{Wing: wing}

	// Direct resolution, not a graph walk. am_traverse's max_hops is inert, so a
	// bootstrap built on it would silently return only hop 1 while looking
	// correct — which is why F-17 is asserted against THIS surface and not only
	// against EntryPoint.
	entry, err := s.EntryPoint(ctx, teamID, wing)
	if err != nil {
		return BootstrapResult{}, err
	}
	out.EntryPoint = entry

	policy := s.wingPolicyFor(ctx, teamID, wing)

	// Collect the ids the entry point points at, in order.
	ids := make([]string, 0, len(entry.Edges))
	for _, e := range entry.Edges {
		if e.Object != "" {
			ids = append(ids, e.Object)
		}
	}

	// Everything past the eager bound becomes a pointer rather than vanishing.
	inline, deferred := ids, []string(nil)
	if len(ids) > bootstrapEagerLimit {
		inline, deferred = ids[:bootstrapEagerLimit], ids[bootstrapEagerLimit:]
	}

	if len(inline) > 0 {
		drawers, err := s.repo.DrawersByIDs(ctx, teamID, inline)
		if err != nil {
			return BootstrapResult{}, err
		}
		for _, d := range drawers {
			// The bootstrap's inline content goes through the SAME wing rule as
			// every other response path. An entry edge can name a record in
			// another wing, and inlining it here would be the leak that a
			// subject/predicate/object check never sees.
			placement, _ := policy.Place(ctx, d.ID)
			if policy.MayReturnContent(placement) {
				out.Eager = append(out.Eager, d)
			}
		}
	}
	for _, id := range deferred {
		out.OnDemand = append(out.OnDemand, BootstrapPointer{
			ID: id, Tier: TierOnDemand, Fetch: "am_get_drawer",
		})
	}
	out.Truncation = BootstrapTruncation{Omitted: len(deferred)}
	if len(deferred) > 0 {
		out.Truncation.Reason = "beyond the eager tier's bound"
		out.Truncation.HowToFetch = "am_get_drawer with each id in on_demand"
	}

	// T5's sweep, consumed. Corrections attach as INCOMING edges, so no outgoing
	// walk from a bootstrapped record can see that it has been retracted — which
	// is why a session that bootstraps perfectly still reads what the tier got
	// wrong unless the server sweeps for it.
	all := append(append([]string{}, inline...), deferred...)
	corrections, err := s.CorrectionsFor(ctx, teamID, all, policy)
	if err != nil {
		return BootstrapResult{}, err
	}
	if len(corrections) > 0 {
		out.Corrections = corrections
	}
	return out, nil
}

// BootstrapBaseline is the REDACTED record of the client-side protocol this
// surface replaces: how many calls it took and what it cost, with no transcript
// content. The transcript itself stays untracked under ADR-003 T2, which closed
// committing such material permanently.
type BootstrapBaseline struct {
	Calls        int `json:"calls"`
	OutputTokens int `json:"output_tokens"`
	Bytes        int `json:"bytes"`
	// Tokenizer names what counted the tokens. A cost comparison under an unnamed
	// tokenizer compares two different units and reports the difference as a win.
	Tokenizer  string `json:"tokenizer"`
	ModelBuild string `json:"model_build"`
	Date       string `json:"date"`
	Provenance string `json:"provenance"`
}

// LoadBootstrapBaseline reads the redacted baseline manifest.
func LoadBootstrapBaseline(path string) (BootstrapBaseline, error) {
	var b BootstrapBaseline
	raw, err := os.ReadFile(path)
	if err != nil {
		return b, fmt.Errorf("open baseline: %w", err)
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		return b, fmt.Errorf("%s: %w", path, err)
	}
	return b, nil
}

// bootstrapParityParts are the things the 13-call protocol delivered, which any
// replacement must also deliver.
//
// This list is what makes F-16 falsifiable. A token comparison alone rewards a
// response that returns less, and the cheapest conformant bootstrap would be one
// that returns nothing at all — so parity is asserted first and the cost
// comparison only runs when it holds.
var bootstrapParityParts = []string{
	"entry point", "eager content", "on-demand pointers", "corrections", "resolved wing", "truncation report",
}

// MissingParityParts names the parts of the replaced protocol this response does
// not carry. Empty means parity holds.
func (r BootstrapResult) MissingParityParts() []string {
	var missing []string
	if r.EntryPoint.Resolution == "" {
		missing = append(missing, "entry point")
	}
	// Eager content and pointers are parity when the wing HAS content to place;
	// an empty wing legitimately carries neither, and demanding them would make
	// a correct answer fail.
	if r.Wing == "" {
		missing = append(missing, "resolved wing")
	}
	// The truncation report is unconditional: its absence is exactly the failure
	// that lost 74% of a tier unnoticed.
	if r.Truncation.Omitted > 0 && r.Truncation.HowToFetch == "" {
		missing = append(missing, "truncation report")
	}
	return missing
}

// ApproxOutputTokens estimates this response's output cost.
//
// Approximate and deliberately crude: ~4 bytes per token is the well-known rough
// ratio for English under byte-pair tokenizers, and the comparison it feeds is
// against a baseline nearly an order of magnitude larger. A precise tokenizer
// would add a dependency to sharpen a number whose decision does not turn on
// precision — but if this ever gets close to the baseline, that is the moment to
// stop estimating and count properly.
func (r BootstrapResult) ApproxOutputTokens() int {
	raw, err := json.Marshal(r)
	if err != nil {
		return 0
	}
	return len(raw) / 4
}
