package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/datasetdoc"
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
			&cli.StringFlag{Name: "push", Usage: "POST the bundle to a running server's /import instead of writing a file, e.g. https://example.com/import"},
			&cli.StringFlag{Name: "token", Sources: cli.EnvVars(mcpprotocol.TokenEnvVar), Usage: "workspace Bearer token for --push"},
			&cli.StringFlag{Name: "as", Usage: "wing to file every record into when pushing (the ?as= the endpoint takes). Self-hosted names it with `wing import --as` instead"},
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

	if o.push != "" {
		if err := pushBundle(ctx, o.push, o.token, o.as, buf.Bytes()); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "pushed %d dataset(s) to %s\n", n, o.push)
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
	if err := os.WriteFile(out, body, 0o644); err != nil {
		return fmt.Errorf("write bundle: %w", err)
	}
	return nil
}

// pushBundle POSTs the bundle to a running server's /import.
//
// The response body is included in a failure because the endpoint explains
// itself — an expired token and a malformed record produce different messages,
// and swallowing them would leave an operator with a status code and no lead.
func pushBundle(ctx context.Context, endpoint, token, as string, body []byte) error {
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse --push URL: %w", err)
	}
	if as != "" {
		q := u.Query()
		q.Set("as", as)
		u.RawQuery = q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/x-ndjson")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("push to %s: %w", endpoint, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("push to %s: %s: %s", endpoint, resp.Status, bytes.TrimSpace(detail))
	}
	return nil
}
