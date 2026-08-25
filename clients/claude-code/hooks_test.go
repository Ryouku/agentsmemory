package main

import (
	"encoding/json"
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
	out, _ := runStopHookWithInput(t,
		`{"hook_event_name":"Stop","stop_hook_active":false}`, statsBody,
		append([]string{"AGENTSMEMORY_STOP_HOOK=on"}, env...)...)
	if strings.TrimSpace(out) == "" {
		t.Fatalf("the hook produced no output at all, so every assertion below would be vacuous")
	}
	return out
}

// runStopHookWithInput drives the same script with a caller-supplied event JSON
// and returns its stderr and exit code.
//
// The mode is NOT set here. runStopHook's callers want "on" and the subagent
// tests want "once" or a second switch, and appending a duplicate key to the
// child's environment leaves which one wins up to the platform — a test whose
// subject is a mode must set exactly one.
//
// Nor does it fatal on empty output: silence is the expected result for a
// disabled hook, and asserting on it is this task's job rather than the helper's.
func runStopHookWithInput(t *testing.T, input, statsBody string, env ...string) (string, int) {
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
	cmd.Stdin = strings.NewReader(input)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		// Same injection the installer prefixes onto the registered command.
		// Without it the helper skips /stats and every report assertion is vacuous.
		"AGENTSMEMORY_MCP_URL=http://127.0.0.1:9/mcp",
	)
	cmd.Env = append(cmd.Env, env...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	// The hook exits 2 by design, so a non-zero status is expected and the exit
	// CODE is a subject in its own right: exit 0 means the text never reaches the
	// agent, which is indistinguishable from a hook that was never registered.
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run hook: %v (stderr: %s)", err, stderr.String())
		}
		code = ee.ExitCode()
	}
	return stderr.String(), code
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

// runSubagentHook drives the SubagentStart hook and returns its STDOUT.
//
// stdout, not stderr, and that is the whole contract: a SubagentStart hook
// injects context by printing a JSON envelope on stdout, where the Stop hook
// merely talks to a human on stderr. A hook that wrote its envelope to stderr
// would look correct in a terminal and inject nothing.
func runSubagentHook(t *testing.T, env ...string) (string, int) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is not installed, so the shipped hook cannot be executed: %v. "+
			"This test asserts on the hook's OUTPUT; without bash every assertion below "+
			"would be made against an empty string.", err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks",
		"agentsmemory-subagent-start-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(`{"hook_event_name":"SubagentStart"}`)
	cmd.Env = append(os.Environ(), env...)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("run hook: %v (stderr: %s)", err, stderr.String())
		}
	}
	return stdout.String(), code
}

// TestSubagentStartHookEmitsAContextEnvelope pins the one thing the injector must
// get right: a well-formed hookSpecificOutput envelope on stdout.
//
// The measurement this hook exists for compares "the whole protocol" against "the
// whole protocol plus one paragraph next to the task". If the envelope is
// malformed the harness drops it silently, the paragraph never arrives, and the
// control and treatment arms become the same arm — producing a confident "the
// injection changed nothing" that is really "the injection never happened".
func TestSubagentStartHookEmitsAContextEnvelope(t *testing.T) {
	out, code := runSubagentHook(t, "AGENTSMEMORY_SUBAGENT_HOOK=on")
	if code != 0 {
		t.Errorf("hook exited %d; a SubagentStart hook that fails blocks the dispatch", code)
	}
	if strings.TrimSpace(out) == "" {
		t.Fatal("the hook printed nothing on stdout, so every assertion below would be vacuous")
	}

	var env struct {
		HookSpecificOutput struct {
			HookEventName     string `json:"hookEventName"`
			AdditionalContext string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not the JSON envelope the harness parses: %v\n%s", err, out)
	}
	if env.HookSpecificOutput.HookEventName != "SubagentStart" {
		t.Errorf("hookEventName is %q, want SubagentStart — the harness matches on this and "+
			"drops what it does not recognise", env.HookSpecificOutput.HookEventName)
	}
	if !strings.Contains(env.HookSpecificOutput.AdditionalContext, "am_search") {
		t.Errorf("the injected context never names am_search, so it cannot be what causes a "+
			"subagent to call it:\n%s", env.HookSpecificOutput.AdditionalContext)
	}
}

// TestSubagentStartHookFailsOpen pins that the hook cannot break a dispatch.
//
// Same rule as the SessionStart verify hook: nothing here is worth blocking work
// for. Disabled, or with no server reachable, it must exit 0 — and when disabled
// it must emit NOTHING, because the control arm of T1's measurement is exactly
// "this hook produced no context". An injector that still printed something when
// switched off would make the two arms identical and the measurement meaningless.
func TestSubagentStartHookFailsOpen(t *testing.T) {
	out, code := runSubagentHook(t, "AGENTSMEMORY_SUBAGENT_HOOK=off")
	if code != 0 {
		t.Errorf("disabled hook exited %d, want 0", code)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("the disabled hook still injected context, so T1's control arm would carry "+
			"the treatment:\n%s", out)
	}

	// A broken PATH stands in for every environment failure at once.
	out, code = runSubagentHook(t, "AGENTSMEMORY_SUBAGENT_HOOK=on", "PATH=/nonexistent")
	if code != 0 {
		t.Errorf("hook exited %d with an unusable PATH; it must fail OPEN and never block a "+
			"dispatch over bookkeeping (stdout was %q)", code, out)
	}
}

// runSubagentHookWithEnv is runSubagentHook's stdout-only sibling, used by the
// installer tests that care about the context's CONTENT rather than its exit code.
func runSubagentHookWithEnv(t *testing.T, env ...string) string {
	t.Helper()
	out, _ := runSubagentHook(t, env...)
	return out
}

// subagentStopEvent is a REAL SubagentStop payload, captured from this harness by
// registering a hook that did nothing but tee its stdin, and dispatching one
// trivial subagent. Local paths and ids are neutralised; the SHAPE is verbatim.
//
// It is captured rather than written because a hand-authored fixture proves the
// branch works for the JSON the test's author imagined. Two of its fields decide
// this whole task and neither could be settled by reading:
//
//   - `stop_hook_active` IS sent on SubagentStop. The published payload reference
//     does not list it, and without it an exit-2 nudge would re-fire forever. It
//     is here, so the existing loop guard covers subagents too.
//   - `session_id` is IDENTICAL to the parent session's. That is what makes the
//     `once`-per-session marker a collision rather than a theory: the main
//     session and every subagent under it key the same file.
const subagentStopEvent = `{"session_id":"11111111-2222-3333-4444-555555555555",` +
	`"transcript_path":"/tmp/projects/example/11111111.jsonl","cwd":"/tmp/work",` +
	`"prompt_id":"66666666-7777-8888-9999-000000000000","permission_mode":"default",` +
	`"agent_id":"a1b2c3d4e5f607182","agent_type":"general-purpose",` +
	`"hook_event_name":"SubagentStop","stop_hook_active":false,` +
	`"agent_transcript_path":"/tmp/projects/example/11111111/subagents/agent-a1b2c3d4e5f607182.jsonl",` +
	`"last_assistant_message":"pong","background_tasks":[],"session_crons":[]}`

// sessionStopEvent is the main-session Stop payload, for the same reason.
const sessionStopEvent = `{"session_id":"11111111-2222-3333-4444-555555555555",` +
	`"transcript_path":"/tmp/projects/example/11111111.jsonl",` +
	`"hook_event_name":"Stop","stop_hook_active":false}`

// TestStopHookAsksASubagentForFindingsNotASummary is the point of ADR-017 T3.
//
// A subagent is asked for what it FOUND — a drawer, a fact — and explicitly not
// for a session summary. The dispatcher writes that one. A diary entry per
// subagent is how a journal stops being read: a 16-way fan-out would file
// seventeen summaries of one piece of work, sixteen of them written by an agent
// that saw a sliver of it.
//
// It also pins the WING advice, which is not the same advice the start hook
// gives. There, a guessed wing costs a bad recall — noise. Here it costs a WRITE
// into another project's palace, which the protocol names as poisoning it. The
// asymmetry is the reason this assertion exists on the stop side at all.
func TestStopHookAsksASubagentForFindingsNotASummary(t *testing.T) {
	sub, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on")
	if code != 2 {
		t.Errorf("subagent nudge exited %d, want 2 — any other code and the text never "+
			"reaches the agent, so the hook is registered, fires, and changes nothing", code)
	}
	for _, want := range []string{"am_add_drawer", "am_kg_add"} {
		if !strings.Contains(sub, want) {
			t.Errorf("the subagent nudge does not name %s:\n%s", want, sub)
		}
	}
	if strings.Contains(sub, "am_diary_write") {
		t.Errorf("a subagent is being asked for a session summary; its dispatcher writes "+
			"that, and one diary entry per subagent is how a journal stops being read:\n%s", sub)
	}
	// The wing: a wrong-wing READ is noise, a wrong-wing WRITE is another
	// project's palace. The subagent must be told to pass none.
	if !strings.Contains(sub, "no wing") {
		t.Errorf("the subagent nudge does not tell it to pass no wing, so it will guess one "+
			"and file this project's work somewhere else:\n%s", sub)
	}
	// The server-wide recall report belongs to a session, not to each of its
	// subagents. The fake curl in the helper serves it on demand, so its presence
	// here would mean the subagent branch ran the whole session path.
	if strings.Contains(sub, "searches recalled") {
		t.Errorf("the session recall report was printed into a subagent's nudge:\n%s", sub)
	}

	// And the two must actually DIFFER. Without this the mutant "use the session
	// nudge verbatim" survives everything above that the session nudge happens to
	// satisfy.
	session, _ := runStopHookWithInput(t, sessionStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on")
	if sub == session {
		t.Errorf("a subagent gets the session checkpoint verbatim:\n%s", sub)
	}
}

// TestSubagentStopIsNotSwallowedByTheOnceGuard pins the collision the captured
// payload proved: SubagentStop carries the PARENT session's `session_id`, so the
// `once`-per-session marker — the default mode — is one file shared by the main
// session and every subagent under it.
//
// Both directions are defects and only one of them is obvious. The main session
// stopping first silences every subagent afterwards; a subagent stopping first
// silences the human's own checkpoint. The fix is that a subagent stop neither
// reads nor writes that marker.
func TestSubagentStopIsNotSwallowedByTheOnceGuard(t *testing.T) {
	t.Run("a main stop that already fired does not silence subagents", func(t *testing.T) {
		tmp := t.TempDir()
		// The marker the session path writes, created directly: the subject here is
		// the guard, not the path that happens to set it.
		marker := filepath.Join(tmp, "agentsmemory-stop-11111111-2222-3333-4444-555555555555.done")
		if err := os.WriteFile(marker, nil, 0o644); err != nil {
			t.Fatalf("seed marker: %v", err)
		}
		out, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
			"AGENTSMEMORY_STOP_HOOK=once", "TMPDIR="+tmp)
		if code != 2 || !strings.Contains(out, "am_add_drawer") {
			t.Errorf("the subagent nudge was swallowed by the session's marker (exit %d):\n%s",
				code, out)
		}
	})

	t.Run("a subagent stop does not silence the session", func(t *testing.T) {
		tmp := t.TempDir()
		if _, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
			"AGENTSMEMORY_STOP_HOOK=once", "TMPDIR="+tmp); code != 2 {
			t.Fatalf("subagent nudge exited %d, want 2", code)
		}
		marker := filepath.Join(tmp, "agentsmemory-stop-11111111-2222-3333-4444-555555555555.done")
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("a subagent stop claimed the session's once-marker at %s, so the human's "+
				"own checkpoint is now silenced for the rest of the session", marker)
		}
		out, code := runStopHookWithInput(t, sessionStopEvent, statsWithSuggestions,
			"AGENTSMEMORY_STOP_HOOK=once", "TMPDIR="+tmp)
		if code != 2 || !strings.Contains(out, "am_diary_write") {
			t.Errorf("the session checkpoint was swallowed by a subagent's stop (exit %d):\n%s",
				code, out)
		}
	})
}

// TestUnknownStopEventKeepsTheSessionBehaviour pins the degradation.
//
// The subagent branch turns on a string match against `hook_event_name`. If a
// future harness spells it differently, the match fails — and what must happen
// then is the CURRENT behaviour, not silence. A branch that failed closed would
// take the human's checkpoint away too, on a rename nobody announced.
func TestUnknownStopEventKeepsTheSessionBehaviour(t *testing.T) {
	out, code := runStopHookWithInput(t,
		`{"session_id":"s","hook_event_name":"SomethingElse","stop_hook_active":false}`,
		statsWithSuggestions, "AGENTSMEMORY_STOP_HOOK=on")
	if code != 2 || !strings.Contains(out, "am_diary_write") {
		t.Errorf("an unrecognised stop event lost the session checkpoint (exit %d):\n%s", code, out)
	}
}

// TestSubagentStopHookCanBeDisabledOnItsOwn pins the switch.
//
// Exit 2 costs a subagent one extra turn, and a wide fan-out pays it once per
// branch. That is a real bill, so it has its own off switch rather than forcing a
// choice between subagent writes and the human's checkpoint.
func TestSubagentStopHookCanBeDisabledOnItsOwn(t *testing.T) {
	out, code := runStopHookWithInput(t, subagentStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on", "AGENTSMEMORY_SUBAGENT_STOP_HOOK=off")
	if code != 0 || strings.TrimSpace(out) != "" {
		t.Errorf("disabling the subagent half still nudged (exit %d):\n%s", code, out)
	}
	// ...and the session half is untouched by that switch.
	sess, code := runStopHookWithInput(t, sessionStopEvent, statsWithSuggestions,
		"AGENTSMEMORY_STOP_HOOK=on", "AGENTSMEMORY_SUBAGENT_STOP_HOOK=off")
	if code != 2 || !strings.Contains(sess, "am_diary_write") {
		t.Errorf("the subagent switch also disabled the session checkpoint (exit %d):\n%s",
			code, sess)
	}
}

// TestRetiredStatsEnvNamesAreGone fails when a second name for /stats or its
// off-switch returns. AGENTSMEMORY_STATS_URL, AGENTSMEMORY_STATS_BASE, and
// AGENTSMEMORY_SESSION_REPORT were three names for one endpoint and two
// switches for one report; setting one did not move the others.
func TestRetiredStatsEnvNamesAreGone(t *testing.T) {
	root := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks")
	retired := []string{"AGENTSMEMORY_STATS_URL", "AGENTSMEMORY_STATS_BASE", "AGENTSMEMORY_SESSION_REPORT"}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		found++
		body, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range retired {
			if strings.Contains(string(body), name) {
				t.Errorf("%s still names %s; that is a second knob for the same /stats fetch", e.Name(), name)
			}
		}
	}
	if found < 4 {
		t.Fatalf("only %d hook scripts; a missing file would let this pass against nothing", found)
	}
}

// TestHookScriptsDoNotGuessAPalace fails when a hook hardcodes localhost or the
// hosted origin. The installer injects AGENTSMEMORY_MCP_URL; a default in the
// script is a second palace.
func TestHookScriptsDoNotGuessAPalace(t *testing.T) {
	root := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks")
	banned := []string{"localhost:8080", "127.0.0.1:8080", "aiagentmemory.dev"}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(root, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		for _, lit := range banned {
			if strings.Contains(string(body), lit) {
				t.Errorf("%s hardcodes %s; the palace is AGENTSMEMORY_MCP_URL from the installer", e.Name(), lit)
			}
		}
	}
}

// TestStatsFetchUsesTheMCPOrigin drives the real scripts against a curl that
// records its URL, so a second origin (STATS_BASE, a localhost default) cannot
// return silently.
func TestStatsFetchUsesTheMCPOrigin(t *testing.T) {
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "curl-args")
	fakeCurl := filepath.Join(dir, "curl")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"" + argsFile + "\"\ncat <<'BODY'\n" + statsWithSuggestions + "\nBODY\n"
	if err := os.WriteFile(fakeCurl, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Fatalf("bash is not installed: %v", err)
	}

	palace := "http://palace.test:9/mcp"
	run := func(scriptName string, stdout bool) {
		t.Helper()
		os.Remove(argsFile)
		hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", scriptName)
		cmd := exec.Command("bash", hook)
		cmd.Stdin = strings.NewReader(`{"hook_event_name":"Stop","stop_hook_active":false}`)
		cmd.Env = append(os.Environ(),
			"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"AGENTSMEMORY_MCP_URL="+palace,
			"AGENTSMEMORY_STOP_HOOK=on",
			"AGENTSMEMORY_STATS=on",
		)
		var buf strings.Builder
		if stdout {
			cmd.Stdout = &buf
		} else {
			cmd.Stderr = &buf
		}
		_ = cmd.Run()
		raw, err := os.ReadFile(argsFile)
		if err != nil {
			t.Fatalf("%s did not invoke curl: %v", scriptName, err)
		}
		if !strings.Contains(string(raw), "http://palace.test:9/stats?") {
			t.Errorf("%s curl args = %q, want origin derived from AGENTSMEMORY_MCP_URL", scriptName, raw)
		}
	}
	run("agentsmemory-stop-hook.sh", false)
	run("agentsmemory-session-end-hook.sh", true)
}

// TestSessionEndHonoursTheSharedStatsOffSwitch pins that SessionEnd and Stop
// share AGENTSMEMORY_STATS. A second name (SESSION_REPORT) meant turning stats
// off in one hook left the other printing.
func TestSessionEndHonoursTheSharedStatsOffSwitch(t *testing.T) {
	dir := t.TempDir()
	fakeCurl := filepath.Join(dir, "curl")
	if err := os.WriteFile(fakeCurl, []byte("#!/bin/sh\necho should-not-run\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	hook := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "hooks", "agentsmemory-session-end-hook.sh")
	cmd := exec.Command("bash", hook)
	cmd.Stdin = strings.NewReader(`{}`)
	cmd.Env = append(os.Environ(),
		"PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"AGENTSMEMORY_MCP_URL=http://palace.test:9/mcp",
		"AGENTSMEMORY_STATS=off",
	)
	var stdout strings.Builder
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		t.Fatalf("session-end: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "" {
		t.Errorf("AGENTSMEMORY_STATS=off still printed: %q", got)
	}
}
