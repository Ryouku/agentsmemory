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

// ensureStopHook registers hookCmd as a Claude Code Stop hook in the settings
// JSON at path, idempotently. It preserves any existing settings, backs the file
// up (timestamped) before writing, and never adds a duplicate entry for the same
// command. It returns true if it changed the file.
//
// obsoleteCmd, when non-empty, names a command this install supersedes: any Stop
// entry running it is dropped in the same read-modify-write. That is what keeps a
// relocated hook script from leaving a second entry behind, pointing at a file the
// install just deleted — which would fail on every stop rather than fail loudly
// once.
//
// This is the Go replacement for the jq block in the old install.sh — same
// behaviour and same on-disk shape, with no external jq dependency.
func ensureStopHook(path, hookCmd, obsoleteCmd string) (bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, err
	}

	settings := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			// Refuse to touch a file we can't parse: overwriting a user's
			// hand-edited settings.json would be worse than failing loudly.
			return false, fmt.Errorf("parse %s: %w", path, err)
		}
	}

	hooks, err := childObject(settings, "hooks")
	if err != nil {
		return false, err
	}
	stop, err := childArray(hooks, "Stop")
	if err != nil {
		return false, err
	}

	pruned, dropped := dropStopHook(stop, obsoleteCmd)
	if stopHookPresent(pruned, hookCmd) && !dropped {
		return false, nil
	}

	if !stopHookPresent(pruned, hookCmd) {
		// Append a matcher-less Stop entry carrying our command — the same shape
		// Claude Code writes and the same shape the old install.sh produced.
		pruned = append(pruned, map[string]any{
			"hooks": []any{
				map[string]any{"type": "command", "command": hookCmd},
			},
		})
	}
	hooks["Stop"] = pruned
	settings["hooks"] = hooks

	// Back up the original before writing, mirroring install.sh's .bak.<ts>.
	// Nanosecond precision avoids clobbering an earlier backup on a same-second re-run.
	if len(raw) > 0 {
		backup := fmt.Sprintf("%s.bak.%d", path, time.Now().UnixNano())
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			return false, fmt.Errorf("backup %s: %w", path, err)
		}
	}

	out, err := json.MarshalIndent(settings, "", "  ")
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

// dropStopHook returns stop without any entry that runs cmd, and reports whether
// anything was removed. An entry that carries other hooks alongside cmd keeps
// those: only the matching hook is taken out, so a user who added their own
// command next to ours does not lose it. An empty cmd is a no-op, which is what
// callers with nothing to supersede pass.
func dropStopHook(stop []any, cmd string) ([]any, bool) {
	if cmd == "" {
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
				if c, _ := hm["command"].(string); c == cmd {
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

// stopHookPresent reports whether any Stop entry already registers command cmd,
// so re-running the installer never duplicates the hook.
func stopHookPresent(stop []any, cmd string) bool {
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
