package datasetdoc

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/wingbundle"

	"github.com/pelletier/go-toml/v2"
)

// Config is the mapping file a project commits beside its data.
//
// It is TOML rather than JSON because its most important field is prose — the
// explanation of what a dataset is for — and TOML has multi-line strings and
// comments where JSON has neither. It lives in the project's own repository so
// the description of a dataset is reviewed in the same pull request as any
// change to the dataset itself.
type Config struct {
	// Wing is the destination this project's data belongs to. It is a default
	// and a piece of documentation, not a binding: the wing is finally named on
	// the way IN (`wing import --as`, or `?as=` on the endpoint), because a
	// bundle carries contents rather than a place.
	Wing string `toml:"wing"`
	// Datasets are the files to describe, in the order they should be read.
	Datasets []Dataset `toml:"dataset"`
}

// Dataset is one JSONL file and the human account of it.
type Dataset struct {
	// File is the path to the JSONL, relative to the mapping file.
	File string `toml:"file"`
	// Room is the aspect it is filed under, e.g. "refdata" or "schema".
	Room string `toml:"room"`
	// Title is the one-line answer to "what is this file".
	Title string `toml:"title"`
	// Why is the half no tool can measure: what the dataset is for, why it looks
	// like this, what a field means that its name does not say. It is REQUIRED —
	// a dataset drawer carrying only a profile records what a reader could have
	// derived themselves, and filing it would spend recall on nothing.
	Why string `toml:"why"`
}

// ParseConfig reads a mapping file and refuses one that cannot produce a useful
// memory.
//
// Every refusal here is a case where filing would appear to succeed: a config
// with no datasets writes an empty bundle, and a dataset with no explanation
// writes a drawer that says only what the file already says. Both are the shape
// this repository keeps naming — an operation that reports success and leaves
// nothing worth reading.
func ParseConfig(r io.Reader) (Config, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := toml.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config: %w", err)
	}
	if len(cfg.Datasets) == 0 {
		return Config{}, fmt.Errorf("config declares no [[dataset]] — an import with nothing to " +
			"describe would write an empty bundle and report success")
	}
	for i, d := range cfg.Datasets {
		switch {
		case strings.TrimSpace(d.File) == "":
			return Config{}, fmt.Errorf("dataset %d: file is required", i+1)
		case strings.TrimSpace(d.Room) == "":
			return Config{}, fmt.Errorf("dataset %d (%s): room is required — a drawer with no room "+
				"is filed where nobody looks", i+1, d.File)
		case strings.TrimSpace(d.Title) == "":
			return Config{}, fmt.Errorf("dataset %d (%s): title is required", i+1, d.File)
		case strings.TrimSpace(d.Why) == "":
			return Config{}, fmt.Errorf("dataset %d (%s): why is required — a profile without it "+
				"records only what the file already says, and the explanation is the half worth "+
				"recalling", i+1, d.File)
		}
	}
	return cfg, nil
}

// Opener resolves a dataset's declared path to a reader. It is a parameter
// rather than a direct os.Open so the bundler can be driven from a test without
// a filesystem, and so a caller can resolve paths relative to the mapping file
// rather than to the working directory.
type Opener func(path string) (io.ReadCloser, error)

// Bundle profiles every dataset and writes the result as bundle NDJSON.
//
// measuredAt is passed in rather than read from the clock so a caller can
// produce a deterministic bundle, and so the date on the drawer is the date of
// the run rather than of the moment each line happened to be written.
//
// A dataset that cannot be opened is a hard error. Skipping it would leave the
// mapping file describing a dataset the bundle does not contain, and the import
// would succeed while quietly dropping the thing someone asked for.
func Bundle(cfg Config, open Opener, measuredAt time.Time, w io.Writer) (int, error) {
	enc := json.NewEncoder(w)
	if err := enc.Encode(wingbundle.Record{
		Kind:   wingbundle.KindManifest,
		Format: wingbundle.Format,
		Total:  len(cfg.Datasets),
	}); err != nil {
		return 0, fmt.Errorf("write manifest: %w", err)
	}

	written := 0
	for _, d := range cfg.Datasets {
		rc, err := open(d.File)
		if err != nil {
			return written, fmt.Errorf("dataset %q: %w", d.File, err)
		}
		p, err := ProfileJSONL(rc)
		closeErr := rc.Close()
		if err != nil {
			return written, fmt.Errorf("dataset %q: %w", d.File, err)
		}
		if closeErr != nil {
			return written, fmt.Errorf("dataset %q: close: %w", d.File, closeErr)
		}
		if err := enc.Encode(drawerFor(d, p, measuredAt)); err != nil {
			return written, fmt.Errorf("dataset %q: write record: %w", d.File, err)
		}
		written++
	}
	return written, nil
}

// drawerFor composes one dataset's memory: the human account first, the measured
// profile second.
//
// The order is deliberate. Recall returns a window around the match, and the
// explanation is what a reader needs to understand any of the numbers below it —
// so the half that cannot be re-derived goes where a snippet will find it.
func drawerFor(d Dataset, p Profile, measuredAt time.Time) wingbundle.Record {
	var b strings.Builder

	// A question as the first line, matching how memories in this palace are
	// written: recall matches a question better than it matches a filename.
	fmt.Fprintf(&b, "WHAT IS IN %s, AND WHY DOES IT LOOK LIKE THIS? — %s\n\n", d.File, d.Title)
	b.WriteString(strings.TrimSpace(d.Why))
	b.WriteString("\n\n")

	fmt.Fprintf(&b, "MEASURED FROM THE FILE on %s — these are facts about THIS FILE, not "+
		"constraints the domain guarantees.\n", measuredAt.Format("2006-01-02"))
	fmt.Fprintf(&b, "· %d rows", p.Rows)
	if p.Malformed > 0 {
		fmt.Fprintf(&b, ", ⚠%d line(s) that did not parse as a JSON object — worth checking the "+
			"export that produced this", p.Malformed)
	}
	b.WriteString("\n")

	for _, f := range p.Fields {
		fmt.Fprintf(&b, "· %s (%s)", f.Name, strings.Join(f.Types, "|"))
		if f.Present < p.Rows {
			fmt.Fprintf(&b, ", present in %d of %d rows", f.Present, p.Rows)
		}
		switch {
		case f.Values != nil:
			fmt.Fprintf(&b, ", takes %d value(s) here: %s", f.Distinct, strings.Join(f.Values, ", "))
		case f.Distinct > maxDistinct:
			fmt.Fprintf(&b, ", more than %d distinct values", maxDistinct)
		}
		if f.Earliest != "" {
			fmt.Fprintf(&b, ", ranging %s to %s", f.Earliest, f.Latest)
		}
		b.WriteString("\n")
	}

	if p.Example != "" {
		fmt.Fprintf(&b, "\nONE ROW, VERBATIM:\n%s\n", p.Example)
	}

	return wingbundle.Record{
		Kind: wingbundle.KindDrawer,
		Room: d.Room,
		// The dataset's own path is the identity. Import is idempotent by source,
		// so re-running after the data changes REPLACES this memory rather than
		// filing a second one beside it — which is what makes the mapping file
		// worth committing.
		SourceFile:  d.File,
		Content:     b.String(),
		Entities:    entitiesFor(p),
		ContentDate: measuredAt.Format("2006-01-02"),
	}
}

// entitiesFor names the field set, so a search for a column name reaches the
// dataset that has it. Bounded, and drawn from the data rather than from prose.
func entitiesFor(p Profile) []string {
	names := make([]string, 0, len(p.Fields))
	for _, f := range p.Fields {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	if len(names) > maxDistinct {
		names = names[:maxDistinct]
	}
	return names
}
