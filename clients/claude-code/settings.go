package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// ensureHook registers hookCmd for a Claude Code hook EVENT ("Stop",
// "SessionStart", …) in the settings
// JSON at path, idempotently. It preserves any existing settings, backs the file
// up (timestamped) before writing, and never adds a duplicate entry for the same
// command. It returns true if it changed the file.
//
// isObsolete, when non-nil, marks commands this install supersedes: any Stop
// entry running one is dropped in the same read-modify-write. Two cases need it,
// both of which would otherwise leave a second entry behind — a relocated hook
// script (the old entry then runs a deleted file), and a settings.json copied in
// from another config dir with --copy (the old entry runs *that* dir's script, so
// the hook fires twice per stop).
//
// This is the Go replacement for the jq block in the old install.sh — same
// behaviour and same on-disk shape, with no external jq dependency.
func ensureHook(path, event, hookCmd string, isObsolete func(cmd string) bool) (bool, error) {
	changed, err := ensureHooks(path, []hookReg{{event: event, cmd: hookCmd, obsolete: isObsolete}})
	return changed[event], err
}

// hookReg is one event → command registration for ensureHooks. A nil obsolete
// supersedes nothing, which is what a caller with no older command to retire
// passes.
type hookReg struct {
	event    string
	cmd      string
	obsolete func(cmd string) bool
}

// ensureHooks registers every entry in regs in ONE read-modify-write of the
// settings JSON at path, and returns the set of events it actually changed.
//
// Batching is not a micro-optimisation, it is the fix for a defect the
// per-event version produced. Every write backs the file up first, so
// registering five events one call at a time left FOUR timestamped backups in
// the user's config dir on every install that added them — the config dir
// filling with copies of itself, and the count growing by one with each hook the
// product gains. One read, one backup, one write.
//
// When every registration is already present and nothing was superseded — the
// common case on a re-install — it writes nothing at all: no file touched, no
// backup, and an empty changed set. That is also why `changed` is a set rather
// than a bool: the caller reports per event, and "which of these five are new"
// is not answerable from a single flag.
//
// A registration that fails to parse or that finds a value of the wrong shape
// aborts the WHOLE batch before anything is written, so the file is never left
// carrying half of an install.
func ensureHooks(path string, regs []hookReg) (map[string]bool, error) {
	changed := map[string]bool{}

	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	settings := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			// Refuse to touch a file we can't parse: overwriting a user's
			// hand-edited settings.json would be worse than failing loudly.
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	hooks, err := childObject(settings, "hooks")
	if err != nil {
		return nil, err
	}

	for _, reg := range regs {
		entries, err := childArray(hooks, reg.event)
		if err != nil {
			return nil, err
		}

		pruned, dropped := dropHook(entries, reg.obsolete)
		if hookPresent(pruned, reg.cmd) && !dropped {
			continue
		}

		if !hookPresent(pruned, reg.cmd) {
			// Append a matcher-less entry carrying our command — the same shape
			// Claude Code writes and the same shape the old install.sh produced.
			pruned = append(pruned, map[string]any{
				"hooks": []any{
					map[string]any{"type": "command", "command": reg.cmd},
				},
			})
		}
		hooks[reg.event] = pruned
		changed[reg.event] = true
	}

	if len(changed) == 0 {
		return changed, nil
	}
	settings["hooks"] = hooks

	// Back up the original before writing, mirroring install.sh's .bak.<ts>.
	// Nanosecond precision avoids clobbering an earlier backup on a same-second re-run.
	if len(raw) > 0 {
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			return nil, fmt.Errorf("backup %s: %w", path, err)
		}
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return nil, err
	}
	return changed, nil
}

// childObject returns settings[key] as a JSON object, creating an empty one if
// the key is absent. It errors if the key exists but holds a non-object, so we
// never silently clobber a value of the wrong shape.
func childObject(m map[string]any, key string) (map[string]any, error) {
	switch v := m[key].(type) {
	case nil:
		return map[string]any{}, nil
	case map[string]any:
		return v, nil
	default:
		return nil, fmt.Errorf("settings key %q is %T, expected an object", key, v)
	}
}

// childArray returns m[key] as a JSON array, creating an empty one if absent, and
// errors if the key holds a non-array.
func childArray(m map[string]any, key string) ([]any, error) {
	switch v := m[key].(type) {
	case nil:
		return []any{}, nil
	case []any:
		return v, nil
	default:
		return nil, fmt.Errorf("settings key %q is %T, expected an array", key, v)
	}
}

// dropHook returns the event's entries without any hook whose command isObsolete matches,
// and reports whether anything was removed. An entry carrying other hooks
// alongside the matched one keeps those: only the matching hook is taken out, so
// a user's own command sitting beside ours survives. A nil predicate is a no-op,
// which is what callers with nothing to supersede pass.
func dropHook(stop []any, isObsolete func(string) bool) ([]any, bool) {
	if isObsolete == nil {
		return stop, false
	}
	out := make([]any, 0, len(stop))
	dropped := false
	for _, entry := range stop {
		em, ok := entry.(map[string]any)
		if !ok {
			out = append(out, entry)
			continue
		}
		inner, ok := em["hooks"].([]any)
		if !ok {
			out = append(out, entry)
			continue
		}
		kept := make([]any, 0, len(inner))
		for _, h := range inner {
			if hm, ok := h.(map[string]any); ok {
				if c, _ := hm["command"].(string); isObsolete(c) {
					dropped = true
					continue
				}
			}
			kept = append(kept, h)
		}
		if len(kept) == 0 {
			continue // the entry existed only to run cmd
		}
		em["hooks"] = kept
		out = append(out, em)
	}
	return out, dropped
}

// hookPresent reports whether any entry already registers command cmd,
// so re-running the installer never duplicates the hook.
func hookPresent(stop []any, cmd string) bool {
	for _, entry := range stop {
		em, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		inner, ok := em["hooks"].([]any)
		if !ok {
			continue
		}
		for _, h := range inner {
			hm, ok := h.(map[string]any)
			if !ok {
				continue
			}
			if c, _ := hm["command"].(string); c == cmd {
				return true
			}
		}
	}
	return false
}

// ensureMCPServer registers an MCP server under "mcpServers" in the JSON file at
// path, idempotently, and reports whether it changed the file.
//
// It exists because Cursor ships no command that registers one: `cursor-agent
// mcp` offers login, list, list-tools, enable and disable, so this is the first
// registration path with no CLI between us and another product's config file.
// Every other agent's `mcp add` merges on our behalf and cannot lose anything.
//
// So it takes the same discipline ensureHooks takes with settings.json — read
// once, merge, back the original up, write once, and write NOTHING when the entry
// is already identical — and refuses a file it cannot parse rather than replacing
// it. mcp.json is shared with every other MCP server the user runs, and a hand
// edit with a trailing comma is common; overwriting it would destroy
// configuration we never read.
func ensureMCPServer(path, name string, entry map[string]any) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	cfg := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &cfg); err != nil {
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	servers, err := childObject(cfg, "mcpServers")
	if err != nil {
		return false, err
	}
	// Already identical: write nothing. Comparing the MARSHALLED forms rather than
	// the maps is what makes a re-install a true no-op — reflect.DeepEqual on
	// values that came back through json.Unmarshal compares interface types, and
	// an entry we built and an entry we read back are not the same types even when
	// they are the same JSON.
	if existing, ok := servers[name]; ok {
		was, err1 := json.Marshal(existing)
		now, err2 := json.Marshal(entry)
		if err1 == nil && err2 == nil && string(was) == string(now) {
			return false, nil
		}
	}
	servers[name] = entry
	cfg["mcpServers"] = servers

	if len(raw) > 0 {
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			return false, fmt.Errorf("backup %s: %w", path, err)
		}
	}

	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil {
		return false, err
	}
	return true, nil
}
