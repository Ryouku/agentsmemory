package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/datasetdoc"
	"github.com/atvirokodosprendimai/agentsmemory/internal/importer"
	"github.com/atvirokodosprendimai/agentsmemory/internal/mcpprotocol"

	"github.com/urfave/cli/v3"
)

// importCommand describes a project's JSONL datasets and files them as memories.
//
// It produces the SAME bundle `wing import` and `POST /import` already consume,
// which is why neither of them needed changing: self-hosted files the bundle
// straight into a database, hosted posts it behind the Bearer gate, and this
// command is only the producer that was missing.
func importCommand() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "Describe a project's JSONL datasets and file them as memories",
		Description: "Reads a mapping file committed beside the data and writes one memory per dataset:\n" +
			"the explanation a person wrote, followed by a profile MEASURED from the file itself —\n" +
			"fields, types, row count, the values a small field actually takes, date ranges.\n\n" +
			"Rows are deliberately not imported. They are already in whatever database the same\n" +
			"JSONL builds, which answers questions about rows better than a vector search, and\n" +
			"filing thousands of them would push every other memory in the wing down its own recall.\n\n" +
			"The bundle carries no wing and no vectors: the destination is named on the way IN, and\n" +
			"the server embeds, so a bundle can never carry vectors from a different model.",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "config", Required: true, Usage: "mapping file (TOML) listing each dataset, its room, and what it is for. Dataset paths resolve relative to this file"},
			&cli.StringFlag{Name: "out", Value: "-", Usage: "write the bundle here ('-' for stdout). Ignored when --push is given and this was not set explicitly"},
			&cli.StringFlag{Name: "push", Usage: "POST the bundle to a running server's /import instead of writing a file, e.g. https://example.com/import. https only, except a loopback host"},
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars(mcpprotocol.TokenEnvVar), Usage: "workspace Bearer token for --push"},
			&cli.StringFlag{Name: "as", Usage: "wing to file every record into, REQUIRED with --push (the ?as= the endpoint takes): a bundle carries no wing, and the endpoint skips a record it cannot address. Self-hosted names it with `wing import --as` instead"},
		},
		Action: func(ctx context.Context, c *cli.Command) error {
			return runImport(ctx, importOptions{
				configPath: c.String("config"),
				out:        c.String("out"),
				outSet:     c.IsSet("out"),
				push:       c.String("push"),
				token:      c.String("token"),
				as:         c.String("as"),
			})
		},
	}
}

// importOptions is the resolved command line, split out so runImport can be
// driven by a test without building a cli.Command.
type importOptions struct {
	configPath string
	out        string
	outSet     bool
	push       string
	token      string
	as         string
}

// runImport builds the bundle and delivers it.
func runImport(ctx context.Context, o importOptions) error {
	// A wing named with no destination to send it to would be silently discarded,
	// and ADR-006 is the standing rule that a knob doing nothing must say so.
	if o.as != "" && o.push == "" {
		return fmt.Errorf("--as applies to --push; a bundle carries no wing of its own, so " +
			"self-hosted you name the destination on the way in: agentsmemory wing import --as <wing>")
	}
	if o.push != "" && o.token == "" {
		return fmt.Errorf("--push needs --token (or $%s): /import is behind the same Bearer "+
			"gate as /mcp", mcpprotocol.TokenEnvVar)
	}
	// And the reverse, which is the worse half: a bundle carries no wing, the
	// endpoint files a record into whatever ?as= names, and the importer SKIPS a
	// record it cannot address rather than refusing it. So a push with no --as
	// uploads the whole bundle, stores nothing, and answers 200 — the shape this
	// repository keeps shipping. `wing import` demands the same flag for the same
	// reason.
	if o.push != "" && o.as == "" {
		return fmt.Errorf("--push needs --as: the bundle carries no wing, so the endpoint would " +
			"file every dataset into an unnamed wing — which it skips, and still reports success")
	}
	target, err := pushTarget(o.push, o.as)
	if err != nil {
		return err
	}

	f, err := os.Open(o.configPath)
	if err != nil {
		return fmt.Errorf("open config: %w", err)
	}
	cfg, err := datasetdoc.ParseConfig(f)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return fmt.Errorf("close config: %w", closeErr)
	}

	// Dataset paths are relative to the MAPPING FILE, not to the working
	// directory: the mapping file is committed beside the data, so its own
	// directory is the only stable base. Resolving against the cwd would make the
	// same committed config work or fail depending on where it was run from.
	base := filepath.Dir(o.configPath)
	open := func(p string) (io.ReadCloser, error) {
		return os.Open(filepath.Join(base, filepath.FromSlash(p)))
	}

	var buf bytes.Buffer
	n, err := datasetdoc.Bundle(cfg, open, time.Now().UTC(), &buf)
	if err != nil {
		return err
	}

	if target != nil {
		if err := pushBundle(ctx, target, o.token, buf.Bytes(), n); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "pushed %d dataset(s) to %s, filed into %s\n", n, o.push, o.as)
		if !o.outSet {
			return nil
		}
	}
	if err := writeBundle(o.out, buf.Bytes()); err != nil {
		return err
	}
	if o.push == "" {
		wing := cfg.Wing
		if wing == "" {
			wing = "<wing>"
		}
		fmt.Fprintf(os.Stderr, "wrote %d dataset(s); file it with: agentsmemory wing import --file %s --as %s\n",
			n, o.out, wing)
	}
	return nil
}

// writeBundle sends the bundle to a path, or to stdout for "-".
func writeBundle(out string, body []byte) error {
	if out == "-" {
		_, err := os.Stdout.Write(body)
		return err
	}
	// 0600, matching `wing export`: a bundle is a palace's contents in plain text,
	// and this one holds whatever the mapping file allowed to be quoted out of a
	// project's data. A world-readable file on a shared host is a disclosure the
	// mapping file's allowlist was written to prevent.
	if err := os.WriteFile(out, body, 0o600); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	return nil
}

// pushTarget resolves --push into the URL the bundle is POSTed to, or nil when
// there is nothing to push to.
//
// It refuses a cleartext endpoint because the workspace token travels with the
// request: over plain http the Authorization header is readable by anything on
// the path, and a leaked workspace token is read/write access to the whole
// palace. Loopback is exempt — a self-hosted server on this machine has no path
// to be listened on, and `--local` binds exactly that.
//
// recompute=1 is set because this is a single-shot import. Hallways and entity
// tunnels are DERIVED from the drawers, so without it the new memories are
// filed but stay outside the graph until something else rebuilds it; the
// batched migration client sets it on its finalize request for the same reason,
// and `wing import` passes the same flag straight to Ingest.
func pushTarget(endpoint, as string) (*url.URL, error) {
	if endpoint == "" {
		return nil, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse --push URL: %w", err)
	}
	switch {
	case u.Scheme == "https":
	case u.Scheme == "http" && isLoopback(u.Hostname()):
	default:
		return nil, fmt.Errorf("--push %s must be an https:// URL (http:// only for a loopback "+
			"host): the workspace token is sent with the bundle, and in cleartext it is readable "+
			"by anything between here and the server", endpoint)
	}
	q := u.Query()
	q.Set("as", as)
	q.Set("recompute", "1")
	u.RawQuery = q.Encode()
	return u, nil
}

// isLoopback reports whether host names this machine, so an http:// endpoint
// there carries the token no further than the process next door.
func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// pushBundle POSTs the bundle to a running server's /import and reads what the
// server says it did.
//
// The status code is not the answer, and treating it as one is how a push files
// nothing and reports success. /import consumes the whole body before replying,
// so a decode or storage failure comes back inside a 200 as Result.Error, and a
// record it cannot address is counted into Skipped rather than refused. So the
// summary is decoded and checked against what was sent: wanted is how many
// datasets the bundle carried, and anything less than that landing is a failure
// the operator has to hear about.
//
// The response body is included in a transport failure too, because the endpoint
// explains itself — an expired token and a malformed record produce different
// messages, and swallowing them would leave an operator with a status code and
// no lead.
func pushBundle(ctx context.Context, u *url.URL, token string, body []byte, wanted int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("push to %s: %w", u.Redacted(), err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("push to %s: %s: %s", u.Redacted(), resp.Status, bytes.TrimSpace(detail))
	}

	var res importer.Result
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&res); err != nil {
		return fmt.Errorf("push to %s: %s, but the summary did not decode, so what was filed is "+
			"unknown: %w", u.Redacted(), resp.Status, err)
	}
	if res.Error != "" {
		return fmt.Errorf("push to %s: the server answered %s and reported: %s",
			u.Redacted(), resp.Status, res.Error)
	}
	if res.Drawers != wanted {
		return fmt.Errorf("push to %s: %d of %d dataset(s) were filed (%d skipped) — a skipped "+
			"record is one the server could not address, so nothing is recallable for it",
			u.Redacted(), res.Drawers, wanted, res.Skipped)
	}
	return nil
}
