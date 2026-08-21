package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/config"
	"github.com/atvirokodosprendimai/agentsmemory/internal/palace"
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
	drifted := palace.DriftReport{Checked: 5, Drifted: []palace.DriftedPoint{
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
