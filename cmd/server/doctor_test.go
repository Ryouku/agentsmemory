package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/urfave/cli/v3"
)

// TestDoctorIsRegistered: a command nothing registers is a command nobody can
// run, and this repository has shipped that shape four times. The check reads
// the CLI's own command list rather than the source, so it fails for the reason
// a user would notice — `agentsmemory doctor` not existing.
func TestDoctorIsRegistered(t *testing.T) {
	root := rootCommand(config.Default())
	var names []string
	for _, c := range root.Commands {
		names = append(names, c.Name)
	}
	found := false
	for _, n := range names {
		if n == "doctor" {
			found = true
		}
	}
	if !found {
		t.Errorf("the CLI registers %v and not \"doctor\" — the check exists and cannot be run", names)
	}
}

// TestDoctorRefusesWithNoCheckSelected: `doctor` with no flag must not report a
// clean palace. A check that ran nothing and exited 0 is indistinguishable from
// one that passed, which is the failure mode the whole command exists to remove.
func TestDoctorRefusesWithNoCheckSelected(t *testing.T) {
	cmd := doctorCommand(config.Default())
	err := cmd.Run(context.Background(), []string{"doctor"})
	if err == nil {
		t.Error("doctor with no check selected exited 0, which reads as a healthy palace")
	}
	if err != nil && !strings.Contains(err.Error(), "--index") {
		t.Errorf("the refusal does not name the flag that would run a check: %v", err)
	}
}

// TestDoctorIndexExitsNonZeroOnDrift pins that the VERDICT is the exit code.
//
// The report is prose and prose is not a gate. A drift that printed a warning
// and exited 0 would sit green in every pipeline that runs this.
func TestDoctorIndexExitsNonZeroOnDrift(t *testing.T) {
	clean := palace.DriftReport{Checked: 5}
	drifted := palace.DriftReport{Checked: 5, Total: 1, Drifted: []palace.DriftedPoint{
		{Store: "index", DrawerID: "d1", Indexed: "wing_acme-legacy", Actual: "wing_acme"},
	}}

	var buf bytes.Buffer
	if err := reportDrift(&buf, clean); err != nil {
		t.Errorf("a clean palace exited non-zero: %v", err)
	}
	if !strings.Contains(buf.String(), "agrees") {
		t.Errorf("a clean report does not say so: %q", buf.String())
	}

	buf.Reset()
	err := reportDrift(&buf, drifted)
	if err == nil {
		t.Error("drift was reported and the command exited 0 — the verdict has to be the exit code")
	}
	out := buf.String()
	for _, want := range []string{"d1", "wing_acme-legacy", "wing_acme", "index"} {
		if !strings.Contains(out, want) {
			t.Errorf("the report does not name %q, so a reader cannot act on it:\n%s", want, out)
		}
	}
}

var _ = cli.Command{}

// TestDoctorDistinguishesAnAbsentPointFromAMislabelledOne: they need different
// actions, so they must not read the same.
//
// A mislabelled memory answers the wrong wing; an ABSENT one answers nothing at
// all and is fixed by a sync rather than by a merge. Reporting them identically
// sends an operator to the wrong repair.
func TestDoctorDistinguishesAnAbsentPointFromAMislabelledOne(t *testing.T) {
	var buf bytes.Buffer
	err := reportDrift(&buf, palace.DriftReport{Checked: 2, Total: 2, Drifted: []palace.DriftedPoint{
		{Store: "index", DrawerID: "d1", Indexed: "wing_acme-legacy", Actual: "wing_acme"},
		{Store: "index", DrawerID: "d2", Actual: "wing_acme", Missing: true},
	}})
	if err == nil {
		t.Error("drift reported and the command exited 0")
	}
	out := buf.String()
	if !strings.Contains(out, "ABSENT") {
		t.Errorf("a drawer with no point at all is not marked absent:\n%s", out)
	}
	if !strings.Contains(out, "sync") {
		t.Errorf("the report does not name the repair for an absent point:\n%s", out)
	}
}

// TestDoctorBoundsItsListingAndKeepsTheCountExact: a fully drifted palace must
// produce a report a human can read, and a count they can trust.
func TestDoctorBoundsItsListingAndKeepsTheCountExact(t *testing.T) {
	var buf bytes.Buffer
	_ = reportDrift(&buf, palace.DriftReport{Checked: 5000, Total: 5000, Drifted: []palace.DriftedPoint{
		{Store: "index", DrawerID: "d1", Indexed: "wing_acme-legacy", Actual: "wing_acme"},
	}})
	out := buf.String()
	if !strings.Contains(out, "5000 stored point(s) disagree") {
		t.Errorf("the exact count is not reported:\n%s", out)
	}
	if !strings.Contains(out, "4999 more, not listed") {
		t.Errorf("the listing was truncated without saying so — silent truncation reads as "+
			"'that was all of them':\n%s", out)
	}
}

// TestDoctorSaysPendingEmbeddingIsNotAFault: a drawer awaiting its first
// embedding has no point yet, and a busy palace must not look broken.
func TestDoctorSaysPendingEmbeddingIsNotAFault(t *testing.T) {
	var buf bytes.Buffer
	if err := reportDrift(&buf, palace.DriftReport{Checked: 10, Pending: 3}); err != nil {
		t.Errorf("a clean palace with a queue exited non-zero: %v", err)
	}
	if !strings.Contains(buf.String(), "queue and not a fault") {
		t.Errorf("pending embeddings are not explained:\n%s", buf.String())
	}
}

// TestDoctorRefusesAMissingDatabase: openDB CREATES a missing file and the
// migrations fill it, so a mistyped --db built an empty palace and reported it
// clean. "The path was wrong" and "the palace is healthy" must not be the same
// output — and the check must not leave a database behind either.
func TestDoctorRefusesAMissingDatabase(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-a-palace.db")
	err := doctorIndex(context.Background(), config.Config{DBPath: missing}, "local", io.Discard)
	if err == nil {
		t.Fatal("doctor inspected a database that does not exist")
	}
	if !strings.Contains(err.Error(), "no database") {
		t.Errorf("the refusal does not name the cause: %v", err)
	}
	if _, statErr := os.Stat(missing); statErr == nil {
		t.Error("doctor created the database it was asked to inspect")
	}
}

// TestDoctorDoesNotReconcileBeforeChecking: a checker must not repair the
// evidence.
//
// Building the chromem-backed store replays the source of truth into the index,
// so an index that had lost points was rebuilt at construction and then reported
// clean — the check could not fail on the fault it exists to find. Read off the
// source, because the reconciliation happens inside a constructor a unit test
// cannot observe without a database and an index on disk.
func TestDoctorDoesNotReconcileBeforeChecking(t *testing.T) {
	src, err := os.ReadFile(filepath.Join(repoRoot(t), "cmd", "server", "doctor.go"))
	if err != nil {
		t.Fatalf("read doctor.go: %v", err)
	}
	if regexp.MustCompile(`buildServices\(`).Match(src) {
		t.Error("doctor.go calls buildServices, which reconciles the search index before the check " +
			"can look at it — use buildServicesWith(cfg, false)")
	}
	if !regexp.MustCompile(`buildServicesWith\(cfg, false\)`).Match(src) {
		t.Error("doctor.go does not build its services with reconciliation disabled")
	}
}

// TestDoctorTakesTheLocalFlag: --local is what switches the search index to
// chromem, so a doctor without it inspects a bare SQLite store while chromem
// serves every query — and exits 0 on a broken palace.
func TestDoctorTakesTheLocalFlag(t *testing.T) {
	cmd := doctorCommand(config.Default())
	var found bool
	for _, f := range cmd.Flags {
		for _, n := range f.Names() {
			if n == "local" {
				found = true
			}
		}
	}
	if !found {
		t.Error("doctor has no --local flag, so on a self-hosted install it checks a backend nobody runs")
	}
}

// readRepoFile reads a file from the repository root, for the checks that can
// only be made against the source because their subject needs a live service.
func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{repoRoot(t)}, parts...)...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(b)
}

// TestDoctorRolesIsSelectable: the role check is reachable from the command
// line. A check with no flag to select it is the exact shape this repository has
// shipped repeatedly — finished, tested, and selected by nothing — so this reads
// the command's own flag list rather than the source.
func TestDoctorRolesIsSelectable(t *testing.T) {
	var names []string
	for _, f := range doctorCommand(config.Default()).Flags {
		names = append(names, f.Names()...)
	}
	found := false
	for _, n := range names {
		if n == "roles" {
			found = true
		}
	}
	if !found {
		t.Errorf("doctor offers %v and not \"roles\" — the check exists and cannot be run", names)
	}
}

// TestDoctorRolesRunsWhenSelected: declaring the flag is half of it. A flag the
// Action never consults leaves `doctor --roles` exiting 0 on a database it never
// opened, which reads as a clean palace.
func TestDoctorRolesRunsWhenSelected(t *testing.T) {
	cmd := doctorCommand(config.Default())
	err := cmd.Run(context.Background(), []string{"doctor", "--roles", "--db", filepath.Join(t.TempDir(), "absent.db")})
	if err == nil {
		t.Fatal("doctor --roles exited 0 against a database that does not exist")
	}
	if !strings.Contains(err.Error(), "no database at") {
		t.Errorf("--roles did not reach the check: %v", err)
	}
}

// TestDoctorRolesExitsNonZeroOnRefusals pins that the VERDICT is the exit code,
// and that a clean report is not an error. Prose is not a gate.
func TestDoctorRolesExitsNonZeroOnRefusals(t *testing.T) {
	if err := reportRefusedWrites(io.Discard, nil); err != nil {
		t.Errorf("a palace where every key may write reported an error: %v", err)
	}
	// A DELIBERATE member role alone must still fail: those agents stop writing
	// on upgrade, and a green exit here is exactly the silence being fixed.
	member := []tenant.ReadOnlyKeys{{TeamID: "team-a", Slug: "acme", Member: 3}}
	if err := reportRefusedWrites(io.Discard, member); err == nil {
		t.Error("member-role keys printed a warning and exited 0, which reads as nobody affected")
	}
}

// TestDoctorRolesSeparatesAChoiceFromAFault: promoting a teammate and repairing
// a broken row are different actions, so the report must not blur them.
func TestDoctorRolesSeparatesAChoiceFromAFault(t *testing.T) {
	out := &bytes.Buffer{}
	_ = reportRefusedWrites(out, []tenant.ReadOnlyKeys{
		{TeamID: "team-a", Slug: "acme", Member: 2, Missing: 1},
	})
	got := out.String()
	for _, want := range []string{"acme", "3 active key(s)", "promote them to writer", "historical data"} {
		if !strings.Contains(got, want) {
			t.Errorf("report is missing %q:\n%s", want, got)
		}
	}

	clean := &bytes.Buffer{}
	_ = reportRefusedWrites(clean, []tenant.ReadOnlyKeys{{TeamID: "team-a", Slug: "acme", Member: 2}})
	if strings.Contains(clean.String(), "historical data") {
		t.Errorf("a workspace with no data faults was told to repair one:\n%s", clean.String())
	}
}

// TestDoctorRolesNamesTheWorkspaceNotTheKey: a doctor report is pasted into an
// issue, so it carries slugs and counts and never key material.
func TestDoctorRolesNamesTheWorkspaceNotTheKey(t *testing.T) {
	out := &bytes.Buffer{}
	_ = reportRefusedWrites(out, []tenant.ReadOnlyKeys{{TeamID: "team-a", Slug: "acme", Member: 2, Empty: 1}})
	got := out.String()
	if !strings.Contains(got, "acme") {
		t.Errorf("report does not name the workspace:\n%s", got)
	}
	if strings.Contains(got, "team-a") {
		t.Errorf("report leaks the internal team id where a slug would do:\n%s", got)
	}
}
