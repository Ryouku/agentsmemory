package main

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// copyStats reports what a config-dir copy did, so the installer can say it out
// loud instead of silently moving hundreds of megabytes.
type copyStats struct {
	Files   int   // files (and symlinks) created in the target
	Bytes   int64 // bytes written
	Skipped int   // entries left alone because the target already had them
}

// skipCopyNames are the directory names never copied into a sandbox. They hold
// per-machine runtime state, not configuration: conversation history, logs,
// caches, extracted binaries and crash debris. Skipping them is what makes the
// copy viable at all — a global ~/.codex is ~800 MB, of which the parts worth
// carrying (credentials, settings, plugins, skills) are a fraction.
//
// The list is matched on any path segment, because the same names appear at the
// top level in one agent and nested in another.
var skipCopyNames = map[string]bool{
	// conversation + project state
	"projects": true, "sessions": true, "session-env": true, "history": true,
	"todos": true, "file-history": true, "tasks": true, "teams": true,
	// caches, logs, telemetry, scratch
	"cache": true, "caches": true, "logs": true, "debug": true, "telemetry": true,
	"statsig": true, "tmp": true, ".tmp": true, "backups": true,
	"shell-snapshots": true, "shell_snapshots": true,
	// regenerable heavyweights
	"bin": true, "sqlite": true, "computer-use": true, "vendor_imports": true,
	"visualizations": true, "node_modules": true, ".git": true,
}

// skipCopySuffixes are file suffixes never copied: databases and their
// write-ahead sidecars (copying an open SQLite file yields a corrupt one at the
// other end), plus the installer's own timestamped backups.
var skipCopySuffixes = []string{
	".sqlite", ".sqlite-wal", ".sqlite-shm", ".db", ".db-wal", ".db-shm", ".log",
}

// skipCopyFiles are individual filenames not worth carrying: append-only history
// and per-install identity that must not be shared between configs.
var skipCopyFiles = map[string]bool{
	"history.jsonl":       true,
	"session_index.jsonl": true,
	"installation_id":     true,
	"stats-cache.json":    true,
}

// skipCopy reports whether the entry at the slash-separated relative path rel is
// excluded from a config copy. Directories are matched by name so the whole
// subtree is pruned in one decision.
func skipCopy(rel string, isDir bool) bool {
	segments := strings.Split(rel, "/")
	for _, seg := range segments {
		if skipCopyNames[seg] {
			return true
		}
	}
	if isDir {
		return false
	}
	base := segments[len(segments)-1]
	if skipCopyFiles[base] {
		return true
	}
	if strings.Contains(base, ".bak.") || strings.HasSuffix(base, ".bak") {
		return true
	}
	for _, suffix := range skipCopySuffixes {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return false
}

// copyConfigTree copies the agent configuration under src into dst, skipping the
// runtime state skipCopy excludes and never overwriting a file the target
// already has. Not overwriting is the important half: the copy seeds a new
// sandbox, and re-running an install must not undo whatever the user changed
// inside it since.
//
// File modes are preserved, which matters more than it looks — auth.json and our
// agentsmemory.env are 0600, and a credential copied world-readable would be a
// quiet downgrade. Symlinks are recreated as symlinks rather than followed, so a
// linked plugin directory stays a link instead of being duplicated.
func copyConfigTree(src, dst string) (copyStats, error) {
	var stats copyStats
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, path)
		if relErr != nil {
			return relErr
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if skipCopy(rel, d.IsDir()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		target := filepath.Join(dst, filepath.FromSlash(rel))
		switch {
		case d.IsDir():
			info, statErr := d.Info()
			if statErr != nil {
				return statErr
			}
			return os.MkdirAll(target, info.Mode().Perm())
		case d.Type()&fs.ModeSymlink != 0:
			return copySymlink(path, target, &stats)
		case !d.Type().IsRegular():
			// Sockets, fifos and devices are runtime artefacts; a config copy has
			// no business recreating them.
			return nil
		default:
			return copyRegular(path, target, d, &stats)
		}
	})
	return stats, err
}

// copySymlink recreates a link at target, leaving an existing entry untouched.
func copySymlink(path, target string, stats *copyStats) error {
	if _, err := os.Lstat(target); err == nil {
		stats.Skipped++
		return nil
	}
	dest, err := os.Readlink(path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	if err := os.Symlink(dest, target); err != nil {
		return err
	}
	stats.Files++
	return nil
}

// copyRegular copies one file, preserving its mode and skipping it when the
// target already exists.
func copyRegular(path, target string, d fs.DirEntry, stats *copyStats) error {
	if _, err := os.Lstat(target); err == nil {
		stats.Skipped++
		return nil
	}
	info, err := d.Info()
	if err != nil {
		return err
	}

	in, err := os.Open(path)
	if err != nil {
		// An unreadable source file is not worth aborting a whole copy over —
		// report nothing here and let the caller's summary show a lower count.
		return nil
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, info.Mode().Perm())
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeErr != nil {
		return closeErr
	}
	stats.Files++
	stats.Bytes += written
	return nil
}

// humanBytes renders a byte count for the installer's progress line.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for n/div >= unit && exp < 3 {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGT"[exp])
}
