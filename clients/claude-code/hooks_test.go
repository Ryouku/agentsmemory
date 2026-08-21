package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runStopHook executes the real Stop hook against a fake /stats server and
// returns everything it wrote to stderr.
//
// The hook is shell, so this drives the shipped script rather than a Go
// re-implementation of it — a re-implementation would pass while the file that
// actually runs on every Stop said something else.
func runStopHook(t *testing.T, statsBody string, env ...string) string {
	t.Helper()
	dir := t.TempDir()

	// A stand-in `curl` that ignores its arguments and prints the body. The hook
	// reaches the server through curl and nothing else, so replacing curl is
	// enough to control what the report is built from.
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\ncat <<'BODY'\n" + statsBody + "\nBODY\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake curl: %v", err)
	}

	// A missing bash must FAIL, never skip and never pass quietly. Without it the
	// command does not start, stderr is empty, and every assertion of the form
	// "the output does not contain X" passes for free — which is exactly the
	// vacuous green this repository has a rule about. The hook's shebang is bash
	// and it uses bash-isms, so bash is a requirement of the test, not a detail.
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is not installed, so the shipped hook cannot be executed: %v. "+
			"This test asserts on the hook's OUTPUT; without bash every negative assertion "+
			"would pass against an empty string.", err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-stop-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":false}`)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_STOP_HOOK=on",
	)
	cmd.Env = append(cmd.Env, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// The hook exits 2 by design, so a non-zero status is expected and the OUTPUT
	// is the subject. But empty output means it never ran, and asserting on that
	// is asserting on nothing.
	_ = cmd.Run()
	out := stderr.String()
	if strings.TrimSpace(out) == "" {
		t.Fatalf("the hook produced no output at all, so every assertion below would be vacuous")
	}
	return out
}

func repoRootForHooks(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	return filepath.Dir(filepath.Dir(wd)) // clients/claude-code -> repo root
}

// statsWithSuggestions is a /stats body in the shape the server emits, including
// the "  write: " lines the hook re-renders as a task list.
const statsWithSuggestions = `memory, this session: 40 searches recalled, 38 answered (95%), 12 memories filed
  wing_acme                20/20 answered (100%), 52 drawers
  wing_alpha               18/20 answered (90%), 40 drawers
  write: 1x how long is the retention window [wing_alpha]
  write: 1x which service owns the invoice pdf [wing_acme]`

// TestNoTaskListWithoutAttribution is the whole point of this task.
//
// The "memories to write" section is not a statistic. It is a TASK LIST, and the
// server cannot say whose searches it is built from: search_events has no session
// column, so /stats filters by team and time only. On a machine running several
// sessions against one palace — eleven were live when this was found — each
// session is handed every other session's unanswered questions under a heading
// that reads as its own.
//
// Following it means writing a memory about a question you never asked, into a
// wing you never opened, from no evidence you hold. One session caught that and
// refused. The next will not, and the more diligent the agent the worse the
// outcome — so the list is printed only when the recalls can be attributed.
func TestNoTaskListWithoutAttribution(t *testing.T) {
	out := runStopHook(t, statsWithSuggestions)

	if strings.Contains(out, "memories to write") {
		t.Errorf("the hook printed a task list it cannot attribute to this session:\n%s", out)
	}
	if strings.Contains(out, "retention window") {
		t.Errorf("another session's unanswered question was handed to this one as a to-do:\n%s", out)
	}
	// The NUMBERS still print. They are useful at any scope; it is the task list
	// that is only useful when it is yours.
	if !strings.Contains(out, "searches recalled") {
		t.Errorf("the statistics were suppressed along with the task list; they are worth keeping:\n%s", out)
	}
}

// TestReportNamesItsPopulation: a statistic that names its population is useful
// at any scope, and one that does not is the defect. The heading must say whose
// recalls these are rather than leaving "this session" to be assumed.
func TestReportNamesItsPopulation(t *testing.T) {
	out := runStopHook(t, statsWithSuggestions)
	if !strings.Contains(out, "every session on this server") {
		t.Errorf("the report does not say whose recalls it describes, so it reads as this session's:\n%s", out)
	}
	if strings.Contains(out, "memory, this session:") {
		t.Errorf("the report still claims to be this session's, which the server cannot know:\n%s", out)
	}
}

// TestStopHookStillNudgesAndNeverBreaks: the checkpoint is the hook's actual job
// and must survive every change to the reporting half.
func TestStopHookStillNudgesAndNeverBreaks(t *testing.T) {
	out := runStopHook(t, statsWithSuggestions)
	for _, want := range []string{"am_diary_write", "am_kg_add", "am_add_drawer"} {
		if !strings.Contains(out, want) {
			t.Errorf("the persist checkpoint no longer names %s:\n%s", want, out)
		}
	}
}
