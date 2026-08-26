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
	// ShowValues names the fields whose distinct values may be QUOTED in the
	// memory. Everything else about a field is reported whether or not it is
	// named: type, presence, distinct count, date range.
	//
	// It is an allowlist rather than a list of exclusions because the two fail in
	// opposite directions, and only one of the failures is recoverable. A column
	// added to the dataset after this file was written is merely absent from the
	// next memory here; an exclusion list written before that column existed
	// publishes it. Publishing is the one that cannot be taken back — the content
	// is embedded and indexed on arrival, and a later re-import replaces the
	// drawer long after every session in the wing could already recall it.
	//
	// The cost is the omission being silent, and the profile answers that by
	// still reporting the distinct COUNT of an unnamed field: a reader sees that
	// six values exist and were not listed, which is a pointer rather than a
	// blank.
	ShowValues []string `toml:"show_values"`
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
// the run rather than of the moment each line happened to be written. It becomes
// the record's content_date and nothing else: a date inside the text would put
// the day of the run into the drawer's id (see drawerFor).
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
		p, err := ProfileJSONL(rc, d.ShowValues)
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

	// The measurement DATE is deliberately not written into this text. A drawer's
	// id is a hash of its content, so a date here would make tomorrow's run over
	// an unchanged file a different memory — one new drawer per run, each saying
	// the same thing. It travels as the record's content_date instead, which
	// every recall returns beside the text, so the snapshot is still dated
	// without the date deciding the drawer's identity.
	b.WriteString("MEASURED FROM THE FILE on the date this memory carries — these are facts about " +
		"THAT FILE, not constraints the domain guarantees.\n")
	fmt.Fprintf(&b, "· %d rows", p.Rows)
	if p.Malformed > 0 {
		fmt.Fprintf(&b, ", ⚠%d line(s) that did not parse as a JSON object — worth checking the "+
			"export that produced this", p.Malformed)
	}
	if p.Truncated {
		fmt.Fprintf(&b, ", ⚠READING STOPPED EARLY at a line over %d bytes, so every count here "+
			"describes only the part of the file that was reached", maxLineBytes)
	}
	b.WriteString("\n")

	for _, f := range p.Fields {
		fmt.Fprintf(&b, "· %s (%s)", f.Name, strings.Join(f.Types, "|"))
		if f.Present < p.Rows {
			fmt.Fprintf(&b, ", present in %d of %d rows", f.Present, p.Rows)
		}
		switch {
		case f.Compound:
			// Not "more than 25 distinct values", which is what a shared overflow
			// flag used to print here: a nested value is not a column of values, and
			// a false statement about the data is worse than an absent one.
			b.WriteString(", nested — its interior is not profiled")
		case f.Values != nil:
			fmt.Fprintf(&b, ", takes %d value(s) here: %s", f.Distinct, quoted(f.Values))
		case f.Distinct > maxDistinct:
			fmt.Fprintf(&b, ", more than %d distinct values", maxDistinct)
		default:
			fmt.Fprintf(&b, ", %d distinct value(s), not listed", f.Distinct)
		}
		if f.Earliest != "" {
			fmt.Fprintf(&b, ", ranging %s to %s", f.Earliest, f.Latest)
		}
		b.WriteString("\n")
	}

	// Said once, at the end, rather than beside each field: an unexplained "not
	// listed" reads as "this field has nothing interesting in it", which is the
	// opposite of what it means, and repeating the explanation per field would
	// crowd out the profile it is annotating.
	if p.Withheld > 0 {
		fmt.Fprintf(&b, "\n⚠%d field(s) above were COUNTED AND NOT QUOTED. Values appear only for "+
			"fields the mapping file names in show_values, because a drawer is recalled by every "+
			"agent in the wing and a column quoted here is published to all of them. The count is "+
			"the pointer: if a field's values are worth recalling, name it and re-import.\n", p.Withheld)
	}

	return wingbundle.Record{
		Kind: wingbundle.KindDrawer,
		Room: d.Room,
		// The dataset's own path is the identity, and the id the importer mints is
		// a hash of it together with this text — so a re-import over UNCHANGED data
		// upserts the same row however often it runs, which is what makes a
		// committed mapping file worth committing and a scheduled re-import safe.
		//
		// It does NOT replace the memory when the data has changed: the import path
		// absorbs and never purges by source (a batched migration would otherwise
		// delete the earlier batches of the source it is still uploading), so a
		// changed dataset files a NEW profile and yesterday's stays recallable
		// until someone deletes it. Receipted in docs/adr/BACKLOG.md §"From
		// ADR-035" rather than papered over here.
		SourceFile:  d.File,
		Content:     b.String(),
		Entities:    entitiesFor(p),
		ContentDate: measuredAt.Format("2006-01-02"),
	}
}

// quoted renders a value set for reading. An empty string is shown as "" rather
// than as a gap between two commas: "takes 2 value(s) here: , open" reads as a
// rendering fault instead of what it is — a column that is sometimes blank,
// which is worth knowing about seed data.
func quoted(vs []string) string {
	out := make([]string, len(vs))
	for i, v := range vs {
		if v == "" {
			v = `""`
		}
		out[i] = v
	}
	return strings.Join(out, ", ")
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
