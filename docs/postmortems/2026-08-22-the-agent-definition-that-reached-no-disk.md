---
date: 2026-08-22
category: silent-failure
severity: medium
files_changed:
  - clients/claude-code/assets.go
  - clients/claude-code/installer.go
  - clients/claude-code/installer_test.go
  - scripts/redeploy.sh
tags: [reachability, embed, installer, vacuous-test, deploy-gate, adr-017]
---

## Symptom

`aiagentmemory install --agent claude` reported success and `~/.claude/agents/`
contained only three definitions belonging to an unrelated product. The
definition this repository ships, `agentsmemory-researcher.md`, was present in
the source tree, present in the binary's `go:embed` directive, covered by a
passing test — and on no disk anywhere. Nothing errored, and no output said
anything was missing.

## Context

ADR-017's Decision names three mechanisms and calls the first one — shipping
agent definitions whose `tools:` allowlist includes the `am_*` tools — "the one
unambiguous structural fix… the only one of the three that cannot fail for
compliance reasons, because it changes what is POSSIBLE rather than what is
asked". An agent definition with a `tools:` allowlist can call only what the
list names, so a subagent defined without the memory tools cannot recall however
it is instructed.

T2 (`f9e45eb`) created `clients/claude-code/agents/agentsmemory-researcher.md`,
added `agents/*.md` to the `go:embed` line in `clients/claude-code/assets.go:48`,
and added `TestShippedAgentDefinitionsNameTheMemoryTools` to
`clients/claude-code/installer_test.go`. It never added an install path.
`Installer.writeAssets` (`clients/claude-code/installer.go:525`) writes commands,
four hook scripts and the bootstrap file; there was no `agents/` branch.

## Root Cause

Two independent misses, and the second is what made the first invisible.

**The install path was never written.** The embed directive makes a file part of
the binary; it does not make anything write it out. `writeAssets` enumerates its
outputs explicitly, and `agents/` was not among them:

```go
//go:embed commands/M.md ... hooks/agentsmemory-subagent-start-hook.sh agents/*.md bootstrap.md ...
var assets embed.FS
```

```go
// writeAssets, claude-only branch — every output is explicit, and agents/ is absent.
subHook, _ := i.source().ReadFile(subagentHookAsset)
i.writeFile(i.subagentHookPath(), subHook, 0o755)
endHook, _ := i.source().ReadFile(sessionEndHookAsset)
i.writeFile(i.sessionEndHookPath(), endHook, 0o755)
// (nothing reads agentsDirName)
```

**The covering test located its subject in the repository, not in the install.**

```go
func TestShippedAgentDefinitionsNameTheMemoryTools(t *testing.T) {
	dir := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "agents")
	entries, _ := filepath.Glob(filepath.Join(dir, "*.md"))
	// ...asserts each file's CONTENT names am_search
}
```

The test was written carefully — it even fails when the glob returns nothing, so
"zero definitions inspected" cannot masquerade as "every definition passed". But
the only thing that could ever make it fail is deleting or editing the source
file. It asserts the component says the right thing and says nothing about
whether anything selects it. This is rung 1 of the reachability ladder with no
rung 2, in the very commit whose purpose was to add the capability — the fifth
instance of that shape in this corpus.

The safety net that existed did not catch it either. `scripts/redeploy.sh` holds
a client-kit freshness gate that byte-compares installed artifacts against the
checkout and exits 1 on drift. Its pair list is hand-maintained, and T2 added two
installed artifacts without adding either to it — so the newest artifacts were
precisely the ones the staleness gate could not see.

## Investigation

The defect was not found by reviewing T2. It was found sideways, while doing T3.

1. **The advisor flagged a list, not a bug.** Reviewing T3's plan, it noted that
   `redeploy.sh`'s freshness pair list has seven entries and
   `agentsmemory-subagent-start-hook.sh` — added by T2 — is not one of them. That
   is a gate-coverage gap, not a shipping defect, and the suggested fix was one
   line.

2. **Deciding to close it mechanically rather than by hand.** A hand-maintained
   list that has already drifted once will drift again, so instead of adding the
   entry I set out to write a test asserting the list covers everything an
   install writes. That required enumerating what an install writes.

3. **The enumeration is where it surfaced.** Grepping `installer.go` for the
   agents directory returned nothing:
   `grep -n "agentsDir\|agents/\|\"agents\"" clients/claude-code/installer.go` →
   no output. The only `agents` hit anywhere in the package was
   `projectconfig.go:27`, an unrelated constant.

4. **Ruling out "it installs under another name".** `ls -la ~/.claude/agents/` on
   a machine where the kit had been installed that morning listed three
   `codebase-memory-*.md` files, all dated four days earlier, and no
   `agentsmemory-researcher.md`. So it was not a naming or path question; nothing
   had ever written it.

5. **Confirming the test could not have caught it.** Re-reading
   `TestShippedAgentDefinitionsNameTheMemoryTools`, its `dir` is
   `repoRootForHooks(t) + "/clients/claude-code/agents"` — the source tree. Asking
   the standing question ("what would it take for this to fail?") the answer was
   "delete the source file", which is the definition of testing the component
   rather than the selection.

6. **A false start worth recording.** The first version of the new coverage test
   globbed the kit's directories instead of reading the installer's asset lists,
   and immediately failed on `clients/claude-code/commands/CLAUDE.md` — a file
   generated by an unrelated tool and committed into the commands directory,
   which no install has ever shipped. Directory contents are not the install set;
   the lists the installer iterates are.

## Fix

### Before

```go
// assets.go — embedded, and that is all.
//go:embed ... agents/*.md ...
var assets embed.FS

var commandAssets = []string{"M.md", "am.md", "load-skill.md"}
```

```go
// installer.go — writeAssets, claude branch. No agents/ output.
i.ok("hook %s", filepath.Base(i.sessionEndHookPath()))
}
```

```go
// installer_test.go — the covering test reads the REPOSITORY.
dir := filepath.Join(repoRootForHooks(t), "clients", "claude-code", "agents")
entries, _ := filepath.Glob(filepath.Join(dir, "*.md"))
```

### After

```go
// assets.go — an explicit list, because assetSource is ReadFile-only:
// `update-skill` fetches the same names over HTTP, where there is nothing to walk.
var agentAssets = []string{"agentsmemory-researcher.md"}
```

```go
// installer.go — the install path, called from writeAssets' claude branch.
const agentsDirName = "agents"

func (i *Installer) agentDefinitionPath(name string) string {
	return filepath.Join(i.targetDir, agentsDirName, name)
}

func (i *Installer) writeAgentDefinitions() error {
	for _, name := range agentAssets {
		data, err := i.source().ReadFile(agentsDirName + "/" + name)
		if err != nil {
			return err
		}
		if err := i.writeFile(i.agentDefinitionPath(name), data, 0o644); err != nil {
			return err
		}
		i.ok("agent %s", name)
	}
	return nil
}
```

```go
// installer_test.go — the assertion that could have failed: read the INSTALLED file.
func TestInstallerInstallsAgentDefinitions(t *testing.T) {
	inst, _, dir := newTestInstaller(t, false)
	if err := inst.run(); err != nil {
		t.Fatalf("install: %v", err)
	}
	for _, name := range agentAssets {
		body, err := os.ReadFile(filepath.Join(dir, agentsDirName, name))
		if err != nil {
			t.Fatalf("agent definition %s was not installed: %v", name, err)
		}
		if !strings.Contains(string(body), "am_search") {
			t.Errorf("installed %s does not name am_search", name)
		}
	}
}
```

Two more gates close the surrounding holes rather than this instance of them.
`TestEveryShippedAgentDefinitionIsInstalled` fails when a definition is added to
the repository but not to `agentAssets` — the exact "added the file, forgot the
list" step that produced this. `TestRedeployKitCheckCoversEveryInstalledArtifact`
derives the expected set from `commandAssets`, `agentAssets` and the four hook
asset constants, and fails when `redeploy.sh`'s freshness list stops covering
them, so the deploy gate can no longer go blind to the newest artifact.

Each was driven red before being trusted: dropping the `writeAgentDefinitions`
call, removing an entry from `agentAssets`, and removing an artifact from
`redeploy.sh`'s list each produced exit 1 under `adr-verify --mutant`.

The fix is correct because it separates the two things the old arrangement
conflated. Embedding decides what the binary CONTAINS; `agentAssets` plus
`writeAgentDefinitions` decide what an install PRODUCES; and the new tests assert
the produced artifact rather than the source one. The old test is kept — a
definition that installs but omits `am_search` is still useless — but it is no
longer the only thing standing between the ADR's primary mechanism and silence.

## Lesson

Never let a test locate its subject in the source tree when the defect class is
"nothing installs it" — ask what it would take for the test to fail, and if the
answer is "delete the source file", it is testing the component and not the
selection. Adding a file to an embed directive, an asset bundle, or a build
manifest ships it INTO the artifact and never OUT of it: always name the line
that writes it to its destination, and assert the destination.
