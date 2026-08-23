package palace

import (
	"strings"
	"testing"
)

// TestGatedArmIsTheShippedShape: the pre-registered arm must be the one the
// SHIPPED configuration ranks with.
//
// It was a constant under a comment saying it "must change in the same commit
// that changes production ranking". ADR-014 changed production on two dimensions
// — fusion to rrf and the closet prior off — and the constant stayed at
// ArmReranked, which is linear plus a full closet prior: a pipeline nobody runs.
// Both arms appear in the report, so the lookup found one and reported nothing
// amiss. A rule enforced by remembering is a rule that eventually is not.
//
// It is now derived from the same mapping a running service uses, so there is
// one mapping rather than two that can disagree.
func TestGatedArmIsTheShippedShape(t *testing.T) {
	if got := SupersessionGatedArm(); got != ArmRRFReranked {
		t.Errorf("the shipped shape (rrf, prior retired, reranker configured) gates on %q, want %q",
			got, ArmRRFReranked)
	}
	if got := defaultShapeService().gatedArm(false); got != ArmRRF {
		t.Errorf("the shipped shape WITHOUT a reranker gates on %q, want %q — a deployment with no "+
			"RERANK_URL is the common one, and it must not be refused as degraded", got, ArmRRF)
	}
}

// TestGatedArmIsARegisteredArm: an arm the gate names but the report never
// contains makes every verdict "no comparable arm", which reads as a corpus
// problem rather than a wiring one.
func TestGatedArmIsARegisteredArm(t *testing.T) {
	registered := map[EvalArm]bool{}
	for _, a := range evalArms(EvalOptions{Contextual: true}, true) {
		registered[a] = true
	}
	// Every configuration the mapping is willing to NAME must be an arm the run
	// actually registers. A name nothing registers makes every verdict read
	// "no comparable arm", which looks like a corpus problem and is a wiring one.
	for _, tc := range []struct {
		name string
		svc  *Service
	}{
		{"shipped shape", defaultShapeService()},
		{"bare", NewService(nil, nil, nil, 0)},
		{"linear, fixed 0.4, prior on", NewService(nil, nil, nil, 0).WithBM25Weight(false, hybridBM25Weight)},
		{"linear, adaptive, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0)},
		{"linear, adaptive idf, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithLexicalIDF(true)},
		{"saturating, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithLexNorm("saturating")},
		{"fixed 0.6 ceiling, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithBM25Weight(false, 0.6).WithLexNorm("ceiling")},
	} {
		for _, reranked := range []bool{true, false} {
			arm := tc.svc.gatedArm(reranked)
			if arm == "" {
				continue // an unrepresentable shape names nothing, which is the point
			}
			if !registered[arm] {
				t.Errorf("%s (rerank=%v) gates on %q, which evalArms never registers — every verdict "+
					"would report 'no comparable arm'", tc.name, reranked, arm)
			}
		}
	}
}

// TestServiceReportsItsOwnGatedArm: a run against a non-default configuration
// must be gated on the arm IT serves — or on none, when no arm reconstructs it.
func TestServiceReportsItsOwnGatedArm(t *testing.T) {
	for _, tc := range []struct {
		name string
		svc  func() *Service
		want EvalArm
	}{
		{
			// A bare service fuses ADAPTIVELY (bm25Auto defaults on) and carries a
			// full closet prior. No arm does both: every closet-bearing arm is
			// built on rankHybrid, which is the FIXED 0.4 weight. This read as
			// "hybrid+closet" until a reviewer checked the functions rather than
			// the names.
			name: "linear, adaptive weight, full closet",
			svc:  func() *Service { return NewService(nil, nil, nil, 0) },
			want: "",
		},
		{
			name: "rrf with a closet prior — no RRF arm carries one",
			svc:  func() *Service { return NewService(nil, nil, nil, 0).WithFusion("rrf") },
			want: "",
		},
		{
			name: "the shipped default without a reranker",
			svc:  func() *Service { return NewService(nil, nil, nil, 0).WithFusion("rrf").WithClosetBoost(0) },
			want: ArmRRF,
		},
		{
			name: "linear, adaptive weight, prior retired",
			svc:  func() *Service { return NewService(nil, nil, nil, 0).WithClosetBoost(0) },
			want: ArmAdaptive,
		},
		{
			name: "linear, adaptive IDF weight, prior retired",
			svc: func() *Service {
				return NewService(nil, nil, nil, 0).WithClosetBoost(0).WithLexicalIDF(true)
			},
			want: ArmAdaptiveIDF,
		},
		{
			name: "linear, the fixed 0.4 weight, prior retired",
			svc: func() *Service {
				return NewService(nil, nil, nil, 0).WithClosetBoost(0).WithBM25Weight(false, hybridBM25Weight)
			},
			want: ArmHybrid,
		},
		{
			name: "linear, fixed 0.4, closet prior in force",
			svc: func() *Service {
				return NewService(nil, nil, nil, 0).WithBM25Weight(false, hybridBM25Weight)
			},
			want: ArmHybridCloset,
		},
		{
			name: "an anchored normaliser the sweep does register",
			svc: func() *Service {
				return NewService(nil, nil, nil, 0).WithClosetBoost(0).WithLexNorm("saturating")
			},
			want: anchoredArm(ArmAdaptive, "saturating"),
		},
		{
			// 0.35 is nobody's swept weight, so no arm ranks with it.
			name: "a fixed weight outside the sweep",
			svc: func() *Service {
				return NewService(nil, nil, nil, 0).WithClosetBoost(0).WithBM25Weight(false, 0.35)
			},
			want: "",
		},
	} {
		if got := tc.svc().SupersessionGatedArmFor(); got != tc.want {
			t.Errorf("%s: reported %q, want %q", tc.name, got, tc.want)
		}
	}
}

// TestGatedArmReconstructsTheServedRanking is the check that cannot be
// satisfied by a plausible name.
//
// For every configuration that names an arm, it runs BOTH rankers over the same
// candidates and compares the ORDER: production's own fusionRanker against the
// ranker the eval scores that arm with, boosts included exactly as armBoosts
// decides them. A named arm whose order differs from production is the defect
// this mechanism exists to prevent — the gate judging a pipeline nobody runs.
//
// It asserts nothing about the configurations that name NO arm. Two rankers
// agreeing on a fixture does not make them the same function: a 0.35 lexical
// weight orders identically to 0.40 on almost any input, so "some arm matched"
// would fire constantly and be learned as noise. What keeps "" from swallowing
// everything is the mapping table above plus the shipped-default assertion at
// the end of this test.
func TestGatedArmReconstructsTheServedRanking(t *testing.T) {
	// A battery rather than one page: rankers differ from each other only on
	// inputs where the lexical and vector channels disagree, so a single fixture
	// lets two different functions look identical.
	type fixture struct {
		query  string
		docs   []string
		dists  []float64
		closet []float64
	}
	fixtures := []fixture{
		{
			query: "rerank pool size",
			docs: []string{
				"the rerank pool ships at ten because a cross encoder is linear in pool size",
				"pool size and rerank latency, measured: twenty two seconds at fifty",
				"an unrelated memory about cache invalidation and ttl defaults",
				"rerank pool ten pool ten pool ten, a document that repeats the terms",
				"nothing here matches anything in the query at all",
			},
			dists:  []float64{0.21, 0.24, 0.55, 0.30, 0.61},
			closet: []float64{0.4, 0, 0, 0.25, 0},
		},
		{
			// The lexical winner is the vector loser: this is where a weight change
			// actually moves the order.
			query: "supersession bar",
			docs: []string{
				"a note that never says the word, but sits close in embedding space",
				"supersession supersession supersession bar bar bar, lexically overwhelming",
				"the bar was set at one tenth and the supersession rate measured against it",
				"unrelated",
			},
			dists:  []float64{0.10, 0.58, 0.34, 0.62},
			closet: []float64{0, 0.4, 0, 0},
		},
		{
			// One long document and three short ones: BM25 length normalisation is
			// the term that separates the fixed weights from the adaptive ones.
			query: "closet prior",
			docs: []string{
				"closet prior " + strings.Repeat("filler words that dilute the term frequency ", 40),
				"closet prior",
				"prior",
				"closet",
			},
			dists:  []float64{0.30, 0.31, 0.29, 0.33},
			closet: []float64{0, 0, 0.4, 0},
		},
		{
			// No lexical signal at all: every ranker collapses to vector order, so
			// this one separates nothing about the lexical term and is here to pin
			// that they agree where they must.
			query: "wholly absent vocabulary",
			docs: []string{
				"alpha beta gamma", "delta epsilon", "zeta eta theta", "iota kappa",
			},
			dists:  []float64{0.4, 0.2, 0.6, 0.3},
			closet: []float64{0, 0, 0, 0.4},
		},
		{
			// PARTIAL coverage — three of the five query terms appear nowhere — is
			// what separates an adaptive weight from a fixed one: adaptive scales
			// the lexical half down by how much signal the query actually has, and
			// here that flips the top two. Without a fixture like this, naming
			// ArmHybrid for an adaptive service passes every equivalence check,
			// which is exactly what it did on the first version of this battery.
			query: "rerank pool zzzzq qqqqz wwwwq",
			docs: []string{
				"pool pool pool pool rerank rerank",
				"a close neighbour in embedding space with no shared words",
				"rerank once",
				"unrelated filler",
			},
			dists:  []float64{0.45, 0.12, 0.40, 0.60},
			closet: []float64{0, 0.4, 0, 0},
		},
		{
			// One rare term against one common one: the IDF-weighted coverage
			// feature reads these differently from plain coverage, so this is what
			// separates ArmAdaptive from ArmAdaptiveIDF.
			query: "pool",
			docs: []string{
				"pool pool pool pool", "vector neighbour", "pool", "x",
			},
			dists:  []float64{0.50, 0.14, 0.44, 0.7},
			closet: []float64{0, 0, 0.4, 0},
		},
		{
			query: "alpha zzz",
			docs: []string{
				"alpha alpha alpha", "near neighbour", "alpha", "none",
			},
			dists:  []float64{0.48, 0.16, 0.42, 0.7},
			closet: []float64{0.4, 0, 0, 0},
		},
	}

	orderOf := func(hs []HybridScore) []int {
		out := make([]int, 0, len(hs))
		for _, h := range hs {
			out = append(out, h.Index)
		}
		return out
	}
	same := func(a, b []int) bool {
		if len(a) != len(b) {
			return false
		}
		for i := range a {
			if a[i] != b[i] {
				return false
			}
		}
		return true
	}

	named := 0
	for _, tc := range []struct {
		name string
		svc  *Service
	}{
		{"bare", NewService(nil, nil, nil, 0)},
		{"rrf with prior", NewService(nil, nil, nil, 0).WithFusion("rrf")},
		{"rrf, prior off", NewService(nil, nil, nil, 0).WithFusion("rrf").WithClosetBoost(0)},
		{"adaptive, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0)},
		{"adaptive idf, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithLexicalIDF(true)},
		{"fixed 0.4, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithBM25Weight(false, hybridBM25Weight)},
		{"fixed 0.4, prior on", NewService(nil, nil, nil, 0).WithBM25Weight(false, hybridBM25Weight)},
		{"fixed 0.2, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithBM25Weight(false, 0.2)},
		{"saturating, prior off", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithLexNorm("saturating")},
		{"ceiling at a swept weight", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithBM25Weight(false, 0.6).WithLexNorm("ceiling")},
		{"weight outside the sweep", NewService(nil, nil, nil, 0).WithClosetBoost(0).WithBM25Weight(false, 0.35)},
	} {
		arm := tc.svc.SupersessionGatedArmFor()
		if arm == "" {
			continue
		}
		named++
		ranker := fusionRankerFor(arm, hybridBM25Weight)
		for i, f := range fixtures {
			boosts := f.closet
			if tc.svc.closetBoostScale == 0 {
				boosts = make([]float64, len(f.closet))
			}
			served := orderOf(tc.svc.fusionRanker()(f.query, f.docs, f.dists, boosts))

			var got []int
			switch {
			case ranker != nil:
				got = orderOf(ranker(f.query, f.docs, f.dists, armBoosts(arm, f.closet)))
			case arm == ArmRRF:
				got = orderOf(rankRRF(f.query, f.docs, f.dists, armBoosts(arm, f.closet)))
			default:
				continue // a reranked arm: needs a live cross-encoder, covered elsewhere
			}
			if !same(got, served) {
				t.Errorf("%s / fixture %d: the gate names %q, but that arm orders %v while production "+
					"orders %v — the gate would judge a pipeline nobody runs", tc.name, i, arm, got, served)
			}
		}
	}
	// A mapping that refused everything would satisfy every assertion above.
	if named < 8 {
		t.Errorf("only %d of the configurations named an arm; the mapping has collapsed into refusing "+
			"everything, which passes every equivalence check by measuring nothing", named)
	}
}
