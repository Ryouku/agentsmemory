package palace

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
)

// FactCorpusManifest is the REDACTED record of a fact-retrieval corpus.
//
// It exists because the corpus itself may not be committed. ADR-003 T2 closed
// that permanently: case files carry queries and drawer ids from a private
// palace. So the repository holds a synthetic fixture the tests run against, and
// this manifest — counts, a hash, provenance and a date — as the auditable trace
// of the REAL run. Anyone with palace access can recompute the hash and check the
// two describe the same corpus; anyone without learns nothing from it.
//
// Every field here is an aggregate on purpose. Adding one that carries case text,
// a drawer id or a triple id would reintroduce exactly what the manifest exists
// to avoid, and TestADR036FixturesCarryNoPrivatePalaceContent fails when one does.
type FactCorpusManifest struct {
	// Cases is the denominator. A rate quoted without it is not a result: 40% over
	// 5 cases and 40% over 200 are different claims, and only one of them survives
	// a single case changing.
	Cases int `json:"cases"`
	// CorpusSHA256 pins WHICH corpus produced the numbers, without disclosing it.
	CorpusSHA256 string `json:"corpus_sha256"`
	// Provenance names where the corpus came from in terms a reader can act on,
	// without naming private content.
	Provenance string `json:"provenance"`
	// Date is when it was taken. An undated measurement is unfalsifiable a month
	// later and actively misleading once the data moves.
	Date string `json:"date"`
}

// FactAnswerRate is an answerable-rate that cannot be quoted without its
// denominator.
//
// The type exists to make that structural rather than a convention: a bare
// float64 can be printed as "0.40" by any caller, and the denominator is exactly
// what a reader needs to know whether the number means anything.
type FactAnswerRate struct {
	Answered int
	Cases    int
}

// Fraction returns the rate in [0,1], or 0 when the corpus is empty. An empty
// corpus scores zero rather than dividing by zero, and String makes the emptiness
// visible so a 0/0 is never read as a measured failure.
func (r FactAnswerRate) Fraction() float64 {
	if r.Cases == 0 {
		return 0
	}
	return float64(r.Answered) / float64(r.Cases)
}

// String always carries the denominator, e.g. "12/30 (40.0%)".
func (r FactAnswerRate) String() string {
	return fmt.Sprintf("%d/%d (%.1f%%)", r.Answered, r.Cases, 100*r.Fraction())
}

// FactAnswerRateFrom derives the rate from per-case ranks, where a rank of 0
// means the gold triple never reached the response.
//
// It reads the same Ranks slice every other arm produces, which is what lets the
// fact arm feed BootstrapMRR and PairedDelta unchanged — F-6's requirement is met
// by producing ranks, not by inventing a second statistics path.
func FactAnswerRateFrom(ranks []int) FactAnswerRate {
	r := FactAnswerRate{Cases: len(ranks)}
	for _, rank := range ranks {
		if rank > 0 {
			r.Answered++
		}
	}
	return r
}

// LoadFactCases reads a fact-case fixture in JSON Lines form.
//
// Every row must be marked synthetic and must carry a gold triple. The synthetic
// marker is not decoration: it is the one machine-checkable difference between a
// fixture that may be committed and a corpus that may not, and the hygiene gate
// reads it.
func LoadFactCases(path string) ([]EvalCase, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open fact cases: %w", err)
	}
	defer f.Close()
	return parseFactCases(f, path)
}

type factCaseRow struct {
	Question     string `json:"question"`
	ExpectTriple string `json:"expect_triple"`
	Wing         string `json:"wing,omitempty"`
	Synthetic    bool   `json:"synthetic"`
}

func parseFactCases(r io.Reader, path string) ([]EvalCase, error) {
	var cases []EvalCase
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "//") {
			continue
		}
		var row factCaseRow
		if err := json.Unmarshal([]byte(text), &row); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		if !row.Synthetic {
			// Refuse loudly rather than load it. A committed fixture that is not
			// marked synthetic is either mismarked or is real palace content, and
			// the second is the case worth stopping for.
			return nil, fmt.Errorf("%s:%d: fact case is not marked synthetic; committed fixtures must be", path, line)
		}
		if row.Question == "" || row.ExpectTriple == "" {
			return nil, fmt.Errorf("%s:%d: fact case needs both a question and a gold triple", path, line)
		}
		cases = append(cases, EvalCase{
			Query:        row.Question,
			ExpectTriple: row.ExpectTriple,
			Wing:         row.Wing,
			Category:     CatFact,
		})
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("%s: no fact cases; an empty corpus makes every rate 0/0", path)
	}
	return cases, nil
}

// LoadFactCorpusManifest reads the redacted manifest and checks it is internally
// consistent with the fixture it claims to describe.
func LoadFactCorpusManifest(path string) (FactCorpusManifest, error) {
	var m FactCorpusManifest
	b, err := os.ReadFile(path)
	if err != nil {
		return m, fmt.Errorf("open manifest: %w", err)
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("%s: %w", path, err)
	}
	if m.Cases <= 0 || m.CorpusSHA256 == "" || m.Provenance == "" || m.Date == "" {
		return m, fmt.Errorf("%s: manifest must carry cases, corpus_sha256, provenance and date", path)
	}
	return m, nil
}

// CorpusDigest hashes a corpus file so a manifest can pin WHICH corpus produced a
// number without carrying any of its content.
func CorpusDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
