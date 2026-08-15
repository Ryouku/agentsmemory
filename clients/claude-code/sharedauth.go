package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// sharedAuthMarker records which credential files a config dir shares with the
// global config. It exists so `run` can notice when a link has been replaced by a
// regular file — an agent that rewrites credentials atomically (write to a temp
// file, rename over the target) silently breaks the share, and a sandbox that has
// quietly stopped sharing looks exactly like one that never did.
const sharedAuthMarker = ".agentsmemory-shared-auth"

// sharedAuthLink is one credential file to link: where it lives in the target
// config dir, and the global file it should point at.
type sharedAuthLink struct {
	Name   string // file name inside the config dir
	Target string // absolute path of the global file
	Link   string // absolute path of the link to create
}

// planSharedAuth lists the credential links for a kit, given the global config
// dir and the target. Only files the global config actually has are returned —
// linking to a credential that does not exist would leave a dangling link the
// agent then fails to read. It is pure so the decision is testable without a
// filesystem full of credentials.
func planSharedAuth(kit agentKit, globalDir, targetDir string, exists func(string) bool) []sharedAuthLink {
	var links []sharedAuthLink
	for _, name := range kit.authFiles {
		global := filepath.Join(globalDir, name)
		if !exists(global) {
			continue
		}
		links = append(links, sharedAuthLink{
			Name:   name,
			Target: global,
			Link:   filepath.Join(targetDir, name),
		})
	}
	return links
}

// linkSharedAuth points the target's credential files at the global config, so a
// login in any sandbox — or in the global agent — is a login everywhere. It runs
// after the optional --copy seed, deliberately: the copy may have just written a
// private snapshot of those credentials, and sharing supersedes that.
//
// An existing regular file is backed up before being replaced, because it may be
// the only copy of a credential the user still wants.
func (i *Installer) linkSharedAuth() error {
	if !i.sharedAuth {
		return nil
	}
	global := i.kit.globalConfigDir(homeDir())
	if sameDir(global, i.targetDir) {
		return fmt.Errorf("--shared-auth needs a target other than the global config dir: pass --sandbox <name> or --config-dir <dir>")
	}

	links := planSharedAuth(i.kit, global, i.targetDir, func(p string) bool {
		_, err := os.Stat(p)
		return err == nil
	})
	if len(links) == 0 {
		// Claude on macOS is the expected case: credentials live in the login
		// keychain, which every config dir already reads.
		i.ok("%s keeps no credential file in its config dir — nothing to share", i.kit.name)
		return nil
	}

	var linked []string
	for _, l := range links {
		if i.dryRun {
			fmt.Fprintf(i.out, "  would link %s → %s\n", l.Link, l.Target)
			linked = append(linked, l.Name)
			continue
		}
		if err := replaceWithLink(l); err != nil {
			i.warn("could not share %s: %v", l.Name, err)
			continue
		}
		i.ok("shared %s → %s", l.Name, l.Target)
		linked = append(linked, l.Name)
	}
	if len(linked) == 0 || i.dryRun {
		return nil
	}
	// The marker is what makes a later break detectable; failing to write it costs
	// the warning, not the sharing, so it is not fatal.
	if err := os.WriteFile(filepath.Join(i.targetDir, sharedAuthMarker), []byte(strings.Join(linked, "\n")+"\n"), 0o600); err != nil {
		i.warn("could not record the shared-auth marker: %v", err)
	}
	return nil
}

// replaceWithLink makes l.Link a symlink to l.Target, preserving anything that
// was there. An already-correct link is left alone so re-running the installer is
// a no-op; a link pointing elsewhere is repointed; a regular file is moved aside
// with a timestamp first.
func replaceWithLink(l sharedAuthLink) error {
	info, err := os.Lstat(l.Link)
	switch {
	case err != nil:
		// Nothing there — the common case for a fresh sandbox.
	case info.Mode()&os.ModeSymlink != 0:
		if dest, rerr := os.Readlink(l.Link); rerr == nil && dest == l.Target {
			return nil // already shared with the global config
		}
		if rerr := os.Remove(l.Link); rerr != nil {
			return rerr
		}
	default:
		backup := fmt.Sprintf("%s.bak.%d", l.Link, time.Now().UnixNano())
		if rerr := os.Rename(l.Link, backup); rerr != nil {
			return rerr
		}
	}
	if err := os.MkdirAll(filepath.Dir(l.Link), 0o755); err != nil {
		return err
	}
	return os.Symlink(l.Target, l.Link)
}

// brokenSharedAuth returns the credential files a config dir claims to share but
// no longer does. `run` calls it before launching: if an agent rewrote a
// credential by replacing the file rather than writing through the link, the
// sandbox has silently stopped sharing, and the user would only find out the next
// time they had to log in again.
func brokenSharedAuth(configDir string) []string {
	raw, err := os.ReadFile(filepath.Join(configDir, sharedAuthMarker))
	if err != nil {
		return nil // this config dir never opted in
	}
	var broken []string
	for _, name := range strings.Split(string(raw), "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		info, err := os.Lstat(filepath.Join(configDir, name))
		if err != nil {
			continue // gone entirely: the agent will recreate it on next login
		}
		if info.Mode()&os.ModeSymlink == 0 {
			broken = append(broken, name)
		}
	}
	return broken
}
